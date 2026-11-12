package logger

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/insistanan/api-proxy/internal/orchestration/availability"
	"github.com/stretchr/testify/require"
)

func TestAvailabilityPersistsAggregatesAndFollowsLogDeletion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "logs.db")
	store, err := Open(path)
	require.NoError(t, err)
	now := time.Now()
	for i := 0; i < 65; i++ {
		require.NoError(t, store.RecordRequest(RequestLogRecord{UnixMilli: now.UnixMilli(), RequestID: "retry-chain", APIType: "chat",
			Payload:      []byte(`{"requestId":"retry-chain","model":"m","status":"completed"}`),
			Availability: &availability.Sample{Kind: "chat", ChannelID: "stable-id", Model: "m", CandidateID: "fingerprint", Outcome: availability.Success, Reason: availability.Success}}))
	}
	// 旧日志没有稳定身份，不根据 channelIndex 补造健康证据。
	require.NoError(t, store.RecordRequest(RequestLogRecord{UnixMilli: now.UnixMilli(), Payload: []byte(`{"channelIndex":0,"model":"m","success":true}`)}))
	require.NoError(t, store.Flush())
	require.NoError(t, store.Close())
	store, err = Open(path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	rows, err := store.QueryAvailability(context.Background(), "chat", now.Add(time.Second))
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, int64(65), rows[0].Count, "不能沿用请求日志列表的 50 条截断")
	other, err := store.QueryAvailability(context.Background(), "messages", now.Add(time.Second))
	require.NoError(t, err)
	require.Empty(t, other)
	_, err = store.db.Exec(`DELETE FROM request_logs WHERE request_id = ?`, "retry-chain")
	require.NoError(t, err)
	rows, err = store.QueryAvailability(context.Background(), "chat", now.Add(time.Second))
	require.NoError(t, err)
	require.Empty(t, rows)
}

func TestAvailabilityUsesLatestCooldownWithinSameBucket(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "logs.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	now := time.Now().Truncate(time.Hour).Add(10 * time.Minute)
	for i, cooldown := range []time.Duration{time.Hour, time.Minute} {
		require.NoError(t, store.RecordRequest(RequestLogRecord{UnixMilli: now.Add(time.Duration(i) * time.Second).UnixMilli(),
			Payload: []byte(`{}`), Availability: &availability.Sample{Kind: "chat", ChannelID: "c", Model: "m", CandidateID: "key",
				Outcome: availability.RateLimited, Reason: "rate_limited", CooldownUntil: now.Add(cooldown).UnixMilli()}}))
	}
	require.NoError(t, store.Flush())
	rows, err := store.QueryAvailability(context.Background(), "chat", now.Add(2*time.Second))
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, now.Add(time.Minute).UnixMilli(), rows[0].CooldownUntil, "最近一次明确冷却时间不能被同一小时内旧的较长时间覆盖")
}

func TestChannelAvailabilityIsolatesChannelAndProtocol(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "logs.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	now := time.Now()
	for _, scope := range []struct{ kind, channel string }{{"chat", "free"}, {"chat", "other"}, {"messages", "free"}} {
		require.NoError(t, store.RecordRequest(RequestLogRecord{UnixMilli: now.UnixMilli(), Payload: []byte(`{}`),
			Availability: &availability.Sample{Kind: scope.kind, ChannelID: scope.channel, Model: "m",
				CandidateID: "candidate", Outcome: availability.Success}}))
	}
	require.NoError(t, store.Flush())
	rows, err := store.QueryChannelAvailability(context.Background(), "chat", "free", now.Add(time.Second))
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "free", rows[0].ChannelID)
	require.Equal(t, int64(1), rows[0].Count)
	_, err = store.QueryChannelAvailability(context.Background(), "chat", "", now)
	require.ErrorContains(t, err, "缺少渠道身份")
}
