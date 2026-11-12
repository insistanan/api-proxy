package logger

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestContextSnapshotSeparatesToolsAndPreservesCaptureBoundary(t *testing.T) {
	entry := TrafficLog{Phase: PhaseClientRequest, Body: `{"instructions":"遵守项目约定","tools":[{"type":"function","name":"read"}],"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"读取文件"}]},{"type":"function_call","call_id":"call-a","name":"read","arguments":"{}"},{"type":"function_call_output","call_id":"call-a","output":"文件内容"}]}`}
	snapshot := AnalyzeContext(entry)
	require.Equal(t, "captured", snapshot.Completeness)
	require.Equal(t, 3, snapshot.MessageCount)
	parts := make(map[string]ContextPart)
	for _, part := range snapshot.Parts {
		parts[part.Kind] = part
	}
	require.Equal(t, 1, parts["tools"].Items)
	require.Equal(t, 1, parts["tool_call"].Items)
	require.Equal(t, 1, parts["tool_result"].Items)
	require.Positive(t, snapshot.EstimatedTokens)
	entry.Truncated = true
	require.Equal(t, "unavailable", AnalyzeContext(entry).Completeness)
	entry.Truncated = false
	entry.Body = `{"input":[],"previous_response_id":"resp-a"}`
	referenced := AnalyzeContext(entry)
	require.Equal(t, "resp-a", referenced.PreviousResponseID)
	require.False(t, CompareContexts("previous", snapshot, referenced).Comparable)
}

func TestContextChangesTrackReplacementOrderAndSummaryLimits(t *testing.T) {
	makeSnapshot := func(body string) *ContextSnapshot {
		return AnalyzeContext(TrafficLog{Phase: PhaseClientRequest, Body: body})
	}
	previous := makeSnapshot(`{"messages":[{"role":"user","content":"A"},{"role":"assistant","content":"B"}]}`)
	current := makeSnapshot(`{"messages":[{"role":"assistant","content":"B"},{"role":"user","content":"A"}]}`)
	change := CompareContexts("previous", previous, current)
	require.True(t, change.Comparable)
	require.True(t, change.OrderChanged)
	require.Zero(t, change.Added)
	require.Zero(t, change.Removed)
	replaced := makeSnapshot(`{"messages":[{"role":"user","content":"C"}]}`)
	change = CompareContexts("previous", previous, replaced)
	require.Equal(t, 1, change.Added)
	require.Equal(t, 2, change.Removed)
	summary := AnalyzeContext(TrafficLog{Phase: PhaseClientInput, Body: `{"messages":[{"role":"user","content":"尾部"}],"_log":{"originalMessages":100,"messageOffset":99}}`})
	require.Equal(t, "summary", summary.Completeness)
	require.Equal(t, 100, summary.OriginalMessages)
	require.False(t, CompareContexts("previous", previous, summary).Comparable)
}

func TestLogSearchMatchesOlderCallWithoutChangingSessionCount(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "logs.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	now := time.Now().UnixMilli()
	state := RequestLifecycle{ConversationID: "one", StartedAt: now, Status: "failed", PromptPreview: "修复日志 搜索_100%", ClientFamily: "codex"}
	body, err := json.Marshal(state)
	require.NoError(t, err)
	require.NoError(t, store.RecordTraffic(TrafficLog{RequestID: "older", APIType: "responses", Phase: PhaseRequestEnd, UnixMilli: now, Body: string(body)}))
	recordIndexedRound(t, store, "latest", "one", now+10, "completed")
	recordIndexedRound(t, store, "another", "two", now+20, "completed")
	require.NoError(t, store.Flush())
	page, err := store.ListLogConversations(context.Background(), LogConversationListOptions{Search: "搜索_100%", Status: "failed"})
	require.NoError(t, err)
	require.Equal(t, int64(1), page.Total)
	require.Len(t, page.Conversations, 1)
	require.Equal(t, int64(2), page.Conversations[0].RequestCount)
	require.Equal(t, "latest", page.Conversations[0].Latest.RequestID)
	require.Equal(t, state.PromptPreview, page.Conversations[0].Title)
	cursor, err := encodeLogCursor(page.Conversations[0].Latest.UnixMilli, page.Conversations[0].Key)
	require.NoError(t, err)
	exhausted, err := store.ListLogConversations(context.Background(), LogConversationListOptions{Search: "搜索_100%", Cursor: cursor})
	require.NoError(t, err)
	require.Empty(t, exhausted.Conversations)
	require.Equal(t, int64(1), exhausted.Total)
	page, err = store.ListLogConversations(context.Background(), LogConversationListOptions{Search: "搜索_100%", From: now + 1})
	require.NoError(t, err)
	require.Empty(t, page.Conversations)
}

