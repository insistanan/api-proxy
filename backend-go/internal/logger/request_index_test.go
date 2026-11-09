package logger

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func recordIndexedRound(t *testing.T, store *Store, id, conversationID string, started int64, status string) {
	t.Helper()
	state := RequestLifecycle{ConversationID: conversationID, StartedAt: started, BootID: store.BootID(), Model: "test", Status: status, StatusCode: 200}
	body, err := json.Marshal(state)
	require.NoError(t, err)
	require.NoError(t, store.RecordTraffic(TrafficLog{RequestID: id, APIType: "Responses", Phase: PhaseRequestStart, UnixMilli: started, Body: string(body)}))
	require.NoError(t, store.RecordTraffic(TrafficLog{RequestID: id, APIType: "Responses", Phase: PhaseRequestEnd, UnixMilli: started + 1, Body: string(body)}))
}

func TestConversationIndexPagesBeyondTwoHundredRequests(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "logs.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	now := time.Now().UnixMilli()
	for index := 0; index < 240; index++ {
		recordIndexedRound(t, store, fmt.Sprintf("request-%03d", index), "conversation", now+int64(index*10), "completed")
	}
	recordIndexedRound(t, store, "independent-a", "", now+3000, "completed")
	recordIndexedRound(t, store, "independent-b", "", now+3010, "completed")
	require.NoError(t, store.Flush())
	groups, err := store.ListLogConversations(context.Background(), LogConversationListOptions{Limit: 100})
	require.NoError(t, err)
	require.Len(t, groups.Conversations, 3)
	require.Equal(t, int64(240), groups.Conversations[2].RequestCount)
	seen := make(map[string]bool)
	cursor := ""
	for {
		page, err := store.ListLogConversationRequests(context.Background(), LogConversationRequestOptions{Key: "responses:conversation", Before: cursor, Limit: 37})
		require.NoError(t, err)
		for _, summary := range page.Logs {
			require.False(t, seen[summary.RequestID], "分页不能重复请求")
			seen[summary.RequestID] = true
		}
		cursor = page.NextCursor
		if cursor == "" {
			break
		}
	}
	require.Len(t, seen, 240)
	anchored, err := store.ListLogConversationRequests(context.Background(), LogConversationRequestOptions{Key: "responses:conversation", Anchor: "request-003", Limit: 3})
	require.NoError(t, err)
	require.Equal(t, "request-003", anchored.Logs[len(anchored.Logs)-1].RequestID)
}

func TestConversationIndexKeepsRequestOutcomeAndCleansWithTraffic(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "logs.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	now := time.Now().UnixMilli()
	recordIndexedRound(t, store, "cancelled", "one", now, "cancelled")
	recordIndexedRound(t, store, "done", "two", now+10, "completed")
	require.NoError(t, store.Flush())
	// 开始顺序相同仍须按 requestId 稳定分页，完成顺序不决定输入前序。
	recordIndexedRound(t, store, "tie-a", "tie", now+20, "completed")
	recordIndexedRound(t, store, "tie-b", "tie", now+20, "completed")
	require.NoError(t, store.Flush())
	first, err := store.ListLogConversationRequests(context.Background(), LogConversationRequestOptions{Key: "responses:tie", Limit: 1})
	require.NoError(t, err)
	second, err := store.ListLogConversationRequests(context.Background(), LogConversationRequestOptions{Key: "responses:tie", Before: first.NextCursor, Limit: 1})
	require.NoError(t, err)
	require.NotEqual(t, first.Logs[0].RequestID, second.Logs[0].RequestID)
	list, err := store.ListSystemLogSummaries(context.Background(), SystemLogListOptions{APIType: "RESPONSES"})
	require.NoError(t, err)
	require.Equal(t, "cancelled", list[len(list)-1].Status)
	_, err = store.Purge(context.Background(), PurgeOptions{All: true})
	require.NoError(t, err)
	groups, err := store.ListLogConversations(context.Background(), LogConversationListOptions{})
	require.NoError(t, err)
	require.Empty(t, groups.Conversations)
}

