package cachekeepalive

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"insistanan/api-proxy/internal/protocol/types"
)

func TestKeepalive_RejectsUnboundedPolicyBeforeRetainingBody(t *testing.T) {
	m, _, _ := fixture(t)
	reads := 0
	source := sampleSource(sampleBody, "attempt", &reads)
	source.Upstream.CacheKeepalive.MaxAttempts = 0
	source.Upstream.CacheKeepalive.MaxIdleMinutes = 0
	finishReal(m.Begin("session"), source)
	require.Zero(t, reads)
	require.Zero(t, m.bytes)
	require.Empty(t, m.queue)
	require.Equal(t, "stopped", m.Status("channel-test")[0].State)
}

func TestKeepalive_ActiveChannelsExcludeRoundsThatCannotSend(t *testing.T) {
	for _, scenario := range []string{"deadline", "count", "expired-cache", "config-change", "new-request", "manual-stop", "no-next-heartbeat"} {
		t.Run(scenario, func(t *testing.T) {
			m, now, revision := fixture(t)
			reads := 0
			source := sampleSource(sampleBody, "attempt", &reads)
			if scenario == "no-next-heartbeat" {
				source.Upstream.CacheKeepalive.MaxIdleMinutes = 2
			}
			finishReal(m.Begin("session"), source)
			*now = now.Add(time.Minute)
			runDue(t, m)
			switch scenario {
			case "deadline":
				*now = m.entries["session"].deadline
			case "count":
				m.entries["session"].status.Attempts = source.Upstream.CacheKeepalive.MaxAttempts
			case "expired-cache":
				*now = now.Add(5 * time.Minute)
			case "config-change":
				(*revision)++
			case "new-request":
				m.Begin("session")
			case "manual-stop":
				m.StopChannel("channel-test", "session")
			case "no-next-heartbeat":
				require.Zero(t, m.Status("channel-test")[0].NextHeartbeatAt, "检查期限不应展示为心跳时间")
			}
			require.Empty(t, m.ActiveChannels(), "没有可执行保活的会话时应隐藏图标")
		})
	}
}

func TestKeepalive_RechecksDeadlineImmediatelyBeforeSending(t *testing.T) {
	m, now, _ := fixture(t)
	reads := 0
	source := sampleSource(sampleBody, "attempt", &reads)
	source.Upstream.CacheKeepalive.MaxAttempts = 0
	source.Upstream.CacheKeepalive.MaxIdleMinutes = 5
	finishReal(m.Begin("session"), source)
	*now = now.Add(time.Minute)
	runDue(t, m)
	*now = now.Add(220 * time.Second)
	m.dispatch()
	require.Len(t, m.jobs, 1)
	job := <-m.jobs
	// 已调度但尚未执行时达到期限，不得因为调度阶段检查通过就继续发送。
	*now = now.Add(20 * time.Second)
	sends := 0
	m.send = func(context.Context, *snapshot) sendResult { sends++; return sendResult{} }
	m.run(job)
	require.Zero(t, sends)
	require.Zero(t, m.bytes)
	require.Empty(t, m.queue)
	require.Equal(t, "已达到最长保活时间", m.Status("channel-test")[0].Reason)
}

func TestKeepalive_StopsAtFirstConfiguredLimitAndNeverRetriesFailures(t *testing.T) {
	for _, tc := range []struct {
		name              string
		attempts, minutes int
		failed            bool
	}{
		{"次数先到", 1, 20, false},
		{"时间先到", 100, 6, false},
		{"仅按时间", 0, 6, false},
		{"发送失败", 100, 120, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, now, _ := fixture(t)
			started := *now
			reads, sends := 0, 0
			source := sampleSource(sampleBody, "attempt", &reads)
			source.Upstream.CacheKeepalive.MaxAttempts = tc.attempts
			source.Upstream.CacheKeepalive.MaxIdleMinutes = tc.minutes
			finishReal(m.Begin("session"), source)
			*now = started.Add(time.Minute)
			runDue(t, m)
			m.send = func(context.Context, *snapshot) sendResult {
				sends++
				result := sendResult{sent: true, sentAt: *now, usage: &types.Usage{CacheReadInputTokens: 1000}}
				if tc.failed {
					result.err = context.DeadlineExceeded
				}
				return result
			}
			*now = started.Add(280 * time.Second)
			runDue(t, m)
			*now = started.Add(time.Duration(tc.minutes) * time.Minute)
			m.dispatch()
			status := m.Status("channel-test")[0]
			require.Equal(t, "stopped", status.State)
			require.Equal(t, 1, status.Attempts, "发送失败也算一次尝试，不退还后重发")
			require.Equal(t, 1, sends)
			require.Empty(t, m.jobs)
			require.Empty(t, m.queue)
			require.Zero(t, m.bytes)
		})
	}
}

func TestKeepalive_DeadlineCancelsInFlightWithoutSchedulingAnotherHeartbeat(t *testing.T) {
	m, now, _ := fixture(t)
	started := *now
	reads := 0
	source := sampleSource(sampleBody, "attempt", &reads)
	source.Upstream.CacheKeepalive.MaxAttempts = 0
	source.Upstream.CacheKeepalive.MaxIdleMinutes = 5
	finishReal(m.Begin("session"), source)
	*now = started.Add(time.Minute)
	runDue(t, m)
	m.send = func(ctx context.Context, _ *snapshot) sendResult {
		sentAt := *now
		*now = started.Add(5 * time.Minute)
		m.clean()
		require.ErrorIs(t, ctx.Err(), context.Canceled)
		return sendResult{sent: true, sentAt: sentAt, usage: &types.Usage{CacheReadInputTokens: 1000}}
	}
	*now = started.Add(280 * time.Second)
	runDue(t, m)
	require.Equal(t, "stopped", m.Status("channel-test")[0].State)
	require.Empty(t, m.queue)
	require.Zero(t, m.bytes)
}

