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
	// GateDecisionAllow / GateDecisionBlock 是会话内同命令的白/黑名单决定。
	GateDecisionAllow = "allow"
	GateDecisionBlock = "block"

	gateTimeLayout = "2006-01-02T15:04:05.000000000Z07:00"
)

// GatePending 表示一条等待用户决定的危险命令命中。
// status: waiting（等待中）/ allowed（用户放行）/ blocked（用户拦截）/
// expired（等待超时，按拦截处理）/ cancelled（客户端断连，按拦截处理）。
type GatePending struct {
	ID               int64     `json:"id"`
	CreatedAt        time.Time `json:"createdAt"`
	ConversationID   string    `json:"conversationId"`
	RequestID        string    `json:"requestId"`
	APIType          string    `json:"apiType"`
	Model            string    `json:"model,omitempty"`
	Rule             string    `json:"rule"`
	Command          string    `json:"command"`
	CommandHash      string    `json:"commandHash,omitempty"`
	ToolName         string    `json:"toolName,omitempty"`
	ExecutionContext string    `json:"executionContext,omitempty"`
	Source           string    `json:"source"`    // code_block / tool
	AIVerdict        string    `json:"aiVerdict"` // allow / block
	AIReason         string    `json:"aiReason,omitempty"`
	ExpiresAt        time.Time `json:"expiresAt"`
	Status           string    `json:"status"`
}