func TestConversationIndexBackfillsLegacyTrafficWithoutRewritingBody(t *testing.T) {
	path := filepath.Join(t.TempDir(), "logs.db")
	store, err := Open(path)
	require.NoError(t, err)
	recordIndexedRound(t, store, "old", "legacy", time.Now().UnixMilli(), "completed")
	require.NoError(t, store.RecordTraffic(TrafficLog{RequestID: "old", APIType: "Responses", Phase: PhaseClientRequest, Body: "原始历史正文"}))
	require.NoError(t, store.Close())
	db, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	// 只操作测试临时库，模拟尚未具有摘要索引的历史版本。
	_, err = db.Exec("DROP TABLE log_request_index")
	require.NoError(t, err)
	_, err = db.Exec("DROP TABLE log_index_state")
	require.NoError(t, err)
	require.NoError(t, db.Close())
	reopened, err := Open(path)
	require.NoError(t, err)
	defer func() { require.NoError(t, reopened.Close()) }()
	groups, err := reopened.ListLogConversations(context.Background(), LogConversationListOptions{})
	require.NoError(t, err)
	require.Len(t, groups.Conversations, 1)
	require.Equal(t, "responses:legacy", groups.Conversations[0].Key)
	rows, err := reopened.QueryTrafficByRequestID(context.Background(), "old")
	require.NoError(t, err)
	var clientBodies []string
	for _, row := range rows {
		if row.Phase == PhaseClientRequest {
			clientBodies = append(clientBodies, row.Body)
		}
	}
	require.Equal(t, []string{"原始历史正文"}, clientBodies)
}

func TestConversationIndexRejectsBadCursorAndCorruptSummary(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "logs.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	recordIndexedRound(t, store, "one", "conversation", time.Now().UnixMilli(), "completed")
	require.NoError(t, store.Flush())
	_, err = store.ListLogConversations(context.Background(), LogConversationListOptions{Cursor: "bad!"})
	require.ErrorIs(t, err, ErrInvalidLogCursor)
	_, err = store.db.Exec("UPDATE log_request_index SET summary_json = '{'")
	require.NoError(t, err)
	_, err = store.ListSystemLogSummaries(context.Background(), SystemLogListOptions{})
	require.Error(t, err)
}

func TestConversationIndexRejectsInvalidMetadataBeforeEnqueue(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "logs.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	require.Error(t, store.RecordTraffic(TrafficLog{RequestID: "bad", Phase: PhaseRequestStart, Body: "{"}))
	require.Error(t, store.RecordRequest(RequestLogRecord{RequestID: "bad", Payload: []byte(`{"status":42}`)}))
	recordIndexedRound(t, store, "good", "conversation", time.Now().UnixMilli(), "completed")
	require.NoError(t, store.Flush())
	groups, err := store.ListLogConversations(context.Background(), LogConversationListOptions{})
	require.NoError(t, err)
	require.Len(t, groups.Conversations, 1)
	require.Equal(t, "good", groups.Conversations[0].Latest.RequestID)
}

func TestConversationIndexRebuildsAfterPartialCleanup(t *testing.T) {
	for _, mode := range []string{"purge", "expired"} {
		t.Run(mode, func(t *testing.T) {
			store, err := Open(filepath.Join(t.TempDir(), "logs.db"))
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, store.Close()) })
			now := time.Now()
			oldDay := now.AddDate(0, 0, -1)
			old := time.Date(oldDay.Year(), oldDay.Month(), oldDay.Day(), 12, 0, 0, 0, time.Local).UnixMilli()
			state := RequestLifecycle{ConversationID: "conversation", StartedAt: old, BootID: store.BootID(), Status: "completed"}
			body, err := json.Marshal(state)
			require.NoError(t, err)
			require.NoError(t, store.RecordTraffic(TrafficLog{RequestID: "cross-day", Phase: PhaseClientRequest, UnixMilli: old, Body: "原始输入"}))
			require.NoError(t, store.RecordTraffic(TrafficLog{RequestID: "cross-day", Phase: PhaseRequestEnd, UnixMilli: now.UnixMilli(), Body: string(body)}))
			require.NoError(t, store.RecordRequest(RequestLogRecord{RequestID: "cross-day", UnixMilli: old, Payload: []byte(`{"channelName":"已清理的渠道"}`)}))
			require.NoError(t, store.Flush())
			if mode == "purge" {
				_, err = store.Purge(context.Background(), PurgeOptions{Date: oldDay.Format("2006-01-02")})
				require.NoError(t, err)
			} else {
				startToday := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local).UnixMilli()
				for _, table := range []string{"traffic_logs", "request_logs"} {
					_, err := store.deleteExpiredIndexedLogs(context.Background(), table, startToday, 32)
					require.NoError(t, err)
				}
			}
			detail, err := store.GetSystemLogDetail(context.Background(), "cross-day")
			require.NoError(t, err)
			list, err := store.ListSystemLogSummaries(context.Background(), SystemLogListOptions{})
			require.NoError(t, err)
			require.Len(t, list, 1)
			require.Equal(t, detail.Summary, list[0])
			require.Empty(t, list[0].ChannelName)
			require.Equal(t, []string{PhaseRequestEnd}, list[0].Phases)
		})
	}
}
