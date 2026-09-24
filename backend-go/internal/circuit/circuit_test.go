package circuit

import (
	"testing"
	"time"
)

func TestBreakerClosedAllowsRequests(t *testing.T) {
	b := NewBreaker(DefaultConfig())
	for i := 0; i < 10; i++ {
		if got := b.Allow(); !got.Allowed {
			t.Fatalf("Closed 状态第 %d 次请求被拒绝: %+v", i, got)
		}
		b.RecordSuccess()
	}
	snap := b.Snapshot()
	if snap.State != StateClosed {
		t.Fatalf("期望 Closed，实际 %s", snap.State)
	}
}

func TestBreakerOpensOnConsecutiveFailures(t *testing.T) {
	b := NewBreaker(Config{FailureThreshold: 4})
	for i := 0; i < 3; i++ {
		if got := b.Allow(); !got.Allowed {
			t.Fatalf("未达阈值前被拒绝: %+v", got)
		}
		b.RecordFailure()
	}
	if got := b.Allow(); !got.Allowed {
		t.Fatalf("连续失败 3 次（阈值 4）不应熔断: %+v", got)
	}
	b.RecordFailure()

	// 连续失败 4 次 → Open，拒绝后续请求。
	if got := b.Allow(); got.Allowed {
		t.Fatalf("连续失败 4 次后应熔断拒绝: %+v", got)
	}
	snap := b.Snapshot()
	if snap.State != StateOpen {
		t.Fatalf("期望 Open，实际 %s", snap.State)
	}
	if snap.CooldownRemainingSec <= 0 {
		t.Fatalf("Open 状态应报告冷却剩余时间: %+v", snap)
	}
}

func TestBreakerSuccessResetsConsecutiveFailures(t *testing.T) {
	b := NewBreaker(Config{FailureThreshold: 4})
	for i := 0; i < 3; i++ {
		b.Allow()
		b.RecordFailure()
	}
	b.Allow()
	b.RecordSuccess()
	for i := 0; i < 3; i++ {
		b.Allow()
		b.RecordFailure()
	}
	if got := b.Allow(); !got.Allowed {
		t.Fatalf("成功清零连续失败后不应熔断: %+v", got)
	}
}

// TestBreakerErrorRateAloneDoesNotTrip 锁定新契约：错误率只作为展示指标，
// 不是熔断触发条件——唯一触发路径是连续失败达到阈值。
func TestBreakerErrorRateAloneDoesNotTrip(t *testing.T) {
	b := NewBreaker(DefaultConfig())
	// "4 连败 + 1 成功" 三轮：15 次请求 12 次失败（错误率 80%，远超旧阈值 0.6），
	// 但连续失败永远到不了 5。
	for cycle := 0; cycle < 3; cycle++ {
		for i := 0; i < 4; i++ {
			b.Allow()
			b.RecordFailure()
		}
		b.Allow()
		b.RecordSuccess()
	}
	snap := b.Snapshot()
	if snap.State != StateClosed {
		t.Fatalf("错误率不应触发熔断，实际 %s", snap.State)
	}
	if snap.TotalRequests != 15 || snap.FailedRequests != 12 {
		t.Fatalf("错误率样本应照常累计（供前端展示），实际 total=%d failed=%d", snap.TotalRequests, snap.FailedRequests)
	}

	// 连续失败补齐到阈值才熔断。
	b.Allow()
	b.RecordFailure()
	for i := 0; i < 4; i++ {
		b.Allow()
		b.RecordFailure()
	}
	if got := b.Allow(); got.Allowed {
		t.Fatalf("连续失败 %d 次后应熔断拒绝: %+v", DefaultConfig().FailureThreshold, got)
	}
	if b.Snapshot().State != StateOpen {
		t.Fatalf("期望 Open，实际 %s", b.Snapshot().State)
	}
}

