package circuit

import (
	"testing"
	"time"
)

// fastBreaker 返回冷却 10ms 的熔断器，便于触发 Open→HalfOpen 迁移。
func fastBreaker() *Breaker {
	return NewBreaker(Config{
		FailureThreshold:   1,
		MinRequests:        1,
		ErrorRateThreshold: 0.5,
		Cooldown:           10 * time.Millisecond,
		SuccessThreshold:   2,
	})
}

// forceHalfOpen 触发一次失败转 Open，等待冷却后返回（下次 Allow 惰性转 HalfOpen）。
func forceHalfOpen(b *Breaker) {
	b.RecordFailure()
	time.Sleep(15 * time.Millisecond)
}

// 探测结束的三种结果都释放名额；成功与中性保持半开，失败回到 Open。
func TestProbePermitReleasedByOutcome(t *testing.T) {
	for _, tc := range []struct {
		name   string
		record func(b *Breaker, generation uint64)
		want   State
	}{
		{"成功", func(b *Breaker, g uint64) { b.RecordSuccessGeneration(g) }, StateHalfOpen},
		{"中性", func(b *Breaker, g uint64) { b.RecordNeutralGeneration(g) }, StateHalfOpen},
		{"失败", func(b *Breaker, g uint64) { b.RecordFailureGeneration(g) }, StateOpen},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := fastBreaker()
			forceHalfOpen(b)
			allow := b.Allow()
			if !allow.Allowed || !allow.Probe {
				t.Fatalf("半开状态应放行探测，实际: %+v", allow)
			}
			tc.record(b, allow.Generation)
			snap := b.Snapshot()
			if snap.HalfOpenInFlight != 0 || snap.State != tc.want {
				t.Fatalf("探测名额应释放且状态为 %s，实际: %+v", tc.want, snap)
			}
		})
	}
}

// 核心：手动重置后，旧请求的迟到结果不得释放新探测名额。
func TestStaleGenerationResultIgnoredAfterReset(t *testing.T) {
	b := fastBreaker()
	forceHalfOpen(b)

	first := b.Allow()
	if !first.Probe {
		t.Fatal("第一次 Allow 应取得探测名额")
	}

	b.Reset()

	forceHalfOpen(b)
	second := b.Allow()
	if !second.Probe {
		t.Fatal("重置后重新进入半开，应再次取得探测名额")
	}
	if second.Generation == first.Generation {
		t.Fatalf("重置后新许可代次应不同: first=%d second=%d", first.Generation, second.Generation)
	}

	b.RecordSuccessGeneration(first.Generation)
	snap := b.Snapshot()
	if snap.State != StateHalfOpen || snap.HalfOpenInFlight != 1 {
		t.Fatalf("迟到结果不得释放新名额或改变状态，实际: %+v", snap)
	}

	b.RecordFailureGeneration(first.Generation)
	if snap = b.Snapshot(); snap.State != StateHalfOpen {
		t.Fatalf("旧代次失败不得影响新探测轮次，实际: %+v", snap)
	}

	b.RecordSuccessGeneration(second.Generation)
	if snap = b.Snapshot(); snap.HalfOpenInFlight != 0 {
		t.Fatalf("当前代次记账应释放名额，实际: %+v", snap)
	}
}

// 重新熔断（tripOpen）后同样隔离旧代次。
func TestStaleGenerationResultIgnoredAfterReopen(t *testing.T) {
	b := fastBreaker()
	forceHalfOpen(b)

	first := b.Allow()
	b.RecordFailureGeneration(first.Generation)

	time.Sleep(15 * time.Millisecond)
	second := b.Allow()
	if !second.Probe {
		t.Fatal("新一轮半开探测应放行")
	}

	b.RecordNeutralGeneration(first.Generation)
	if snap := b.Snapshot(); snap.HalfOpenInFlight != 1 {
		t.Fatalf("旧代次中性不得释放新名额，实际: %+v", snap)
	}
}

// Peek 不占名额；Allow 占用探测名额后，第二个 Allow 被拒绝。
func TestProbeSlotIsOccupiedOnlyByAllow(t *testing.T) {
	b := fastBreaker()
	forceHalfOpen(b)

	if peek := b.Peek(); !peek.Allowed {
		t.Fatalf("Peek 应放行: %+v", peek)
	}
	first := b.Allow()
	if !first.Allowed || !first.Probe {
		t.Fatalf("Peek 不应占用名额，首个 Allow 应取得探测: %+v", first)
	}
	if second := b.Allow(); second.Allowed {
		t.Fatalf("探测名额占用期间第二个 Allow 应被拒绝: %+v", second)
	}
}
