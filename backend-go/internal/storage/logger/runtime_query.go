package logger

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

type RuntimeLogOptions struct {
	Search string
	Before string
	Limit  int
}

type RuntimeLogPage struct {
	Logs       []AppLog `json:"logs"`
	NextCursor string   `json:"nextCursor,omitempty"`
}

func (s *Store) ListRuntimeLogs(ctx context.Context, opts RuntimeLogOptions) (RuntimeLogPage, error) {
	page := RuntimeLogPage{Logs: []AppLog{}}
	if s == nil {
		return page, fmt.Errorf("日志存储不可用")
	}
	limit := opts.Limit
	if limit <= 0 {
		limit = 100
	}
	limit = min(limit, 100)
	query := `SELECT id, unix_milli, timestamp, tag, message FROM app_logs WHERE 1 = 1`
	args := []any{}
	if search := strings.TrimSpace(opts.Search); search != "" {
		query += ` AND instr(lower(tag || ' ' || message), lower(?)) > 0`
		args = append(args, search)
	}
	if opts.Before != "" {
		cursor, err := decodeLogCursor(opts.Before)
		if err != nil {
			return page, err
		}
		id, err := strconv.ParseInt(cursor.Key, 10, 64)
		if err != nil || id <= 0 {
			return page, ErrInvalidLogCursor
		}
		query += ` AND (unix_milli < ? OR (unix_milli = ? AND id < ?))`
		args = append(args, cursor.Time, cursor.Time, id)
	}
	query += ` ORDER BY unix_milli DESC, id DESC LIMIT ?`
	args = append(args, limit+1)
	rows, err := s.readDB.QueryContext(ctx, query, args...)
	if err != nil {
		return page, fmt.Errorf("查询运行日志失败: %w", err)
	}
	defer rows.Close()
	pageBytes := 0
	hasMore := false
	for rows.Next() {
		var entry AppLog
		if err := rows.Scan(&entry.ID, &entry.UnixMilli, &entry.Timestamp, &entry.Tag, &entry.Message); err != nil {
			return page, err
		}
		entryBytes := len(entry.Message) + len(entry.Tag) + len(entry.Timestamp)
		if len(page.Logs) > 0 && pageBytes+entryBytes > 1<<20 {
			hasMore = true
			break
		}
		pageBytes += entryBytes
		page.Logs = append(page.Logs, entry)
	}
	if err := rows.Err(); err != nil {
		return page, err
	}
	if len(page.Logs) > limit {
		page.Logs = page.Logs[:limit]
		hasMore = true
	}
	if hasMore {
		last := page.Logs[len(page.Logs)-1]
		page.NextCursor, err = encodeLogCursor(last.UnixMilli, strconv.FormatInt(last.ID, 10))
	}
	return page, err
}