// TestBreakerDefaultConfigMatchesOrchestrationContract 锁定渠道编排契约的
// 熔断口径：连续失败 5 次熔断、冷却 15 分钟、半开连续成功 2 次恢复。
func TestBreakerDefaultConfigMatchesOrchestrationContract(t *testing.T) {
	cfg := DefaultConfig().withDefaults()
	if cfg.FailureThreshold != 5 {
		t.Fatalf("连续失败阈值 = %d, want 5", cfg.FailureThreshold)
	}
	if cfg.Cooldown != 15*time.Minute {
		t.Fatalf("冷却时长 = %s, want 15m", cfg.Cooldown)
	}
	if cfg.SuccessThreshold != 2 {
		t.Fatalf("半开成功阈值 = %d, want 2", cfg.SuccessThreshold)
	}
}

func TestBreakerOpenCooldownExpiryTransitionsToHalfOpen(t *testing.T) {
	b := NewBreaker(Config{FailureThreshold: 1, Cooldown: 10 * time.Millisecond})
	b.Allow()
	b.RecordFailure()
	if b.Snapshot().State != StateOpen {
		t.Fatalf("期望 Open，实际 %s", b.Snapshot().State)
	}

	time.Sleep(20 * time.Millisecond)
	got := b.Allow()
	if !got.Allowed || !got.Probe {
		t.Fatalf("冷却到期应放行探测请求: %+v", got)
	}
	snap := b.Snapshot()
	if snap.State != StateHalfOpen {
		t.Fatalf("期望 HalfOpen，实际 %s", snap.State)
	}
}

func TestBreakerHalfOpenAllowsOnlyOneProbe(t *testing.T) {
	b := NewBreaker(Config{FailureThreshold: 1, Cooldown: 10 * time.Millisecond})
	b.Allow()
	b.RecordFailure()
	time.Sleep(20 * time.Millisecond)

	first := b.Allow()
	if !first.Allowed || !first.Probe {
		t.Fatalf("首个探测应放行: %+v", first)
	}
	second := b.Allow()
	if second.Allowed {
		t.Fatalf("探测在途时第二个请求应被拒绝: %+v", second)
	}
	if second.State != StateHalfOpen {
		t.Fatalf("第二个请求判定时应处于 HalfOpen: %+v", second)
	}
}

func TestBreakerHalfOpenProbeSuccessCloses(t *testing.T) {
	b := NewBreaker(Config{FailureThreshold: 1, Cooldown: 10 * time.Millisecond, SuccessThreshold: 2})
	b.Allow()
	b.RecordFailure()
	time.Sleep(20 * time.Millisecond)

	b.Allow()
	b.RecordSuccess()
	if snap := b.Snapshot(); snap.State != StateHalfOpen {
		t.Fatalf("首次探测成功仍在 HalfOpen: %+v", snap)
	}
	b.Allow()
	b.RecordSuccess()
	if snap := b.Snapshot(); snap.State != StateClosed {
		t.Fatalf("连续成功达到阈值应转 Closed: %+v", snap)
	}
	// 恢复后计数清零。
	snap := b.Snapshot()
	if snap.ConsecutiveFailures != 0 || snap.TotalRequests != 0 || snap.FailedRequests != 0 {
		t.Fatalf("恢复后应清零计数: %+v", snap)
	}
}

func TestBreakerHalfOpenProbeFailureReopens(t *testing.T) {
	b := NewBreaker(Config{FailureThreshold: 1, Cooldown: 50 * time.Millisecond})
	b.Allow()
	b.RecordFailure()
	time.Sleep(20 * time.Millisecond)

	b.Allow()
	b.RecordFailure()
	if snap := b.Snapshot(); snap.State != StateOpen {
		t.Fatalf("探测失败应回 Open: %+v", snap)
	}

	// 冷却重新计时：50ms 未到仍拒绝。
	if got := b.Allow(); got.Allowed {
		t.Fatalf("重新熔断后冷却未到应拒绝: %+v", got)
	}
}

func TestBreakerNeutralReleasesProbeWithoutAffectingHealth(t *testing.T) {
	b := NewBreaker(Config{FailureThreshold: 1, Cooldown: 10 * time.Millisecond, SuccessThreshold: 1})
	b.Allow()
	b.RecordFailure()
	time.Sleep(20 * time.Millisecond)

	if got := b.Allow(); !got.Probe {
		t.Fatalf("应放行探测: %+v", got)
	}
	b.RecordNeutral() // 客户端取消类结果
	if snap := b.Snapshot(); snap.HalfOpenInFlight != 0 {
		t.Fatalf("Neutral 应释放探测名额: %+v", snap)
	}
	if snap := b.Snapshot(); snap.State != StateHalfOpen {
		t.Fatalf("Neutral 不应改变状态: %+v", snap)
	}
	// 名额已释放，下一个探测可以进入。
	if got := b.Allow(); !got.Probe {
		t.Fatalf("名额释放后应再次放行探测: %+v", got)
	}
}

