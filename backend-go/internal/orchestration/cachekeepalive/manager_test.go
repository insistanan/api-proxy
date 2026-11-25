package cachekeepalive

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/tidwall/gjson"
	"insistanan/api-proxy/internal/infra/config"
	"insistanan/api-proxy/internal/protocol/types"
)

const sampleBody = `{"model":"claude-test","max_tokens":1024,"tools":[{"name":"read","input_schema":{"type":"object"}}],"system":[{"type":"text","text":"固定前缀","cache_control":{"type":"ephemeral"}}],"messages":[{"role":"user","content":[{"type":"text","text":"真实请求","cache_control":{"type":"ephemeral"}}]}]}`

func samplePolicy() config.CacheKeepaliveConfig {
	return config.CacheKeepaliveConfig{Enabled: true, IntervalSeconds: 280, MaxAttempts: 2,
		MaxIdleMinutes: 120, Prompt: "接下来应该怎么做？", MinCacheHitPercent: 90, MaxOutputTokens: 16}
}

func sampleSource(body, attempt string, reads *int) Source {
	return Source{Kind: "messages", Open: func() (io.ReadCloser, error) {
		(*reads)++
		return io.NopCloser(strings.NewReader(body)), nil
	}, Size: int64(len(body)), Header: http.Header{}, URL: "https://example.test/v1/messages", APIKey: "test-key",
		ConversationID: "conv-test", AttemptID: attempt,
		Upstream: &config.UpstreamConfig{ID: "channel-test", ServiceType: "claude", CacheKeepalive: samplePolicy()}}
}

func fixture(t *testing.T) (*Manager, *time.Time, *uint64) {
	t.Helper()
	now := time.Date(2026, 11, 25, 0, 0, 0, 0, time.UTC)
	revision := uint64(1)
	m := newManager(func() (bool, uint64) { return true, revision }, func() time.Time { return now })
	m.jobs = make(chan work, 1)
	m.send = func(context.Context, *snapshot) sendResult {
		return sendResult{sent: true, sentAt: now,
			usage: &types.Usage{InputTokens: 10, CacheReadInputTokens: 1000, OutputTokens: 1}}
	}
	t.Cleanup(m.Stop)
	return m, &now, &revision
}

func finishReal(turn *Turn, source Source) {
	turn.Capture(source)
	turn.Result(source.AttemptID, true, &types.Usage{InputTokens: 10, CacheCreationInputTokens: 1000, OutputTokens: 5})
	turn.End()
}

func runDue(t *testing.T, m *Manager) {
	t.Helper()
	m.dispatch()
	select {
	case job := <-m.jobs:
		m.run(job)
	default:
		t.Fatal("预期到期任务未被调度")
	}
}

func TestKeepalive_OnlyPreparesAfterIdleAndReusesFrozenRequest(t *testing.T) {
	m, now, _ := fixture(t)
	reads := 0
	finishReal(m.Begin("session"), sampleSource(sampleBody, "attempt", &reads))
	*now = now.Add(59 * time.Second)
	m.dispatch()
	if reads != 0 || len(m.jobs) != 0 {
		t.Fatal("空闲未满一分钟时读取了上下文")
	}
	*now = now.Add(time.Second)
	runDue(t, m)
	if reads != 1 {
		t.Fatalf("正文应只延迟读取一次，实际 %d", reads)
	}
	var sentBodies []string
	m.send = func(_ context.Context, s *snapshot) sendResult {
		sentBodies = append(sentBodies, string(s.body))
		return sendResult{sent: true, sentAt: *now, usage: &types.Usage{InputTokens: 10, CacheReadInputTokens: 1000, OutputTokens: 1}}
	}
	*now = now.Add(220 * time.Second)
	runDue(t, m)
	*now = now.Add(280 * time.Second)
	runDue(t, m)
	if len(sentBodies) != 2 || sentBodies[0] != sentBodies[1] {
		t.Fatalf("两次心跳没有使用同一冻结请求: %d", len(sentBodies))
	}
	if gjson.Get(sentBodies[0], "messages.#").Int() != 2 || gjson.Get(sentBodies[0], "messages.0").Raw != gjson.Get(sampleBody, "messages.0").Raw {
		t.Fatal("心跳改变了原消息前缀，或累计了额外心跳")
	}
	status := m.Status("channel-test")[0]
	if status.Attempts != 2 || status.State != "stopped" || m.bytes != 0 || len(m.queue) != 0 {
		t.Fatalf("次数达到上限后未释放快照: %+v, bytes=%d", status, m.bytes)
	}
}

