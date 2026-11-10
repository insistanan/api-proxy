// 日志查询与入库规范化：查询只读连接，保持现有排序、限制和返回字段。
package logger

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

func (s *Store) QueryAppLogs(ctx context.Context, opts QueryOptions) ([]AppLog, error) {
	if s == nil {
		return nil, nil
	}
	query := `SELECT id, unix_milli, timestamp, tag, message FROM app_logs WHERE unix_milli >= ? AND unix_milli <= ?`
	args := []any{opts.From, opts.To}
	query += ` ORDER BY unix_milli ASC, id ASC`
	if opts.Limit > 0 {
		query += ` LIMIT ?`
		args = append(args, opts.Limit)
	}
	rows, err := s.readDB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("查询 app_logs 失败: %w", err)
	}
	defer rows.Close()
	entries := make([]AppLog, 0)
	for rows.Next() {
		var entry AppLog
		if err := rows.Scan(&entry.ID, &entry.UnixMilli, &entry.Timestamp, &entry.Tag, &entry.Message); err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, rows.Err()
}

func (s *Store) QueryTrafficLogs(ctx context.Context, opts QueryOptions) ([]TrafficLog, error) {
	if s == nil {
		return nil, nil
	}
	query := s.trafficSelect() + ` WHERE t.unix_milli >= ? AND t.unix_milli <= ?`
	args := []any{opts.From, opts.To}
	if opts.RequestID != "" {
		query += ` AND t.request_id = ?`
		args = append(args, opts.RequestID)
	}
	query += ` ORDER BY t.unix_milli ASC, t.id ASC`
	if opts.Limit > 0 {
		query += ` LIMIT ?`
		args = append(args, opts.Limit)
	}
	rows, err := s.readDB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("查询 traffic_logs 失败: %w", err)
	}
	defer rows.Close()
	entries := make([]TrafficLog, 0)
	for rows.Next() {
		var entry TrafficLog
		var streamValue, truncatedValue int
		if err := rows.Scan(
			&entry.ID, &entry.UnixMilli, &entry.Timestamp, &entry.RequestID, &entry.AttemptID,
			&entry.Phase, &entry.APIType, &entry.Method, &entry.URL, &entry.StatusCode,
			&streamValue, &entry.HeadersJSON, &entry.Body, &truncatedValue, &entry.OriginalBytes,
		); err != nil {
			return nil, err
		}
		entry.Stream = streamValue != 0
		entry.Truncated = truncatedValue != 0
		entries = append(entries, entry)
	}
	return entries, rows.Err()
}

func (s *Store) QueryRequestLogs(ctx context.Context, opts QueryOptions) ([]json.RawMessage, error) {
	if s == nil {
		return nil, nil
	}
	query := `SELECT payload_json FROM request_logs WHERE 1=1`
	args := make([]any, 0, 6)
	if opts.From > 0 {
		query += ` AND unix_milli >= ?`
		args = append(args, opts.From)
	}
	if opts.To > 0 {
		query += ` AND unix_milli <= ?`
		args = append(args, opts.To)
	}
	if opts.RequestID != "" {
		query += ` AND request_id = ?`
		args = append(args, opts.RequestID)
	}
	if opts.APIType != "" {
		query += ` AND api_type = ?`
		args = append(args, opts.APIType)
	}
	order := ` ORDER BY unix_milli DESC, id DESC`
	if opts.Ascending {
		order = ` ORDER BY unix_milli ASC, id ASC`
	}
	query += order
	if opts.Limit > 0 {
		query += ` LIMIT ?`
		args = append(args, opts.Limit)
	}
	rows, err := s.readDB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("查询 request_logs 失败: %w", err)
	}
	defer rows.Close()
	entries := make([]json.RawMessage, 0)
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		entries = append(entries, json.RawMessage(payload))
	}
	return entries, rows.Err()
}

func normalizeAppLog(entry AppLog) AppLog {
	now := time.Now()
	if entry.UnixMilli == 0 {
		if parsed, err := time.Parse(time.RFC3339Nano, entry.Timestamp); err == nil {
			entry.UnixMilli = parsed.UnixMilli()
			now = parsed
		} else {
			entry.UnixMilli = now.UnixMilli()
		}
	} else {
		now = time.UnixMilli(entry.UnixMilli)
	}
	if entry.Timestamp == "" {
		entry.Timestamp = now.UTC().Format(time.RFC3339Nano)
	}
	return entry
}

func normalizeTrafficLog(entry TrafficLog) TrafficLog {
	now := time.Now()
	if entry.UnixMilli == 0 {
		entry.UnixMilli = now.UnixMilli()
	} else {
		now = time.UnixMilli(entry.UnixMilli)
	}
	if entry.Timestamp == "" {
		entry.Timestamp = now.UTC().Format(time.RFC3339Nano)
	}
	if entry.Body != "" && !entry.bodyPrepared {
		prepared, originalBytes, truncated := PrepareBody([]byte(entry.Body))
		entry.Body = prepared
		if entry.OriginalBytes == 0 {
			entry.OriginalBytes = originalBytes
		}
		entry.Truncated = entry.Truncated || truncated
	}
	return entry
}

func normalizeRequestRecord(entry RequestLogRecord) RequestLogRecord {
	now := time.Now()
	if entry.UnixMilli == 0 {
		if parsed, err := time.Parse(time.RFC3339Nano, entry.Timestamp); err == nil {
			entry.UnixMilli = parsed.UnixMilli()
			now = parsed
		} else {
			entry.UnixMilli = now.UnixMilli()
		}
	}
	if entry.Timestamp == "" {
		entry.Timestamp = now.UTC().Format(time.RFC3339Nano)
	}
	if entry.APIType == "" {
		entry.APIType = "unknown"
	}
	if len(entry.Payload) == 0 {
		entry.Payload = []byte("{}")
	}
	return entry
}
