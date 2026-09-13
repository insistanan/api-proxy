// Package assistant 提供管理界面的 AI 配置助手：用自然语言回答配置问题，
// 并通过白名单行动协议代为创建渠道 / 分组 / 模型映射。
//
// 模型调用复用 internal/llmgate（渠道优先级 messages → responses → chat → gemini）；
// 配置写入复用 config.ConfigManager 既有校验路径，绝不绕过。
// API Key 永不进入提示词（摘要中掩码展示），行动参数也不含 key 字段——
// 修改渠道时保留原有 Key。
package assistant

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// 会话与消息持久化（.config/assistant.db，WAL sqlite）。

const (
	assistantTimeLayout = "2006-01-02T15:04:05.000000000Z07:00"
	// maxHistoryMessages 单次调用模型时携带的最大历史条数（含本轮用户消息），
	// 控制上下文规模；超出时保留最近的部分。
	maxHistoryMessages = 40
)

var (
	// ErrStoreClosed 表示助手存储已关闭。
	ErrStoreClosed = errors.New("助手存储已关闭")
	// ErrSessionNotFound 表示会话不存在。
	ErrSessionNotFound = errors.New("会话不存在")
)

// Session 是一个助手会话。
type Session struct {
	ID        int64     `json:"id"`
	Title     string    `json:"title"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// ActionRecord 是一条已执行的行动（结果随消息持久化）。
type ActionRecord struct {
	Action  string `json:"action"`
	Success bool   `json:"success"`
	Detail  string `json:"detail"`
}

// Message 是会话内的一条消息。
type Message struct {
	ID        int64          `json:"id"`
	SessionID int64          `json:"sessionId"`
	Role      string         `json:"role"` // user / assistant
	Content   string         `json:"content"`
	Actions   []ActionRecord `json:"actions,omitempty"`
	CreatedAt time.Time      `json:"createdAt"`
}

// Store 持久化助手会话与消息。
type Store struct {
	db        *sql.DB
	mu        sync.RWMutex
	closed    bool
	closeOnce sync.Once
}

// NewStore 创建助手存储。
func NewStore(path string) (*Store, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, fmt.Errorf("助手存储数据库路径不能为空")
	}
	if path != ":memory:" {
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return nil, fmt.Errorf("创建助手存储数据库目录失败: %w", err)
		}
	}
	dsn := path + "?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(5000)"
	if path == ":memory:" {
		dsn = "file:assistant?mode=memory&cache=shared&_pragma=busy_timeout(5000)"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("打开助手存储数据库失败: %w", err)
	}
	db.SetMaxOpenConns(1)
	store := &Store{db: db}
	if err := store.initSchema(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (s *Store) initSchema() error {
	if _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS assistant_sessions (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		title TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL
	)`); err != nil {
		return fmt.Errorf("初始化助手会话表失败: %w", err)
	}
	if _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS assistant_messages (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		session_id INTEGER NOT NULL,
		role TEXT NOT NULL,
		content TEXT NOT NULL DEFAULT '',
		actions_json TEXT NOT NULL DEFAULT '[]',
		created_at TEXT NOT NULL
	)`); err != nil {
		return fmt.Errorf("初始化助手消息表失败: %w", err)
	}
	if _, err := s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_assistant_messages_session ON assistant_messages(session_id, id)`); err != nil {
		return fmt.Errorf("初始化助手消息索引失败: %w", err)
	}
	return nil
}

