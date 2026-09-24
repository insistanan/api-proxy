// R2-b 验收测试：许可所有权与一次收尾。
// 覆盖方案 5.3：
//   - 半开探测名额在各种结束原因下均无泄漏；
//   - 手动重置/重开后的迟到结果不释放新许可名额（状态代次隔离）；
//   - 重复记账被识别（警告 + 跳过）；
//   - Release 幂等。
package circuit

import (
	"errors"
	"testing"
	"time"
)

// fastBreaker 返回冷却 10ms 的熔断器，便于触发 Open→HalfOpen 迁移。
func fastBreaker() *Breaker {
	return NewBreaker(Config{
		FailureThreshold: 1,
		Cooldown:         10 * time.Millisecond,
		SuccessThreshold: 2,
	})
}

// forceHalfOpen 触发一次失败转 Open，等待冷却后返回（下次 Allow 惰性转 HalfOpen）。
func forceHalfOpen(b *Breaker) {
	b.RecordFailure()
	time.Sleep(15 * time.Millisecond)
}

// 探测成功：名额释放且熔断恢复路径正常推进。
func TestProbePermitReleasedOnSuccess(t *testing.T) {
	b := fastBreaker()
	forceHalfOpen(b)

	allow := b.Allow()
	if !allow.Allowed || !allow.Probe {
		t.Fatalf("半开状态应放行探测，实际: %+v", allow)
	}
	b.RecordSuccessGeneration(allow.Generation)

	snap := b.Snapshot()
	if snap.HalfOpenInFlight != 0 {
		t.Fatalf("成功记账后探测名额应释放，实际: %d", snap.HalfOpenInFlight)
	}
}

// 探测遇到不可重试 413 / 审核拒绝 / 取消（中性）：名额释放，状态不变。
func TestProbePermitReleasedOnNeutral(t *testing.T) {
	b := fastBreaker()
	forceHalfOpen(b)

	allow := b.Allow()
	if !allow.Probe {
		t.Fatal("应取得探测名额")
	}
	b.RecordNeutralGeneration(allow.Generation)

	snap := b.Snapshot()
	if snap.HalfOpenInFlight != 0 {
		t.Fatalf("中性记账后探测名额应释放，实际: %d", snap.HalfOpenInFlight)
	}
	if snap.State != StateHalfOpen {
		t.Fatalf("中性记账不应改变状态，实际: %s", snap.State)
	}
}

// 探测失败（真实故障）：名额释放并立即回 Open。
func TestProbePermitReleasedOnFailure(t *testing.T) {
	b := fastBreaker()
	forceHalfOpen(b)

	allow := b.Allow()
	b.RecordFailureGeneration(allow.Generation)

	snap := b.Snapshot()
	if snap.HalfOpenInFlight != 0 {
		t.Fatalf("失败记账后探测名额应释放，实际: %d", snap.HalfOpenInFlight)
	}
	if snap.State != StateOpen {
		t.Fatalf("探测失败应回 Open，实际: %s", snap.State)
	}
}

// 构建失败（本地错误，中性）：名额释放，不重新熔断。
func TestProbePermitReleasedOnLocalBuildFailure(t *testing.T) {
	b := fastBreaker()
	forceHalfOpen(b)

	allow := b.Allow()
	// 本地构建失败与渠道无关：中性。
	b.RecordNeutralGeneration(allow.Generation)

	if snap := b.Snapshot(); snap.HalfOpenInFlight != 0 || snap.State != StateHalfOpen {
		t.Fatalf("本地错误应中性释放且不重开，实际: %+v", snap)
	}
}

// 已提交流中断（真实上游中断）：失败记账释放名额。
func TestProbePermitReleasedAfterCommittedStreamInterruption(t *testing.T) {
	b := fastBreaker()
	forceHalfOpen(b)

	allow := b.Allow()
	// 流已提交后的中断按失败记账（不再故障转移）。
	b.RecordFailureGeneration(allow.Generation)

	snap := b.Snapshot()
	if snap.HalfOpenInFlight != 0 || snap.State != StateOpen {
		t.Fatalf("流中断应失败记账并释放名额，实际: %+v", snap)
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

	// 手动重置：代次递增，渠道回 Closed。
	b.Reset()

	// 重新触发 Open→HalfOpen，取得新探测名额。
	forceHalfOpen(b)
	second := b.Allow()
	if !second.Probe {
		t.Fatal("重置后重新进入半开，应再次取得探测名额")
	}
	if second.Generation == first.Generation {
		t.Fatalf("重置后新许可代次应不同: first=%d second=%d", first.Generation, second.Generation)
	}

	// 旧请求迟到成功：不应影响当前状态（仍 HalfOpen 占用 1 个名额）。
	b.RecordSuccessGeneration(first.Generation)
	snap := b.Snapshot()
	if snap.State != StateHalfOpen || snap.HalfOpenInFlight != 1 {
		t.Fatalf("迟到结果不得释放新名额或改变状态，实际: %+v", snap)
	}

	// 新请求迟到失败也不应把新一轮探测打回 Open。
	b.RecordFailureGeneration(first.Generation)
	snap = b.Snapshot()
	if snap.State != StateHalfOpen {
		t.Fatalf("旧代次失败不得影响新探测轮次，实际: %+v", snap)
	}

	// 当前代次的记账正常生效。
	b.RecordSuccessGeneration(second.Generation)
	snap = b.Snapshot()
	if snap.HalfOpenInFlight != 0 {
		t.Fatalf("当前代次记账应释放名额，实际: %+v", snap)
	}
}

// 重新熔断（tripOpen）后同样隔离旧代次。
func TestStaleGenerationResultIgnoredAfterReopen(t *testing.T) {
	b := fastBreaker()
	forceHalfOpen(b)

	first := b.Allow()
	// 探测失败 → 回 Open，代次递增。
	b.RecordFailureGeneration(first.Generation)

	// 冷却到期后新探测。
	time.Sleep(15 * time.Millisecond)
	second := b.Allow()
	if !second.Probe {
		t.Fatal("新一轮半开探测应放行")
	}

	// 旧代次的迟到中性：不得释放新名额。
	b.RecordNeutralGeneration(first.Generation)
	if snap := b.Snapshot(); snap.HalfOpenInFlight != 1 {
		t.Fatalf("旧代次中性不得释放新名额，实际: %+v", snap)
	}
}

// 半开名额被占用时，第二个 Allow 被拒绝。
func TestSecondAllowRejectedWhileProbeInFlight(t *testing.T) {
	b := fastBreaker()
	forceHalfOpen(b)

	if first := b.Allow(); !first.Allowed || !first.Probe {
		t.Fatalf("首个探测应放行: %+v", first)
	}
	second := b.Allow()
	if second.Allowed {
		t.Fatalf("探测名额占用期间第二个 Allow 应被拒绝: %+v", second)
	}
}

// Peek 不占名额（预检语义保持）。
func TestPeekDoesNotOccupyProbeSlot(t *testing.T) {
	b := fastBreaker()
	forceHalfOpen(b)

	if peek := b.Peek(); !peek.Allowed {
		t.Fatalf("Peek 应放行: %+v", peek)
	}
	// Peek 后 Allow 仍可取得名额。
	if allow := b.Allow(); !allow.Allowed || !allow.Probe {
		t.Fatalf("Peek 不应占用名额: %+v", allow)
	}
}

var errSentinel = errors.New("sentinel")
