package logger

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// RequestStatsFilter 与请求日志共用筛选口径，统计对象是一行一次上游尝试。
type RequestStatsFilter struct {
	From          int64
	To            int64
	APIType       string
	Channel       string
	Model         string
	Status        string
	ErrorType     string
	Upstream      string
	KeyMask       string
	Search        string
	DimensionSort string
}

type RequestStatsPoint struct {
	Timestamp           time.Time `json:"timestamp"`
	RequestCount        int64     `json:"requestCount"`
	SuccessCount        int64     `json:"successCount"`
	FailureCount        int64     `json:"failureCount"`
	SuccessRate         float64   `json:"successRate"`
	InputTokens         int64     `json:"inputTokens"`
	OutputTokens        int64     `json:"outputTokens"`
	CacheCreationTokens int64     `json:"cacheCreationTokens"`
	CacheReadTokens     int64     `json:"cacheReadTokens"`
	RetriedCount        int64     `json:"retriedCount"`
	AvgDurationMs       float64   `json:"avgDurationMs"`
	AvgFirstTokenMs     float64   `json:"avgFirstTokenMs"`
}

type RequestStatsSummary struct {
	TotalRequests            int64   `json:"totalRequests"`
	TotalCalls               int64   `json:"totalCalls"`
	TotalSuccess             int64   `json:"totalSuccess"`
	TotalFailure             int64   `json:"totalFailure"`
	TotalRetried             int64   `json:"totalRetried"`
	RetryRate                float64 `json:"retryRate"`
	TotalInputTokens         int64   `json:"totalInputTokens"`
	TotalOutputTokens        int64   `json:"totalOutputTokens"`
	TotalCacheCreationTokens int64   `json:"totalCacheCreationTokens"`
	TotalCacheReadTokens     int64   `json:"totalCacheReadTokens"`
	AvgSuccessRate           float64 `json:"avgSuccessRate"`
	CacheHitRate             float64 `json:"cacheHitRate"`
	AvgDurationMs            float64 `json:"avgDurationMs"`
	P50DurationMs            float64 `json:"p50DurationMs"`
	P95DurationMs            float64 `json:"p95DurationMs"`
	MaxDurationMs            float64 `json:"maxDurationMs"`
	AvgFirstTokenMs          float64 `json:"avgFirstTokenMs"`
	P50FirstTokenMs          float64 `json:"p50FirstTokenMs"`
	P95FirstTokenMs          float64 `json:"p95FirstTokenMs"`
	AvgTPS                   float64 `json:"avgTps"`
	DurationSamples          int64   `json:"durationSamples"`
	FirstTokenSamples        int64   `json:"firstTokenSamples"`
	Duration                 string  `json:"duration"`
}

type RequestStatsDimension struct {
	Name          string  `json:"name"`
	RequestCount  int64   `json:"requestCount"`
	SuccessCount  int64   `json:"successCount"`
	FailureCount  int64   `json:"failureCount"`
	InputTokens   int64   `json:"inputTokens"`
	OutputTokens  int64   `json:"outputTokens"`
	AvgDurationMs float64 `json:"avgDurationMs"`
	TotalTokens   int64   `json:"totalTokens"`
	SuccessRate   float64 `json:"successRate"`
}

type RequestStatsFacets struct {
	Channels []string `json:"channels"`
	Models   []string `json:"models"`
}

type RequestStatsResult struct {
	From            time.Time               `json:"from"`
	To              time.Time               `json:"to"`
	Interval        string                  `json:"interval"`
	RetentionDays   int                     `json:"retentionDays"`
	Summary         RequestStatsSummary     `json:"summary"`
	PreviousSummary *RequestStatsSummary    `json:"previousSummary,omitempty"`
	DataPoints      []RequestStatsPoint     `json:"dataPoints"`
	Channels        []RequestStatsDimension `json:"channels"`
	Models          []RequestStatsDimension `json:"models"`
	Errors          []RequestStatsDimension `json:"errors"`
	Protocols       []RequestStatsDimension `json:"protocols"`
	Statuses        []RequestStatsDimension `json:"statuses"`
	Upstreams       []RequestStatsDimension `json:"upstreams"`
	Keys            []RequestStatsDimension `json:"keys"`
	Facets          RequestStatsFacets      `json:"facets"`
}

