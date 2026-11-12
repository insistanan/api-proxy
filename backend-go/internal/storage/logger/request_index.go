// 摘要索引与原始日志在同一事务中更新；历史正文和 CLI 格式保持独立。
package logger

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
)

var (
	ErrInvalidLogCursor = errors.New("无效的日志分页游标")
	ErrLogGroupNotFound = errors.New("日志会话或定位请求不存在")
)

type LogConversationSummary struct {
	Key          string           `json:"key"`
	Latest       SystemLogSummary `json:"latest"`
	RequestCount int64            `json:"requestCount"`
	Title        string           `json:"title,omitempty"`
}

type LogConversationsPage struct {
	Conversations []LogConversationSummary `json:"conversations"`
	NextCursor    string                   `json:"nextCursor,omitempty"`
	Total         int64                    `json:"total"`
}

type LogConversationRequestsPage struct {
	Logs       []SystemLogSummary `json:"logs"`
	NextCursor string             `json:"nextCursor,omitempty"`
}

type LogConversationListOptions struct {
	APIType string
	Cursor  string
	Limit   int
	Search  string
	Status  string
	From    int64
	To      int64
}

type LogConversationRequestOptions struct {
	Key    string
	Before string
	Anchor string
	Limit  int
}

type logCursor struct {
	Time int64  `json:"time"`
	Key  string `json:"key"`
}

