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
	ID         int64
	RequestID  string
	UnixMilli  int64
	Timestamp  string
	Phase      string
	APIType    string
	StatusCode int
	Stream     bool
	Truncated  bool
	Lifecycle  *RequestLifecycle
}

type requestLogMeta struct {
	LogID          int64  `json:"-"`
	UnixMilli      int64  `json:"-"`
	FirstAt        int64  `json:"-"`
	Timestamp      string `json:"-"`
	APIType        string `json:"-"`
	Stream         bool   `json:"stream"`
	ChannelName    string `json:"channelName"`
	Model          string `json:"model"`
	ResolvedModel  string `json:"resolvedModel"`
	Status         string `json:"status"`
	ConversationID string `json:"conversationId"`
	StatusCode     int    `json:"statusCode"`
	ErrorType      string `json:"errorType"`
	ErrorMessage   string `json:"errorMessage"`
	Retried        bool   `json:"retried"`
}

// ListSystemLogSummaries 优先读取请求摘要索引；旧库按 request_id 聚合流量记录，列表不带正文。
func (s *Store) ListSystemLogSummaries(ctx context.Context, opts SystemLogListOptions) ([]SystemLogSummary, error) {
	if s == nil {
		return nil, nil
	}
	if s.requestIndex {
		return s.listIndexedSummaries(ctx, opts)
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
		summary := buildSystemLogSummary(requestID, metasByRequest[requestID], requestMetaByID[requestID])
		s.adjustLifecycleStatus(&summary, metasByRequest[requestID])
		summaries = append(summaries, summary)
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
	query := s.trafficSelect() + ` WHERE t.request_id = ? ORDER BY t.unix_milli ASC, t.id ASC`
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
	requestLogs, err := s.QueryRequestLogs(ctx, QueryOptions{RequestID: requestID, Ascending: true})
	if err != nil {
		return nil, err
	}
	if len(trafficLogs) == 0 && len(requestLogs) == 0 {
		return nil, nil
	}
	metas := make([]trafficRowMeta, 0, len(trafficLogs))
	for _, entry := range trafficLogs {
		lifecycle, err := parseTrafficLifecycle(entry.Phase, entry.Body)
		if err != nil {
			return nil, err
		}
		metas = append(metas, trafficRowMeta{
			RequestID:  entry.RequestID,
			ID:         entry.ID,
			UnixMilli:  entry.UnixMilli,
			Timestamp:  entry.Timestamp,
			Phase:      entry.Phase,
			APIType:    entry.APIType,
			StatusCode: entry.StatusCode,
			Stream:     entry.Stream,
			Truncated:  entry.Truncated,
			Lifecycle:  lifecycle,
		})
	}
	requestMetas, err := s.loadRequestLogMeta(ctx, []string{requestID})
	if err != nil {
		return nil, err
	}
	summary := buildSystemLogSummary(requestID, metas, requestMetas[requestID])
	s.adjustLifecycleStatus(&summary, metas)
	contexts := make([]*ContextSnapshot, 0, 3)
	input := inputContext(trafficLogs)
	if input != nil {
		contexts = append(contexts, input)
	}
	for _, entry := range trafficLogs {
		if entry.Phase == PhaseUpstreamRequest {
			snapshot := AnalyzeContext(entry)
			snapshot.InputChange = CompareContexts(requestID, input, snapshot)
			contexts = append(contexts, snapshot)
		}
	}
	change, err := s.contextChange(ctx, summary, input)
	if err != nil {
		return nil, err
	}
	links, err := s.toolLinks(ctx, summary, trafficLogs)
	if err != nil {
		return nil, err
	}
	return &SystemLogDetail{
		RequestID:     requestID,
		Summary:       summary,
		TrafficLogs:   trafficLogs,
		RequestLogs:   requestLogs,
		Contexts:      contexts,
		ContextChange: change,
		ToolLinks:     links,
	}, nil
}

func (s *Store) loadTrafficRowMeta(ctx context.Context, requestIDs []string) (map[string][]trafficRowMeta, error) {
	return loadTrafficRowMetaFrom(ctx, s.readDB, requestIDs)
}

type logQuerier interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func loadTrafficRowMetaFrom(ctx context.Context, db logQuerier, requestIDs []string) (map[string][]trafficRowMeta, error) {
	placeholders, args := sqlInPlaceholders(requestIDs)
	query := `SELECT id, request_id, unix_milli, timestamp, phase, api_type, status_code, stream, truncated,
		CASE WHEN phase IN ('request_start', 'request_end') THEN body ELSE '' END
		FROM traffic_logs WHERE request_id IN (` + placeholders + `)
		ORDER BY unix_milli ASC, id ASC`
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("查询系统日志摘要失败: %w", err)
	}
	defer rows.Close()

	result := make(map[string][]trafficRowMeta, len(requestIDs))
	for rows.Next() {
		var row trafficRowMeta
		var streamValue, truncatedValue int
		var lifecycleBody string
		if err := rows.Scan(
			&row.ID, &row.RequestID, &row.UnixMilli, &row.Timestamp, &row.Phase, &row.APIType,
			&row.StatusCode, &streamValue, &truncatedValue, &lifecycleBody,
		); err != nil {
			return nil, err
		}
		row.Stream = streamValue != 0
		row.Truncated = truncatedValue != 0
		row.Lifecycle, err = parseTrafficLifecycle(row.Phase, lifecycleBody)
		if err != nil {
			return nil, err
		}
		result[row.RequestID] = append(result[row.RequestID], row)
	}
	return result, rows.Err()
}

