package sensitive

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// 危险命令 AI 闸门的待确认与白/黑名单持久化。
// 与 BlockedStore 共用同一个数据库文件（.config/blocked-logs.db），
// 各自持有独立连接（WAL + busy_timeout 支持多连接并发）。

const (
	// GateDecisionAllow / GateDecisionBlock 是会话级白/黑名单决定。
	GateDecisionAllow = "allow"
	GateDecisionBlock = "block"

	gateTimeLayout = "2006-01-02T15:04:05.000000000Z07:00"
)

// GatePending 表示一条等待用户决定的危险命令命中。
// status: waiting（等待中）/ allowed（用户放行）/ blocked（用户拦截）/
// expired（等待超时，按拦截处理）/ cancelled（客户端断连，按拦截处理）。
type GatePending struct {
	ID             int64     `json:"id"`
	CreatedAt      time.Time `json:"createdAt"`
	ConversationID string    `json:"conversationId"`
	RequestID      string    `json:"requestId"`
	APIType        string    `json:"apiType"`
	Model          string    `json:"model,omitempty"`
	Rule           string    `json:"rule"`
	Command        string    `json:"command"`
	Source         string    `json:"source"`    // code_block / tool
	AIVerdict      string    `json:"aiVerdict"` // allow / block
	AIReason       string    `json:"aiReason,omitempty"`
	ExpiresAt      time.Time `json:"expiresAt"`
	Status         string    `json:"status"`
}

// GateDecision 是会话级的白/黑名单决定。同一会话的后续危险命令命中
// 直接按最新决定放行或拦截（后决定覆盖先决定）。
type GateDecision struct {
	ID             int64     `json:"id"`
	ConversationID string    `json:"conversationId"`
	Decision       string    `json:"decision"` // allow / block
	Rule           string    `json:"rule,omitempty"`
	CommandSnippet string    `json:"commandSnippet,omitempty"`
	CommandHash    string    `json:"commandHash,omitempty"`
	CreatedAt      time.Time `json:"createdAt"`
}

var (
	// ErrGateStoreClosed 表示闸门存储已关闭。
	ErrGateStoreClosed = errors.New("闸门存储已关闭")
	// ErrGatePendingNotFound 表示待确认记录不存在或已结束。
	ErrGatePendingNotFound = errors.New("待确认记录不存在或已结束")
)

// GateStore 持久化闸门状态。
type GateStore struct {
	db        *sql.DB
	mu        sync.RWMutex
	closed    bool
	closeOnce sync.Once
}

// NewGateStore 打开（或创建）闸门存储。与 BlockedStore 指向同一文件时
// 依赖 SQLite WAL 模式下的多连接并发。
func NewGateStore(path string) (*GateStore, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, fmt.Errorf("闸门存储数据库路径不能为空")
	}
	if path != ":memory:" {
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return nil, fmt.Errorf("创建闸门存储数据库目录失败: %w", err)
		}
	}
	dsn := path + "?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(5000)"
	if path == ":memory:" {
		dsn = "file:gate_store?mode=memory&cache=shared&_pragma=busy_timeout(5000)"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("打开闸门存储数据库失败: %w", err)
	}
	db.SetMaxOpenConns(1)
	store := &GateStore{db: db}
	if err := store.initSchema(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (s *GateStore) initSchema() error {
	if _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS gate_pending (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		created_at TEXT NOT NULL,
		conversation_id TEXT NOT NULL DEFAULT '',
		request_id TEXT NOT NULL DEFAULT '',
		api_type TEXT NOT NULL DEFAULT '',
		model TEXT NOT NULL DEFAULT '',
		rule TEXT NOT NULL DEFAULT '',
		command TEXT NOT NULL DEFAULT '',
		source TEXT NOT NULL DEFAULT '',
		ai_verdict TEXT NOT NULL DEFAULT '',
		ai_reason TEXT NOT NULL DEFAULT '',
		expires_at TEXT NOT NULL,
		status TEXT NOT NULL DEFAULT 'waiting'
	)`); err != nil {
		return fmt.Errorf("初始化闸门待确认表失败: %w", err)
	}
	if _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS gate_decisions (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		conversation_id TEXT NOT NULL,
		decision TEXT NOT NULL,
		rule TEXT NOT NULL DEFAULT '',
		command_snippet TEXT NOT NULL DEFAULT '',
		command_hash TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL
	)`); err != nil {
		return fmt.Errorf("初始化闸门名单表失败: %w", err)
	}
	statements := []string{
		`CREATE INDEX IF NOT EXISTS idx_gate_pending_status ON gate_pending(status, expires_at)`,
		`CREATE INDEX IF NOT EXISTS idx_gate_decisions_conversation ON gate_decisions(conversation_id, id DESC)`,
	}
	for _, statement := range statements {
		if _, err := s.db.Exec(statement); err != nil {
			return fmt.Errorf("初始化闸门存储索引失败: %w", err)
		}
	}
	return nil
}

