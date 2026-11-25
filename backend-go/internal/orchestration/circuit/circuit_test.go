package circuit

import (
	"testing"
	"time"
)

func TestBreakerOpensOnConsecutiveFailures(t *testing.T) {
	b := NewBreaker(Config{FailureThreshold: 4})
	for i := 0; i < 3; i++ {
		if got := b.Allow(); !got.Allowed {
			t.Fatalf("未达阈值前被拒绝: %+v", got)
		}
		b.RecordFailure()
	}
	// 成功清零连续失败计数，之后的 3 次失败不足阈值。
	b.Allow()
	b.RecordSuccess()
	for i := 0; i < 3; i++ {
		b.Allow()
		b.RecordFailure()
	}
	if got := b.Allow(); !got.Allowed {
		t.Fatalf("成功清零连续失败后不应熔断: %+v", got)
	}
	b.RecordFailure()

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

func TestBreakerOpensOnErrorRate(t *testing.T) {
	// 错误率阈值 0.6 且需至少 10 次请求：9 次请求 5 失败不触发，第 10 次请求失败后 6/10 触发。
	b := NewBreaker(DefaultConfig())
	for i := 0; i < 4; i++ {
		b.Allow()
		b.RecordSuccess()
		b.Allow()
		b.RecordFailure()
	}
	if got := b.Allow(); !got.Allowed {
		t.Fatalf("样本不足 10 不应按错误率熔断: %+v", got)
	}
	b.RecordFailure()
	b.Allow()
	b.RecordFailure()
	if got := b.Allow(); got.Allowed {
		t.Fatalf("错误率 0.6（10 请求）应熔断: %+v", got)
	}
	if b.Snapshot().State != StateOpen {
		t.Fatalf("期望 Open，实际 %s", b.Snapshot().State)
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
	snap := b.Snapshot()
	if snap.State != StateClosed {
		t.Fatalf("连续成功达到阈值应转 Closed: %+v", snap)
	}
	if snap.ConsecutiveFailures != 0 || snap.TotalRequests != 0 || snap.FailedRequests != 0 {
		t.Fatalf("恢复后应清零计数: %+v", snap)
	}
}

func TestBreakerHalfOpenProbeFailureReopensAndIgnoresStrayResults(t *testing.T) {
	b := NewBreaker(Config{FailureThreshold: 1, Cooldown: 50 * time.Millisecond})
	b.Allow()
	b.RecordFailure()
	time.Sleep(20 * time.Millisecond)

	b.Allow()
	b.RecordFailure()
	if snap := b.Snapshot(); snap.State != StateOpen {
		t.Fatalf("探测失败应回 Open: %+v", snap)
	}
	// 冷却重新计时：未到期前拒绝，冷却期内的迟到成功/失败不改变状态。
	if got := b.Allow(); got.Allowed {
		t.Fatalf("重新熔断后冷却未到应拒绝: %+v", got)
	}
	b.RecordSuccess()
	b.RecordFailure()
	if snap := b.Snapshot(); snap.State != StateOpen {
		t.Fatalf("冷却期内迟到结果不应改变状态: %+v", snap)
	}
}

func TestManagerDisabledBypassesAll(t *testing.T) {
	m := NewManager(DefaultConfig(), false)
	m.RecordFailure("messages", 0)
	m.RecordFailure("messages", 0)
	if got := m.Allow("messages", 0); !got.Allowed {
		t.Fatalf("禁用时不应熔断: %+v", got)
	}
	if len(m.Snapshot()) != 0 {
		t.Fatalf("禁用时不应创建熔断器: %+v", m.Snapshot())
	}
}

func TestManagerIsolatesChannelsAndReset(t *testing.T) {
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
	if entries[0].ChannelName != "商汤" || entries[0].Kind != "messages" || entries[0].ChannelIndex != 0 || entries[0].Snapshot.State != StateClosed {
		t.Fatalf("重置后快照不正确: %+v", entries[0])
	}

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
