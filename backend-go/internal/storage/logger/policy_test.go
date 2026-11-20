package logger

import (
	"context"
	"io"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCapturePolicyDoesNotRetainDisabledContent(t *testing.T) {
	for _, test := range []struct {
		name         string
		policy       CapturePolicy
		phase, state string
	}{
		{"metadata", CapturePolicy{Request: true, Response: true, Mode: "metadata", BodyLimit: 16384}, PhaseClientRequest, "metadata"},
		{"disabled_request", CapturePolicy{Response: true, Mode: "bounded", BodyLimit: 16384}, PhaseUpstreamRequest, "disabled"},
		{"disabled_response", CapturePolicy{Request: true, Mode: "bounded", BodyLimit: 16384}, PhaseStreamSynth, "disabled"},
		{"budget", CapturePolicy{Request: true, Response: true, Mode: "bounded", BodyLimit: 16384, Paused: true}, PhaseUpstreamResponse, "budget"},
	} {
		t.Run(test.name, func(t *testing.T) {
			entry := applyCapture(TrafficLog{Phase: test.phase, Body: "private content", HeadersJSON: `{"x":"private"}`, OriginalBytes: 15}.WithCapturePolicy(test.policy))
			require.Empty(t, entry.Body)
			require.Empty(t, entry.HeadersJSON)
			require.Equal(t, test.state, entry.BodyState)
			require.Equal(t, 15, entry.OriginalBytes)
			require.Empty(t, extractToolObservations(entry))
		})
	}
}

func TestCapturePolicyLimitsAndSanitizesWithoutChangingSource(t *testing.T) {
	raw := `{"messages":[{"role":"user","content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"SECRET_IMAGE"}}]}]}`
	policy := CapturePolicy{Request: true, Response: true, Mode: "bounded", BodyLimit: 16384}
	entry := applyCapture(TrafficLog{Phase: PhaseClientRequest, Body: raw}.WithCapturePolicy(policy))
	require.NotContains(t, entry.Body, "SECRET_IMAGE")
	require.Contains(t, raw, "SECRET_IMAGE")
	large := strings.Repeat("secret", 4000)
	entry = applyCapture(TrafficLog{Phase: PhaseClientRequest, Body: large}.WithCapturePolicy(policy))
	require.True(t, entry.Truncated)
	require.Equal(t, "truncated", entry.BodyState)
	require.Less(t, len(entry.Body), policy.BodyLimit)
	require.Equal(t, len(large), entry.OriginalBytes)
	require.NotContains(t, entry.Body, "secret")
	// 已读取的响应前缀不能把完整原始长度和截断标记覆盖成“完整”。
	entry = applyCapture(TrafficLog{Phase: PhaseUpstreamResponse, Body: "prefix", OriginalBytes: 99999, Truncated: true}.WithCapturePolicy(policy))
	require.Equal(t, 99999, entry.OriginalBytes)
	require.True(t, entry.Truncated)
}

func TestResponseCapturePreservesTransportAndBoundsBuffer(t *testing.T) {
	for _, mode := range []string{"bounded", "metadata"} {
		t.Run(mode, func(t *testing.T) {
			raw := strings.Repeat("response", 5000)
			policy := CapturePolicy{Request: true, Response: true, Mode: mode, BodyLimit: 16384}
			capture := CaptureResponseBody(io.NopCloser(strings.NewReader(raw)), TrafficLog{}.WithCapturePolicy(policy)).(*responseCapture)
			transport, err := io.ReadAll(capture)
			require.NoError(t, err)
			require.Equal(t, raw, string(transport))
			require.Equal(t, len(raw), capture.entry.OriginalBytes)
			if mode == "metadata" {
				require.Empty(t, capture.buf)
			} else {
				require.Len(t, capture.buf, policy.BodyLimit)
			}
			require.NoError(t, capture.inner.Close())
		})
	}
}

func TestExpireBodiesPreservesSharedBodySummaryAndToolIndex(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "logs.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	cutoff := retentionCutoffDays(time.Now(), 1).UnixMilli()
	old := RetentionCutoff(time.Now()).Add(time.Hour).UnixMilli()
	body := `{"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"tool-1","name":"read","input":{}}]},{"role":"user","content":"` + strings.Repeat("same", 2000) + `"}]}`
	for _, test := range []struct {
		id    string
		stamp int64
	}{{"old", old}, {"new", time.Now().UnixMilli()}} {
		require.NoError(t, store.RecordTraffic(TrafficLog{RequestID: test.id, UnixMilli: test.stamp, Phase: PhaseClientRequest, Body: body}))
		require.NoError(t, store.RecordTraffic(TrafficLog{RequestID: test.id, UnixMilli: test.stamp, Phase: PhaseRequestEnd, Body: `{"status":"completed","conversationId":"conversation"}`}))
	}
	require.NoError(t, store.Flush())
	_, expired, err := store.expireTrafficBodies(cutoff)
	require.NoError(t, err)
	require.EqualValues(t, 1, expired)
	detail, err := store.GetSystemLogDetail(context.Background(), "old")
	require.NoError(t, err)
	require.Equal(t, "completed", detail.Summary.Status)
	require.Equal(t, "conversation", detail.Summary.ConversationID)
	require.Empty(t, detail.TrafficLogs[0].Body)
	require.Equal(t, "expired", detail.TrafficLogs[0].BodyState)
	require.NotNil(t, detail.ToolLinks)
	require.Len(t, detail.ToolLinks.Observations, 2)
	require.Equal(t, "tool-1", detail.ToolLinks.Observations[0].CallID)
	kept, err := store.QueryTrafficByRequestID(context.Background(), "new")
	require.NoError(t, err)
	require.Contains(t, kept[0].Body, "tool-1")
	var count int
	require.NoError(t, store.readDB.QueryRow(`SELECT COUNT(*) FROM traffic_bodies`).Scan(&count))
	require.Equal(t, 1, count)
	require.NoError(t, store.readDB.QueryRow(`SELECT COUNT(*) FROM log_tool_observations WHERE request_id = 'old'`).Scan(&count))
	require.Positive(t, count)
	_, expired, err = store.expireTrafficBodies(cutoff)
	require.NoError(t, err)
	require.Zero(t, expired)
	_, err = store.deleteExpiredIndexedLogs(context.Background(), "traffic_logs", cutoff, 32)
	require.NoError(t, err)
	require.NoError(t, store.readDB.QueryRow(`SELECT COUNT(*) FROM log_tool_observations WHERE request_id = 'old'`).Scan(&count))
	require.Zero(t, count)
	require.NoError(t, store.readDB.QueryRow(`SELECT COUNT(*) FROM traffic_body_state bs LEFT JOIN traffic_logs t ON t.id = bs.traffic_id WHERE t.id IS NULL`).Scan(&count))
	require.Zero(t, count)
}

func TestRetentionPolicyUsesCalendarDays(t *testing.T) {
	now := time.Date(2026, 11, 13, 17, 30, 0, 0, time.Local)
	require.Equal(t, RetentionCutoff(now), retentionCutoffDays(now, 2))
	require.Equal(t, time.Date(2026, 11, 13, 0, 0, 0, 0, time.Local), retentionCutoffDays(now, 1))
	require.Equal(t, time.Date(2026, 11, 7, 0, 0, 0, 0, time.Local), retentionCutoffDays(now, 7))
}

func TestBudgetPauseAndRecoveryThresholds(t *testing.T) {
	for _, test := range []struct {
		used, budget int64
		paused, want bool
	}{
		{64, 64, false, true}, {63, 64, false, false},
		{91, 100, true, true}, {90, 100, true, false},
		{1000, 0, true, false}, {1000, 0, false, false},
	} {
		require.Equal(t, test.want, budgetShouldPause(test.used<<20, test.budget<<20, test.paused))
	}
}

func TestExpireBodyFailureRollsBackBodyStateAndReference(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "logs.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	body := strings.Repeat("retain", 1000)
	require.NoError(t, store.RecordTraffic(TrafficLog{RequestID: "rollback", Phase: PhaseClientRequest, Body: body}))
	require.NoError(t, store.Flush())
	_, err = store.db.Exec(`CREATE TRIGGER fail_body_expiry BEFORE DELETE ON traffic_body_refs BEGIN SELECT RAISE(ABORT, '模拟正文清理失败'); END`)
	require.NoError(t, err)
	_, expired, err := store.expireTrafficBodies(time.Now().Add(time.Hour).UnixMilli())
	require.ErrorContains(t, err, "模拟正文清理失败")
	require.Zero(t, expired)
	logs, err := store.QueryTrafficByRequestID(context.Background(), "rollback")
	require.NoError(t, err)
	require.Equal(t, body, logs[0].Body)
	require.Equal(t, "stored", logs[0].BodyState)
	status, err := store.GetStorageStatus(context.Background())
	require.NoError(t, err)
	require.Contains(t, status.Maintenance.CleanupError, "模拟正文清理失败")
	// 故障冷却期内不会反复执行同一失败清理。
	again, count, err := store.expireTrafficBodies(time.Now().Add(time.Hour).UnixMilli())
	require.NoError(t, err)
	require.False(t, again)
	require.Zero(t, count)
}

func TestCleanupDefersWhileWritesAreBacklogged(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "logs.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	require.NoError(t, store.Flush())
	// 独立状态对象避免与 Open 启动的清理协程竞争模拟积压。
	probe := &Store{db: store.db, readDB: store.readDB, dbPath: store.dbPath,
		queue: make(chan queuedWrite, writeQueueSize), queueStatus: QueueStatus{PendingEntries: writeQueueSize}}
	more, deleted, err := probe.cleanupExpired()
	require.NoError(t, err)
	require.True(t, more)
	require.Zero(t, deleted)
	status, err := probe.GetStorageStatus(context.Background())
	require.NoError(t, err)
	require.True(t, status.Maintenance.CleanupDeferred)
}

func TestBudgetRecoveryIsVisibleInRuntimeLogs(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "logs.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	store.statusMu.Lock()
	store.budgetPaused = true
	store.statusMu.Unlock()
	require.NoError(t, store.refreshBudget(DefaultStoragePolicy()))
	require.NoError(t, store.Flush())
	status, err := store.GetStorageStatus(context.Background())
	require.NoError(t, err)
	require.False(t, status.BodyCapturePaused)
	require.NotEmpty(t, status.BudgetEvents)
	page, err := store.ListRuntimeLogs(context.Background(), RuntimeLogOptions{Search: "Logger-Budget", Limit: 20})
	require.NoError(t, err)
	require.NotEmpty(t, page.Logs)
}

func TestStorageStatusReportsWriteFailure(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "logs.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	require.NoError(t, store.Flush())
	store.queueMu.Lock()
	store.writeErr = io.ErrShortWrite
	store.queueMu.Unlock()
	status, err := store.GetStorageStatus(context.Background())
	require.NoError(t, err)
	require.Contains(t, status.WriteError, io.ErrShortWrite.Error())
	require.Positive(t, status.UsedBytes)
	store.queueMu.Lock()
	store.writeErr = nil
	store.queueMu.Unlock()
}

func TestCleanupUsesHotUpdatedIndependentRetentionPeriods(t *testing.T) {
	var current atomic.Pointer[StoragePolicy]
	initial := DefaultStoragePolicy()
	current.Store(&initial)
	store, err := openWithPolicy(filepath.Join(t.TempDir(), "logs.db"), func() StoragePolicy { return *current.Load() })
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	old := RetentionCutoff(time.Now()).Add(time.Hour).UnixMilli()
	require.NoError(t, store.RecordAppLog(AppLog{UnixMilli: old, Message: "old-runtime"}))
	require.NoError(t, store.RecordTraffic(TrafficLog{UnixMilli: old, RequestID: "old-call", Phase: PhaseClientRequest, Body: `{"messages":[{"role":"user","content":"old-body"}]}`}))
	require.NoError(t, store.RecordRequest(RequestLogRecord{UnixMilli: old, RequestID: "old-call", Payload: []byte(`{"status":"completed"}`)}))
	require.NoError(t, store.Flush())
	updated := StoragePolicy{RuntimeDays: 1, CallDays: 7, BodyDays: 1}
	current.Store(&updated)
	_, _, err = store.cleanupExpired()
	require.NoError(t, err)
	logs, err := store.QueryAppLogs(context.Background(), QueryOptions{To: time.Now().Add(time.Hour).UnixMilli()})
	require.NoError(t, err)
	for _, entry := range logs {
		require.NotEqual(t, "old-runtime", entry.Message)
	}
	requests, err := store.QueryRequestLogs(context.Background(), QueryOptions{RequestID: "old-call"})
	require.NoError(t, err)
	require.Len(t, requests, 1)
	traffic, err := store.QueryTrafficByRequestID(context.Background(), "old-call")
	require.NoError(t, err)
	require.Len(t, traffic, 1)
	require.Equal(t, "expired", traffic[0].BodyState)
	require.Empty(t, traffic[0].Body)
}