func TestContextOverviewIncludesFullSessionWithBoundedBuckets(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "logs.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	now := time.Now().UnixMilli()
	for index := 0; index < 405; index++ {
		state := RequestLifecycle{ConversationID: "one", StartedAt: now + int64(index), Status: "completed"}
		if index != 404 {
			state.Context = &ContextSnapshot{Completeness: "captured", EstimatedTokens: index + 1, Parts: []ContextPart{{Kind: "user", EstimatedTokens: index + 1}}}
		}
		body, err := json.Marshal(state)
		require.NoError(t, err)
		require.NoError(t, store.RecordTraffic(TrafficLog{RequestID: fmt.Sprintf("request-%03d", index), APIType: "responses", Phase: PhaseRequestEnd, UnixMilli: now + int64(index), Body: string(body)}))
	}
	require.NoError(t, store.Flush())
	overview, err := store.GetContextOverview(context.Background(), "responses:one")
	require.NoError(t, err)
	require.Equal(t, int64(405), overview.Calls)
	require.Equal(t, int64(404), overview.KnownCalls)
	require.LessOrEqual(t, len(overview.Points), 200)
	require.Equal(t, "request-002", overview.Points[0].RequestID)
	require.Equal(t, 3, overview.Points[0].Peak.EstimatedTokens)
}

func TestToolLinksJoinCallsAndResultsAcrossRequestsAndCleanWithTraffic(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "logs.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	now := time.Now().UnixMilli()
	recordIndexedRound(t, store, "call", "one", now, "completed")
	recordIndexedRound(t, store, "result", "one", now+10, "completed")
	recordIndexedRound(t, store, "unrelated", "two", now+20, "completed")
	for _, entry := range []TrafficLog{
		{RequestID: "call", Phase: PhaseStreamSynth, Body: `{"output":[{"type":"function_call","call_id":"call-a","name":"read","arguments":"{}"}]}`},
		{RequestID: "result", Phase: PhaseClientRequest, Body: `{"input":[{"type":"function_call_output","call_id":"call-a","output":"ok"}]}`},
		{RequestID: "unrelated", Phase: PhaseClientRequest, Body: `{"input":[{"type":"function_call_output","call_id":"call-a","output":"other session"}]}`},
	} {
		require.NoError(t, store.RecordTraffic(entry))
	}
	require.NoError(t, store.Flush())
	detail, err := store.GetSystemLogDetail(context.Background(), "call")
	require.NoError(t, err)
	require.NotNil(t, detail.ToolLinks)
	require.Len(t, detail.ToolLinks.Observations, 2)
	require.Equal(t, "model_output", detail.ToolLinks.Observations[0].Source)
	require.Equal(t, "result", detail.ToolLinks.Observations[1].RequestID)
	_, err = store.Purge(context.Background(), PurgeOptions{All: true})
	require.NoError(t, err)
	var remaining int
	require.NoError(t, store.db.QueryRow(`SELECT COUNT(*) FROM log_tool_observations`).Scan(&remaining))
	require.Zero(t, remaining)
}