const requestSummarySelect = `SELECT
	COUNT(*),
	COUNT(DISTINCT NULLIF(request_id, '')),
	COALESCE(SUM(CASE WHEN COALESCE(json_extract(payload_json, '$.success'), 0) = 1 THEN 1 ELSE 0 END), 0),
	COALESCE(SUM(CASE WHEN COALESCE(json_extract(payload_json, '$.success'), 0) != 1 THEN 1 ELSE 0 END), 0),
	COALESCE(SUM(CASE WHEN COALESCE(json_extract(payload_json, '$.retried'), 0) = 1 THEN 1 ELSE 0 END), 0),
	COALESCE(SUM(COALESCE(CAST(json_extract(payload_json, '$.inputTokens') AS INTEGER), 0)), 0),
	COALESCE(SUM(COALESCE(CAST(json_extract(payload_json, '$.outputTokens') AS INTEGER), 0)), 0),
	COALESCE(SUM(COALESCE(CAST(json_extract(payload_json, '$.cacheCreationTokens') AS INTEGER), 0)), 0),
	COALESCE(SUM(COALESCE(CAST(json_extract(payload_json, '$.cacheReadTokens') AS INTEGER), 0)), 0),
	COALESCE(AVG(CASE WHEN COALESCE(json_extract(payload_json, '$.durationMs'), 0) > 0 THEN json_extract(payload_json, '$.durationMs') END), 0),
	COALESCE(MAX(COALESCE(json_extract(payload_json, '$.durationMs'), 0)), 0),
	COALESCE(AVG(CASE WHEN COALESCE(json_extract(payload_json, '$.firstTokenMs'), 0) > 0 THEN json_extract(payload_json, '$.firstTokenMs') END), 0),
	COALESCE(AVG(CASE WHEN COALESCE(json_extract(payload_json, '$.tps'), 0) > 0 THEN json_extract(payload_json, '$.tps') END), 0),
	COALESCE(SUM(CASE WHEN COALESCE(json_extract(payload_json, '$.durationMs'), 0) > 0 THEN 1 ELSE 0 END), 0),
	COALESCE(SUM(CASE WHEN COALESCE(json_extract(payload_json, '$.firstTokenMs'), 0) > 0 THEN 1 ELSE 0 END), 0)
	FROM request_logs`