func (s *Store) loadRequestLogMeta(ctx context.Context, requestIDs []string) (map[string]*requestLogMeta, error) {
	return loadRequestLogMetaFrom(ctx, s.readDB, requestIDs)
}

func loadRequestLogMetaFrom(ctx context.Context, db logQuerier, requestIDs []string) (map[string]*requestLogMeta, error) {
	placeholders, args := sqlInPlaceholders(requestIDs)
	query := `SELECT id, request_id, unix_milli, timestamp, api_type, payload_json FROM request_logs
		WHERE request_id IN (` + placeholders + `)
		ORDER BY unix_milli DESC, id DESC`
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("查询系统日志元数据失败: %w", err)
	}
	defer rows.Close()

	result := make(map[string]*requestLogMeta, len(requestIDs))
	for rows.Next() {
		var requestID, payload string
		var id, stamp int64
		var timestamp, apiType string
		if err := rows.Scan(&id, &requestID, &stamp, &timestamp, &apiType, &payload); err != nil {
			return nil, err
		}
		if meta, exists := result[requestID]; exists {
			meta.FirstAt = min(meta.FirstAt, stamp)
			continue
		}
		result[requestID], err = parseRequestLogMeta(json.RawMessage(payload))
		if err != nil {
			return nil, err
		}
		meta := result[requestID]
		meta.LogID, meta.UnixMilli, meta.FirstAt = id, stamp, stamp
		meta.Timestamp, meta.APIType = timestamp, apiType
	}
	return result, rows.Err()
}

func parseRequestLogMeta(payload json.RawMessage) (*requestLogMeta, error) {
	if len(payload) == 0 {
		return nil, fmt.Errorf("请求日志元数据不能为空")
	}
	var meta requestLogMeta
	if err := json.Unmarshal(payload, &meta); err != nil {
		return nil, fmt.Errorf("解析请求日志元数据失败: %w", err)
	}
	return &meta, nil
}