func encodeLogCursor(stamp int64, key string) (string, error) {
	body, err := json.Marshal(logCursor{Time: stamp, Key: key})
	if err != nil {
		return "", fmt.Errorf("编码日志分页游标失败: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(body), nil
}

func decodeLogCursor(raw string) (logCursor, error) {
	var cursor logCursor
	if len(raw) > 2048 {
		return cursor, ErrInvalidLogCursor
	}
	body, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return cursor, fmt.Errorf("%w: %v", ErrInvalidLogCursor, err)
	}
	if err := json.Unmarshal(body, &cursor); err != nil {
		return cursor, fmt.Errorf("%w: %v", ErrInvalidLogCursor, err)
	}
	if cursor.Time < 0 || cursor.Key == "" {
		return cursor, ErrInvalidLogCursor
	}
	return cursor, nil
}

func logGroupKey(summary SystemLogSummary) string {
	if summary.ConversationID == "" {
		return "request:" + summary.RequestID
	}
	return strings.ToLower(summary.APIType) + ":" + summary.ConversationID
}

func initRequestIndex(db *sql.DB) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS log_request_index (
			request_id TEXT PRIMARY KEY, group_key TEXT NOT NULL, api_type TEXT NOT NULL,
			last_unix INTEGER NOT NULL, last_id INTEGER NOT NULL, started_at INTEGER NOT NULL,
			summary_json TEXT NOT NULL, boot_id TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_log_request_latest ON log_request_index(last_unix DESC, last_id DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_log_request_api_latest ON log_request_index(api_type, last_unix DESC, last_id DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_log_request_started ON log_request_index(started_at DESC, request_id DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_log_request_api_started ON log_request_index(api_type, started_at DESC, request_id DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_log_request_group_order ON log_request_index(group_key, started_at DESC, request_id DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_log_request_group_latest ON log_request_index(group_key, last_unix DESC, last_id DESC)`,
		`CREATE TABLE IF NOT EXISTS log_index_state (singleton INTEGER PRIMARY KEY CHECK(singleton = 1), traffic_id INTEGER NOT NULL, request_id INTEGER NOT NULL)`,
		`INSERT INTO log_index_state VALUES (1, 0, 0) ON CONFLICT(singleton) DO NOTHING`,
		`CREATE TRIGGER IF NOT EXISTS delete_log_request_index AFTER DELETE ON traffic_logs
			BEGIN DELETE FROM log_request_index WHERE request_id = OLD.request_id
			AND NOT EXISTS (SELECT 1 FROM traffic_logs WHERE request_id = OLD.request_id); END`,
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			return fmt.Errorf("初始化日志摘要索引失败: %w", err)
		}
	}
	// 初次升级分批回填；后续启动只补旧程序写入、尚未进入摘要索引的记录。
	var trafficAfter, requestAfter int64
	if err := db.QueryRow(`SELECT traffic_id, request_id FROM log_index_state WHERE singleton = 1`).Scan(&trafficAfter, &requestAfter); err != nil {
		return fmt.Errorf("读取日志索引进度失败: %w", err)
	}
	var trafficEnd, requestEnd int64
	if err := db.QueryRow(`SELECT COALESCE((SELECT MAX(id) FROM traffic_logs), 0), COALESCE((SELECT MAX(id) FROM request_logs), 0)`).Scan(&trafficEnd, &requestEnd); err != nil {
		return fmt.Errorf("读取日志索引回填边界失败: %w", err)
	}
	lastKey := ""
	for {
		rows, err := db.Query(`SELECT request_id FROM (
			SELECT request_id FROM traffic_logs WHERE id > ? AND id <= ?
			UNION SELECT request_id FROM request_logs WHERE id > ? AND id <= ?
		) WHERE request_id != '' AND request_id > ? ORDER BY request_id LIMIT 256`,
			trafficAfter, trafficEnd, requestAfter, requestEnd, lastKey)
		if err != nil {
			return fmt.Errorf("读取待回填日志失败: %w", err)
		}
		ids := make([]string, 0, 256)
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			ids = append(ids, id)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if len(ids) == 0 {
			break
		}
		if err := backfillRequestIndex(db, ids); err != nil {
			return err
		}
		lastKey = ids[len(ids)-1]
	}
	if _, err := db.Exec(`UPDATE log_index_state SET traffic_id = ?, request_id = ? WHERE singleton = 1`, trafficEnd, requestEnd); err != nil {
		return fmt.Errorf("保存日志索引回填进度失败: %w", err)
	}
	// 旧索引曾跳过只有尝试指标的请求；分批补齐，不改写正文或已存在的摘要。
	lastKey = ""
	for {
		rows, err := db.Query(`SELECT DISTINCT r.request_id FROM request_logs r
			WHERE r.request_id != '' AND r.request_id > ?
			AND NOT EXISTS (SELECT 1 FROM log_request_index i WHERE i.request_id = r.request_id)
			ORDER BY r.request_id LIMIT 256`, lastKey)
		if err != nil {
			return fmt.Errorf("读取缺失的调用索引失败: %w", err)
		}
		ids := make([]string, 0, 256)
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			ids = append(ids, id)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if len(ids) == 0 {
			break
		}
		if err := backfillRequestIndex(db, ids); err != nil {
			return err
		}
		lastKey = ids[len(ids)-1]
	}
	return nil
}

func backfillRequestIndex(db *sql.DB, ids []string) error {
	ctx, cancel := context.WithTimeout(context.Background(), logWriteTimeout)
	defer cancel()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("开始日志索引回填失败: %w", err)
	}
	defer tx.Rollback()
	if err := refreshRequestIndex(ctx, tx, ids); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交日志索引回填失败: %w", err)
	}
	return nil
}

func refreshRequestIndex(ctx context.Context, tx *sql.Tx, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	// 手动清理可能跨越很多请求，避免超过 SQLite 的参数数目上限。
	if len(ids) > 256 {
		for offset := 0; offset < len(ids); offset += 256 {
			if err := refreshRequestIndex(ctx, tx, ids[offset:min(offset+256, len(ids))]); err != nil {
				return err
			}
		}
		return nil
	}
	metas, err := loadTrafficRowMetaFrom(ctx, tx, ids)
	if err != nil {
		return err
	}
	requestMetas, err := loadRequestLogMetaFrom(ctx, tx, ids)
	if err != nil {
		return err
	}
	for _, id := range ids {
		rows := metas[id]
		meta := requestMetas[id]
		if len(rows) == 0 && meta == nil {
			if _, err := tx.ExecContext(ctx, `DELETE FROM log_request_index WHERE request_id = ?`, id); err != nil {
				return fmt.Errorf("删除已清理的调用索引失败: %w", err)
			}
			continue
		}
		summary := buildSystemLogSummary(id, rows, requestMetas[id])
		startedAt := summary.StartedAt
		var lastID int64
		if len(rows) == 0 {
			lastID = meta.LogID
		}
		bootID := ""
		for _, row := range rows {
			lastID = max(lastID, row.ID)
			if row.Lifecycle != nil && row.Lifecycle.BootID != "" {
				bootID = row.Lifecycle.BootID
			}
		}
		body, err := json.Marshal(summary)
		if err != nil {
			return fmt.Errorf("编码日志摘要失败: %w", err)
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO log_request_index
			(request_id, group_key, api_type, last_unix, last_id, started_at, summary_json, boot_id)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT(request_id) DO UPDATE SET
			group_key = excluded.group_key, api_type = excluded.api_type, last_unix = excluded.last_unix,
			last_id = excluded.last_id, started_at = excluded.started_at,
			summary_json = excluded.summary_json, boot_id = excluded.boot_id`,
			id, logGroupKey(summary), summary.APIType, summary.UnixMilli, lastID, startedAt, string(body), bootID)
		if err != nil {
			return fmt.Errorf("更新日志摘要索引失败: %w", err)
		}
	}
	return nil
}

// 删除部分阶段后重建剩余请求的摘要，列表与原始详情始终采用同一口径。
// table 与 predicate 只由本包固定 SQL 传入，外部输入仍全部使用参数绑定。
func deleteIndexedLogRows(ctx context.Context, tx *sql.Tx, table, predicate string, args ...any) (sql.Result, error) {
	rows, err := tx.QueryContext(ctx, `SELECT DISTINCT request_id FROM `+table+` WHERE `+predicate, args...)
	if err != nil {
		return nil, fmt.Errorf("读取清理涉及的日志请求失败: %w", err)
	}
	ids := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		if id != "" {
			ids = append(ids, id)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE `+predicate, args...)
	if err != nil {
		return nil, err
	}
	if len(ids) > 0 {
		if err := refreshRequestIndex(ctx, tx, ids); err != nil {
			return nil, err
		}
	}
	return result, nil
}

func (s *Store) deleteExpiredIndexedLogs(ctx context.Context, table string, cutoff int64, batch int) (sql.Result, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	predicate := `id IN (SELECT id FROM ` + table + ` WHERE unix_milli < ? ORDER BY unix_milli, id LIMIT ?)`
	result, err := deleteIndexedLogRows(ctx, tx, table, predicate, cutoff, batch)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return result, nil
}

func (s *Store) decodeIndexedSummary(body, bootID string) (SystemLogSummary, error) {
	var summary SystemLogSummary
	if err := json.Unmarshal([]byte(body), &summary); err != nil {
		return summary, fmt.Errorf("解析日志摘要索引失败: %w", err)
	}
	if (summary.Status == "processing" || summary.Status == "retrying") && s.bootID != "" && bootID != "" && bootID != s.bootID {
		summary.Status = "interrupted"
		summary.ErrorType = "process_interrupted"
		summary.ErrorMessage = "上次进程退出前没有记录请求结束"
	}
	return summary, nil
}

func (s *Store) listIndexedSummaries(ctx context.Context, opts SystemLogListOptions) ([]SystemLogSummary, error) {
	limit := opts.Limit
	if limit <= 0 {
		limit = defaultSystemLogLimit
	}
	limit = min(limit, maxSystemLogLimit)
	query := `SELECT summary_json, boot_id FROM log_request_index`
	args := []any{}
	if apiType := strings.ToLower(strings.TrimSpace(opts.APIType)); apiType != "" {
		query += ` WHERE api_type = ?`
		args = append(args, apiType)
	}
	query += ` ORDER BY last_unix DESC, last_id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.readDB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("读取系统日志摘要索引失败: %w", err)
	}
	defer rows.Close()
	summaries := make([]SystemLogSummary, 0, limit)
	for rows.Next() {
		var body, bootID string
		if err := rows.Scan(&body, &bootID); err != nil {
			return nil, err
		}
		summary, err := s.decodeIndexedSummary(body, bootID)
		if err != nil {
			return nil, err
		}
		summaries = append(summaries, summary)
	}
	return summaries, rows.Err()
}

func requestIndexPredicate(opts LogConversationListOptions, bootID string) (string, []any) {
	query := ""
	args := []any{}
	if apiType := strings.ToLower(strings.TrimSpace(opts.APIType)); apiType != "" {
		query += ` AND api_type = ?`
		args = append(args, apiType)
	}
	if search := strings.TrimSpace(opts.Search); search != "" {
		query += ` AND instr(lower(request_id || ' ' || group_key || ' ' ||
			COALESCE(json_extract(summary_json, '$.promptPreview'), '') || ' ' ||
			COALESCE(json_extract(summary_json, '$.model'), '') || ' ' ||
			COALESCE(json_extract(summary_json, '$.channelName'), '') || ' ' ||
			COALESCE(json_extract(summary_json, '$.client'), '') || ' ' ||
			COALESCE(json_extract(summary_json, '$.clientFamily'), '') || ' ' ||
			COALESCE(json_extract(summary_json, '$.errorMessage'), '')), lower(?)) > 0`
		args = append(args, search)
	}
	if opts.Status != "" {
		if bootID == "" {
			query += ` AND json_extract(summary_json, '$.status') = ?`
			args = append(args, opts.Status)
		} else {
			query += ` AND CASE WHEN json_extract(summary_json, '$.status') IN ('processing', 'retrying')
				AND boot_id != '' AND boot_id != ? THEN 'interrupted'
				ELSE json_extract(summary_json, '$.status') END = ?`
			args = append(args, bootID, opts.Status)
		}
	}
	if opts.From > 0 {
		query += ` AND started_at >= ?`
		args = append(args, opts.From)
	}
	if opts.To > 0 {
		query += ` AND started_at <= ?`
		args = append(args, opts.To)
	}
	return query, args
}

func (s *Store) ListLogConversations(ctx context.Context, opts LogConversationListOptions) (LogConversationsPage, error) {
	page := LogConversationsPage{Conversations: []LogConversationSummary{}}
	if s == nil || !s.requestIndex {
		return page, fmt.Errorf("日志会话索引不可用")
	}
	limit := opts.Limit
	if limit <= 0 {
		limit = 50
	}
	limit = min(limit, 100)
	filter, args := requestIndexPredicate(opts, s.bootID)
	query := `WITH matched AS (SELECT DISTINCT group_key FROM log_request_index WHERE 1 = 1` + filter
	query += `), grouped AS (
		SELECT group_key, MAX(last_unix) AS last_unix, COUNT(*) AS request_count
		FROM log_request_index WHERE group_key IN (SELECT group_key FROM matched) GROUP BY group_key
	)`
	countQuery := query + ` SELECT COUNT(*) FROM grouped`
	countArgs := append([]any(nil), args...)
	query += ` SELECT g.group_key, g.request_count, i.summary_json, i.boot_id,
		COALESCE((SELECT json_extract(title.summary_json, '$.promptPreview') FROM log_request_index title
			WHERE title.group_key = g.group_key AND COALESCE(json_extract(title.summary_json, '$.promptPreview'), '') != ''
			ORDER BY title.started_at, title.request_id LIMIT 1), ''), (SELECT COUNT(*) FROM grouped)
		FROM grouped g JOIN log_request_index i ON i.request_id = (
			SELECT request_id FROM log_request_index WHERE group_key = g.group_key
			ORDER BY last_unix DESC, last_id DESC LIMIT 1)`
	if opts.Cursor != "" {
		cursor, err := decodeLogCursor(opts.Cursor)
		if err != nil {
			return page, err
		}
		query += ` WHERE g.last_unix < ? OR (g.last_unix = ? AND g.group_key > ?)`
		args = append(args, cursor.Time, cursor.Time, cursor.Key)
	}
	query += ` ORDER BY g.last_unix DESC, g.group_key ASC LIMIT ?`
	args = append(args, limit+1)
	rows, err := s.readDB.QueryContext(ctx, query, args...)
	if err != nil {
		return page, fmt.Errorf("查询日志会话列表失败: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var entry LogConversationSummary
		var body, bootID string
		if err := rows.Scan(&entry.Key, &entry.RequestCount, &body, &bootID, &entry.Title, &page.Total); err != nil {
			return page, err
		}
		entry.Latest, err = s.decodeIndexedSummary(body, bootID)
		if err != nil {
			return page, err
		}
		page.Conversations = append(page.Conversations, entry)
	}
	if err := rows.Err(); err != nil {
		return page, err
	}
	if len(page.Conversations) == 0 && opts.Cursor != "" {
		if err := s.readDB.QueryRowContext(ctx, countQuery, countArgs...).Scan(&page.Total); err != nil {
			return page, fmt.Errorf("统计日志会话失败: %w", err)
		}
	}
	if len(page.Conversations) > limit {
		page.Conversations = page.Conversations[:limit]
		last := page.Conversations[len(page.Conversations)-1]
		page.NextCursor, err = encodeLogCursor(last.Latest.UnixMilli, last.Key)
	}
	return page, err
}

func (s *Store) ListLogConversationRequests(ctx context.Context, opts LogConversationRequestOptions) (LogConversationRequestsPage, error) {
	page := LogConversationRequestsPage{Logs: []SystemLogSummary{}}
	if s == nil || !s.requestIndex {
		return page, fmt.Errorf("日志会话索引不可用")
	}
	if strings.TrimSpace(opts.Key) == "" || (opts.Before != "" && opts.Anchor != "") {
		return page, ErrInvalidLogCursor
	}
	limit := opts.Limit
	if limit <= 0 {
		limit = 20
	}
	limit = min(limit, 100)
	query := `SELECT summary_json, boot_id, started_at, request_id FROM log_request_index WHERE group_key = ?`
	args := []any{opts.Key}
	if opts.Before != "" {
		cursor, err := decodeLogCursor(opts.Before)
		if err != nil {
			return page, err
		}
		query += ` AND (started_at < ? OR (started_at = ? AND request_id < ?))`
		args = append(args, cursor.Time, cursor.Time, cursor.Key)
	} else if opts.Anchor != "" {
		var stamp int64
		err := s.readDB.QueryRowContext(ctx, `SELECT started_at FROM log_request_index WHERE group_key = ? AND request_id = ?`, opts.Key, opts.Anchor).Scan(&stamp)
		if errors.Is(err, sql.ErrNoRows) {
			return page, ErrLogGroupNotFound
		}
		if err != nil {
			return page, fmt.Errorf("定位会话请求失败: %w", err)
		}
		query += ` AND (started_at < ? OR (started_at = ? AND request_id <= ?))`
		args = append(args, stamp, stamp, opts.Anchor)
	}
	query += ` ORDER BY started_at DESC, request_id DESC LIMIT ?`
	args = append(args, limit+1)
	rows, err := s.readDB.QueryContext(ctx, query, args...)
	if err != nil {
		return page, fmt.Errorf("查询日志会话轮次失败: %w", err)
	}
	defer rows.Close()
	cursors := make([]logCursor, 0, limit+1)
	for rows.Next() {
		var body, bootID, id string
		var stamp int64
		if err := rows.Scan(&body, &bootID, &stamp, &id); err != nil {
			return page, err
		}
		summary, err := s.decodeIndexedSummary(body, bootID)
		if err != nil {
			return page, err
		}
		page.Logs = append(page.Logs, summary)
		cursors = append(cursors, logCursor{Time: stamp, Key: id})
	}
	if err := rows.Err(); err != nil {
		return page, err
	}
	if len(page.Logs) > limit {
		page.Logs = page.Logs[:limit]
		last := cursors[limit-1]
		page.NextCursor, err = encodeLogCursor(last.Time, last.Key)
	}
	slices.Reverse(page.Logs)
	return page, err
}