func TestKeepalive_LateRealResponseCannotReplaceLatest(t *testing.T) {
	m, _, _ := fixture(t)
	reads := 0
	old := m.Begin("session")
	old.Capture(sampleSource(sampleBody, "old", &reads))
	latest := m.Begin("session")
	if m.bytes != 0 {
		t.Fatal("新请求到来未立即释放旧候选")
	}
	finishReal(latest, sampleSource(sampleBody, "latest", &reads))
	old.Result("old", true, &types.Usage{CacheReadInputTokens: 9999})
	old.End()
	if m.entries["session"].snapshot.source.AttemptID != "latest" || len(m.queue) != 1 {
		t.Fatal("乱序响应覆盖了最新候选或产生重复到期项")
	}
}

func TestKeepalive_ClaudeCodePreserveAllThinkingReachesFirstHeartbeat(t *testing.T) {
	m, now, _ := fixture(t)
	reads := 0
	body := strings.TrimSuffix(sampleBody, "}") + `,"thinking":{"type":"adaptive","display":"updates"},"context_management":{"edits":[{"type":"clear_thinking_20251015","keep":"all"}]}}`
	finishReal(m.Begin("session"), sampleSource(body, "attempt", &reads))
	if len(m.ActiveChannels()) != 0 {
		t.Fatal("尚未保存上下文时不应展示保活图标")
	}
	*now = now.Add(time.Minute)
	runDue(t, m)
	if status := m.Status("channel-test")[0]; status.State != "ready" || status.Attempts != 0 ||
		status.SnapshotAt != now.UnixMilli() || status.NextHeartbeatAt != now.Add(220*time.Second).UnixMilli() {
		t.Fatalf("保留全部思考的 Claude Code 请求未进入等待心跳状态: %+v", status)
	}
	if m.ActiveChannels()["channel-test"] != 1 {
		t.Fatal("等待下一次心跳的会话应展示保活图标")
	}
	snapshotAt := now.UnixMilli()
	*now = now.Add(220 * time.Second)
	runDue(t, m)
	if status := m.Status("channel-test")[0]; status.State != "ready" || status.Attempts != 1 || status.SnapshotAt != snapshotAt {
		t.Fatalf("空闲满配置间隔后没有完成首轮心跳: %+v", status)
	}
	*now = now.Add(280 * time.Second)
	runDue(t, m)
	if status := m.Status("channel-test")[0]; len(m.ActiveChannels()) != 0 || status.NextHeartbeatAt != 0 || status.SnapshotAt != snapshotAt {
		t.Fatalf("次数达到上限后应隐藏图标、清除下一次心跳并保留历史时间: %+v", status)
	}
}

func TestKeepalive_NewRequestCancelsInFlightAndAccountsPinnedMemory(t *testing.T) {
	m, now, _ := fixture(t)
	reads := 0
	finishReal(m.Begin("session"), sampleSource(sampleBody, "initial", &reads))
	*now = now.Add(time.Minute)
	runDue(t, m)
	charge := m.bytes
	m.send = func(ctx context.Context, s *snapshot) sendResult {
		latest := m.Begin("session")
		if ctx.Err() != context.Canceled || m.bytes != charge || len(s.body) == 0 {
			t.Fatal("在途任务未取消，或提前漏算其持有的正文")
		}
		finishReal(latest, sampleSource(sampleBody, "new", &reads))
		if m.bytes != charge*2 {
			t.Fatal("新旧正文同时存活时未全部计入预算")
		}
		return sendResult{sent: true, sentAt: *now, usage: &types.Usage{CacheReadInputTokens: 1000}}
	}
	*now = now.Add(220 * time.Second)
	runDue(t, m)
	status := m.Status("channel-test")[0]
	if status.Attempts != 0 || status.State != "waiting" || m.bytes != charge {
		t.Fatalf("旧心跳回调污染新状态或泄漏内存: %+v bytes=%d", status, m.bytes)
	}
}

