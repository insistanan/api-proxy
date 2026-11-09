package session

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

type sqliteStore struct {
	db *sql.DB
}

func newSQLiteStore(path string) (*sqliteStore, error) {
	if path == "" {
		return nil, fmt.Errorf("session database path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	store := &sqliteStore{db: db}
	if err := store.initSchema(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (s *sqliteStore) initSchema() error {
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
	// 会话删除需要通过 Responses 外部 ID 解析共享对话别名；对仅启用
	// Responses 的测试/部署也先建立兼容表，正式对话组件会复用它。
	if _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS conversation_aliases (
		alias_key TEXT PRIMARY KEY,
		conversation_id TEXT NOT NULL
	)`); err != nil {
		return err
	}
	if _, err := s.db.Exec("CREATE INDEX IF NOT EXISTS idx_response_sessions_conversation_id ON response_sessions(conversation_id)"); err != nil {
		return err
	}
	if _, err := s.db.Exec("CREATE INDEX IF NOT EXISTS idx_response_session_mappings_session_id ON response_session_mappings(session_id)"); err != nil {
		return err
	}
	return nil
}

// loadByResponseID 按需恢复单条会话链。messages_json 可能很大，启动时禁止全表读取。
func (s *sqliteStore) loadByResponseID(responseID string, maxAge time.Duration) (*Session, bool, error) {
	row := s.db.QueryRow(`SELECT rs.id, rs.conversation_id, rs.messages_json, rs.last_response_id,
		rs.created_at, COALESCE(rsa.last_access_at, rs.last_access_at), rs.total_tokens, rs.has_vision_content
		FROM response_session_mappings AS rsm
		JOIN response_sessions AS rs ON rs.id = rsm.session_id
		LEFT JOIN response_session_access AS rsa ON rsa.session_id = rs.id
		WHERE rsm.response_id = ?`, responseID)

	var session Session
	var messagesJSON string
	var createdAt, lastAccessAt int64
	var hasVision int
	if err := row.Scan(&session.ID, &session.ConversationID, &messagesJSON, &session.LastResponseID,
		&createdAt, &lastAccessAt, &session.TotalTokens, &hasVision); err != nil {
		if err == sql.ErrNoRows {
			return nil, false, nil
		}
		return nil, false, err
	}
	session.CreatedAt = time.Unix(createdAt, 0)
	session.LastAccessAt = time.Unix(lastAccessAt, 0)
	if maxAge > 0 && time.Since(session.LastAccessAt) > maxAge {
		if err := s.deleteSession(session.ID); err != nil {
			return nil, false, err
		}
		return nil, false, nil
	}
	if err := json.Unmarshal([]byte(messagesJSON), &session.Messages); err != nil {
		return nil, false, fmt.Errorf("解析 Responses 会话 %s 失败: %w", session.ID, err)
	}
	session.HasVisionContent = hasVision != 0
	return &session, true, nil
}

// loadLatestByConversation 按内部 conversation ID 恢复最近使用的会话。
// 当代理为避免旧链污染而清掉 previous_response_id 后，客户端下一轮
// 可能不再携带 response ID；此时仍必须回到同一个本地 session，而不是
// 每轮创建空 session。
func (s *sqliteStore) loadLatestByConversation(conversationID string, maxAge time.Duration) (*Session, bool, error) {
	row := s.db.QueryRow(`SELECT rs.id, rs.conversation_id, rs.messages_json, rs.last_response_id,
		rs.created_at, COALESCE(rsa.last_access_at, rs.last_access_at), rs.total_tokens, rs.has_vision_content
		FROM response_sessions AS rs
		LEFT JOIN response_session_access AS rsa ON rsa.session_id = rs.id
		WHERE rs.conversation_id = ?
		ORDER BY COALESCE(rsa.last_access_at, rs.last_access_at) DESC, rs.id DESC
		LIMIT 1`, conversationID)

	var session Session
	var messagesJSON string
	var createdAt, lastAccessAt int64
	var hasVision int
	if err := row.Scan(&session.ID, &session.ConversationID, &messagesJSON, &session.LastResponseID,
		&createdAt, &lastAccessAt, &session.TotalTokens, &hasVision); err != nil {
		if err == sql.ErrNoRows {
			return nil, false, nil
		}
		return nil, false, err
	}
	session.CreatedAt = time.Unix(createdAt, 0)
	session.LastAccessAt = time.Unix(lastAccessAt, 0)
	if maxAge > 0 && time.Since(session.LastAccessAt) > maxAge {
		if err := s.deleteSession(session.ID); err != nil {
			return nil, false, err
		}
		return nil, false, nil
	}
	if err := json.Unmarshal([]byte(messagesJSON), &session.Messages); err != nil {
		return nil, false, fmt.Errorf("解析 Responses 会话 %s 失败: %w", session.ID, err)
	}
	session.HasVisionContent = hasVision != 0
	return &session, true, nil
}

func (s *sqliteStore) upsertSession(session *Session) error {
	return s.upsertSessionAndMapping(session, "")
}

func (s *sqliteStore) upsertSessionAndMapping(session *Session, responseID string, owner ...string) error {
	messages, err := json.Marshal(session.Messages)
	if err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.Exec(`INSERT INTO response_sessions (
		id, conversation_id, messages_json, last_response_id, created_at, last_access_at, total_tokens, has_vision_content
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(id) DO UPDATE SET
		conversation_id = excluded.conversation_id,
		messages_json = excluded.messages_json,
		last_response_id = excluded.last_response_id,
		last_access_at = excluded.last_access_at,
		total_tokens = excluded.total_tokens,
		has_vision_content = excluded.has_vision_content`,
		session.ID, session.ConversationID, string(messages), session.LastResponseID, session.CreatedAt.Unix(), session.LastAccessAt.Unix(),
		session.TotalTokens, boolToInt(session.HasVisionContent)); err != nil {
		return err
	}
	if responseID != "" {
		if _, err = tx.Exec(`INSERT INTO response_session_mappings (response_id, session_id, upstream_identity) VALUES (?, ?, ?)
			ON CONFLICT(response_id) DO UPDATE SET session_id = excluded.session_id, upstream_identity = excluded.upstream_identity`, responseID, session.ID, responseOwnerValue(owner)); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(`INSERT INTO response_session_access (session_id, last_access_at) VALUES (?, ?)
		ON CONFLICT(session_id) DO UPDATE SET last_access_at = excluded.last_access_at`, session.ID, session.LastAccessAt.Unix()); err != nil {
		return err
	}
	return tx.Commit()
}

// replaceSessionAndMapping 原子替换 compact 前后的 response ID 映射。
// 如果旧映射继续存在，客户端重放旧 response ID 时可能重新进入压缩前的
// 服务端链，破坏 compact 后的上下文边界。
func (s *sqliteStore) replaceSessionAndMapping(session *Session, previousResponseID, responseID string, owner ...string) error {
	if session == nil {
		return fmt.Errorf("会话不能为空")
	}
	messages, err := json.Marshal(session.Messages)
	if err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err = tx.Exec(`INSERT INTO response_sessions (
		id, conversation_id, messages_json, last_response_id, created_at, last_access_at, total_tokens, has_vision_content
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(id) DO UPDATE SET
		conversation_id = excluded.conversation_id,
		messages_json = excluded.messages_json,
		last_response_id = excluded.last_response_id,
		last_access_at = excluded.last_access_at,
		total_tokens = excluded.total_tokens,
		has_vision_content = excluded.has_vision_content`,
		session.ID, session.ConversationID, string(messages), session.LastResponseID, session.CreatedAt.Unix(), session.LastAccessAt.Unix(),
		session.TotalTokens, boolToInt(session.HasVisionContent)); err != nil {
		return err
	}
	// 这是 compaction 的原子替换操作，不论调用方是否能提供旧 response ID，
	// 都必须删除该 session 的全部旧映射。否则旧 ID 仍可恢复压缩前的边界。
	if _, err = tx.Exec(`DELETE FROM response_session_mappings WHERE session_id = ? AND response_id != ?`, session.ID, responseID); err != nil {
		return err
	}
	if responseID != "" {
		if _, err = tx.Exec(`INSERT INTO response_session_mappings (response_id, session_id, upstream_identity) VALUES (?, ?, ?)
			ON CONFLICT(response_id) DO UPDATE SET session_id = excluded.session_id, upstream_identity = excluded.upstream_identity`, responseID, session.ID, responseOwnerValue(owner)); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(`INSERT INTO response_session_access (session_id, last_access_at) VALUES (?, ?)
		ON CONFLICT(session_id) DO UPDATE SET last_access_at = excluded.last_access_at`, session.ID, session.LastAccessAt.Unix()); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *sqliteStore) touchSession(session *Session, updateConversation bool) error {
	if updateConversation {
		if _, err := s.db.Exec(`UPDATE response_sessions SET conversation_id = ? WHERE id = ?`, session.ConversationID, session.ID); err != nil {
			return err
		}
	}
	return s.upsertSessionAccess(session.ID, session.LastAccessAt)
}

func (s *sqliteStore) upsertSessionAccess(sessionID string, lastAccessAt time.Time) error {
	_, err := s.db.Exec(`INSERT INTO response_session_access (session_id, last_access_at) VALUES (?, ?)
		ON CONFLICT(session_id) DO UPDATE SET last_access_at = excluded.last_access_at`, sessionID, lastAccessAt.Unix())
	return err
}

func (s *sqliteStore) markVisionContent(session *Session) error {
	if _, err := s.db.Exec(`UPDATE response_sessions SET has_vision_content = 1 WHERE id = ?`, session.ID); err != nil {
		return err
	}
	return s.upsertSessionAccess(session.ID, session.LastAccessAt)
}

func (s *sqliteStore) updateLastResponseID(session *Session) error {
	if _, err := s.db.Exec(`UPDATE response_sessions SET last_response_id = ? WHERE id = ?`, session.LastResponseID, session.ID); err != nil {
		return err
	}
	return s.upsertSessionAccess(session.ID, session.LastAccessAt)
}

// pruneToLimit 为新会话腾出持久化容量，并返回被删除的会话 ID。
func (s *sqliteStore) pruneToLimit(limit int) ([]string, error) {
	if limit < 0 {
		return nil, nil
	}
	var count int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM response_sessions").Scan(&count); err != nil {
		return nil, err
	}
	removeCount := count - limit
	if removeCount <= 0 {
		return nil, nil
	}
	rows, err := s.db.Query(`SELECT rs.id FROM response_sessions AS rs
		LEFT JOIN response_session_access AS rsa ON rsa.session_id = rs.id
		ORDER BY CASE WHEN rsa.last_access_at IS NULL THEN 0 ELSE 1 END,
			rsa.last_access_at ASC, rs.rowid ASC LIMIT ?`, removeCount)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, id := range ids {
		if err := s.deleteSession(id); err != nil {
			return nil, err
		}
	}
	return ids, nil
}

func (s *sqliteStore) upsertMapping(responseID string, sessionID string) error {
	_, err := s.db.Exec(`INSERT INTO response_session_mappings (response_id, session_id) VALUES (?, ?)
		ON CONFLICT(response_id) DO UPDATE SET session_id = excluded.session_id`, responseID, sessionID)
	return err
}

func (s *sqliteStore) deleteSession(sessionID string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec("DELETE FROM response_session_mappings WHERE session_id = ?", sessionID); err != nil {
		return err
	}
	if _, err := tx.Exec("DELETE FROM response_session_access WHERE session_id = ?", sessionID); err != nil {
		return err
	}
	if _, err := tx.Exec("DELETE FROM response_sessions WHERE id = ?", sessionID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *sqliteStore) deleteConversationSessions(conversationID string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`CREATE TEMP TABLE response_session_delete_ids (id TEXT PRIMARY KEY)`); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT OR IGNORE INTO response_session_delete_ids (id)
		SELECT id FROM response_sessions WHERE conversation_id = ?
		UNION
		SELECT rsm.session_id FROM response_session_mappings AS rsm
		JOIN conversation_aliases AS ca ON ca.alias_key = lower('responses|' || rsm.response_id)
		WHERE ca.conversation_id = ?`, conversationID, conversationID); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM response_session_mappings WHERE session_id IN (
		SELECT id FROM response_session_delete_ids
	)`); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM response_session_access WHERE session_id IN (
		SELECT id FROM response_session_delete_ids
	)`); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM response_sessions WHERE id IN (
		SELECT id FROM response_session_delete_ids
	)`); err != nil {
		return err
	}
	if _, err := tx.Exec("DROP TABLE response_session_delete_ids"); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *sqliteStore) deleteAllSessions() error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec("DELETE FROM response_session_mappings"); err != nil {
		return err
	}
	if _, err := tx.Exec("DELETE FROM response_sessions"); err != nil {
		return err
	}
	if _, err := tx.Exec("DELETE FROM response_session_access"); err != nil {
		return err
	}
	return tx.Commit()
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
	return s.db.Close()
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
