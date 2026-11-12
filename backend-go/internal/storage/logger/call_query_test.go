package logger

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestLogCallsPageByRequestAndKeepWholeAttemptChain(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "logs.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	now := time.Now().UnixMilli()
	recordIndexedRound(t, store, "retry-call", "one", now, "completed")
	recordIndexedRound(t, store, "local-failure", "two", now+1000, "failed")
	for index := 0; index < 60; index++ {
		body, err := json.Marshal(map[string]any{"requestId": "retry-call", "attemptId": fmt.Sprintf("attempt-%02d", index), "status": "failed", "channelName": fmt.Sprintf("渠道-%d", index), "errorMessage": "需故障转移"})
		require.NoError(t, err)
		require.NoError(t, store.RecordRequest(RequestLogRecord{RequestID: "retry-call", APIType: "responses", UnixMilli: now + int64(index), Payload: body}))
	}
	// 同一尝试更新最终结果，计数应去重，展开保留更新后的指标。
	require.NoError(t, store.RecordRequest(RequestLogRecord{RequestID: "retry-call", APIType: "responses", UnixMilli: now + 100, Payload: []byte(`{"attemptId":"attempt-59","status":"completed","channelName":"最终渠道"}`)}))
	require.NoError(t, store.Flush())
	page, err := store.ListLogCalls(context.Background(), LogConversationListOptions{Limit: 1})
	require.NoError(t, err)
	require.Len(t, page.Calls, 1)
	require.Equal(t, "local-failure", page.Calls[0].Summary.RequestID)
	require.Zero(t, page.Calls[0].AttemptCount)
	page, err = store.ListLogCalls(context.Background(), LogConversationListOptions{Limit: 1, Cursor: page.NextCursor})
	require.NoError(t, err)
	require.Len(t, page.Calls, 1)
	require.Equal(t, "retry-call", page.Calls[0].Summary.RequestID)
	require.Equal(t, "completed", page.Calls[0].Summary.Status)
	require.Equal(t, int64(60), page.Calls[0].AttemptCount)
	chain, err := store.GetLogCallAttempts(context.Background(), "retry-call")
	require.NoError(t, err)
	require.Len(t, chain.Attempts, 60)
	require.False(t, chain.Limited)
	require.Contains(t, string(chain.Attempts[59]), "最终渠道")
	require.NotContains(t, string(chain.Attempts[59]), "需故障转移")
	local, err := store.GetLogCallAttempts(context.Background(), "local-failure")
	require.NoError(t, err)
	require.Empty(t, local.Attempts)
}

func TestLogCallsStableCursorFiltersAndMissingMetricsIDs(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "logs.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	now := time.Now().UnixMilli()
	for _, id := range []string{"a", "b", "c"} {
		recordIndexedRound(t, store, id, "one", now, "completed")
	}
	for index := 0; index < 2; index++ {
		require.NoError(t, store.RecordRequest(RequestLogRecord{RequestID: "b", APIType: "responses", UnixMilli: now, Payload: []byte(`{"status":"failed","errorMessage":"legacy"}`)}))
	}
	require.NoError(t, store.Flush())
	seen := make(map[string]bool)
	cursor := ""
	for index := 0; index < 3; index++ {
		page, err := store.ListLogCalls(context.Background(), LogConversationListOptions{Cursor: cursor, Limit: 1, From: now, To: now, APIType: "RESPONSES", Status: "completed"})
		require.NoError(t, err)
		require.Len(t, page.Calls, 1)
		id := page.Calls[0].Summary.RequestID
		require.False(t, seen[id])
		seen[id] = true
		if id == "b" {
			require.Equal(t, int64(2), page.Calls[0].AttemptCount)
		}
		cursor = page.NextCursor
	}
	require.Empty(t, cursor)
	_, err = store.ListLogCalls(context.Background(), LogConversationListOptions{Cursor: "bad!"})
	require.ErrorIs(t, err, ErrInvalidLogCursor)
	_, err = store.GetLogCallAttempts(context.Background(), "missing")
	require.ErrorIs(t, err, ErrLogGroupNotFound)
}

func TestLogCallsBackfillMetricsOnlyAndRemovePurgedIndex(t *testing.T) {
	path := filepath.Join(t.TempDir(), "logs.db")
	store, err := Open(path)
	require.NoError(t, err)
	now := time.Now().UnixMilli()
	require.NoError(t, store.RecordRequest(RequestLogRecord{RequestID: "metrics-only", APIType: "responses", UnixMilli: now, Payload: []byte(`{"attemptId":"one","status":"failed","channelName":"历史渠道","conversationId":"old"}`)}))
	require.NoError(t, store.Flush())
	// 模拟旧版本已推进回填水位，但没有为纯指标请求创建摘要。
	_, err = store.db.Exec(`DELETE FROM log_request_index WHERE request_id = 'metrics-only'`)
	require.NoError(t, err)
	_, err = store.db.Exec(`UPDATE log_index_state SET request_id = (SELECT MAX(id) FROM request_logs)`)
	require.NoError(t, err)
	require.NoError(t, store.Close())
	store, err = Open(path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	page, err := store.ListLogCalls(context.Background(), LogConversationListOptions{})
	require.NoError(t, err)
	require.Len(t, page.Calls, 1)
	require.Equal(t, "metrics-only", page.Calls[0].Summary.RequestID)
	require.Equal(t, now, page.Calls[0].Summary.StartedAt)
	detail, err := store.GetSystemLogDetail(context.Background(), "metrics-only")
	require.NoError(t, err)
	require.NotNil(t, detail)
	require.Empty(t, detail.TrafficLogs)
	require.Equal(t, page.Calls[0].Summary, detail.Summary)
	_, err = store.Purge(context.Background(), PurgeOptions{Date: time.UnixMilli(now).In(time.Local).Format("2006-01-02")})
	require.NoError(t, err)
	page, err = store.ListLogCalls(context.Background(), LogConversationListOptions{})
	require.NoError(t, err)
	require.Empty(t, page.Calls)
	require.NoError(t, store.RecordRequest(RequestLogRecord{RequestID: "purge-all", APIType: "responses", UnixMilli: now, Payload: []byte(`{"status":"completed"}`)}))
	require.NoError(t, store.Flush())
	_, err = store.Purge(context.Background(), PurgeOptions{All: true})
	require.NoError(t, err)
	page, err = store.ListLogCalls(context.Background(), LogConversationListOptions{})
	require.NoError(t, err)
	require.Empty(t, page.Calls)
}