func TestKeepalive_StopsOnMissingUsageAndExpiredCache(t *testing.T) {
	for _, tc := range []struct {
		name    string
		change  func(*Source)
		elapsed time.Duration
	}{
		{"长响应导致过期", func(*Source) {}, 4 * time.Minute},
		{"实际 TTL 不允许间隔", func(s *Source) { s.Upstream.CacheKeepalive.IntervalSeconds = 300 }, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, now, _ := fixture(t)
			reads := 0
			source := sampleSource(sampleBody, "attempt", &reads)
			tc.change(&source)
			turn := m.Begin("session")
			turn.Capture(source)
			turn.Result("attempt", true, &types.Usage{CacheCreationInputTokens: 1000})
			*now = now.Add(tc.elapsed)
			turn.End()
			*now = now.Add(time.Minute)
			runDue(t, m)
			if m.Status("channel-test")[0].State != "stopped" || m.bytes != 0 {
				t.Fatal("不满足保活条件仍保留了快照")
			}
		})
	}
	m, _, _ := fixture(t)
	reads := 0
	turn := m.Begin("session")
	turn.Capture(sampleSource(sampleBody, "missing", &reads))
	turn.Result("missing", true, nil)
	turn.End()
	if m.bytes != 0 || len(m.queue) != 0 {
		t.Fatal("缺少用量时不应安排付费探测")
	}
}

func TestKeepalive_CountOnlyIgnoresCumulativeTokensAndUnconfiguredDuration(t *testing.T) {
	m, now, _ := fixture(t)
	started := *now
	reads := 0
	body := strings.ReplaceAll(sampleBody, `"type":"ephemeral"`, `"type":"ephemeral","ttl":"1h"`)
	source := sampleSource(body, "attempt", &reads)
	source.Upstream.CacheKeepalive.MaxAttempts = 6
	source.Upstream.CacheKeepalive.MaxIdleMinutes = 0
	turn := m.Begin("session")
	turn.Capture(source)
	turn.Result("attempt", true, &types.Usage{InputTokens: 10, CacheReadInputTokens: 1000000})
	turn.End()
	*now = now.Add(time.Minute)
	runDue(t, m)
	m.send = func(context.Context, *snapshot) sendResult {
		return sendResult{sent: true, sentAt: *now, usage: &types.Usage{InputTokens: 10, CacheReadInputTokens: 1000000, OutputTokens: 1}}
	}
	// 调度晚于旧实现暗加的 (6+1)*280 秒期限，但一小时缓存仍有效。
	*now = started.Add(2000 * time.Second)
	m.clean()
	for attempt := 1; attempt <= 6; attempt++ {
		runDue(t, m)
		status := m.Status("channel-test")[0]
		if status.Attempts != attempt || attempt < 6 && status.State != "ready" {
			t.Fatalf("次数未达到上限却提前停止: %+v", status)
		}
		*now = now.Add(280 * time.Second)
	}
	status := m.Status("channel-test")[0]
	if status.State != "stopped" || status.Reason != "已达到保活次数上限" || status.CacheRead != 6000000 || m.bytes != 0 {
		t.Fatalf("超过旧 Token 限制后没有完整执行六次并释放快照: %+v", status)
	}
}

func TestKeepalive_ExplicitCacheAlsoWaitsConfiguredIdleInterval(t *testing.T) {
	m, now, _ := fixture(t)
	started := *now
	reads, sends := 0, 0
	source := sampleSource(`{"cachedContent":"cachedContents/existing","contents":[]}`, "attempt", &reads)
	source.Upstream.ServiceType = "gemini"
	source.URL = "https://example.test/v1beta/models/gemini-test:generateContent"
	source.Upstream.CacheKeepalive.MaxIdleMinutes = 0
	source.Upstream.CacheKeepalive.MaxAttempts = 1
	finishReal(m.Begin("session"), source)
	m.send = func(_ context.Context, s *snapshot) sendResult {
		sends++
		if s.mode != "explicit" {
			t.Fatal("显式缓存不应发送生成心跳")
		}
		return sendResult{sent: true, sentAt: *now, expiresAt: now.Add(10 * time.Minute)}
	}
	*now = started.Add(time.Minute)
	runDue(t, m)
	m.dispatch()
	if len(m.jobs) != 0 || sends != 0 || m.Status("channel-test")[0].NextAt != started.Add(280*time.Second).UnixMilli() {
		t.Fatal("显式缓存空闲一分钟后提前续期")
	}
	*now = started.Add(280 * time.Second)
	runDue(t, m)
	if status := m.Status("channel-test")[0]; sends != 1 || status.State != "stopped" || status.Attempts != 1 {
		t.Fatalf("显式缓存没有按设定间隔及次数执行: %+v", status)
	}
}