// GateDecision 只作用于同一会话内 CommandHash 完全相同的命令与上下文。
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
		command_hash TEXT NOT NULL DEFAULT '',
		source TEXT NOT NULL DEFAULT '',
		ai_verdict TEXT NOT NULL DEFAULT '',
		ai_reason TEXT NOT NULL DEFAULT '',
		expires_at TEXT NOT NULL,
		status TEXT NOT NULL DEFAULT 'waiting'
	)`); err != nil {
		return fmt.Errorf("初始化闸门待确认表失败: %w", err)
	}
	if err := ensureSQLiteColumn(s.db, "gate_pending", "command_hash", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return fmt.Errorf("迁移待确认命令身份失败: %w", err)
	}
	for _, column := range []string{"tool_name", "execution_context"} {
		if err := ensureSQLiteColumn(s.db, "gate_pending", column, "TEXT NOT NULL DEFAULT ''"); err != nil {
			return fmt.Errorf("迁移命令执行上下文字段失败: %w", err)
		}
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
		`CREATE INDEX IF NOT EXISTS idx_gate_decisions_command ON gate_decisions(conversation_id, command_hash, id DESC)`,
		`CREATE TRIGGER IF NOT EXISTS gate_pending_capacity AFTER INSERT ON gate_pending BEGIN
			DELETE FROM gate_pending WHERE id <= NEW.id - 10000 AND (status <> 'waiting' OR expires_at <= NEW.created_at);
		END`,
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
	entry.Command = truncateAuditText(RedactAuditText(entry.Command), 16384)
	entry.ExecutionContext = truncateAuditText(RedactAuditText(entry.ExecutionContext), 32768)
	entry.AIReason = truncateAuditText(RedactAuditText(entry.AIReason), 1024)
	if entry.CreatedAt.IsZero() {
		entry.CreatedAt = time.Now().UTC()
	}
	entry.CreatedAt = entry.CreatedAt.UTC()
	entry.ExpiresAt = entry.ExpiresAt.UTC()
	result, err := s.db.ExecContext(ctx, `INSERT INTO gate_pending (
		created_at, conversation_id, request_id, api_type, model, rule, command, command_hash, tool_name, execution_context, source, ai_verdict, ai_reason, expires_at, status
	) SELECT ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?,
		CASE (SELECT decision FROM gate_decisions WHERE conversation_id = ? AND command_hash = ? AND command_hash <> '' ORDER BY id DESC LIMIT 1)
			WHEN 'allow' THEN 'allowed' WHEN 'block' THEN 'blocked' ELSE 'waiting' END
		WHERE (SELECT COUNT(*) FROM gate_pending WHERE status = 'waiting' AND expires_at > ?) < 256`,
		entry.CreatedAt.Format(gateTimeLayout), entry.ConversationID, entry.RequestID, entry.APIType,
		entry.Model, entry.Rule, entry.Command, entry.CommandHash, entry.ToolName, entry.ExecutionContext, entry.Source, entry.AIVerdict, entry.AIReason,
		entry.ExpiresAt.Format(gateTimeLayout), entry.ConversationID, entry.CommandHash, time.Now().UTC().Format(gateTimeLayout),
	)
	if err != nil {
		return GatePending{}, fmt.Errorf("写入闸门待确认记录失败: %w", err)
	}
	if affected, err := result.RowsAffected(); err != nil {
		return GatePending{}, fmt.Errorf("读取待确认写入结果失败: %w", err)
	} else if affected == 0 {
		return GatePending{}, fmt.Errorf("待确认命令已达 256 条上限，请稍后重试")
	}
	entry.ID, err = result.LastInsertId()
	if err != nil {
		return GatePending{}, fmt.Errorf("读取闸门待确认 ID 失败: %w", err)
	}
	return s.GetPending(ctx, entry.ID)
}

// ListWaiting 返回当前等待中的待确认记录（按创建时间升序，先到先决）。
func (s *GateStore) ListWaiting(ctx context.Context) ([]GatePending, error) {
	if err := s.ensureOpen(); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, created_at, conversation_id, request_id, api_type, model,
		rule, command, command_hash, tool_name, execution_context, source, ai_verdict, ai_reason, expires_at, status
		FROM gate_pending WHERE status = 'waiting' AND expires_at > ? ORDER BY id ASC LIMIT 256`, time.Now().UTC().Format(gateTimeLayout))
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
		rule, command, command_hash, tool_name, execution_context, source, ai_verdict, ai_reason, expires_at, status
		FROM gate_pending WHERE id = ?`, id)
	entry, err := scanGatePending(row)
	if errors.Is(err, sql.ErrNoRows) {
		return GatePending{}, ErrGatePendingNotFound
	}
	return entry, err
}

// AddDecision 原子替换同会话同命令的旧决定；容量满时明确拒绝新增授权。
func (s *GateStore) AddDecision(ctx context.Context, entry GateDecision) (GateDecision, []int64, error) {
	if err := s.ensureOpen(); err != nil {
		return GateDecision{}, nil, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return GateDecision{}, nil, fmt.Errorf("开始命令授权事务失败: %w", err)
	}
	defer tx.Rollback()
	entry, err = writeGateDecision(ctx, tx, entry)
	if err != nil {
		return GateDecision{}, nil, err
	}
	resolved, err := resolveCommandPendings(ctx, tx, entry)
	if err != nil {
		return GateDecision{}, nil, err
	}
	if err := tx.Commit(); err != nil {
		return GateDecision{}, nil, fmt.Errorf("提交命令授权失败: %w", err)
	}
	return entry, resolved, nil
}

// resolveCommandPendings 与名单写入共用事务，只结束同会话同命令尚未过期的等待。
func resolveCommandPendings(ctx context.Context, tx *sql.Tx, entry GateDecision) ([]int64, error) {
	status := "blocked"
	if entry.Decision == GateDecisionAllow {
		status = "allowed"
	}
	rows, err := tx.QueryContext(ctx, `UPDATE gate_pending SET status = ?
		WHERE conversation_id = ? AND command_hash = ? AND status = 'waiting' AND expires_at > ? RETURNING id`,
		status, entry.ConversationID, entry.CommandHash, time.Now().UTC().Format(gateTimeLayout))
	if err != nil {
		return nil, fmt.Errorf("同步正在等待的命令决定失败: %w", err)
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("读取受影响的命令确认记录失败: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func writeGateDecision(ctx context.Context, tx *sql.Tx, entry GateDecision) (GateDecision, error) {
	entry.Decision = strings.ToLower(strings.TrimSpace(entry.Decision))
	if entry.Decision != GateDecisionAllow && entry.Decision != GateDecisionBlock {
		return GateDecision{}, fmt.Errorf("无效的闸门名单决定 %q", entry.Decision)
	}
	entry.ConversationID = strings.TrimSpace(entry.ConversationID)
	if entry.ConversationID == "" {
		return GateDecision{}, fmt.Errorf("闸门名单决定缺少会话 ID")
	}
	if strings.TrimSpace(entry.CommandHash) == "" {
		return GateDecision{}, fmt.Errorf("闸门名单决定缺少完整命令身份")
	}
	entry.CommandSnippet = truncateAuditText(RedactAuditText(entry.CommandSnippet), 500)
	if entry.CreatedAt.IsZero() {
		entry.CreatedAt = time.Now().UTC()
	}
	entry.CreatedAt = entry.CreatedAt.UTC()
	if _, err := tx.ExecContext(ctx, "DELETE FROM gate_decisions WHERE conversation_id = ? AND command_hash = ?", entry.ConversationID, entry.CommandHash); err != nil {
		return GateDecision{}, fmt.Errorf("替换旧命令授权失败: %w", err)
	}
	var count int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM gate_decisions WHERE command_hash <> ''").Scan(&count); err != nil {
		return GateDecision{}, fmt.Errorf("检查命令授权容量失败: %w", err)
	}
	if count >= 10000 {
		return GateDecision{}, fmt.Errorf("命令授权已达 10000 条上限，请先解除不再使用的授权")
	}
	result, err := tx.ExecContext(ctx, "INSERT INTO gate_decisions (conversation_id, decision, rule, command_snippet, command_hash, created_at) VALUES (?, ?, ?, ?, ?, ?)",
		entry.ConversationID, entry.Decision, entry.Rule, entry.CommandSnippet, entry.CommandHash, entry.CreatedAt.Format(gateTimeLayout))
	if err != nil {
		return GateDecision{}, fmt.Errorf("写入闸门名单决定失败: %w", err)
	}
	entry.ID, err = result.LastInsertId()
	if err != nil {
		return GateDecision{}, fmt.Errorf("读取闸门名单决定 ID 失败: %w", err)
	}
	return entry, nil
}

// DecidePending 保证状态变更和名单写入同时成功；过期记录不能被迟到的批准复活。
func (s *GateStore) DecidePending(ctx context.Context, id int64, decision string) (GatePending, []int64, error) {
	if err := s.ensureOpen(); err != nil {
		return GatePending{}, nil, err
	}
	decision = strings.ToLower(strings.TrimSpace(decision))
	if decision != GateDecisionAllow && decision != GateDecisionBlock {
		return GatePending{}, nil, fmt.Errorf("无效的闸门决定 %q", decision)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return GatePending{}, nil, fmt.Errorf("开始命令决定事务失败: %w", err)
	}
	defer tx.Rollback()
	pending, err := scanGatePending(tx.QueryRowContext(ctx, "SELECT id, created_at, conversation_id, request_id, api_type, model, rule, command, command_hash, tool_name, execution_context, source, ai_verdict, ai_reason, expires_at, status FROM gate_pending WHERE id = ?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return GatePending{}, nil, ErrGatePendingNotFound
	}
	if err != nil {
		return GatePending{}, nil, err
	}
	if pending.Status != "waiting" || !pending.ExpiresAt.After(time.Now()) {
		return GatePending{}, nil, ErrGatePendingNotFound
	}
	if pending.CommandHash == "" {
		return GatePending{}, nil, fmt.Errorf("旧待确认记录缺少完整命令身份，请重新发起审核")
	}
	if pending.AIVerdict == GateDecisionAllow && decision != GateDecisionBlock {
		return GatePending{}, nil, fmt.Errorf("AI 已放行的命令只接受否决")
	}
	status := "blocked"
	if decision == GateDecisionAllow {
		status = "allowed"
	}
	result, err := tx.ExecContext(ctx, "UPDATE gate_pending SET status = ? WHERE id = ? AND status = 'waiting' AND expires_at > ?", status, id, time.Now().UTC().Format(gateTimeLayout))
	if err != nil {
		return GatePending{}, nil, fmt.Errorf("保存命令决定失败: %w", err)
	}
	if affected, err := result.RowsAffected(); err != nil {
		return GatePending{}, nil, err
	} else if affected == 0 {
		return GatePending{}, nil, ErrGatePendingNotFound
	}
	resolved := []int64{id}
	if strings.TrimSpace(pending.ConversationID) != "" {
		entry, err := writeGateDecision(ctx, tx, GateDecision{
			ConversationID: pending.ConversationID,
			Decision:       decision,
			Rule:           pending.Rule,
			CommandSnippet: pending.Command,
			CommandHash:    pending.CommandHash,
		})
		if err != nil {
			return GatePending{}, nil, err
		}
		others, err := resolveCommandPendings(ctx, tx, entry)
		if err != nil {
			return GatePending{}, nil, err
		}
		resolved = append(resolved, others...)
	}
	if err := tx.Commit(); err != nil {
		return GatePending{}, nil, fmt.Errorf("提交命令决定失败: %w", err)
	}
	pending.Status = status
	return pending, resolved, nil
}

// LatestDecisionForCommand 不把缺少身份的旧会话名单应用到新命令。
func (s *GateStore) LatestDecisionForCommand(ctx context.Context, conversationID, commandHash string) (GateDecision, bool, error) {
	if err := s.ensureOpen(); err != nil {
		return GateDecision{}, false, err
	}
	if strings.TrimSpace(conversationID) == "" || commandHash == "" {
		return GateDecision{}, false, nil
	}
	entry, err := scanGateDecision(s.db.QueryRowContext(ctx,
		"SELECT id, conversation_id, decision, rule, command_snippet, command_hash, created_at FROM gate_decisions WHERE conversation_id = ? AND command_hash = ? ORDER BY id DESC LIMIT 1",
		strings.TrimSpace(conversationID), commandHash))
	if errors.Is(err, sql.ErrNoRows) {
		return GateDecision{}, false, nil
	}
	return entry, err == nil, err
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

// ListDecisions 按会话与命令身份返回最新决定，最多 10000 条有效授权。
func (s *GateStore) ListDecisions(ctx context.Context) ([]GateDecision, error) {
	if err := s.ensureOpen(); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT d.id, d.conversation_id, d.decision, d.rule, d.command_snippet, d.command_hash, d.created_at
		FROM gate_decisions d
		JOIN (SELECT conversation_id, command_hash, MAX(id) AS max_id FROM gate_decisions GROUP BY conversation_id, command_hash) latest
			ON d.id = latest.max_id
		WHERE d.command_hash <> ''
		ORDER BY d.id DESC LIMIT 10001`)
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
	if len(decisions) > 10000 {
		return nil, fmt.Errorf("历史命令名单超过 10000 条，请先清理旧授权")
	}
	return decisions, rows.Err()
}

