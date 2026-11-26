package session

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"insistanan/api-proxy/internal/storage/sqlitedb"
)

type sqliteStore struct {
	db     *sql.DB
	ownsDB bool
}

func newSQLiteStore(path string) (*sqliteStore, error) {
	db, err := sqlitedb.Open(path)
	if err != nil {
		return nil, err
	}
	store, err := newSQLiteStoreWithDB(db)
	if err != nil {
		return nil, errors.Join(err, db.Close())
	}
	store.ownsDB = true
	return store, nil
}

func newSQLiteStoreWithDB(db *sql.DB) (*sqliteStore, error) {
	store := &sqliteStore{db: db}
	if err := store.initSchema(); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *sqliteStore) initSchema() error {
	if _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS gemini_shadow_turns (
		id INTEGER PRIMARY KEY, conversation_id TEXT NOT NULL, provider_id TEXT NOT NULL, turn_json TEXT NOT NULL,
		FOREIGN KEY(conversation_id) REFERENCES conversations(id) ON DELETE CASCADE
	)`); err != nil {
		return err
	}
	if _, err := s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_gemini_shadow_conversation_provider ON gemini_shadow_turns(conversation_id,provider_id,id)`); err != nil {
		return err
	}
	if _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS response_sessions (
		id TEXT PRIMARY KEY,
		conversation_id TEXT NOT NULL DEFAULT '',
		messages_json TEXT NOT NULL DEFAULT '[]',
		last_response_id TEXT NOT NULL DEFAULT '',
		created_at INTEGER NOT NULL,
		last_access_at INTEGER NOT NULL,
		total_tokens INTEGER NOT NULL DEFAULT 0,
		has_vision_content INTEGER NOT NULL DEFAULT 0
	)`); err != nil {
		return err
	}
	if err := s.ensureColumn("response_sessions", "conversation_id", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS response_session_mappings (
		response_id TEXT PRIMARY KEY,
		session_id TEXT NOT NULL
	)`); err != nil {
		return err
	}
	if err := s.ensureColumn("response_session_mappings", "upstream_identity", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS response_session_access (
		session_id TEXT PRIMARY KEY,
		last_access_at INTEGER NOT NULL
	)`); err != nil {
		return err
	}
	if _, err := s.db.Exec("CREATE INDEX IF NOT EXISTS idx_response_sessions_conversation_id ON response_sessions(conversation_id)"); err != nil {
		return err
	}
	if _, err := s.db.Exec("CREATE INDEX IF NOT EXISTS idx_response_session_mappings_session_id ON response_session_mappings(session_id)"); err != nil {
		return err
	}
	if err := s.ensureColumn("response_sessions", "version", "INTEGER NOT NULL DEFAULT 0"); err != nil {
		return err
	}
	if err := s.ensureColumn("response_session_mappings", "alias_key", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := s.ensureColumn("response_session_mappings", "boundary_json", "TEXT"); err != nil {
		return err
	}

	return nil
}

const sessionColumns = `rs.id, rs.conversation_id, rs.messages_json, rs.last_response_id,
 rs.created_at, COALESCE(rsa.last_access_at, rs.last_access_at), rs.total_tokens, rs.has_vision_content, rs.version`

func (s *sqliteStore) load(ctx context.Context, where string, args ...any) (*Session, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+sessionColumns+` FROM response_sessions rs
 LEFT JOIN response_session_access rsa ON rsa.session_id = rs.id `+where, args...)
	var result Session
	var messages string
	var created, accessed int64
	var vision int
	if err := row.Scan(&result.ID, &result.ConversationID, &messages, &result.LastResponseID, &created, &accessed, &result.TotalTokens, &vision, &result.Version); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	result.CreatedAt, result.LastAccessAt = time.Unix(created, 0), time.Unix(accessed, 0)
	result.HasVisionContent = vision != 0
	if err := json.Unmarshal([]byte(messages), &result.Messages); err != nil {
		return nil, fmt.Errorf("解析会话 %s 失败: %w", result.ID, err)
	}
	return &result, nil
}

// create 原子插入并执行容量淘汰；创建失败不提前删除其他历史。
func (s *sqliteStore) create(ctx context.Context, session *Session, limit int, active []string) ([]string, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `INSERT INTO response_sessions (id,conversation_id,messages_json,last_response_id,created_at,last_access_at,version) VALUES (?,?,'[]','',?,?,0)`, session.ID, session.ConversationID, session.CreatedAt.Unix(), session.LastAccessAt.Unix())
	if err != nil {
		return nil, err
	}
	var removed []string
	if limit > 0 {
		var count int
		if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM response_sessions`).Scan(&count); err != nil {
			return nil, err
		}
		if count > limit {
			active = append(append([]string{}, active...), session.ID)
			encoded, err := json.Marshal(active)
			if err != nil {
				return nil, err
			}
			rows, err := tx.QueryContext(ctx, `SELECT rs.id FROM response_sessions rs LEFT JOIN response_session_access rsa ON rsa.session_id=rs.id
 WHERE rs.id NOT IN (SELECT value FROM json_each(?)) ORDER BY COALESCE(rsa.last_access_at,rs.last_access_at), rs.rowid LIMIT ?`, string(encoded), count-limit)
			if err != nil {
				return nil, err
			}
			for rows.Next() {
				var id string
				if err = rows.Scan(&id); err != nil {
					rows.Close()
					return nil, err
				}
				removed = append(removed, id)
			}
			err = errors.Join(rows.Err(), rows.Close())
			if err != nil {
				return nil, err
			}
			if len(removed) < count-limit {
				return nil, fmt.Errorf("会话容量已满，现有历史仍有请求在途")
			}
			if err = deleteSessionIDs(ctx, tx, removed); err != nil {
				return nil, err
			}
		}
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return removed, nil
}