// ExpireStalePending 把所有遗留的 waiting 记录标记为 expired。
// 进程重启后内存等待者已不存在，这些命令一律按拦截处理（保守默认）。
func (s *GateStore) ExpireStalePending(ctx context.Context) (int64, error) {
	if err := s.ensureOpen(); err != nil {
		return 0, err
	}
	result, err := s.db.ExecContext(ctx, `UPDATE gate_pending SET status = 'expired' WHERE status = 'waiting'`)
	if err != nil {
		return 0, fmt.Errorf("清理遗留待确认记录失败: %w", err)
	}
	return result.RowsAffected()
}

// RecordPending 写入一条 waiting 状态的待确认记录。
func (s *GateStore) RecordPending(ctx context.Context, entry GatePending) (GatePending, error) {
	if err := s.ensureOpen(); err != nil {
		return GatePending{}, err
	}
	entry.Status = "waiting"
	if entry.CreatedAt.IsZero() {
		entry.CreatedAt = time.Now().UTC()
	}
	result, err := s.db.ExecContext(ctx, `INSERT INTO gate_pending (
		created_at, conversation_id, request_id, api_type, model, rule, command, source, ai_verdict, ai_reason, expires_at, status
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'waiting')`,
		entry.CreatedAt.Format(gateTimeLayout), entry.ConversationID, entry.RequestID, entry.APIType,
		entry.Model, entry.Rule, entry.Command, entry.Source, entry.AIVerdict, entry.AIReason,
		entry.ExpiresAt.Format(gateTimeLayout),
	)
	if err != nil {
		return GatePending{}, fmt.Errorf("写入闸门待确认记录失败: %w", err)
	}
	entry.ID, err = result.LastInsertId()
	if err != nil {
		return GatePending{}, fmt.Errorf("读取闸门待确认 ID 失败: %w", err)
	}
	return entry, nil
}

// ListWaiting 返回当前等待中的待确认记录（按创建时间升序，先到先决）。
func (s *GateStore) ListWaiting(ctx context.Context) ([]GatePending, error) {
	if err := s.ensureOpen(); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, created_at, conversation_id, request_id, api_type, model,
		rule, command, source, ai_verdict, ai_reason, expires_at, status
		FROM gate_pending WHERE status = 'waiting' ORDER BY id ASC`)
	if err != nil {
		return nil, fmt.Errorf("查询闸门待确认记录失败: %w", err)
	}
	defer rows.Close()
	return scanGatePendings(rows)
}

// ResolvePending 原子地把一条 waiting 记录迁移到目标状态。
// 状态竞争（用户决定 vs 超时 vs 断连）由 UPDATE 条件保证只有一个赢家；
// 未命中（已结束）返回 ErrGatePendingNotFound。
func (s *GateStore) ResolvePending(ctx context.Context, id int64, status string) (GatePending, error) {
	if err := s.ensureOpen(); err != nil {
		return GatePending{}, err
	}
	if !validGatePendingStatus(status) {
		return GatePending{}, fmt.Errorf("无效的待确认状态 %q", status)
	}
	result, err := s.db.ExecContext(ctx, `UPDATE gate_pending SET status = ? WHERE id = ? AND status = 'waiting'`, status, id)
	if err != nil {
		return GatePending{}, fmt.Errorf("更新闸门待确认记录失败: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return GatePending{}, fmt.Errorf("读取闸门待确认更新结果失败: %w", err)
	}
	if affected == 0 {
		return GatePending{}, ErrGatePendingNotFound
	}
	return s.GetPending(ctx, id)
}

// GetPending 读取单条待确认记录。
func (s *GateStore) GetPending(ctx context.Context, id int64) (GatePending, error) {
	if err := s.ensureOpen(); err != nil {
		return GatePending{}, err
	}
	row := s.db.QueryRowContext(ctx, `SELECT id, created_at, conversation_id, request_id, api_type, model,
		rule, command, source, ai_verdict, ai_reason, expires_at, status
		FROM gate_pending WHERE id = ?`, id)
	entry, err := scanGatePending(row)
	if errors.Is(err, sql.ErrNoRows) {
		return GatePending{}, ErrGatePendingNotFound
	}
	return entry, err
}

// AddDecision 写入一条会话级白/黑名单决定。
func (s *GateStore) AddDecision(ctx context.Context, entry GateDecision) (GateDecision, error) {
	if err := s.ensureOpen(); err != nil {
		return GateDecision{}, err
	}
	entry.Decision = strings.ToLower(strings.TrimSpace(entry.Decision))
	if entry.Decision != GateDecisionAllow && entry.Decision != GateDecisionBlock {
		return GateDecision{}, fmt.Errorf("无效的闸门名单决定 %q", entry.Decision)
	}
	entry.ConversationID = strings.TrimSpace(entry.ConversationID)
	if entry.ConversationID == "" {
		return GateDecision{}, fmt.Errorf("闸门名单决定缺少会话 ID")
	}
	if entry.CreatedAt.IsZero() {
		entry.CreatedAt = time.Now().UTC()
	}
	result, err := s.db.ExecContext(ctx, `INSERT INTO gate_decisions (
		conversation_id, decision, rule, command_snippet, command_hash, created_at
	) VALUES (?, ?, ?, ?, ?, ?)`,
		entry.ConversationID, entry.Decision, entry.Rule, entry.CommandSnippet, entry.CommandHash,
		entry.CreatedAt.Format(gateTimeLayout),
	)
	if err != nil {
		return GateDecision{}, fmt.Errorf("写入闸门名单决定失败: %w", err)
	}
	entry.ID, err = result.LastInsertId()
	if err != nil {
		return GateDecision{}, fmt.Errorf("读取闸门名单决定 ID 失败: %w", err)
	}
	return entry, nil
}

// LatestDecisionForConversation 返回会话的最新名单决定；没有决定时 ok=false。
func (s *GateStore) LatestDecisionForConversation(ctx context.Context, conversationID string) (GateDecision, bool, error) {
	if err := s.ensureOpen(); err != nil {
		return GateDecision{}, false, err
	}
	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" {
		return GateDecision{}, false, nil
	}
	row := s.db.QueryRowContext(ctx, `SELECT id, conversation_id, decision, rule, command_snippet, command_hash, created_at
		FROM gate_decisions WHERE conversation_id = ? ORDER BY id DESC LIMIT 1`, conversationID)
	entry, err := scanGateDecision(row)
	if errors.Is(err, sql.ErrNoRows) {
		return GateDecision{}, false, nil
	}
	if err != nil {
		return GateDecision{}, false, err
	}
	return entry, true, nil
}

// ListDecisions 返回全部会话级名单决定（每会话只取最新一条），按时间倒序。
func (s *GateStore) ListDecisions(ctx context.Context) ([]GateDecision, error) {
	if err := s.ensureOpen(); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT d.id, d.conversation_id, d.decision, d.rule, d.command_snippet, d.command_hash, d.created_at
		FROM gate_decisions d
		JOIN (SELECT conversation_id, MAX(id) AS max_id FROM gate_decisions GROUP BY conversation_id) latest
			ON d.id = latest.max_id
		ORDER BY d.id DESC`)
	if err != nil {
		return nil, fmt.Errorf("查询闸门名单失败: %w", err)
	}
	defer rows.Close()
	decisions := make([]GateDecision, 0)
	for rows.Next() {
		entry, err := scanGateDecision(rows)
		if err != nil {
			return nil, err
		}
		decisions = append(decisions, entry)
	}
	return decisions, rows.Err()
}