func (s *Store) queryRequestStatsSummary(ctx context.Context, filter RequestStatsFilter) (RequestStatsSummary, error) {
	summary := RequestStatsSummary{Duration: (time.Duration(max(filter.To-filter.From, 0)) * time.Millisecond).String()}
	where, args := requestLogWhere(requestLogFilter{
		From: filter.From, To: filter.To, APIType: filter.APIType, Search: filter.Search,
		Channel: filter.Channel, Model: filter.Model, Status: filter.Status, ErrorType: filter.ErrorType,
		Upstream: filter.Upstream, KeyMask: filter.KeyMask,
	})
	err := s.readDB.QueryRowContext(ctx, requestSummarySelect+where, args...).Scan(
		&summary.TotalRequests, &summary.TotalCalls, &summary.TotalSuccess, &summary.TotalFailure,
		&summary.TotalRetried, &summary.TotalInputTokens, &summary.TotalOutputTokens,
		&summary.TotalCacheCreationTokens, &summary.TotalCacheReadTokens,
		&summary.AvgDurationMs, &summary.MaxDurationMs, &summary.AvgFirstTokenMs,
		&summary.AvgTPS, &summary.DurationSamples, &summary.FirstTokenSamples,
	)
	if err != nil {
		return summary, fmt.Errorf("汇总请求统计失败: %w", err)
	}
	if summary.TotalRequests > 0 {
		summary.AvgSuccessRate = float64(summary.TotalSuccess) / float64(summary.TotalRequests) * 100
		summary.RetryRate = float64(summary.TotalRetried) / float64(summary.TotalRequests) * 100
	}
	if denominator := summary.TotalCacheReadTokens + summary.TotalInputTokens; denominator > 0 {
		summary.CacheHitRate = float64(summary.TotalCacheReadTokens) / float64(denominator) * 100
	}
	if summary.DurationSamples > 0 {
		if summary.P50DurationMs, err = queryRequestLogPercentile(ctx, s.readDB, where, args, "durationMs", .50); err != nil {
			return summary, fmt.Errorf("计算耗时 P50 失败: %w", err)
		}
		if summary.P95DurationMs, err = queryRequestLogPercentile(ctx, s.readDB, where, args, "durationMs", .95); err != nil {
			return summary, fmt.Errorf("计算耗时 P95 失败: %w", err)
		}
	}
	if summary.FirstTokenSamples > 0 {
		if summary.P50FirstTokenMs, err = queryRequestLogPercentile(ctx, s.readDB, where, args, "firstTokenMs", .50); err != nil {
			return summary, fmt.Errorf("计算首字 P50 失败: %w", err)
		}
		if summary.P95FirstTokenMs, err = queryRequestLogPercentile(ctx, s.readDB, where, args, "firstTokenMs", .95); err != nil {
			return summary, fmt.Errorf("计算首字 P95 失败: %w", err)
		}
	}
	return summary, nil
}