// touch 只刷新访问时间，不在读取热路径猜测或修复历史归属。
func (s *sqliteStore) touch(ctx context.Context, session *Session) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO response_session_access(session_id,last_access_at) VALUES (?,?) ON CONFLICT(session_id) DO UPDATE SET last_access_at=excluded.last_access_at`, session.ID, session.LastAccessAt.Unix())
	return err
}

func deleteSessionIDs(ctx context.Context, tx *sql.Tx, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	encoded, err := json.Marshal(ids)
	if err != nil {
		return err
	}
	var hasAliases bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM response_session_mappings WHERE alias_key<>'' AND session_id IN (SELECT value FROM json_each(?)))`, string(encoded)).Scan(&hasAliases); err != nil {
		return err
	}
	if hasAliases {
		if _, err := tx.ExecContext(ctx, `DELETE FROM conversation_aliases WHERE alias_key IN (SELECT alias_key FROM response_session_mappings WHERE session_id IN (SELECT value FROM json_each(?)))`, string(encoded)); err != nil {
			return err
		}
	}
	for _, statement := range []string{
		`DELETE FROM response_session_mappings WHERE session_id IN (SELECT value FROM json_each(?))`,
		`DELETE FROM response_session_access WHERE session_id IN (SELECT value FROM json_each(?))`,
		`DELETE FROM response_sessions WHERE id IN (SELECT value FROM json_each(?))`,
	} {
		if _, err = tx.ExecContext(ctx, statement, string(encoded)); err != nil {
			return err
		}
	}
	return nil
}

func (s *sqliteStore) prune(ctx context.Context, cutoff time.Time, maxMessages, maxTokens int, active []string) ([]string, error) {
	encoded, err := json.Marshal(active)
	if err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	conditions := []string{}
	args := []any{string(encoded)}
	if !cutoff.IsZero() {
		conditions = append(conditions, `COALESCE(rsa.last_access_at,rs.last_access_at) < ?`)
		args = append(args, cutoff.Unix())
	}
	if maxMessages > 0 {
		conditions = append(conditions, `json_array_length(rs.messages_json)>?`)
		args = append(args, maxMessages)
	}
	if maxTokens > 0 {
		conditions = append(conditions, `rs.total_tokens>?`)
		args = append(args, maxTokens)
	}
	if len(conditions) == 0 {
		return nil, nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT rs.id FROM response_sessions rs LEFT JOIN response_session_access rsa ON rsa.session_id=rs.id WHERE rs.id NOT IN (SELECT value FROM json_each(?)) AND (`+strings.Join(conditions, " OR ")+`)`, args...)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	if err = errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, err
	}
	if err = deleteSessionIDs(ctx, tx, ids); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return ids, nil
}

func (s *sqliteStore) ensureColumn(tableName string, columnName string, declaration string) error {
	rows, err := s.db.Query("PRAGMA table_info(" + tableName + ")")
	if err != nil {
		return err
	}
	found := false
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, dataType string
		var defaultValue interface{}
		if err := rows.Scan(&cid, &name, &dataType, &notNull, &defaultValue, &primaryKey); err != nil {
			_ = rows.Close()
			return err
		}
		if name == columnName {
			found = true
		}
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if found {
		return nil
	}
	_, err = s.db.Exec("ALTER TABLE " + tableName + " ADD COLUMN " + columnName + " " + declaration)
	return err
}

func (s *sqliteStore) close() error {
	if s == nil || s.db == nil {
		return nil
	}
	if s.ownsDB {
		return s.db.Close()
	}
	return nil
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