func TestContextItemLimitStillCountsAllCapturedContent(t *testing.T) {
	messages := make([]map[string]string, maxContextItems+1)
	for index := range messages {
		messages[index] = map[string]string{"role": "user", "content": fmt.Sprintf("内容-%d", index)}
	}
	body, err := json.Marshal(map[string]any{"messages": messages})
	require.NoError(t, err)
	snapshot := AnalyzeContext(TrafficLog{Phase: PhaseClientRequest, Body: string(body)})
	require.Equal(t, "captured", snapshot.Completeness)
	require.True(t, snapshot.ItemsLimited)
	require.Len(t, snapshot.Items, maxContextItems)
	require.Equal(t, maxContextItems+1, snapshot.Parts[0].Items)
	require.False(t, CompareContexts("previous", snapshot, snapshot).Comparable)
}

func TestContextChangePreservesRoleAndToolOwnershipButIgnoresObjectKeyOrder(t *testing.T) {
	snapshot := func(body string) *ContextSnapshot {
		return AnalyzeContext(TrafficLog{Phase: PhaseClientRequest, Body: body})
	}
	previous := snapshot(`{"messages":[{"role":"tool","tool_call_id":"a","content":"ok"}]}`)
	current := snapshot(`{"messages":[{"role":"tool","tool_call_id":"b","content":"ok"}]}`)
	change := CompareContexts("previous", previous, current)
	require.Equal(t, 1, change.Added)
	require.Equal(t, 1, change.Removed)
	previous = snapshot(`{"messages":[{"role":"user","content":[{"type":"text","text":"ok"}]}]}`)
	current = snapshot(`{"messages":[{"content":[{"text":"ok","type":"text"}],"role":"user"}]}`)
	change = CompareContexts("previous", previous, current)
	require.True(t, change.Comparable)
	require.Zero(t, change.Added)
	require.Zero(t, change.Removed)
}

func TestToolObservationsUseExplicitGeminiIDsAndIgnoreNameOnlyResults(t *testing.T) {
	entry := TrafficLog{Phase: PhaseClientRequest, Body: `{"contents":[{"role":"model","parts":[{"functionCall":{"id":"call-a","name":"read","args":{}}}]},{"role":"user","parts":[{"functionResponse":{"id":"call-a","name":"read","response":{"ok":true}}},{"functionResponse":{"name":"read","response":{"ok":true}}}]}]}`}
	observations := extractToolObservations(entry)
	require.Len(t, observations, 2)
	require.Equal(t, "call", observations[0].Role)
	require.Equal(t, "result", observations[1].Role)
	require.Equal(t, observations[0].CallID, observations[1].CallID)
	require.Equal(t, "input_history", observations[1].Source)
	require.Empty(t, extractToolObservations(TrafficLog{Phase: PhaseStreamSynth, Body: `{"output":[{"type":"function_call","id":"item-id","name":"read"}]}`}))
	entry.Truncated = true
	require.Empty(t, extractToolObservations(entry))
}

func TestRuntimeLogCursorPreservesSameMillisecondRowsAcrossByteBudget(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "logs.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	now := time.Now().UnixMilli()
	for index := 0; index < 3; index++ {
		require.NoError(t, store.RecordAppLog(AppLog{UnixMilli: now, Tag: "Context-Test", Message: fmt.Sprintf("%d 搜索_100%% %s", index, strings.Repeat("x", 600*1024))}))
	}
	require.NoError(t, store.RecordAppLog(AppLog{UnixMilli: now, Tag: "Other", Message: "不匹配"}))
	require.NoError(t, store.Flush())
	seen := make(map[int64]bool)
	cursor := ""
	for index := 0; index < 3; index++ {
		page, err := store.ListRuntimeLogs(context.Background(), RuntimeLogOptions{Search: "搜索_100%", Before: cursor})
		require.NoError(t, err)
		require.Len(t, page.Logs, 1)
		require.False(t, seen[page.Logs[0].ID])
		seen[page.Logs[0].ID] = true
		cursor = page.NextCursor
		if index < 2 {
			require.NotEmpty(t, cursor)
		} else {
			require.Empty(t, cursor)
		}
	}
	_, err = store.ListRuntimeLogs(context.Background(), RuntimeLogOptions{Before: "invalid"})
	require.ErrorIs(t, err, ErrInvalidLogCursor)
}