func TestKeepalive_ConfigChangesDeletionAndCleanupReleaseContext(t *testing.T) {
	m, now, revision := fixture(t)
	reads := 0
	finishReal(m.Begin("session"), sampleSource(sampleBody, "attempt", &reads))
	(*revision)++
	m.invalidate()
	if m.bytes != 0 || len(m.queue) != 0 {
		t.Fatal("配置变化未释放旧快照")
	}
	*now = now.Add(statusRetention)
	m.clean()
	if len(m.entries) != 0 {
		t.Fatal("定期清理未移除过期状态记录")
	}
	finishReal(m.Begin("session"), sampleSource(sampleBody, "next", &reads))
	m.Forget("conv-test")
	if m.bytes != 0 || len(m.entries) != 0 || len(m.queue) != 0 {
		t.Fatal("删除会话后仍持有上下文")
	}
}

func TestKeepalive_DropsWhenCacheCoverageDecreases(t *testing.T) {
	m, now, _ := fixture(t)
	reads := 0
	finishReal(m.Begin("session"), sampleSource(sampleBody, "attempt", &reads))
	*now = now.Add(time.Minute)
	runDue(t, m)
	m.send = func(context.Context, *snapshot) sendResult {
		// 比例仍为 100%，但绝对命中量已远小于原缓存前缀。
		return sendResult{sent: true, sentAt: *now, usage: &types.Usage{CacheReadInputTokens: 10}}
	}
	*now = now.Add(220 * time.Second)
	runDue(t, m)
	if m.Status("channel-test")[0].State != "stopped" || m.bytes != 0 {
		t.Fatal("只比较命中率会误保活已经丢失的大段上下文")
	}
}

func TestKeepalive_UsesRelativeBaselineForPartiallyCachedRequests(t *testing.T) {
	m, now, _ := fixture(t)
	reads := 0
	source := sampleSource(sampleBody, "attempt", &reads)
	turn := m.Begin("session")
	turn.Capture(source)
	turn.Result("attempt", true, &types.Usage{InputTokens: 400, CacheReadInputTokens: 600, OutputTokens: 1})
	turn.End()
	*now = now.Add(time.Minute)
	runDue(t, m)
	m.send = func(context.Context, *snapshot) sendResult {
		return sendResult{sent: true, sentAt: *now, usage: &types.Usage{InputTokens: 410, CacheReadInputTokens: 600, OutputTokens: 1}}
	}
	*now = now.Add(220 * time.Second)
	runDue(t, m)
	status := m.Status("channel-test")[0]
	if status.State != "ready" || status.Attempts != 1 || status.BaselineCacheHit != 60 {
		t.Fatalf("基本不变的部分命中不应被绝对 90%% 阈值误停: %+v", status)
	}
}

func TestKeepalive_FirstHeartbeatWaitsFullIdleInterval(t *testing.T) {
	m, now, _ := fixture(t)
	reads := 0
	// 使用一小时窗口，较慢的真实响应不应让首轮心跳提前。
	sourceBody := strings.ReplaceAll(sampleBody, `"type":"ephemeral"`, `"type":"ephemeral","ttl":"1h"`)
	source := sampleSource(sourceBody, "attempt", &reads)
	turn := m.Begin("session")
	turn.Capture(source)
	*now = now.Add(2 * time.Minute)
	turn.Result("attempt", true, &types.Usage{CacheReadInputTokens: 1000})
	turn.End()
	ended := *now
	*now = now.Add(time.Minute)
	runDue(t, m)
	want := ended.Add(280 * time.Second).UnixMilli()
	if status := m.Status("channel-test")[0]; status.NextAt != want {
		t.Fatalf("首次心跳没有等待完整空闲间隔: next=%d want=%d", status.NextAt, want)
	}
}

