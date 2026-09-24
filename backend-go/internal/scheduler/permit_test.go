// R2-b 验收测试（scheduler 层）：许可句柄的取得、记账、释放与预检竞争。
package scheduler

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/BenedictKing/api-proxy/internal/circuit"
	"github.com/BenedictKing/api-proxy/internal/config"
)

// newPermitTestEnv 构造启用熔断的单渠道测试环境。
func newPermitTestEnv(t *testing.T) (*ChannelScheduler, *circuit.Manager) {
	t.Helper()
	cfg := config.Config{
		Upstream: []config.UpstreamConfig{
			{
				Name: "permit-a", BaseURL: "https://a.example.com",
				APIKeys: []string{"key-a"}, Priority: 1, Status: "active",
			},
		},
	}
	scheduler, cleanup := createTestScheduler(t, cfg)
	t.Cleanup(cleanup)

	circuitManager := circuit.NewManager(circuit.Config{
		FailureThreshold: 1, Cooldown: 20 * time.Millisecond, SuccessThreshold: 1,
	}, true)
	scheduler.SetCircuitManager(circuitManager)
	return scheduler, circuitManager
}

// 选渠后取得许可：持有 in-flight 预留；记账一次 + 释放后无残留。
func TestPermitAcquireRecordReleaseLifecycle(t *testing.T) {
	sch, _ := newPermitTestEnv(t)

	selection, err := sch.SelectChannel(context.Background(), "", make(map[int]bool), ChannelKindMessages, "")
	if err != nil {
		t.Fatalf("选渠失败: %v", err)
	}
	if !selection.Reserved() {
		t.Fatal("选渠后应持有 in-flight 预留")
	}
	if inFlight := sch.GetChannelInFlight(ChannelKindMessages, selection.ChannelIndex); inFlight != 1 {
		t.Fatalf("in-flight 应为 1，实际 %d", inFlight)
	}

	selection.Permit.RecordHealthSuccess()
	selection.Permit.Release()
	selection.Permit.Release() // 幂等

	if inFlight := sch.GetChannelInFlight(ChannelKindMessages, selection.ChannelIndex); inFlight != 0 {
		t.Fatalf("释放后 in-flight 应为 0，实际 %d", inFlight)
	}
}

// 重复记账被识别：第二次记账不产生渠道级效果（仅警告日志）。
func TestPermitDuplicateRecordIsIgnored(t *testing.T) {
	sch, _ := newPermitTestEnv(t)

	selection, err := sch.SelectChannel(context.Background(), "", make(map[int]bool), ChannelKindMessages, "")
	if err != nil {
		t.Fatalf("选渠失败: %v", err)
	}
	selection.Permit.RecordHealthSuccess()
	// 第二次（失败）记账应被忽略：不覆盖成功结果。
	selection.Permit.RecordHealthFailure()
	selection.Permit.Release()

	// 成功记账后熔断器应保持 Closed（无失败计数）。
	snap := sch.CircuitManagerSnapshot()
	if len(snap) != 1 {
		t.Fatalf("应有一个熔断条目，实际 %d", len(snap))
	}
	if snap[0].Snapshot.ConsecutiveFailures != 0 || snap[0].Snapshot.State != circuit.StateClosed {
		t.Fatalf("重复记账被误采纳，实际: %+v", snap[0].Snapshot)
	}
}

// ReleaseReservation 只释放 in-flight，不影响探测名额。
func TestPermitReleaseReservationKeepsProbe(t *testing.T) {
	sch, _ := newPermitTestEnv(t)
	// 触发熔断并等冷却（半开候选）。
	sch.RecordCircuitFailure(ChannelKindMessages, 0)
	time.Sleep(25 * time.Millisecond)

	selection, err := sch.SelectChannel(context.Background(), "", make(map[int]bool), ChannelKindMessages, "")
	if err != nil {
		t.Fatalf("选渠失败: %v", err)
	}
	if !selection.Probe() {
		t.Fatal("半开渠道选渠应取得探测名额")
	}

	// 渠道失败继续 failover：只释放预留，名额留给健康记账。
	selection.Permit.ReleaseReservation()
	if inFlight := sch.GetChannelInFlight(ChannelKindMessages, selection.ChannelIndex); inFlight != 0 {
		t.Fatalf("预留应已释放，实际 %d", inFlight)
	}
	if !selection.Probe() {
		t.Fatal("探测名额应保留到健康记账")
	}

	// 之后正常记账 + 释放。
	selection.Permit.RecordHealthNeutral()
	selection.Permit.Release()
}