// CreateSession 创建会话，标题默认取首条用户消息前缀（调用方在首条消息后可改）。
func (s *Store) CreateSession(title string) (Session, error) {
	if err := s.ensureOpen(); err != nil {
		return Session{}, err
	}
	now := time.Now().UTC()
	result, err := s.db.Exec(`INSERT INTO assistant_sessions (title, created_at, updated_at) VALUES (?, ?, ?)`,
		strings.TrimSpace(title), now.Format(assistantTimeLayout), now.Format(assistantTimeLayout))
	if err != nil {
		return Session{}, fmt.Errorf("创建助手会话失败: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return Session{}, fmt.Errorf("读取助手会话 ID 失败: %w", err)
	}
	return Session{ID: id, Title: strings.TrimSpace(title), CreatedAt: now, UpdatedAt: now}, nil
}

// ListSessions 按更新时间倒序列出会话。
func (s *Store) ListSessions() ([]Session, error) {
	if err := s.ensureOpen(); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(`SELECT id, title, created_at, updated_at FROM assistant_sessions ORDER BY updated_at DESC, id DESC`)
	if err != nil {
		return nil, fmt.Errorf("查询助手会话失败: %w", err)
	}
	defer rows.Close()
	sessions := make([]Session, 0)
	for rows.Next() {
		var session Session
		var createdAt, updatedAt string
		if err := rows.Scan(&session.ID, &session.Title, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		if session.CreatedAt, err = time.Parse(assistantTimeLayout, createdAt); err != nil {
			return nil, err
		}
		if session.UpdatedAt, err = time.Parse(assistantTimeLayout, updatedAt); err != nil {
			return nil, err
		}
		sessions = append(sessions, session)
	}
	return sessions, rows.Err()
}

// GetSession 返回单个会话。
func (s *Store) GetSession(id int64) (Session, error) {
	if err := s.ensureOpen(); err != nil {
		return Session{}, err
	}
	var session Session
	var createdAt, updatedAt string
	err := s.db.QueryRow(`SELECT id, title, created_at, updated_at FROM assistant_sessions WHERE id = ?`, id).
		Scan(&session.ID, &session.Title, &createdAt, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, ErrSessionNotFound
	}
	if err != nil {
		return Session{}, fmt.Errorf("查询助手会话失败: %w", err)
	}
	if session.CreatedAt, err = time.Parse(assistantTimeLayout, createdAt); err != nil {
		return Session{}, err
	}
	if session.UpdatedAt, err = time.Parse(assistantTimeLayout, updatedAt); err != nil {
		return Session{}, err
	}
	return session, nil
}

// DeleteSession 删除会话及其全部消息。
func (s *Store) DeleteSession(id int64) (bool, error) {
	if err := s.ensureOpen(); err != nil {
		return false, err
	}
	result, err := s.db.Exec(`DELETE FROM assistant_sessions WHERE id = ?`, id)
	if err != nil {
		return false, fmt.Errorf("删除助手会话失败: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if affected == 0 {
		return false, nil
	}
	if _, err := s.db.Exec(`DELETE FROM assistant_messages WHERE session_id = ?`, id); err != nil {
		return false, fmt.Errorf("删除助手会话消息失败: %w", err)
	}
	return true, nil
}

// AppendMessage 追加一条消息并刷新会话更新时间。
func (s *Store) AppendMessage(message Message) (Message, error) {
	if err := s.ensureOpen(); err != nil {
		return Message{}, err
	}
	if message.Role != "user" && message.Role != "assistant" {
		return Message{}, fmt.Errorf("无效的消息角色 %q", message.Role)
	}
	if message.CreatedAt.IsZero() {
		message.CreatedAt = time.Now().UTC()
	}
	tx, err := s.db.Begin()
	if err != nil {
		return Message{}, fmt.Errorf("打开助手事务失败: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	actionsJSON := "[]"
	if len(message.Actions) > 0 {
		encoded, err := marshalActions(message.Actions)
		if err != nil {
			return Message{}, err
		}
		actionsJSON = encoded
	}
	result, err := tx.Exec(`INSERT INTO assistant_messages (session_id, role, content, actions_json, created_at)
		VALUES (?, ?, ?, ?, ?)`,
		message.SessionID, message.Role, message.Content, actionsJSON, message.CreatedAt.Format(assistantTimeLayout))
	if err != nil {
		return Message{}, fmt.Errorf("写入助手消息失败: %w", err)
	}
	message.ID, err = result.LastInsertId()
	if err != nil {
		return Message{}, err
	}
	if _, err := tx.Exec(`UPDATE assistant_sessions SET updated_at = ? WHERE id = ?`,
		time.Now().UTC().Format(assistantTimeLayout), message.SessionID); err != nil {
		return Message{}, fmt.Errorf("刷新助手会话时间失败: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Message{}, fmt.Errorf("提交助手事务失败: %w", err)
	}
	return message, nil
}

// ListMessages 返回会话的全部消息（按时间升序）。
func (s *Store) ListMessages(sessionID int64) ([]Message, error) {
	if err := s.ensureOpen(); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(`SELECT id, session_id, role, content, actions_json, created_at
		FROM assistant_messages WHERE session_id = ? ORDER BY id ASC`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("查询助手消息失败: %w", err)
	}
	defer rows.Close()
	messages := make([]Message, 0)
	for rows.Next() {
		var message Message
		var actionsJSON, createdAt string
		if err := rows.Scan(&message.ID, &message.SessionID, &message.Role, &message.Content, &actionsJSON, &createdAt); err != nil {
			return nil, err
		}
		if message.CreatedAt, err = time.Parse(assistantTimeLayout, createdAt); err != nil {
			return nil, err
		}
		message.Actions, err = unmarshalActions(actionsJSON)
		if err != nil {
			return nil, err
		}
		messages = append(messages, message)
	}
	return messages, rows.Err()
}

// Close 幂等关闭数据库连接。
func (s *Store) Close() error {
	if s == nil {
		return nil
	}
	var closeErr error
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		closeErr = s.db.Close()
		s.mu.Unlock()
	})
	return closeErr
}

func (s *Store) ensureOpen() error {
	if s == nil || s.db == nil {
		return ErrStoreClosed
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return ErrStoreClosed
	}
	return nil
}
