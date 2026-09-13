package sensitive

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

const (
	BlockTypeSensitiveWord = "sensitive_word"
	BlockTypeSensitiveInfo = "sensitive_info"
	BlockTypeCredential    = "credential"
	BlockTypeDangerousCmd  = "dangerous_cmd"
	BlockTypeWhitelist     = "whitelist"
	// BlockTypeDangerousCmdAI 危险命令经 AI 闸门判定后的拦截
	//（AI 判危险且用户未放行 / AI 失败保守拦截 / 事后否决）。
	BlockTypeDangerousCmdAI = "dangerous_cmd_ai"
	// BlockTypeDangerousCmdAllow 危险命令经 AI 闸门放行的审计事件
	//（语义对齐 BlockTypeWhitelist 的"已放行"条目）。
	BlockTypeDangerousCmdAllow = "dangerous_cmd_allow"
	// BlockTypeAllowlist 字符串级放行字面量命中的审计事件（整段相等跳过检测）。
	BlockTypeAllowlist = "allowlist"
	// BlockTypeCustomRule 自定义规则可逆脱敏的审计事件（占位符已替换）。
	BlockTypeCustomRule = "custom_rule"
	// BlockTypeRestoreMiss 还原时遇到持久库中不存在的占位符（如重启后密钥
	// 已轮换），原样下发的留痕事件。
	BlockTypeRestoreMiss = "restore_miss"

	defaultBlockedLogPageSize = 20
	maxBlockedLogPageSize     = 100
	maxPromptSnippetRunes     = 500
	blockedLogTimeLayout      = "2006-01-02T15:04:05.000000000Z07:00"
)

var ErrBlockedStoreClosed = errors.New("拦截记录存储已关闭")

// ErrInvalidBlockedLogGroupKey 表示调用方传入的组标识格式非法。
var ErrInvalidBlockedLogGroupKey = errors.New("无效的拦截记录组标识")

// CommandAIReview 保存模型的实际复核结论，独立于最终的名单/人工裁决。
// RequestID 指向日志库中的内部模型调用；复用结论时仍指向原复核记录。
type CommandAIReview struct {
	RequestID   string `json:"requestId"`
	Model       string `json:"model,omitempty"`
	ChannelName string `json:"channelName,omitempty"`
	Verdict     string `json:"verdict"`
	Reason      string `json:"reason"`
	DurationMs  int64  `json:"durationMs"`
	Reused      bool   `json:"reused,omitempty"`
}

// BlockedLog 表示一次内容安全拦截或掩码事件。
type BlockedLog struct {
	ID             int64            `json:"id"`
	Timestamp      time.Time        `json:"timestamp"`
	APIType        string           `json:"apiType"`
	BlockType      string           `json:"blockType"`
	RuleName       string           `json:"ruleName,omitempty"`
	PromptSnippet  string           `json:"promptSnippet,omitempty"`
	Command        string           `json:"command,omitempty"`
	CommandHash    string           `json:"commandHash,omitempty"`
	ChannelName    string           `json:"channelName,omitempty"`
	Model          string           `json:"model,omitempty"`
	RequestID      string           `json:"requestId,omitempty"`
	ConversationID string           `json:"conversationId,omitempty"`
	CreatedAt      time.Time        `json:"createdAt"`
	AIReview       *CommandAIReview `json:"aiReview,omitempty"`
}

// 危险命令类拦截类别（拦截记录页「危险命令相关」筛选项）：
// 覆盖纯规则拦截、AI 闸门拦截与 AI 闸门放行三类事件。
const BlockCategoryDangerousCmd = "dangerous_cmd"

var blockCategoryTypes = map[string][]string{
	BlockCategoryDangerousCmd: {BlockTypeDangerousCmd, BlockTypeDangerousCmdAI, BlockTypeDangerousCmdAllow},
}

// BlockedLogListOptions 描述拦截记录的筛选与分页参数。
type BlockedLogListOptions struct {
	APIType   string
	BlockType string
	// Category 是按类别聚合的多类型筛选（如 dangerous_cmd 命中
	// dangerous_cmd / dangerous_cmd_ai / dangerous_cmd_allow），
	// 与 BlockType 互斥使用，同时给出时返回错误。
	Category string
	// Conversations 限定会话范围（如「活跃会话」筛选取当前活跃会话 ID 集）。
	// 非 nil 且为空切片时匹配不到任何记录。
	Conversations []string
	From          time.Time
	To            time.Time
	Page          int
	PageSize      int
}