// RemoveDecisionsForConversation 删除会话的全部名单决定（解除名单）。
func (s *GateStore) RemoveDecisionsForConversation(ctx context.Context, conversationID string) (int64, error) {
	if err := s.ensureOpen(); err != nil {
		return 0, err
	}
	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" {
		return 0, fmt.Errorf("会话 ID 不能为空")
	}
	result, err := s.db.ExecContext(ctx, `DELETE FROM gate_decisions WHERE conversation_id = ?`, conversationID)
	if err != nil {
		return 0, fmt.Errorf("删除闸门名单失败: %w", err)
	}
	return result.RowsAffected()
}

// Close 幂等关闭数据库连接。
func (s *GateStore) Close() error {
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

func (s *GateStore) ensureOpen() error {
	if s == nil || s.db == nil {
		return ErrGateStoreClosed
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return ErrGateStoreClosed
	}
	return nil
}

func validGatePendingStatus(status string) bool {
	switch status {
	case "waiting", "allowed", "blocked", "expired", "cancelled":
		return true
	default:
		return false
	}
}

func scanGatePendings(rows *sql.Rows) ([]GatePending, error) {
	entries := make([]GatePending, 0)
	for rows.Next() {
		entry, err := scanGatePending(rows)
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, rows.Err()
}

func scanGatePending(scanner interface{ Scan(...any) error }) (GatePending, error) {
	var entry GatePending
	var createdAt, expiresAt string
	if err := scanner.Scan(&entry.ID, &createdAt, &entry.ConversationID, &entry.RequestID, &entry.APIType,
		&entry.Model, &entry.Rule, &entry.Command, &entry.Source, &entry.AIVerdict, &entry.AIReason,
		&expiresAt, &entry.Status); err != nil {
		return GatePending{}, err
	}
	var err error
	if entry.CreatedAt, err = time.Parse(gateTimeLayout, createdAt); err != nil {
		return GatePending{}, fmt.Errorf("解析闸门待确认创建时间失败: %w", err)
	}
	if entry.ExpiresAt, err = time.Parse(gateTimeLayout, expiresAt); err != nil {
		return GatePending{}, fmt.Errorf("解析闸门待确认截止时间失败: %w", err)
	}
	return entry, nil
}

func scanGateDecision(scanner interface{ Scan(...any) error }) (GateDecision, error) {
	var entry GateDecision
	var createdAt string
	if err := scanner.Scan(&entry.ID, &entry.ConversationID, &entry.Decision, &entry.Rule,
		&entry.CommandSnippet, &entry.CommandHash, &createdAt); err != nil {
		return GateDecision{}, err
	}
	var err error
	if entry.CreatedAt, err = time.Parse(gateTimeLayout, createdAt); err != nil {
		return GateDecision{}, fmt.Errorf("解析闸门名单决定时间失败: %w", err)
	}
	return entry, nil
}