func TestKeepalive_MissingSendProgressStopsInsteadOfRepeating(t *testing.T) {
	for _, scenario := range []string{"missing-send", "missing-time", "stale-time"} {
		t.Run(scenario, func(t *testing.T) {
			m, now, _ := fixture(t)
			reads := 0
			source := sampleSource(sampleBody, "attempt", &reads)
			source.Upstream.CacheKeepalive.MaxIdleMinutes = 0
			finishReal(m.Begin("session"), source)
			*now = now.Add(time.Minute)
			runDue(t, m)
			sends := 0
			m.send = func(_ context.Context, s *snapshot) sendResult {
				sends++
				result := sendResult{sent: true, sentAt: *now, usage: &types.Usage{CacheReadInputTokens: 1000}}
				switch scenario {
				case "missing-send":
					result.sent = false
				case "missing-time":
					result.sentAt = time.Time{}
				case "stale-time":
					result.sentAt = s.lastSent
				}
				return result
			}
			*now = now.Add(220 * time.Second)
			runDue(t, m)
			for range 3 {
				*now = now.Add(280 * time.Second)
				m.dispatch()
			}
			require.Equal(t, 1, sends)
			require.Equal(t, "stopped", m.Status("channel-test")[0].State)
			require.Empty(t, m.queue)
			require.Empty(t, m.jobs)
			require.Zero(t, m.bytes)
		})
	}
}

func TestKeepalive_SameChannelSessionsKeepSeparateBodiesCountersAndIdleTimes(t *testing.T) {
	m, now, _ := fixture(t)
	started := *now
	readsA, readsB := 0, 0
	source := func(conversation, attempt string, reads *int) Source {
		value := sampleSource(strings.ReplaceAll(sampleBody, "真实请求", attempt), attempt, reads)
		value.ConversationID = conversation
		return value
	}
	oldA := m.Begin("session-A")
	oldA.Capture(source("conv-A", "old-A", &readsA))
	turnB := m.Begin("session-B")
	turnB.Capture(source("conv-B", "request-B", &readsB))
	finishReal(m.Begin("session-A"), source("conv-A", "latest-A", &readsA))
	turnB.Result("request-B", true, &types.Usage{InputTokens: 10, CacheReadInputTokens: 1000})
	turnB.End()
	*now = started.Add(time.Minute)
	runDue(t, m)
	require.Zero(t, readsA, "A 仍有真实请求在途，不能提前准备")
	require.Equal(t, 1, readsB)
	*now = started.Add(80 * time.Second)
	oldA.Result("old-A", true, &types.Usage{CacheReadInputTokens: 9999})
	oldA.End()
	*now = started.Add(140 * time.Second)
	runDue(t, m)
	require.Equal(t, 1, readsA)
	require.Equal(t, "latest-A", m.entries["session-A"].snapshot.source.AttemptID)
	require.Equal(t, started.Add(360*time.Second).UnixMilli(), m.entries["session-A"].status.NextAt)
	require.Equal(t, started.Add(280*time.Second).UnixMilli(), m.entries["session-B"].status.NextAt)
	sent := map[string][]string{}
	m.send = func(_ context.Context, s *snapshot) sendResult {
		sent[s.source.ConversationID] = append(sent[s.source.ConversationID], string(s.body))
		return sendResult{sent: true, sentAt: *now, usage: &types.Usage{InputTokens: 10, CacheReadInputTokens: 1000}}
	}
	*now = started.Add(280 * time.Second)
	runDue(t, m)
	*now = started.Add(281 * time.Second)
	finishReal(m.Begin("session-A"), source("conv-A", "new-A", &readsA))
	require.Equal(t, 1, m.entries["session-B"].status.Attempts)
	require.Equal(t, started.Add(560*time.Second).UnixMilli(), m.entries["session-B"].status.NextAt)
	*now = started.Add(341 * time.Second)
	runDue(t, m)
	*now = started.Add(560 * time.Second)
	runDue(t, m)
	*now = started.Add(561 * time.Second)
	runDue(t, m)
	require.Len(t, sent["conv-A"], 1)
	require.Len(t, sent["conv-B"], 2)
	require.Contains(t, sent["conv-A"][0], "new-A")
	require.NotContains(t, sent["conv-A"][0], "request-B")
	require.Equal(t, sent["conv-B"][0], sent["conv-B"][1])
	require.Contains(t, sent["conv-B"][0], "request-B")
	require.Equal(t, 1, m.entries["session-A"].status.Attempts)
	require.Equal(t, 2, m.entries["session-B"].status.Attempts)
	require.Equal(t, "ready", m.entries["session-A"].status.State)
	require.Equal(t, "stopped", m.entries["session-B"].status.State)
	// 单会话停止不会误删同渠道的另一个会话状态。
	require.Equal(t, 1, m.StopChannel("channel-test", "session-A"))
	require.Len(t, m.Status("channel-test"), 2)
	require.Zero(t, m.bytes)
	require.Empty(t, m.queue)
}