// Purge 清理已结束的确认历史，不自动撤销仍生效的人工命令决定。
func (s *GateStore) Purge(ctx context.Context) error {
	if err := s.ensureOpen(); err != nil {
		return err
	}
	now := time.Now().UTC()
	if _, err := s.db.ExecContext(ctx, `UPDATE gate_pending SET status = 'expired' WHERE status = 'waiting' AND expires_at <= ?`, now.Format(gateTimeLayout)); err != nil {
		return fmt.Errorf("清理过期待确认记录失败: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM gate_pending WHERE status <> 'waiting' AND (created_at < ? OR id <= (SELECT COALESCE(MAX(id), 0) - 10000 FROM gate_pending))`, now.Add(-7*24*time.Hour).Format(gateTimeLayout)); err != nil {
		return fmt.Errorf("清理命令确认历史失败: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM gate_decisions WHERE command_hash = '' OR id NOT IN (SELECT MAX(id) FROM gate_decisions GROUP BY conversation_id, command_hash)`); err != nil {
		return fmt.Errorf("清理失效的旧命令决定失败: %w", err)
	}
	return nil
}

// RemoveDecisionsForConversation 删除会话的全部名单决定（解除名单）。
func (s *GateStore) RemoveDecisionsForConversation(ctx context.Context, conversationID string, commandHashes ...string) (int64, error) {
	if err := s.ensureOpen(); err != nil {
		return 0, err
	}
	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" {
		return 0, fmt.Errorf("会话 ID 不能为空")
	}
	if len(commandHashes) > 1 {
		return 0, fmt.Errorf("一次只能解除一个命令身份")
	}
	query := `DELETE FROM gate_decisions WHERE conversation_id = ?`
	args := []any{conversationID}
	if len(commandHashes) == 1 {
		if commandHashes[0] == "" {
			return 0, fmt.Errorf("命令身份不能为空")
		}
		query += ` AND command_hash = ?`
		args = append(args, commandHashes[0])
	}
	result, err := s.db.ExecContext(ctx, query, args...)
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
		&entry.Model, &entry.Rule, &entry.Command, &entry.CommandHash, &entry.ToolName, &entry.ExecutionContext, &entry.Source, &entry.AIVerdict, &entry.AIReason,
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