// BlockedLogPage 是分页查询结果。
type BlockedLogPage struct {
	Logs     []BlockedLog `json:"logs"`
	Total    int64        `json:"total"`
	Page     int          `json:"page"`
	PageSize int          `json:"pageSize"`
}

// BlockedLogGroup 表示同一会话下的全部内容安全事件。没有会话 ID 的旧记录
// 只按请求 ID 合并；请求 ID 也缺失时每条记录保持独立。
// 列表接口只返回组摘要与组内最新一条代表记录，组内明细通过
// ListGroupEntries 按需分页加载，避免大组全量传输导致前端卡死。
type BlockedLogGroup struct {
	Key             string      `json:"key"`
	ConversationID  string      `json:"conversationId,omitempty"`
	LatestTimestamp time.Time   `json:"latestTimestamp"`
	Count           int64       `json:"count"`
	APITypes        []string    `json:"apiTypes"`
	BlockTypes      []string    `json:"blockTypes"`
	LatestLog       *BlockedLog `json:"latestLog,omitempty"`
}

// BlockedLogGroupPage 按会话分页。Total 是命中过滤条件的记录总数，
// TotalGroups 是分页使用的会话组总数。
type BlockedLogGroupPage struct {
	Groups      []BlockedLogGroup `json:"groups"`
	Total       int64             `json:"total"`
	TotalGroups int64             `json:"totalGroups"`
	Page        int               `json:"page"`
	PageSize    int               `json:"pageSize"`
}

// BlockedStore 使用独立 blocked_logs 表持久化内容安全事件。
type BlockedStore struct {
	db        *sql.DB
	mu        sync.RWMutex
	closed    bool
	closeOnce sync.Once
}

