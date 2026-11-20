package logger

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func recordStatsAttempt(t *testing.T, store *Store, stamp int64, requestID, attemptID, apiType string, payload map[string]any) {
	t.Helper()
	payload["requestId"] = requestID
	payload["attemptId"] = attemptID
	body, err := json.Marshal(payload)
	require.NoError(t, err)
	require.NoError(t, store.RecordRequest(RequestLogRecord{UnixMilli: stamp, RequestID: requestID, APIType: apiType, Payload: body}))
}

func TestRequestStatsAndLogPageUsePersistentRichFields(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "logs.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	now := time.Now().UnixMilli()
	recordStatsAttempt(t, store, now, "request-1", "attempt-1", "responses", map[string]any{
		"status": "completed", "success": true, "durationMs": 1000, "firstTokenMs": 200,
		"inputTokens": 100, "outputTokens": 20, "cacheReadTokens": 40, "channelName": "渠道 A", "resolvedModel": "gpt-test", "tps": 20,
	})
	recordStatsAttempt(t, store, now+1, "request-1", "attempt-2", "responses", map[string]any{
		"status": "failed", "success": false, "durationMs": 3000, "inputTokens": 200,
		"retried": true, "channelName": "渠道 B", "resolvedModel": "gpt-test", "errorType": "network", "errorMessage": "连接中断",
		"baseUrl": "https://upstream.example", "keyMask": "key-***",
	})
	recordStatsAttempt(t, store, now+2, "request-2", "attempt-3", "chat", map[string]any{
		"status": "completed", "success": true, "durationMs": 2000, "firstTokenMs": 400,
		"inputTokens": 50, "outputTokens": 10, "cacheCreationTokens": 5, "channelName": "渠道 A", "resolvedModel": "claude-test",
	})
	require.NoError(t, store.Flush())

	filter := RequestStatsFilter{From: now - 1, To: now + 2, APIType: "responses", DimensionSort: "failures"}
	result, err := store.QueryRequestStats(context.Background(), filter, time.Minute, nil)
	require.NoError(t, err)
	require.Equal(t, int64(2), result.Summary.TotalRequests)
	require.Equal(t, int64(1), result.Summary.TotalCalls)
	require.Equal(t, int64(1), result.Summary.TotalSuccess)
	require.Equal(t, int64(1), result.Summary.TotalFailure)
	require.Equal(t, 50.0, result.Summary.AvgSuccessRate)
	require.Equal(t, 50.0, result.Summary.RetryRate)
	require.Equal(t, 2000.0, result.Summary.AvgDurationMs)
	require.Equal(t, 1000.0, result.Summary.P50DurationMs)
	require.Equal(t, 3000.0, result.Summary.P95DurationMs)
	require.Equal(t, int64(300), result.Summary.TotalInputTokens)
	require.Equal(t, int64(20), result.Summary.TotalOutputTokens)
	require.InDelta(t, 11.764, result.Summary.CacheHitRate, .01)
	require.Len(t, result.Channels, 2)
	require.Equal(t, "渠道 B", result.Channels[0].Name)
	require.Len(t, result.Protocols, 1)
	require.Equal(t, "responses", result.Protocols[0].Name)
	require.Len(t, result.Statuses, 2)
	require.Equal(t, "failed", result.Statuses[0].Name)
	require.Len(t, result.Upstreams, 2)
	require.Equal(t, "https://upstream.example", result.Upstreams[0].Name)
	require.Len(t, result.Keys, 2)
	require.Equal(t, "key-***", result.Keys[0].Name)
	require.Len(t, result.Models, 1)
	require.Len(t, result.Errors, 1)
	require.Equal(t, "network", result.Errors[0].Name)
	require.ElementsMatch(t, []string{"渠道 A", "渠道 B"}, result.Facets.Channels)
	require.Len(t, result.DataPoints, 1)
	require.Equal(t, int64(2), result.DataPoints[0].RequestCount)

	retried := true
	page, err := store.QueryRequestLogPage(context.Background(), RequestLogPageOptions{From: now - 1, To: now + 2, Retried: &retried})
	require.NoError(t, err)
	require.Equal(t, int64(1), page.Total)
	require.Len(t, page.Payloads, 1)
	require.Contains(t, string(page.Payloads[0]), "attempt-2")

	page, err = store.QueryRequestLogPage(context.Background(), RequestLogPageOptions{From: now - 1, To: now + 2, PageSize: 1, Page: 2, Sort: "durationMs", Order: "desc"})
	require.NoError(t, err)
	require.Equal(t, int64(3), page.Total)
	require.Len(t, page.Payloads, 1)
	require.Contains(t, string(page.Payloads[0]), "attempt-3")

	page, err = store.QueryRequestLogPage(context.Background(), RequestLogPageOptions{Upstream: result.Upstreams[0].Name, KeyMask: result.Keys[0].Name})
	require.NoError(t, err)
	require.Equal(t, int64(1), page.Total)
	require.Contains(t, string(page.Payloads[0]), "attempt-2")
}