func (s *Store) queryRequestStatsPoints(ctx context.Context, filter RequestStatsFilter, interval time.Duration) ([]RequestStatsPoint, error) {
	where, args := requestLogWhere(requestLogFilter{
		From: filter.From, To: filter.To, APIType: filter.APIType, Search: filter.Search,
		Channel: filter.Channel, Model: filter.Model, Status: filter.Status, ErrorType: filter.ErrorType,
		Upstream: filter.Upstream, KeyMask: filter.KeyMask,
	})
	intervalMs := interval.Milliseconds()
	bucketExpr := `(((unix_milli - ?) / ?) * ? + ?)`
	bucketArgs := []any{filter.From, intervalMs, intervalMs, filter.From}
	query := `SELECT ` + bucketExpr + ` AS bucket,
		COUNT(*),
		COALESCE(SUM(CASE WHEN COALESCE(json_extract(payload_json, '$.success'), 0) = 1 THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(COALESCE(CAST(json_extract(payload_json, '$.inputTokens') AS INTEGER), 0)), 0),
		COALESCE(SUM(COALESCE(CAST(json_extract(payload_json, '$.outputTokens') AS INTEGER), 0)), 0),
		COALESCE(SUM(COALESCE(CAST(json_extract(payload_json, '$.cacheCreationTokens') AS INTEGER), 0)), 0),
		COALESCE(SUM(COALESCE(CAST(json_extract(payload_json, '$.cacheReadTokens') AS INTEGER), 0)), 0),
		COALESCE(SUM(CASE WHEN COALESCE(json_extract(payload_json, '$.retried'), 0) = 1 THEN 1 ELSE 0 END), 0),
		COALESCE(AVG(CASE WHEN COALESCE(json_extract(payload_json, '$.durationMs'), 0) > 0 THEN json_extract(payload_json, '$.durationMs') END), 0),
		COALESCE(AVG(CASE WHEN COALESCE(json_extract(payload_json, '$.firstTokenMs'), 0) > 0 THEN json_extract(payload_json, '$.firstTokenMs') END), 0)
		FROM request_logs` + where + ` GROUP BY bucket ORDER BY bucket`
	queryArgs := append(bucketArgs, args...)
	rows, err := s.readDB.QueryContext(ctx, query, queryArgs...)
	if err != nil {
		return nil, fmt.Errorf("查询请求趋势失败: %w", err)
	}
	defer rows.Close()
	type aggregate struct {
		requests, success, input, output, creation, read, retried int64
		duration, firstToken                                      float64
	}
	aggregates := make(map[int64]aggregate)
	for rows.Next() {
		var bucket int64
		var item aggregate
		if err := rows.Scan(&bucket, &item.requests, &item.success, &item.input, &item.output, &item.creation, &item.read, &item.retried, &item.duration, &item.firstToken); err != nil {
			return nil, err
		}
		aggregates[bucket] = item
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	points := make([]RequestStatsPoint, 0, max(1, int((filter.To-filter.From)/intervalMs)+1))
	for start := filter.From; start <= filter.To; start += intervalMs {
		item := aggregates[start]
		point := RequestStatsPoint{
			Timestamp: time.UnixMilli(start), RequestCount: item.requests, SuccessCount: item.success,
			FailureCount: item.requests - item.success, InputTokens: item.input, OutputTokens: item.output,
			CacheCreationTokens: item.creation, CacheReadTokens: item.read, RetriedCount: item.retried,
			AvgDurationMs: item.duration, AvgFirstTokenMs: item.firstToken,
		}
		if point.RequestCount > 0 {
			point.SuccessRate = float64(point.SuccessCount) / float64(point.RequestCount) * 100
		}
		points = append(points, point)
		if intervalMs <= 0 {
			break
		}
	}
	return points, nil
}

func (s *Store) queryRequestStatsDimensions(ctx context.Context, filter RequestStatsFilter, expression, extraWhere string) ([]RequestStatsDimension, error) {
	where, args := requestLogWhere(requestLogFilter{
		From: filter.From, To: filter.To, APIType: filter.APIType, Search: filter.Search,
		Channel: filter.Channel, Model: filter.Model, Status: filter.Status, ErrorType: filter.ErrorType,
		Upstream: filter.Upstream, KeyMask: filter.KeyMask,
	})
	where += extraWhere
	orderBy := "COUNT(*) DESC"
	switch filter.DimensionSort {
	case "failures":
		orderBy = "(COUNT(*) - successes) DESC, COUNT(*) DESC"
	case "failureRate":
		orderBy = "(1.0 - 1.0 * successes / COUNT(*)) DESC, COUNT(*) DESC"
	case "tokens":
		orderBy = "(input_tokens + output_tokens) DESC"
	case "duration":
		orderBy = "avg_duration DESC"
	}

	query := `SELECT ` + expression + ` AS name, COUNT(*),
		COALESCE(SUM(CASE WHEN COALESCE(json_extract(payload_json, '$.success'), 0) = 1 THEN 1 ELSE 0 END), 0) AS successes,
		COALESCE(SUM(COALESCE(CAST(json_extract(payload_json, '$.inputTokens') AS INTEGER), 0)), 0) AS input_tokens,
		COALESCE(SUM(COALESCE(CAST(json_extract(payload_json, '$.outputTokens') AS INTEGER), 0)), 0) AS output_tokens,
		COALESCE(AVG(CASE WHEN COALESCE(json_extract(payload_json, '$.durationMs'), 0) > 0 THEN json_extract(payload_json, '$.durationMs') END), 0) AS avg_duration
		FROM request_logs` + where + ` GROUP BY name ORDER BY ` + orderBy + `, name LIMIT 20`
	rows, err := s.readDB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("查询统计维度失败: %w", err)
	}
	defer rows.Close()
	items := make([]RequestStatsDimension, 0)
	for rows.Next() {
		var item RequestStatsDimension
		if err := rows.Scan(&item.Name, &item.RequestCount, &item.SuccessCount, &item.InputTokens, &item.OutputTokens, &item.AvgDurationMs); err != nil {
			return nil, err
		}
		item.FailureCount = item.RequestCount - item.SuccessCount
		item.TotalTokens = item.InputTokens + item.OutputTokens
		if item.RequestCount > 0 {
			item.SuccessRate = float64(item.SuccessCount) / float64(item.RequestCount) * 100
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) queryRequestStatsFacets(ctx context.Context, filter RequestStatsFilter) (RequestStatsFacets, error) {
	facets := RequestStatsFacets{Channels: []string{}, Models: []string{}}
	for _, item := range []struct {
		expression string
		target     *[]string
	}{{requestChannelExpr, &facets.Channels}, {requestModelExpr, &facets.Models}} {
		where, args := requestLogWhere(requestLogFilter{From: filter.From, To: filter.To, APIType: filter.APIType})
		rows, err := s.readDB.QueryContext(ctx, `SELECT DISTINCT `+item.expression+` AS name FROM request_logs`+where+` ORDER BY name LIMIT 200`, args...)
		if err != nil {
			return facets, fmt.Errorf("读取统计筛选项失败: %w", err)
		}
		for rows.Next() {
			var value string
			if err := rows.Scan(&value); err != nil {
				rows.Close()
				return facets, err
			}
			if strings.TrimSpace(value) != "" {
				*item.target = append(*item.target, value)
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return facets, err
		}
		rows.Close()
	}
	return facets, nil
}

func (s *Store) QueryRequestStats(ctx context.Context, filter RequestStatsFilter, interval time.Duration, previous *RequestStatsFilter) (RequestStatsResult, error) {
	result := RequestStatsResult{
		From: time.UnixMilli(filter.From), To: time.UnixMilli(filter.To), Interval: interval.String(),
		DataPoints: []RequestStatsPoint{}, Channels: []RequestStatsDimension{}, Models: []RequestStatsDimension{}, Errors: []RequestStatsDimension{},
		Facets: RequestStatsFacets{Channels: []string{}, Models: []string{}},
	}
	if s == nil || s.readDB == nil {
		return result, fmt.Errorf("请求日志存储未初始化")
	}
	if filter.From <= 0 || filter.To < filter.From || interval < time.Minute {
		return result, fmt.Errorf("统计时间范围或聚合粒度无效")
	}
	if count := (filter.To-filter.From)/interval.Milliseconds() + 1; count > 200 {
		return result, fmt.Errorf("统计数据点超过 200 个，请增大聚合粒度")
	}
	result.RetentionDays = s.storagePolicy().CallDays
	var err error
	if result.Summary, err = s.queryRequestStatsSummary(ctx, filter); err != nil {
		return result, err
	}
	if result.DataPoints, err = s.queryRequestStatsPoints(ctx, filter, interval); err != nil {
		return result, err
	}
	for _, dimension := range []struct {
		items      *[]RequestStatsDimension
		expression string
		predicate  string
	}{
		{&result.Channels, requestChannelExpr, ""},
		{&result.Models, requestModelExpr, ""},
		{&result.Errors, requestErrorExpr, requestErrorWhere},
		{&result.Protocols, "api_type", ` AND api_type <> ''`},
		{&result.Statuses, requestStatusExpr, ""},
		{&result.Upstreams, requestUpstreamExpr, ""},
		{&result.Keys, requestKeyMaskExpr, ""},
	} {
		if *dimension.items, err = s.queryRequestStatsDimensions(ctx, filter, dimension.expression, dimension.predicate); err != nil {
			return result, err
		}
	}
	if result.Facets, err = s.queryRequestStatsFacets(ctx, filter); err != nil {
		return result, err
	}
	if previous != nil {
		value, err := s.queryRequestStatsSummary(ctx, *previous)
		if err != nil {
			return result, err
		}
		result.PreviousSummary = &value
	}
	return result, nil
}
