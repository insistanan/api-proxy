package logger

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

type LogCallSummary struct {
	Summary         SystemLogSummary `json:"summary"`
	AttemptCount    int64            `json:"attemptCount"`
	AttemptsVersion int64            `json:"attemptsVersion"`
}

type LogCallsPage struct {
	Calls      []LogCallSummary `json:"calls"`
	NextCursor string           `json:"nextCursor,omitempty"`
}

type LogCallAttempts struct {
	Attempts []json.RawMessage `json:"attempts"`
	Limited  bool              `json:"limited"`
	Notice   string            `json:"notice"`
}

// 有明确 attemptId 的记录按 ID 去重；旧记录缺少 ID 时逐条保留，不猜测关联。
const attemptRecordKeySQL = `CASE WHEN COALESCE(json_extract(payload_json, '$.attemptId'), '') = ''
	THEN 'row:' || id ELSE 'attempt:' || json_extract(payload_json, '$.attemptId') END`

// ListLogCalls 先按调用分页，再统计该调用的全部尝试；不下载任何请求或响应正文。
// 用开始记录时间分页，调用结束时不会因为更新时间变化跨页移动。
func (s *Store) ListLogCalls(ctx context.Context, opts LogConversationListOptions) (LogCallsPage, error) {
	page := LogCallsPage{Calls: []LogCallSummary{}}
	if s == nil || !s.requestIndex {
		return page, fmt.Errorf("日志调用索引不可用")
	}
	limit := opts.Limit
	if limit <= 0 {
		limit = 50
	}
	limit = min(limit, 100)
	filter, args := requestIndexPredicate(opts, s.bootID)
	query := `SELECT summary_json, boot_id,
		(SELECT COUNT(DISTINCT ` + attemptRecordKeySQL + `) FROM request_logs r WHERE r.request_id = i.request_id),
		COALESCE((SELECT MAX(id) FROM request_logs r WHERE r.request_id = i.request_id), 0)
		FROM log_request_index i WHERE 1 = 1` + filter
	if opts.Cursor != "" {
		cursor, err := decodeLogCursor(opts.Cursor)
		if err != nil {
			return page, err
		}
		query += ` AND (started_at < ? OR (started_at = ? AND request_id < ?))`
		args = append(args, cursor.Time, cursor.Time, cursor.Key)
	}
	query += ` ORDER BY started_at DESC, request_id DESC LIMIT ?`
	args = append(args, limit+1)
	rows, err := s.readDB.QueryContext(ctx, query, args...)
	if err != nil {
		return page, fmt.Errorf("查询模型调用失败: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var call LogCallSummary
		var body, bootID string
		if err := rows.Scan(&body, &bootID, &call.AttemptCount, &call.AttemptsVersion); err != nil {
			return page, err
		}
		call.Summary, err = s.decodeIndexedSummary(body, bootID)
		if err != nil {
			return page, err
		}
		page.Calls = append(page.Calls, call)
	}
	if err := rows.Err(); err != nil {
		return page, err
	}
	if len(page.Calls) > limit {
		page.Calls = page.Calls[:limit]
		last := page.Calls[len(page.Calls)-1].Summary
		page.NextCursor, err = encodeLogCursor(last.StartedAt, last.RequestID)
	}
	return page, err
}

// GetLogCallAttempts 只查询单次调用的指标，独立于正文详情和会话工具关联。
func (s *Store) GetLogCallAttempts(ctx context.Context, requestID string) (LogCallAttempts, error) {
	page := LogCallAttempts{Attempts: []json.RawMessage{}, Notice: "按记录时间展示已观测尝试；相同尝试 ID 仅保留最后一条记录。缺少 ID 的旧记录无法确认是否重复，次数不代表实际计费次数。"}
	if s == nil || !s.requestIndex {
		return page, fmt.Errorf("日志调用索引不可用")
	}
	tx, err := s.readDB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return page, fmt.Errorf("开始调用尝试查询失败: %w", err)
	}
	defer tx.Rollback()
	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT 1 FROM log_request_index WHERE request_id = ?`, requestID).Scan(&exists); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return page, ErrLogGroupNotFound
		}
		return page, fmt.Errorf("查询调用标识失败: %w", err)
	}
	rows, err := tx.QueryContext(ctx, `SELECT payload_json FROM (
		SELECT payload_json, unix_milli, id,
			MIN(unix_milli) OVER (PARTITION BY `+attemptRecordKeySQL+`) AS first_at,
			MIN(id) OVER (PARTITION BY `+attemptRecordKeySQL+`) AS first_id,
			ROW_NUMBER() OVER (
			PARTITION BY `+attemptRecordKeySQL+` ORDER BY unix_milli DESC, id DESC) AS position
		FROM request_logs WHERE request_id = ?
	) WHERE position = 1 ORDER BY first_at, first_id LIMIT 101`, requestID)
	if err != nil {
		return page, fmt.Errorf("查询调用尝试失败: %w", err)
	}
	defer rows.Close()
	bytes := 0
	for rows.Next() {
		var body string
		if err := rows.Scan(&body); err != nil {
			return page, err
		}
		if len(page.Attempts) >= 100 || (len(page.Attempts) > 0 && bytes+len(body) > 1<<20) {
			page.Limited = true
			break
		}
		if len(body) > 1<<20 {
			return page, fmt.Errorf("单条尝试指标超过 1 MiB 查询上限，请使用 CLI 查询")
		}
		if !json.Valid([]byte(body)) {
			return page, fmt.Errorf("调用尝试指标不是有效 JSON")
		}
		bytes += len(body)
		page.Attempts = append(page.Attempts, json.RawMessage(body))
	}
	if err := rows.Err(); err != nil {
		return page, err
	}
	if err := rows.Close(); err != nil {
		return page, err
	}
	if err := tx.Commit(); err != nil {
		return page, err
	}
	return page, nil
}
