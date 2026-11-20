package logger

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
)

// RequestLogPageOptions 描述逐次上游尝试的服务端筛选、分页与排序。
// 所有动态值均使用参数绑定；排序字段只能取自固定白名单。
type RequestLogPageOptions struct {
	From      int64
	To        int64
	APIType   string
	Search    string
	Channel   string
	Model     string
	Status    string
	ErrorType string
	Upstream  string
	KeyMask   string
	RequestID string
	Retried   *bool
	Stream    *bool
	Page      int
	PageSize  int
	Sort      string
	Order     string
}

type RequestLogPayloadPage struct {
	Payloads []json.RawMessage `json:"logs"`
	Total    int64             `json:"total"`
	Page     int               `json:"page"`
	PageSize int               `json:"pageSize"`
}

type requestLogFilter struct {
	From      int64
	To        int64
	APIType   string
	Search    string
	Channel   string
	Model     string
	Status    string
	ErrorType string
	Upstream  string
	KeyMask   string
	RequestID string
	Retried   *bool
	Stream    *bool
}

const (
	requestChannelExpr  = `COALESCE(NULLIF(json_extract(payload_json, '$.channelName'), ''), '#' || COALESCE(CAST(json_extract(payload_json, '$.channelIndex') AS TEXT), '?'))`
	requestStatusExpr   = `COALESCE(NULLIF(json_extract(payload_json, '$.status'), ''), 'unknown')`
	requestErrorExpr    = `COALESCE(NULLIF(json_extract(payload_json, '$.errorType'), ''), 'unknown_error')`
	requestErrorWhere   = ` AND (COALESCE(json_extract(payload_json, '$.success'), 0) != 1 OR COALESCE(json_extract(payload_json, '$.errorMessage'), '') != '')`
	requestUpstreamExpr = `COALESCE(NULLIF(json_extract(payload_json, '$.baseUrl'), ''), '未记录')`
	requestKeyMaskExpr  = `COALESCE(NULLIF(json_extract(payload_json, '$.keyMask'), ''), '未记录')`
	requestModelExpr    = `COALESCE(NULLIF(json_extract(payload_json, '$.resolvedModel'), ''), NULLIF(json_extract(payload_json, '$.model'), ''), '未知模型')`
)