// NewBlockedStore 创建并初始化拦截记录存储。
func NewBlockedStore(path string) (*BlockedStore, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, fmt.Errorf("拦截记录数据库路径不能为空")
	}
	if path != ":memory:" {
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return nil, fmt.Errorf("创建拦截记录数据库目录失败: %w", err)
		}
	}
	dsn := path + "?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(5000)"
	if path == ":memory:" {
		dsn = "file:blocked_logs?mode=memory&cache=shared&_pragma=busy_timeout(5000)"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("打开拦截记录数据库失败: %w", err)
	}
	db.SetMaxOpenConns(1)
	store := &BlockedStore{db: db}
	if err := store.initSchema(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (s *BlockedStore) initSchema() error {
	if _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS blocked_logs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			timestamp TEXT NOT NULL,
			api_type TEXT NOT NULL,
			block_type TEXT NOT NULL,
			rule_name TEXT NOT NULL DEFAULT '',
			prompt_snippet TEXT NOT NULL DEFAULT '',
			channel_name TEXT NOT NULL DEFAULT '',
			model TEXT NOT NULL DEFAULT '',
			request_id TEXT NOT NULL DEFAULT '',
			conversation_id TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL
		)`); err != nil {
		return fmt.Errorf("初始化拦截记录表失败: %w", err)
	}
	if err := s.ensureColumn("blocked_logs", "conversation_id", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return fmt.Errorf("迁移拦截记录会话字段失败: %w", err)
	}
	if err := s.ensureColumn("blocked_logs", "command", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return fmt.Errorf("迁移拦截记录命令字段失败: %w", err)
	}
	if err := s.ensureColumn("blocked_logs", "ai_review_json", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return fmt.Errorf("迁移命令 AI 复核记录失败: %w", err)
	}
	if err := s.ensureColumn("blocked_logs", "command_hash", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return fmt.Errorf("迁移命令授权身份失败: %w", err)
	}
	statements := []string{
		`CREATE INDEX IF NOT EXISTS idx_blocked_logs_timestamp ON blocked_logs(timestamp DESC, id DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_blocked_logs_type_timestamp ON blocked_logs(block_type, timestamp DESC, id DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_blocked_logs_api_timestamp ON blocked_logs(api_type, timestamp DESC, id DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_blocked_logs_conversation_timestamp ON blocked_logs(conversation_id, timestamp DESC, id DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_blocked_logs_unassigned_request ON blocked_logs(request_id) WHERE conversation_id = ''`,
		`CREATE TRIGGER IF NOT EXISTS blocked_logs_capacity AFTER INSERT ON blocked_logs BEGIN
			DELETE FROM blocked_logs WHERE id <= NEW.id - 100000;
		END`,
	}
	for _, statement := range statements {
		if _, err := s.db.Exec(statement); err != nil {
			return fmt.Errorf("初始化拦截记录表失败: %w", err)
		}
	}
	return nil
}

// Record 写入一条拦截记录并返回持久化后的数据。
func (s *BlockedStore) Record(ctx context.Context, entry BlockedLog) (BlockedLog, error) {
	if err := s.ensureOpen(); err != nil {
		return BlockedLog{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	entry, err := normalizeBlockedLog(entry)
	if err != nil {
		return BlockedLog{}, err
	}
	if entry.Timestamp.IsZero() {
		entry.Timestamp = time.Now().UTC()
	} else {
		entry.Timestamp = entry.Timestamp.UTC()
	}
	entry.CreatedAt = time.Now().UTC()
	reviewJSON := ""
	if entry.AIReview != nil {
		encoded, err := json.Marshal(entry.AIReview)
		if err != nil {
			return BlockedLog{}, fmt.Errorf("序列化命令 AI 复核记录失败: %w", err)
		}
		reviewJSON = string(encoded)
	}
	result, err := s.db.ExecContext(ctx, `INSERT INTO blocked_logs (
		timestamp, api_type, block_type, rule_name, prompt_snippet, command, channel_name, model, request_id, conversation_id, created_at, ai_review_json, command_hash
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		entry.Timestamp.Format(blockedLogTimeLayout), entry.APIType, entry.BlockType, entry.RuleName,
		entry.PromptSnippet, entry.Command, entry.ChannelName, entry.Model, entry.RequestID, entry.ConversationID, entry.CreatedAt.Format(blockedLogTimeLayout), reviewJSON, entry.CommandHash,
	)
	if err != nil {
		return BlockedLog{}, fmt.Errorf("写入拦截记录失败: %w", err)
	}
	entry.ID, err = result.LastInsertId()
	if err != nil {
		return BlockedLog{}, fmt.Errorf("读取拦截记录 ID 失败: %w", err)
	}
	return entry, nil
}

// AssignConversation 将同一 HTTP 请求已经写入的内容安全事件关联到内部会话。
// 仅补空值，防止重试或错误调用把已归属的记录迁移到其他会话。
func (s *BlockedStore) AssignConversation(ctx context.Context, requestID, conversationID string) error {
	if err := s.ensureOpen(); err != nil {
		return err
	}
	requestID = strings.TrimSpace(requestID)
	conversationID = strings.TrimSpace(conversationID)
	if requestID == "" {
		return fmt.Errorf("关联拦截记录时请求 ID 不能为空")
	}
	if conversationID == "" {
		return fmt.Errorf("关联拦截记录时会话 ID 不能为空")
	}
	if len([]rune(requestID)) > 256 || len([]rune(conversationID)) > 256 {
		return fmt.Errorf("关联拦截记录的请求 ID 或会话 ID 不能超过 256 个字符")
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE blocked_logs SET conversation_id = ?
		WHERE request_id = ? AND conversation_id = ''`, conversationID, requestID); err != nil {
		return fmt.Errorf("关联拦截记录会话失败: %w", err)
	}
	return nil
}

// List 按条件分页查询拦截记录。
func (s *BlockedStore) List(ctx context.Context, options BlockedLogListOptions) (BlockedLogPage, error) {
	if err := s.ensureOpen(); err != nil {
		return BlockedLogPage{}, err
	}
	options, err := normalizeBlockedLogListOptions(options)
	if err != nil {
		return BlockedLogPage{}, err
	}
	where, args := blockedLogWhere(options)
	var total int64
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM blocked_logs`+where, args...).Scan(&total); err != nil {
		return BlockedLogPage{}, fmt.Errorf("统计拦截记录失败: %w", err)
	}

	queryArgs := append(append([]any(nil), args...), options.PageSize, (options.Page-1)*options.PageSize)
	rows, err := s.db.QueryContext(ctx, `SELECT id, timestamp, api_type, block_type, rule_name,
		prompt_snippet, command, channel_name, model, request_id, conversation_id, created_at, ai_review_json, command_hash
		FROM blocked_logs`+where+` ORDER BY timestamp DESC, id DESC LIMIT ? OFFSET ?`, queryArgs...)
	if err != nil {
		return BlockedLogPage{}, fmt.Errorf("查询拦截记录失败: %w", err)
	}
	defer rows.Close()

	logs := make([]BlockedLog, 0, options.PageSize)
	for rows.Next() {
		entry, err := scanBlockedLog(rows)
		if err != nil {
			return BlockedLogPage{}, err
		}
		logs = append(logs, entry)
	}
	if err := rows.Err(); err != nil {
		return BlockedLogPage{}, fmt.Errorf("读取拦截记录失败: %w", err)
	}
	return BlockedLogPage{Logs: logs, Total: total, Page: options.Page, PageSize: options.PageSize}, nil
}

const blockedLogGroupKeySQL = `CASE
	WHEN conversation_id <> '' THEN 'conversation:' || conversation_id
	WHEN request_id <> '' THEN 'request:' || request_id
	ELSE 'record:' || CAST(id AS TEXT)
END`

// ListGrouped 按会话组分页，只返回组摘要（总数、类型集合、最新一条代表记录）。
// 组内明细由 ListGroupEntries 按需分页加载。
func (s *BlockedStore) ListGrouped(ctx context.Context, options BlockedLogListOptions) (BlockedLogGroupPage, error) {
	if err := s.ensureOpen(); err != nil {
		return BlockedLogGroupPage{}, err
	}
	options, err := normalizeBlockedLogListOptions(options)
	if err != nil {
		return BlockedLogGroupPage{}, err
	}
	where, args := blockedLogWhere(options)
	var total int64
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM blocked_logs`+where, args...).Scan(&total); err != nil {
		return BlockedLogGroupPage{}, fmt.Errorf("统计拦截记录失败: %w", err)
	}
	var totalGroups int64
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(DISTINCT `+blockedLogGroupKeySQL+`) FROM blocked_logs`+where, args...).Scan(&totalGroups); err != nil {
		return BlockedLogGroupPage{}, fmt.Errorf("统计拦截记录会话数失败: %w", err)
	}

	// GROUP_CONCAT 的 DISTINCT 聚合产出组内出现过的类型集合；
	// api_type 与 block_type 均为受限枚举值，不会包含分隔符逗号。
	queryArgs := append(append([]any(nil), args...), options.PageSize, (options.Page-1)*options.PageSize)
	rows, err := s.db.QueryContext(ctx, `SELECT `+blockedLogGroupKeySQL+` AS group_key,
		MAX(timestamp) AS latest_timestamp, MAX(id) AS latest_id, COUNT(*) AS entry_count,
		GROUP_CONCAT(DISTINCT api_type) AS api_types, GROUP_CONCAT(DISTINCT block_type) AS block_types
		FROM blocked_logs`+where+`
		GROUP BY group_key
		ORDER BY latest_timestamp DESC, latest_id DESC LIMIT ? OFFSET ?`, queryArgs...)
	if err != nil {
		return BlockedLogGroupPage{}, fmt.Errorf("查询拦截记录会话失败: %w", err)
	}
	type groupSummary struct {
		key        string
		latest     time.Time
		latestID   int64
		count      int64
		apiTypes   []string
		blockTypes []string
	}
	summaries := make([]groupSummary, 0, options.PageSize)
	for rows.Next() {
		var summary groupSummary
		var latest string
		var apiTypes, blockTypes string
		if err := rows.Scan(&summary.key, &latest, &summary.latestID, &summary.count, &apiTypes, &blockTypes); err != nil {
			_ = rows.Close()
			return BlockedLogGroupPage{}, fmt.Errorf("读取拦截记录会话失败: %w", err)
		}
		summary.latest, err = time.Parse(blockedLogTimeLayout, latest)
		if err != nil {
			_ = rows.Close()
			return BlockedLogGroupPage{}, fmt.Errorf("解析拦截记录会话时间失败: %w", err)
		}
		summary.apiTypes = splitGroupConcatValues(apiTypes)
		summary.blockTypes = splitGroupConcatValues(blockTypes)
		summaries = append(summaries, summary)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return BlockedLogGroupPage{}, fmt.Errorf("读取拦截记录会话失败: %w", err)
	}
	if err := rows.Close(); err != nil {
		return BlockedLogGroupPage{}, fmt.Errorf("关闭拦截记录会话结果失败: %w", err)
	}
	page := BlockedLogGroupPage{
		Groups: make([]BlockedLogGroup, 0, len(summaries)), Total: total, TotalGroups: totalGroups,
		Page: options.Page, PageSize: options.PageSize,
	}
	if len(summaries) == 0 {
		return page, nil
	}

	latestIDs := make([]int64, len(summaries))
	for index, summary := range summaries {
		latestIDs[index] = summary.latestID
	}
	latestLogs, err := s.blockedLogsByIDs(ctx, latestIDs)
	if err != nil {
		return BlockedLogGroupPage{}, err
	}
	for _, summary := range summaries {
		group := BlockedLogGroup{
			Key: summary.key, LatestTimestamp: summary.latest, Count: summary.count,
			APITypes: summary.apiTypes, BlockTypes: summary.blockTypes,
		}
		if latest, ok := latestLogs[summary.latestID]; ok {
			group.ConversationID = latest.ConversationID
			group.LatestLog = &latest
		}
		page.Groups = append(page.Groups, group)
	}
	return page, nil
}

// ListGroupEntries 分页返回单个组内命中过滤条件的明细记录。
// groupKey 是 ListGrouped 返回的组标识。
func (s *BlockedStore) ListGroupEntries(ctx context.Context, options BlockedLogListOptions, groupKey string) (BlockedLogPage, error) {
	if err := s.ensureOpen(); err != nil {
		return BlockedLogPage{}, err
	}
	groupKey = strings.TrimSpace(groupKey)
	if !validBlockedLogGroupKey(groupKey) {
		return BlockedLogPage{}, fmt.Errorf("%w: %q", ErrInvalidBlockedLogGroupKey, groupKey)
	}
	options, err := normalizeBlockedLogListOptions(options)
	if err != nil {
		return BlockedLogPage{}, err
	}
	where, args := blockedLogWhere(options)
	if where == "" {
		where = " WHERE "
	} else {
		where += " AND "
	}
	where += blockedLogGroupKeySQL + " = ?"
	args = append(args, groupKey)

	var total int64
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM blocked_logs`+where, args...).Scan(&total); err != nil {
		return BlockedLogPage{}, fmt.Errorf("统计拦截记录明细失败: %w", err)
	}

	queryArgs := append(append([]any(nil), args...), options.PageSize, (options.Page-1)*options.PageSize)
	rows, err := s.db.QueryContext(ctx, `SELECT id, timestamp, api_type, block_type, rule_name,
		prompt_snippet, command, channel_name, model, request_id, conversation_id, created_at, ai_review_json, command_hash
		FROM blocked_logs`+where+` ORDER BY timestamp DESC, id DESC LIMIT ? OFFSET ?`, queryArgs...)
	if err != nil {
		return BlockedLogPage{}, fmt.Errorf("查询拦截记录明细失败: %w", err)
	}
	defer rows.Close()

	logs := make([]BlockedLog, 0, options.PageSize)
	for rows.Next() {
		entry, err := scanBlockedLog(rows)
		if err != nil {
			return BlockedLogPage{}, err
		}
		logs = append(logs, entry)
	}
	if err := rows.Err(); err != nil {
		return BlockedLogPage{}, fmt.Errorf("读取拦截记录明细失败: %w", err)
	}
	return BlockedLogPage{Logs: logs, Total: total, Page: options.Page, PageSize: options.PageSize}, nil
}

// blockedLogsByIDs 按 ID 批量读取记录，返回 ID 到记录的映射。
func (s *BlockedStore) blockedLogsByIDs(ctx context.Context, ids []int64) (map[int64]BlockedLog, error) {
	placeholders := make([]string, len(ids))
	args := make([]any, len(ids))
	for index, id := range ids {
		placeholders[index] = "?"
		args[index] = id
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, timestamp, api_type, block_type, rule_name,
		prompt_snippet, command, channel_name, model, request_id, conversation_id, created_at, ai_review_json, command_hash
		FROM blocked_logs WHERE id IN (`+strings.Join(placeholders, ",")+`)`, args...)
	if err != nil {
		return nil, fmt.Errorf("查询拦截记录代表记录失败: %w", err)
	}
	defer rows.Close()
	result := make(map[int64]BlockedLog, len(ids))
	for rows.Next() {
		entry, err := scanBlockedLog(rows)
		if err != nil {
			return nil, err
		}
		result[entry.ID] = entry
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("读取拦截记录代表记录失败: %w", err)
	}
	return result, nil
}

// Get 返回指定 ID 的拦截记录。
func (s *BlockedStore) Get(ctx context.Context, id int64) (BlockedLog, bool, error) {
	if err := s.ensureOpen(); err != nil {
		return BlockedLog{}, false, err
	}
	if id <= 0 {
		return BlockedLog{}, false, fmt.Errorf("拦截记录 ID 必须大于 0")
	}
	entry, err := scanBlockedLog(s.db.QueryRowContext(ctx, `SELECT id, timestamp, api_type, block_type, rule_name,
		prompt_snippet, command, channel_name, model, request_id, conversation_id, created_at, ai_review_json, command_hash FROM blocked_logs WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return BlockedLog{}, false, nil
	}
	if err != nil {
		return BlockedLog{}, false, err
	}
	return entry, true, nil
}

// Delete 删除指定 ID 的拦截记录。
func (s *BlockedStore) Delete(ctx context.Context, id int64) (bool, error) {
	if err := s.ensureOpen(); err != nil {
		return false, err
	}
	if id <= 0 {
		return false, fmt.Errorf("拦截记录 ID 必须大于 0")
	}
	result, err := s.db.ExecContext(ctx, `DELETE FROM blocked_logs WHERE id = ?`, id)
	if err != nil {
		return false, fmt.Errorf("删除拦截记录失败: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("读取删除结果失败: %w", err)
	}
	return count > 0, nil
}

// Clear 清空全部拦截记录并返回删除数量。
func (s *BlockedStore) Clear(ctx context.Context) (int64, error) {
	if err := s.ensureOpen(); err != nil {
		return 0, err
	}
	result, err := s.db.ExecContext(ctx, `DELETE FROM blocked_logs`)
	if err != nil {
		return 0, fmt.Errorf("清空拦截记录失败: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("读取清空结果失败: %w", err)
	}
	return count, nil
}

// Close 幂等关闭数据库连接。
func (s *BlockedStore) Close() error {
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

func (s *BlockedStore) ensureOpen() error {
	if s == nil || s.db == nil {
		return ErrBlockedStoreClosed
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return ErrBlockedStoreClosed
	}
	return nil
}

func normalizeBlockedLog(entry BlockedLog) (BlockedLog, error) {
	entry.APIType = strings.ToLower(strings.TrimSpace(entry.APIType))
	entry.BlockType = strings.ToLower(strings.TrimSpace(entry.BlockType))
	entry.RuleName = strings.TrimSpace(entry.RuleName)
	entry.PromptSnippet = truncatePromptSnippet(RedactAuditText(strings.TrimSpace(entry.PromptSnippet)))
	entry.Command = truncateAuditText(RedactAuditText(strings.TrimSpace(entry.Command)), 4096)
	if entry.AIReview != nil {
		review := *entry.AIReview
		review.Reason = truncateAuditText(RedactAuditText(review.Reason), 1024)
		entry.AIReview = &review
	}
	entry.ChannelName = strings.TrimSpace(entry.ChannelName)
	entry.Model = strings.TrimSpace(entry.Model)
	entry.RequestID = strings.TrimSpace(entry.RequestID)
	entry.ConversationID = strings.TrimSpace(entry.ConversationID)
	if !validBlockedLogAPIType(entry.APIType) {
		return BlockedLog{}, fmt.Errorf("不支持的 API 类型 %q", entry.APIType)
	}
	if !validBlockType(entry.BlockType) {
		return BlockedLog{}, fmt.Errorf("不支持的拦截类型 %q", entry.BlockType)
	}
	for label, value := range map[string]string{
		"规则名": entry.RuleName, "渠道名": entry.ChannelName, "模型": entry.Model,
		"请求 ID": entry.RequestID, "会话 ID": entry.ConversationID,
	} {
		if len([]rune(value)) > 256 {
			return BlockedLog{}, fmt.Errorf("%s不能超过 256 个字符", label)
		}
	}
	return entry, nil
}

func normalizeBlockedLogListOptions(options BlockedLogListOptions) (BlockedLogListOptions, error) {
	options.APIType = strings.ToLower(strings.TrimSpace(options.APIType))
	options.BlockType = strings.ToLower(strings.TrimSpace(options.BlockType))
	options.Category = strings.ToLower(strings.TrimSpace(options.Category))
	if options.APIType != "" && !validBlockedLogAPIType(options.APIType) {
		return BlockedLogListOptions{}, fmt.Errorf("不支持的 API 类型 %q", options.APIType)
	}
	if options.BlockType != "" && !validBlockType(options.BlockType) {
		return BlockedLogListOptions{}, fmt.Errorf("不支持的拦截类型 %q", options.BlockType)
	}
	if options.Category != "" {
		if _, ok := blockCategoryTypes[options.Category]; !ok {
			return BlockedLogListOptions{}, fmt.Errorf("不支持的拦截类别 %q", options.Category)
		}
		if options.BlockType != "" {
			return BlockedLogListOptions{}, fmt.Errorf("拦截类型与类别不能同时筛选")
		}
	}
	if options.Page == 0 {
		options.Page = 1
	}
	if options.Page < 1 {
		return BlockedLogListOptions{}, fmt.Errorf("页码必须大于 0")
	}
	if options.PageSize == 0 {
		options.PageSize = defaultBlockedLogPageSize
	}
	if options.PageSize < 1 || options.PageSize > maxBlockedLogPageSize {
		return BlockedLogListOptions{}, fmt.Errorf("每页数量必须在 1 到 %d 之间", maxBlockedLogPageSize)
	}
	if !options.From.IsZero() {
		options.From = options.From.UTC()
	}
	if !options.To.IsZero() {
		options.To = options.To.UTC()
	}
	if !options.From.IsZero() && !options.To.IsZero() && options.From.After(options.To) {
		return BlockedLogListOptions{}, fmt.Errorf("开始时间不能晚于结束时间")
	}
	return options, nil
}

func blockedLogWhere(options BlockedLogListOptions) (string, []any) {
	conditions := make([]string, 0, 4)
	args := make([]any, 0, 4)
	if options.APIType != "" {
		conditions = append(conditions, "api_type = ?")
		args = append(args, options.APIType)
	}
	if options.BlockType != "" {
		conditions = append(conditions, "block_type = ?")
		args = append(args, options.BlockType)
	}
	if options.Category != "" {
		placeholders := make([]string, 0, len(blockCategoryTypes[options.Category]))
		for _, blockType := range blockCategoryTypes[options.Category] {
			placeholders = append(placeholders, "?")
			args = append(args, blockType)
		}
		conditions = append(conditions, "block_type IN ("+strings.Join(placeholders, ",")+")")
	}
	if options.Conversations != nil {
		if len(options.Conversations) == 0 {
			// 显式空集：不匹配任何记录（活跃会话为空时的语义）。
			conditions = append(conditions, "1=0")
		} else {
			placeholders := make([]string, 0, len(options.Conversations))
			for _, conversationID := range options.Conversations {
				placeholders = append(placeholders, "?")
				args = append(args, conversationID)
			}
			conditions = append(conditions, "conversation_id IN ("+strings.Join(placeholders, ",")+")")
		}
	}
	if !options.From.IsZero() {
		conditions = append(conditions, "timestamp >= ?")
		args = append(args, options.From.Format(blockedLogTimeLayout))
	}
	if !options.To.IsZero() {
		conditions = append(conditions, "timestamp <= ?")
		args = append(args, options.To.Format(blockedLogTimeLayout))
	}
	if len(conditions) == 0 {
		return "", args
	}
	return " WHERE " + strings.Join(conditions, " AND "), args
}

type blockedLogScanner interface {
	Scan(...any) error
}

func scanBlockedLog(scanner blockedLogScanner) (BlockedLog, error) {
	var entry BlockedLog
	var timestamp string
	var createdAt string
	var reviewJSON string
	if err := scanner.Scan(&entry.ID, &timestamp, &entry.APIType, &entry.BlockType, &entry.RuleName,
		&entry.PromptSnippet, &entry.Command, &entry.ChannelName, &entry.Model, &entry.RequestID, &entry.ConversationID, &createdAt, &reviewJSON, &entry.CommandHash); err != nil {
		return BlockedLog{}, err
	}
	if reviewJSON != "" {
		if err := json.Unmarshal([]byte(reviewJSON), &entry.AIReview); err != nil {
			return BlockedLog{}, fmt.Errorf("解析命令 AI 复核记录失败: %w", err)
		}
	}
	var err error
	entry.Timestamp, err = time.Parse(blockedLogTimeLayout, timestamp)
	if err != nil {
		return BlockedLog{}, fmt.Errorf("解析拦截时间失败: %w", err)
	}
	entry.CreatedAt, err = time.Parse(blockedLogTimeLayout, createdAt)
	if err != nil {
		return BlockedLog{}, fmt.Errorf("解析创建时间失败: %w", err)
	}
	return entry, nil
}

// splitGroupConcatValues 拆分 SQLite GROUP_CONCAT 的逗号拼接结果。
// api_type 与 block_type 均为受限枚举值，不会包含逗号。
func splitGroupConcatValues(value string) []string {
	if value == "" {
		return []string{}
	}
	return strings.Split(value, ",")
}

// validBlockedLogGroupKey 校验组标识格式（conversation:/request:/record: 前缀）。
func validBlockedLogGroupKey(value string) bool {
	if value == "" || len(value) > 300 {
		return false
	}
	prefix, rest, found := strings.Cut(value, ":")
	if !found || rest == "" {
		return false
	}
	switch prefix {
	case "conversation", "request", "record":
		return true
	default:
		return false
	}
}

func (s *BlockedStore) ensureColumn(tableName, columnName, declaration string) error {
	return ensureSQLiteColumn(s.db, tableName, columnName, declaration)
}

func ensureSQLiteColumn(db *sql.DB, tableName, columnName, declaration string) error {
	rows, err := db.Query("PRAGMA table_info(" + tableName + ")")
	if err != nil {
		return err
	}
	found := false
	for rows.Next() {
		var cid int
		var name, dataType string
		var notNull, primaryKey int
		var defaultValue any
		if err := rows.Scan(&cid, &name, &dataType, &notNull, &defaultValue, &primaryKey); err != nil {
			_ = rows.Close()
			return err
		}
		if name == columnName {
			found = true
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if found {
		return nil
	}
	_, err = db.Exec("ALTER TABLE " + tableName + " ADD COLUMN " + columnName + " " + declaration)
	return err
}

func truncatePromptSnippet(value string) string {
	return truncateAuditText(value, maxPromptSnippetRunes)
}

func truncateAuditText(value string, limit int) string {
	count := 0
	cut := len(value)
	for index := range value {
		if count == limit-3 {
			cut = index
		}
		if count == limit {
			return value[:cut] + "..."
		}
		count++
	}
	return value
}

// Purge 保留 90 天内、最近 10 万次写入范围中的审计记录。
func (s *BlockedStore) Purge(ctx context.Context) error {
	if err := s.ensureOpen(); err != nil {
		return err
	}
	cutoff := time.Now().UTC().Add(-90 * 24 * time.Hour).Format(blockedLogTimeLayout)
	if _, err := s.db.ExecContext(ctx, `DELETE FROM blocked_logs WHERE timestamp < ? OR id <= (SELECT COALESCE(MAX(id), 0) - 100000 FROM blocked_logs)`, cutoff); err != nil {
		return fmt.Errorf("清理内容安全审计历史失败: %w", err)
	}
	return nil
}

func validBlockedLogAPIType(value string) bool {
	switch value {
	case "messages", "responses", "chat", "gemini":
		return true
	default:
		return false
	}
}

// ValidBlockType 校验拦截类型的完整枚举（写入与筛选共用，保持两侧一致）。
func ValidBlockType(value string) bool {
	return validBlockType(value)
}

func validBlockType(value string) bool {
	switch value {
	case BlockTypeSensitiveWord, BlockTypeSensitiveInfo, BlockTypeCredential, BlockTypeDangerousCmd, BlockTypeWhitelist,
		BlockTypeDangerousCmdAI, BlockTypeDangerousCmdAllow, BlockTypeAllowlist, BlockTypeCustomRule, BlockTypeRestoreMiss:
		return true
	default:
		return false
	}
}
