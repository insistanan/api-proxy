package logger

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"slices"
	"sort"
	"strings"
	"time"
)

const (
	defaultSystemLogLimit = 100
	maxSystemLogLimit     = 200
)

var requiredSystemLogPhases = []string{
	PhaseClientRequest,
	PhaseUpstreamRequest,
	PhaseUpstreamResponse,
}

type trafficRowMeta struct {
	RequestID  string
	UnixMilli  int64
	Timestamp  string
	Phase      string
	APIType    string
	StatusCode int
	Stream     bool
	Truncated  bool
}

type requestLogMeta struct {
	ChannelName   string `json:"channelName"`
	Model         string `json:"model"`
	ResolvedModel string `json:"resolvedModel"`
	Status        string `json:"status"`
}

// ListSystemLogSummaries 按 request_id 聚合 traffic_logs，列表不带正文。
func (s *Store) ListSystemLogSummaries(ctx context.Context, opts SystemLogListOptions) ([]SystemLogSummary, error) {
	if s == nil {
		return nil, nil
	}
	limit := opts.Limit
	if limit <= 0 {
		limit = defaultSystemLogLimit
	}
	if limit > maxSystemLogLimit {
		limit = maxSystemLogLimit
	}
	apiType := strings.ToLower(strings.TrimSpace(opts.APIType))

	query := `SELECT request_id, MAX(unix_milli) AS last_unix, MAX(id) AS last_id
		FROM traffic_logs
		WHERE request_id != ''`
	args := make([]any, 0, 2)
	if apiType != "" {
		query += ` AND LOWER(api_type) = ?`
		args = append(args, apiType)
	}
	query += ` GROUP BY request_id ORDER BY last_unix DESC, last_id DESC LIMIT ?`
	args = append(args, limit)

	rows, err := s.readDB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("查询系统日志列表失败: %w", err)
	}
	defer rows.Close()

	requestIDs := make([]string, 0, limit)
	for rows.Next() {
		var requestID string
		var lastUnix, lastID int64
		if err := rows.Scan(&requestID, &lastUnix, &lastID); err != nil {
			return nil, err
		}
		requestIDs = append(requestIDs, requestID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(requestIDs) == 0 {
		return []SystemLogSummary{}, nil
	}

	metasByRequest, err := s.loadTrafficRowMeta(ctx, requestIDs)
	if err != nil {
		return nil, err
	}
	requestMetaByID, err := s.loadRequestLogMeta(ctx, requestIDs)
	if err != nil {
		return nil, err
	}

	summaries := make([]SystemLogSummary, 0, len(requestIDs))
	for _, requestID := range requestIDs {
		summaries = append(summaries, buildSystemLogSummary(requestID, metasByRequest[requestID], requestMetaByID[requestID]))
	}
	return summaries, nil
}

// QueryTrafficByRequestID 返回某次请求的全部流量行（含正文），按写入顺序。
func (s *Store) QueryTrafficByRequestID(ctx context.Context, requestID string) ([]TrafficLog, error) {
	if s == nil {
		return nil, nil
	}
	requestID = strings.TrimSpace(requestID)
	if requestID == "" {
		return nil, fmt.Errorf("requestId 不能为空")
	}
	query := `SELECT id, unix_milli, timestamp, request_id, attempt_id, phase, api_type, method, url,
		status_code, stream, headers_json, body, truncated, original_bytes
		FROM traffic_logs WHERE request_id = ?
		ORDER BY unix_milli ASC, id ASC`
	rows, err := s.readDB.QueryContext(ctx, query, requestID)
	if err != nil {
		return nil, fmt.Errorf("查询系统日志详情失败: %w", err)
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

// GetSystemLogDetail 返回一次请求的摘要 + 全部流量正文。
func (s *Store) GetSystemLogDetail(ctx context.Context, requestID string) (*SystemLogDetail, error) {
	if s == nil {
		return nil, nil
	}
	requestID = strings.TrimSpace(requestID)
	if requestID == "" {
		return nil, fmt.Errorf("requestId 不能为空")
	}
	trafficLogs, err := s.QueryTrafficByRequestID(ctx, requestID)
	if err != nil {
		return nil, err
	}
	if len(trafficLogs) == 0 {
		return nil, nil
	}
	requestLogs, err := s.QueryRequestLogs(ctx, QueryOptions{RequestID: requestID, Ascending: true})
	if err != nil {
		return nil, err
	}
	metas := make([]trafficRowMeta, 0, len(trafficLogs))
	for _, entry := range trafficLogs {
		metas = append(metas, trafficRowMeta{
			RequestID:  entry.RequestID,
			UnixMilli:  entry.UnixMilli,
			Timestamp:  entry.Timestamp,
			Phase:      entry.Phase,
			APIType:    entry.APIType,
			StatusCode: entry.StatusCode,
			Stream:     entry.Stream,
			Truncated:  entry.Truncated,
		})
	}
	var latestRequestMeta *requestLogMeta
	if len(requestLogs) > 0 {
		latestRequestMeta = parseRequestLogMeta(requestLogs[0])
	}
	return &SystemLogDetail{
		RequestID:   requestID,
		Summary:     buildSystemLogSummary(requestID, metas, latestRequestMeta),
		TrafficLogs: trafficLogs,
		RequestLogs: requestLogs,
	}, nil
}

func (s *Store) loadTrafficRowMeta(ctx context.Context, requestIDs []string) (map[string][]trafficRowMeta, error) {
	placeholders, args := sqlInPlaceholders(requestIDs)
	query := `SELECT request_id, unix_milli, timestamp, phase, api_type, status_code, stream, truncated
		FROM traffic_logs WHERE request_id IN (` + placeholders + `)
		ORDER BY unix_milli ASC, id ASC`
	rows, err := s.readDB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("查询系统日志摘要失败: %w", err)
	}
	defer rows.Close()

	result := make(map[string][]trafficRowMeta, len(requestIDs))
	for rows.Next() {
		var row trafficRowMeta
		var streamValue, truncatedValue int
		if err := rows.Scan(
			&row.RequestID, &row.UnixMilli, &row.Timestamp, &row.Phase, &row.APIType,
			&row.StatusCode, &streamValue, &truncatedValue,
		); err != nil {
			return nil, err
		}
		row.Stream = streamValue != 0
		row.Truncated = truncatedValue != 0
		result[row.RequestID] = append(result[row.RequestID], row)
	}
	return result, rows.Err()
}

func (s *Store) loadRequestLogMeta(ctx context.Context, requestIDs []string) (map[string]*requestLogMeta, error) {
	placeholders, args := sqlInPlaceholders(requestIDs)
	query := `SELECT request_id, payload_json FROM request_logs
		WHERE request_id IN (` + placeholders + `)
		ORDER BY unix_milli DESC, id DESC`
	rows, err := s.readDB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("查询系统日志元数据失败: %w", err)
	}
	defer rows.Close()

	result := make(map[string]*requestLogMeta, len(requestIDs))
	for rows.Next() {
		var requestID, payload string
		if err := rows.Scan(&requestID, &payload); err != nil {
			return nil, err
		}
		if _, exists := result[requestID]; exists {
			continue
		}
		result[requestID] = parseRequestLogMeta(json.RawMessage(payload))
	}
	return result, rows.Err()
}

func parseRequestLogMeta(payload json.RawMessage) *requestLogMeta {
	if len(payload) == 0 {
		return nil
	}
	var meta requestLogMeta
	if err := json.Unmarshal(payload, &meta); err != nil {
		return nil
	}
	return &meta
}

func buildSystemLogSummary(requestID string, rows []trafficRowMeta, meta *requestLogMeta) SystemLogSummary {
	summary := SystemLogSummary{
		RequestID: requestID,
		Phases:    make([]string, 0, 4),
	}
	seenPhase := make(map[string]struct{}, 4)
	for _, row := range rows {
		if _, exists := seenPhase[row.Phase]; !exists && row.Phase != "" {
			seenPhase[row.Phase] = struct{}{}
			summary.Phases = append(summary.Phases, row.Phase)
		}
		if row.UnixMilli >= summary.UnixMilli {
			summary.UnixMilli = row.UnixMilli
			summary.Timestamp = row.Timestamp
		}
		if summary.APIType == "" && row.Phase == PhaseClientRequest && row.APIType != "" {
			summary.APIType = row.APIType
		}
		if row.Phase == PhaseUpstreamResponse {
			summary.StatusCode = row.StatusCode
		}
		if row.Stream {
			summary.Stream = true
		}
		if row.Truncated {
			summary.Truncated = true
		}
	}
	if summary.APIType == "" {
		for _, row := range rows {
			if row.APIType != "" {
				summary.APIType = row.APIType
				break
			}
		}
	}
	for _, phase := range requiredSystemLogPhases {
		if _, exists := seenPhase[phase]; !exists {
			summary.MissingPhases = append(summary.MissingPhases, phase)
		}
	}
	if meta != nil {
		summary.ChannelName = strings.TrimSpace(meta.ChannelName)
		summary.Status = strings.TrimSpace(meta.Status)
		summary.Model = strings.TrimSpace(meta.ResolvedModel)
		if summary.Model == "" {
			summary.Model = strings.TrimSpace(meta.Model)
		}
	}
	return summary
}

func sqlInPlaceholders(values []string) (string, []any) {
	placeholders := make([]string, 0, len(values))
	args := make([]any, 0, len(values))
	for _, value := range values {
		placeholders = append(placeholders, "?")
		args = append(args, value)
	}
	return strings.Join(placeholders, ","), args
}

// dayTables 是手动清理与日期枚举要覆盖的全部日志表。
var dayTables = []string{"app_logs", "traffic_logs", "request_logs"}

// LocalDayRange 返回某个本地日历日 [00:00:00, 次日 00:00) 的 unix 毫秒区间。
// date 必须为 YYYY-MM-DD；非法格式直接报错，不做静默兜底。
func LocalDayRange(date string) (int64, int64, error) {
	trimmed := strings.TrimSpace(date)
	parsed, err := time.ParseInLocation("2006-01-02", trimmed, time.Local)
	if err != nil {
		return 0, 0, fmt.Errorf("无效的日期 %q（应为 YYYY-MM-DD）: %w", date, err)
	}
	start := time.Date(parsed.Year(), parsed.Month(), parsed.Day(), 0, 0, 0, 0, time.Local)
	return start.UnixMilli(), start.AddDate(0, 0, 1).UnixMilli(), nil
}

// ListAvailableDates 枚举三张日志表中存在数据的本地日历日（按日期倒序），
// 并统计 logs 目录下属于该日的轮转文件（app-YYYY-MM-DD.log）个数与大小。
func (s *Store) ListAvailableDates(ctx context.Context) ([]AvailableDate, error) {
	if s == nil {
		return nil, nil
	}
	counts := make(map[string]*AvailableDate)
	for _, table := range dayTables {
		rows, err := s.readDB.QueryContext(ctx,
			`SELECT strftime('%Y-%m-%d', datetime(unix_milli / 1000, 'unixepoch', 'localtime')) AS day, COUNT(*) FROM `+table+` GROUP BY day`)
		if err != nil {
			return nil, fmt.Errorf("统计日志日期失败: %w", err)
		}
		for rows.Next() {
			var day sql.NullString
			var count int64
			if err := rows.Scan(&day, &count); err != nil {
				rows.Close()
				return nil, err
			}
			if !day.Valid || day.String == "" {
				continue
			}
			entry, exists := counts[day.String]
			if !exists {
				entry = &AvailableDate{Date: day.String}
				counts[day.String] = entry
			}
			entry.Count += count
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}
	dates := slices.Collect(maps.Values(counts))
	sort.Slice(dates, func(i, j int) bool { return dates[i].Date > dates[j].Date })
	result := make([]AvailableDate, 0, len(dates))
	for _, entry := range dates {
		result = append(result, *entry)
	}
	return result, nil
}

// Purge 手动清理日志：按全部或单个本地日历日删除三张表的记录。
// 与自动清理同一约定：直接走写连接，靠 s.mu 与批量写入串行；
// 先 Flush 排空已接收未落库的日志，最后 CHECKPOINT + VACUUM 收缩 logs.db。
func (s *Store) Purge(ctx context.Context, opts PurgeOptions) (PurgeResult, error) {
	var result PurgeResult
	if s == nil {
		return result, fmt.Errorf("日志存储不可用")
	}
	if s.readOnly {
		return result, fmt.Errorf("日志存储只读，无法清理")
	}
	if opts.All == (opts.Date != "") {
		return result, fmt.Errorf("清理范围必须且只能指定一项：all 或 date")
	}
	startMilli, endMilli := int64(0), int64(math.MaxInt64)
	if !opts.All {
		start, end, err := LocalDayRange(opts.Date)
		if err != nil {
			return result, err
		}
		startMilli, endMilli = start, end-1
	}
	if err := s.Flush(); err != nil {
		return result, fmt.Errorf("清理前排空日志队列失败: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return result, fmt.Errorf("开始日志清理事务失败: %w", err)
	}
	defer tx.Rollback()
	counters := []*int64{&result.DeletedAppLogs, &result.DeletedTrafficLogs, &result.DeletedRequestLogs}
	for i, table := range dayTables {
		outcome, err := tx.ExecContext(ctx,
			`DELETE FROM `+table+` WHERE unix_milli >= ? AND unix_milli <= ?`, startMilli, endMilli)
		if err != nil {
			return result, fmt.Errorf("清理 %s 失败: %w", table, err)
		}
		deleted, err := outcome.RowsAffected()
		if err != nil {
			return result, fmt.Errorf("读取 %s 清理结果失败: %w", table, err)
		}
		*counters[i] = deleted
	}
	if err := tx.Commit(); err != nil {
		return result, fmt.Errorf("提交日志清理事务失败: %w", err)
	}
	// VACUUM 不能在事务内执行；DELETE 不收缩文件，磁盘空间不 VACUUM 不归还。
	if _, err := s.db.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		return result, fmt.Errorf("日志库 checkpoint 失败: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `VACUUM`); err != nil {
		return result, fmt.Errorf("日志库 VACUUM 失败: %w", err)
	}
	return result, nil
}