func TestBreakerOpenIgnoresStrayResults(t *testing.T) {
	b := NewBreaker(Config{FailureThreshold: 1, Cooldown: time.Hour})
	b.Allow()
	b.RecordFailure()
	// 冷却期内到达的迟到成功/失败不改变状态。
	b.RecordSuccess()
	b.RecordFailure()
	if snap := b.Snapshot(); snap.State != StateOpen {
		t.Fatalf("冷却期内迟到结果不应改变状态: %+v", snap)
	}
}

func TestBreakerReset(t *testing.T) {
	b := NewBreaker(Config{FailureThreshold: 1})
	b.Allow()
	b.RecordFailure()
	if b.Snapshot().State != StateOpen {
		t.Fatalf("期望 Open")
	}
	b.Reset()
	snap := b.Snapshot()
	if snap.State != StateClosed || snap.ConsecutiveFailures != 0 {
		t.Fatalf("重置后应回到 Closed 并清零: %+v", snap)
	}
	if got := b.Allow(); !got.Allowed {
		t.Fatalf("重置后应放行: %+v", got)
	}
}

func TestManagerDisabledBypassesAll(t *testing.T) {
	m := NewManager(DefaultConfig(), false)
	if got := m.Allow("messages", 0); !got.Allowed {
		t.Fatalf("禁用时应放行: %+v", got)
	}
	m.RecordFailure("messages", 0)
	m.RecordFailure("messages", 0)
	if got := m.Allow("messages", 0); !got.Allowed {
		t.Fatalf("禁用时不应熔断: %+v", got)
	}
	if len(m.Snapshot()) != 0 {
		t.Fatalf("禁用时不应创建熔断器: %+v", m.Snapshot())
	}
}

func TestManagerPerKindChannelIsolation(t *testing.T) {
	m := NewManager(Config{FailureThreshold: 1}, true)
	m.Allow("messages", 0)
	m.RecordFailure("messages", 0)

	if got := m.Allow("messages", 0); got.Allowed {
		t.Fatalf("messages:0 应已熔断: %+v", got)
	}
	if got := m.Allow("messages", 1); !got.Allowed {
		t.Fatalf("messages:1 不应受影响: %+v", got)
	}
	if got := m.Allow("chat", 0); !got.Allowed {
		t.Fatalf("chat:0 不应受影响: %+v", got)
	}
}

func TestManagerResetAndSnapshot(t *testing.T) {
	m := NewManager(Config{FailureThreshold: 1}, true)
	m.SetChannelName("messages", 0, "商汤")
	m.Allow("messages", 0)
	m.RecordFailure("messages", 0)

	if !m.Reset("messages", 0) {
		t.Fatalf("Reset 已存在的渠道应返回 true")
	}
	if m.Reset("messages", 99) {
		t.Fatalf("Reset 不存在的渠道应返回 false")
	}
	entries := m.Snapshot()
	if len(entries) != 1 {
		t.Fatalf("期望 1 条快照: %+v", entries)
	}
	if entries[0].ChannelName != "商汤" || entries[0].Kind != "messages" || entries[0].ChannelIndex != 0 {
		t.Fatalf("快照字段不正确: %+v", entries[0])
	}
	if entries[0].Snapshot.State != StateClosed {
		t.Fatalf("重置后快照应为 Closed: %+v", entries[0])
	}
}

func TestManagerSnapshotParsesMultiDigitIndex(t *testing.T) {
	m := NewManager(Config{FailureThreshold: 1}, true)
	m.Allow("responses", 12)
	m.RecordFailure("responses", 12)

	entries := m.Snapshot()
	if len(entries) != 1 {
		t.Fatalf("期望 1 条快照: %+v", entries)
	}
	if entries[0].Kind != "responses" || entries[0].ChannelIndex != 12 {
		t.Fatalf("快照解析错误: %+v", entries[0])
	}
}