func TestKeepalive_ManualStopIsScopedAndLateResultsCannotRestart(t *testing.T) {
	m, now, _ := fixture(t)
	reads := 0
	finishReal(m.Begin("first"), sampleSource(sampleBody, "first-attempt", &reads))
	other := sampleSource(sampleBody, "other-attempt", &reads)
	other.Upstream.ID = "other-channel"
	finishReal(m.Begin("other"), other)
	if stopped := m.StopChannel("channel-test", "other"); stopped != 0 {
		t.Fatal("停止接口越过渠道边界")
	}
	*now = now.Add(time.Minute)
	runDue(t, m)
	// 测试队列仅容纳一个任务，先准备剩余渠道。
	*now = now.Add(time.Second)
	runDue(t, m)
	m.send = func(ctx context.Context, s *snapshot) sendResult {
		if s.source.Upstream.ID != "channel-test" {
			t.Fatal("未预期的渠道进入心跳")
		}
		if m.StopChannel("channel-test", "first") != 1 || ctx.Err() == nil {
			t.Fatal("手动停止未取消在途心跳")
		}
		return sendResult{sent: true, sentAt: *now, usage: &types.Usage{CacheReadInputTokens: 1000}}
	}
	// 另一渠道尚未到期，防止最小堆同一时刻的任意顺序影响断言。
	m.mu.Lock()
	m.schedule(m.entries["other"], now.Add(time.Hour))
	m.mu.Unlock()
	*now = now.Add(219 * time.Second)
	runDue(t, m)
	if status := m.Status("channel-test")[0]; status.State != "stopped" || status.Attempts != 0 {
		t.Fatalf("旧发送回调复活了手动停止的任务: %+v", status)
	}
	if m.Status("other-channel")[0].State != "ready" {
		t.Fatal("停止一个会话影响其他渠道")
	}
	turn := m.Begin("first")
	turn.Capture(sampleSource(sampleBody, "inflight", &reads))
	if m.StopChannel("channel-test", "") != 1 {
		t.Fatal("无法停止处理中会话")
	}
	turn.Result("inflight", true, &types.Usage{CacheReadInputTokens: 1000})
	turn.End()
	if m.Status("channel-test")[0].State != "stopped" {
		t.Fatal("旧真实请求的晚返回复活了保活")
	}
	finishReal(m.Begin("first"), sampleSource(sampleBody, "fresh", &reads))
	if m.Status("channel-test")[0].State != "waiting" {
		t.Fatal("新的真实请求未建立新一轮保活")
	}
}

func TestKeepalive_CapacityAndLongActiveRequestStayBounded(t *testing.T) {
	m, now, _ := fixture(t)
	for i := 0; i < MaxSessions; i++ {
		if m.Begin(fmt.Sprintf("session-%d", i)) == nil {
			t.Fatal("达到容量前不应拒绝会话")
		}
	}
	if m.Begin("overflow") != nil || m.CapacityRejections() != 1 || len(m.entries) != MaxSessions {
		t.Fatal("容量满时没有明确记账或突破会话上限")
	}
	m.Forget("")
	reads := 0
	turn := m.Begin("long-stream")
	turn.Capture(sampleSource(sampleBody, "attempt", &reads))
	*now = now.Add(time.Hour)
	m.clean()
	if m.bytes != 0 || reads != 0 || m.Status("channel-test")[0].State != "stopped" {
		t.Fatal("未结束的长请求无限保留了过期候选，或清理时读取了正文")
	}
	turn.End()
}

func TestKeepalive_RejectsOversizedSnapshotsWithoutReading(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*Manager, *Source)
	}{
		{"正文超限", func(_ *Manager, s *Source) { s.Size = MaxBodyBytes + 1 }},
		{"请求头超限", func(_ *Manager, s *Source) { s.Header.Set("X-Large", strings.Repeat("x", 33<<10)) }},
		{"总预算不足", func(m *Manager, _ *Source) { m.bytes = MaxMemoryBytes }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, _, _ := fixture(t)
			reads := 0
			source := sampleSource(sampleBody, "attempt", &reads)
			tc.change(m, &source)
			before := m.bytes
			finishReal(m.Begin("session"), source)
			status := m.Status("channel-test")[0]
			if m.bytes != before || reads != 0 || status.State != "stopped" || status.Reason == "" {
				t.Fatalf("超限时未拒绝候选或额外读取上下文: %+v", status)
			}
		})
	}
}

func TestKeepalive_DeadlineAndPromptRejectionReleaseSnapshot(t *testing.T) {
	for _, rejectPrompt := range []bool{false, true} {
		m, now, _ := fixture(t)
		reads := 0
		source := sampleSource(sampleBody, "attempt", &reads)
		source.Upstream.CacheKeepalive.MaxIdleMinutes = 3
		if rejectPrompt {
			m.checkPrompt = func(context.Context, string, string) error { return fmt.Errorf("心跳内容未通过检查") }
		}
		finishReal(m.Begin("session"), source)
		*now = now.Add(time.Minute)
		runDue(t, m)
		if !rejectPrompt {
			*now = now.Add(2 * time.Minute)
			m.dispatch()
		}
		status := m.Status("channel-test")[0]
		if status.State != "stopped" || status.Attempts != 0 || m.bytes != 0 || len(m.jobs) != 0 {
			t.Fatalf("期限或内容检查未阻止后台发送: %+v", status)
		}
		if reads != 1 {
			t.Fatal("空闲阶段应只读取一次历史")
		}
	}
}