func requestLogWhere(filter requestLogFilter) (string, []any) {
	query := ` WHERE 1=1`
	args := make([]any, 0, 16)
	if filter.From > 0 {
		query += ` AND unix_milli >= ?`
		args = append(args, filter.From)
	}
	if filter.To > 0 {
		query += ` AND unix_milli <= ?`
		args = append(args, filter.To)
	}
	if value := strings.ToLower(strings.TrimSpace(filter.APIType)); value != "" {
		query += ` AND api_type = ?`
		args = append(args, value)
	}
	if value := strings.TrimSpace(filter.RequestID); value != "" {
		query += ` AND request_id = ?`
		args = append(args, value)
	}
	if value := strings.TrimSpace(filter.Search); value != "" {
		query += ` AND instr(lower(request_id || ' ' || COALESCE(json_extract(payload_json, '$.attemptId'), '') || ' ' ||
			COALESCE(json_extract(payload_json, '$.conversationId'), '') || ' ' ||
			COALESCE(json_extract(payload_json, '$.channelName'), '') || ' ' ||
			COALESCE(json_extract(payload_json, '$.model'), '') || ' ' ||
			COALESCE(json_extract(payload_json, '$.resolvedModel'), '') || ' ' ||
			COALESCE(json_extract(payload_json, '$.errorMessage'), '')), lower(?)) > 0`
		args = append(args, value)
	}
	if value := strings.TrimSpace(filter.Channel); value != "" {
		query += ` AND ` + requestChannelExpr + ` = ?`
		args = append(args, value)
	}
	if value := strings.TrimSpace(filter.Model); value != "" {
		query += ` AND ` + requestModelExpr + ` = ?`
		args = append(args, value)
	}
	if value := strings.TrimSpace(filter.Status); value != "" {
		query += ` AND ` + requestStatusExpr + ` = ?`
		args = append(args, value)
	}
	if value := strings.TrimSpace(filter.ErrorType); value != "" {
		query += requestErrorWhere + ` AND ` + requestErrorExpr + ` = ?`
		args = append(args, value)
	}
	if value := strings.TrimSpace(filter.Upstream); value != "" {
		query += ` AND ` + requestUpstreamExpr + ` = ?`
		args = append(args, value)
	}
	if value := strings.TrimSpace(filter.KeyMask); value != "" {
		query += ` AND ` + requestKeyMaskExpr + ` = ?`
		args = append(args, value)
	}
	if filter.Retried != nil {
		query += ` AND COALESCE(json_extract(payload_json, '$.retried'), 0) = ?`
		args = append(args, boolInt(*filter.Retried))
	}
	if filter.Stream != nil {
		query += ` AND COALESCE(json_extract(payload_json, '$.stream'), 0) = ?`
		args = append(args, boolInt(*filter.Stream))
	}
	return query, args
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func (s *Store) QueryRequestLogPage(ctx context.Context, opts RequestLogPageOptions) (RequestLogPayloadPage, error) {
	page := RequestLogPayloadPage{Payloads: []json.RawMessage{}, Page: max(opts.Page, 1), PageSize: opts.PageSize}
	if page.PageSize <= 0 {
		page.PageSize = 50
	}
	page.PageSize = min(page.PageSize, 100)
	if s == nil || s.readDB == nil {
		return page, fmt.Errorf("请求日志存储未初始化")
	}
	where, args := requestLogWhere(requestLogFilter{
		From: opts.From, To: opts.To, APIType: opts.APIType, Search: opts.Search,
		Channel: opts.Channel, Model: opts.Model, Status: opts.Status, ErrorType: opts.ErrorType,
		Upstream: opts.Upstream, KeyMask: opts.KeyMask,
		RequestID: opts.RequestID, Retried: opts.Retried, Stream: opts.Stream,
	})
	if err := s.readDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM request_logs`+where, args...).Scan(&page.Total); err != nil {
		return page, fmt.Errorf("统计请求日志数量失败: %w", err)
	}

	order := map[string]string{
		"timestamp":    "unix_milli",
		"durationMs":   "COALESCE(CAST(json_extract(payload_json, '$.durationMs') AS INTEGER), 0)",
		"firstTokenMs": "COALESCE(CAST(json_extract(payload_json, '$.firstTokenMs') AS INTEGER), 0)",
		"inputTokens":  "COALESCE(CAST(json_extract(payload_json, '$.inputTokens') AS INTEGER), 0)",
		"outputTokens": "COALESCE(CAST(json_extract(payload_json, '$.outputTokens') AS INTEGER), 0)",
		"totalTokens":  "COALESCE(CAST(json_extract(payload_json, '$.inputTokens') AS INTEGER), 0) + COALESCE(CAST(json_extract(payload_json, '$.outputTokens') AS INTEGER), 0)",
	}
	sortExpression := order[opts.Sort]
	if sortExpression == "" {
		sortExpression = order["timestamp"]
	}
	direction := "DESC"
	if strings.EqualFold(opts.Order, "asc") {
		direction = "ASC"
	}
	query := `SELECT payload_json FROM request_logs` + where + ` ORDER BY ` + sortExpression + ` ` + direction + `, id ` + direction + ` LIMIT ? OFFSET ?`
	queryArgs := append(append([]any(nil), args...), page.PageSize, (page.Page-1)*page.PageSize)
	rows, err := s.readDB.QueryContext(ctx, query, queryArgs...)
	if err != nil {
		return page, fmt.Errorf("查询请求日志分页失败: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			return page, err
		}
		page.Payloads = append(page.Payloads, json.RawMessage(payload))
	}
	return page, rows.Err()
}

// queryRequestLogPercentile 只读取有有效数值的记录，避免无耗时的历史数据把分位数拉成零。
func queryRequestLogPercentile(ctx context.Context, db *sql.DB, where string, args []any, field string, percentile float64) (float64, error) {
	if field != "durationMs" && field != "firstTokenMs" {
		return 0, fmt.Errorf("不支持的统计字段 %s", field)
	}
	expression := `COALESCE(CAST(json_extract(payload_json, '$.` + field + `') AS REAL), 0)`
	filteredWhere := where + ` AND ` + expression + ` > 0`
	query := `WITH samples AS (SELECT ` + expression + ` AS value FROM request_logs` + filteredWhere + `)
		SELECT value FROM samples ORDER BY value LIMIT 1 OFFSET (SELECT CAST(COUNT(*) * ? + 0.999999 AS INTEGER) - 1 FROM samples)`
	var value float64
	if err := db.QueryRowContext(ctx, query, append(append([]any(nil), args...), percentile)...).Scan(&value); err != nil {
		if err == sql.ErrNoRows {
			return 0, nil
		}
		return 0, err
	}
	return value, nil
}