func buildSystemLogSummary(requestID string, rows []trafficRowMeta, meta *requestLogMeta) SystemLogSummary {
	summary := SystemLogSummary{
		RequestID: requestID,
		Phases:    make([]string, 0, 4),
	}
	seenPhase := make(map[string]struct{}, 4)
	var started, ended *RequestLifecycle
	for _, row := range rows {
		if row.Phase == PhaseRequestStart {
			started = row.Lifecycle
		}
		if row.Phase == PhaseRequestEnd {
			ended = row.Lifecycle
		}
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
		if row.Phase == PhaseUpstreamResponse || row.Phase == PhaseUpstreamHeaders {
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
		// 有流量记录时沿用旧索引口径；只有尝试指标的调用才补齐时间与入口。
		if len(rows) == 0 {
			summary.UnixMilli, summary.Timestamp = meta.UnixMilli, meta.Timestamp
			summary.APIType, summary.Stream, summary.StatusCode = meta.APIType, meta.Stream, meta.StatusCode
		}
		summary.ConversationID = meta.ConversationID
		summary.ErrorType = meta.ErrorType
		summary.ErrorMessage = meta.ErrorMessage
		summary.ChannelName = strings.TrimSpace(meta.ChannelName)
		summary.Status = strings.TrimSpace(meta.Status)
		summary.Model = strings.TrimSpace(meta.ResolvedModel)
		if summary.Model == "" {
			summary.Model = strings.TrimSpace(meta.Model)
		}
	}
	if started != nil {
		summary.PromptPreview = started.PromptPreview
		summary.ClientFamily = started.ClientFamily
		summary.IdentitySource = started.IdentitySource
		summary.Context = started.Context
		if started.ConversationID != "" {
			summary.ConversationID = started.ConversationID
		}
		summary.RequestHash = started.RequestHash
		summary.Client = started.Client
		summary.StartedAt = started.StartedAt
		if summary.Model == "" {
			summary.Model = started.Model
		}
		summary.Status = "processing"
		if meta != nil && meta.Retried {
			summary.Status = "retrying"
		}
	}
	if ended != nil {
		summary.PromptPreview = ended.PromptPreview
		summary.ClientFamily = ended.ClientFamily
		summary.IdentitySource = ended.IdentitySource
		summary.Context = ended.Context
		if summary.ConversationID == "" {
			summary.ConversationID = ended.ConversationID
		}
		summary.RequestHash = ended.RequestHash
		summary.Client = ended.Client
		summary.StartedAt = ended.StartedAt
		summary.Status = ended.Status
		summary.StatusCode = ended.StatusCode
		summary.ErrorType = ended.ErrorType
		summary.ErrorMessage = ended.ErrorMessage
		if summary.Model == "" {
			summary.Model = ended.Model
		}
	}
	summary.APIType = strings.ToLower(summary.APIType)
	if summary.StartedAt == 0 && len(rows) > 0 {
		summary.StartedAt = rows[0].UnixMilli
	}
	if summary.StartedAt == 0 && meta != nil {
		summary.StartedAt = meta.FirstAt
	}
	return summary
}

func parseTrafficLifecycle(phase, body string) (*RequestLifecycle, error) {
	if phase != PhaseRequestStart && phase != PhaseRequestEnd {
		return nil, nil
	}
	var state RequestLifecycle
	if err := json.Unmarshal([]byte(body), &state); err != nil {
		return nil, fmt.Errorf("解析请求生命周期日志失败: %w", err)
	}
	return &state, nil
}

func (s *Store) adjustLifecycleStatus(summary *SystemLogSummary, rows []trafficRowMeta) {
	if summary.Status != "processing" && summary.Status != "retrying" {
		return
	}
	for _, row := range rows {
		if row.Phase == PhaseRequestStart && row.Lifecycle != nil && s.bootID != "" && row.Lifecycle.BootID != "" && row.Lifecycle.BootID != s.bootID {
			summary.Status = "interrupted"
			summary.ErrorType = "process_interrupted"
			summary.ErrorMessage = "上次进程退出前没有记录请求结束"
		}
	}
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

// ListAvailableDates 汇总三张日志表的本地日历日与记录数，按日期倒序返回。
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

// Purge 按全部或单个本地日历日删除日志，并清理摘要索引与无引用正文。
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
		var outcome sql.Result
		if !opts.All && i > 0 {
			outcome, err = deleteIndexedLogRows(ctx, tx, table, `unix_milli >= ? AND unix_milli <= ?`, startMilli, endMilli)
		} else {
			outcome, err = tx.ExecContext(ctx,
				`DELETE FROM `+table+` WHERE unix_milli >= ? AND unix_milli <= ?`, startMilli, endMilli)
		}
		if err != nil {
			return result, fmt.Errorf("清理 %s 失败: %w", table, err)
		}
		deleted, err := outcome.RowsAffected()
		if err != nil {
			return result, fmt.Errorf("读取 %s 清理结果失败: %w", table, err)
		}
		*counters[i] = deleted
	}
	if opts.All {
		if _, err := tx.ExecContext(ctx, `DELETE FROM log_request_index`); err != nil {
			return result, fmt.Errorf("清理调用摘要索引失败: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM traffic_bodies WHERE NOT EXISTS (SELECT 1 FROM traffic_body_refs r WHERE r.body_hash = traffic_bodies.body_hash)`); err != nil {
		return result, fmt.Errorf("清理无引用正文失败: %w", err)
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