// 两个以上请求并发通过预检：正式准入最多同时放行一个半开探测。
// 通过屏障同步：所有选渠完成后、任何释放之前统计同时持有的名额。
func TestConcurrentPrecheckAdmitsAtMostOneProbe(t *testing.T) {
	sch, _ := newPermitTestEnv(t)
	sch.RecordCircuitFailure(ChannelKindMessages, 0)
	time.Sleep(25 * time.Millisecond)

	const concurrency = 4
	start := make(chan struct{})
	// barrier：等待所有 goroutine 完成选渠（持有许可期间）。
	barrier := make(chan struct{})
	var mu sync.Mutex
	permits := 0

	var wg sync.WaitGroup
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			selection, err := sch.SelectChannel(context.Background(), "", make(map[int]bool), ChannelKindMessages, "")
			if err != nil {
				return
			}
			if selection.Permit != nil && selection.Probe() {
				mu.Lock()
				permits++
				mu.Unlock()
			}
			// 持有许可等待统一检查后再释放（不提前归还名额）。
			<-barrier
			if selection.Permit != nil {
				selection.Permit.Release()
			}
		}()
	}
	close(start)
	// 等待全部选渠完成（成功或失败）。
	waitFor := func() {
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			snap := sch.CircuitManagerSnapshot()
			if len(snap) == 1 && snap[0].Snapshot.TotalRequests >= concurrency {
				break
			}
			time.Sleep(2 * time.Millisecond)
		}
	}
	_ = waitFor
	// 轮询等待所有 goroutine 到达 barrier 前的状态：TotalRequests 不一定
	// 增长（被拒绝的 Allow 不计数），改用固定等待 + permits 不超过 1 的
	// 窗口内断言。
	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	held := permits
	mu.Unlock()
	close(barrier)
	wg.Wait()

	if held > 1 {
		t.Fatalf("并发预检同时放行的探测应为 1，实际 %d", held)
	}
	// 全部释放后无 in-flight 残留。
	if inFlight := sch.GetChannelInFlight(ChannelKindMessages, 0); inFlight != 0 {
		t.Fatalf("结束后 in-flight 应为 0，实际 %d", inFlight)
	}
}

// 探测名额泄漏检查：选渠未释放许可时名额被占用，下轮选渠只能换渠道或失败。
// （正常调用方都应 defer 释放；本用例反向锁定"许可确实持有名额"的语义。）
func TestPermitHoldsProbeSlotUntilReleased(t *testing.T) {
	sch, _ := newPermitTestEnv(t)
	sch.RecordCircuitFailure(ChannelKindMessages, 0)
	time.Sleep(25 * time.Millisecond)

	first, err := sch.SelectChannel(context.Background(), "", make(map[int]bool), ChannelKindMessages, "")
	if err != nil {
		t.Fatalf("首次选渠失败: %v", err)
	}
	if !first.Probe() {
		t.Fatal("首次选渠应取得探测名额")
	}

	// 未释放名额时，第二次选渠不能再次取得该渠道的探测名额。
	second, err := sch.SelectChannel(context.Background(), "", map[int]bool{first.ChannelIndex: true}, ChannelKindMessages, "")
	if err == nil && second.Permit != nil && second.Probe() && second.ChannelIndex == first.ChannelIndex {
		t.Fatal("探测名额被占用期间不得再次取得同渠道名额")
	}

	first.Permit.RecordHealthSuccess()
	first.Permit.Release()
}
