// R2-b 验收测试（scheduler 层）：许可句柄的取得、记账、释放与预检竞争。
package scheduler

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/insistanan/api-proxy/internal/infra/config"
	"github.com/insistanan/api-proxy/internal/orchestration/circuit"
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
		FailureThreshold: 1, MinRequests: 1, Cooldown: 20 * time.Millisecond, SuccessThreshold: 1,
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
	if selection.Permit == nil {
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

// 第二次记账不产生渠道级效果。
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

func TestPermitConcurrentReleaseClosesLifecycle(t *testing.T) {
	sch, _ := newPermitTestEnv(t)
	selection, err := sch.SelectChannel(context.Background(), "", make(map[int]bool), ChannelKindMessages, "")
	if err != nil {
		t.Fatal(err)
	}
	// 同一渠道的另一请求仍持有预留，重复释放不能扣掉它的计数。
	sch.ReserveChannel(ChannelKindMessages, 0)
	defer sch.ReleaseChannelReservation(ChannelKindMessages, 0)
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(selection.Permit.Release)
	}
	wg.Wait()
	selection.Permit.RecordHealthFailure()
	if got := sch.GetChannelInFlight(ChannelKindMessages, 0); got != 1 {
		t.Fatalf("重复释放影响其他请求的预留: %d", got)
	}
	if snap := sch.CircuitManager().SnapshotEntry("messages", 0); snap.ConsecutiveFailures != 0 {
		t.Fatalf("许可结束后仍接受迟到结果: %+v", snap)
	}
}

func TestPermitStaleResultKeepsNewProbe(t *testing.T) {
	sch, manager := newPermitTestEnv(t)
	manager.RecordFailure("messages", 0)
	time.Sleep(25 * time.Millisecond)
	old := manager.Allow("messages", 0)
	permit := sch.NewCircuitProbePermit(ChannelKindMessages, 0, old.Generation)
	manager.Reset("messages", 0)
	manager.RecordFailure("messages", 0)
	time.Sleep(25 * time.Millisecond)
	current := manager.Allow("messages", 0)
	if !old.Probe || !current.Probe || old.Generation == current.Generation {
		t.Fatal("未取得不同代次的探测许可")
	}
	defer manager.RecordNeutralGeneration("messages", 0, current.Generation)
	permit.RecordHealthSuccess()
	permit.Release()
	if snap := manager.SnapshotEntry("messages", 0); snap.State != circuit.StateHalfOpen || snap.HalfOpenInFlight != 1 {
		t.Fatalf("旧请求结果影响了新探测: %+v", snap)
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
	if sch.CircuitManager().SnapshotEntry("messages", 0).HalfOpenInFlight != 1 {
		t.Fatal("半开渠道选渠应取得探测名额")
	}

	// 渠道失败继续 failover：只释放预留，名额留给健康记账。
	selection.Permit.ReleaseReservation()
	if inFlight := sch.GetChannelInFlight(ChannelKindMessages, selection.ChannelIndex); inFlight != 0 {
		t.Fatalf("预留应已释放，实际 %d", inFlight)
	}
	if sch.CircuitManager().SnapshotEntry("messages", 0).HalfOpenInFlight != 1 {
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
	var selected sync.WaitGroup
	selected.Add(concurrency)
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			selection, err := sch.SelectChannel(context.Background(), "", make(map[int]bool), ChannelKindMessages, "")
			if err != nil {
				selected.Done()
				return
			}
			if selection.Permit != nil {
				mu.Lock()
				permits++
				mu.Unlock()
			}
			selected.Done()
			// 持有许可等待统一检查后再释放（不提前归还名额）。
			<-barrier
			if selection.Permit != nil {
				selection.Permit.Release()
			}
		}()
	}
	close(start)
	selected.Wait()
	mu.Lock()
	held := permits
	mu.Unlock()
	close(barrier)
	wg.Wait()

	if held != 1 {
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
	if sch.CircuitManager().SnapshotEntry("messages", 0).HalfOpenInFlight != 1 {
		t.Fatal("首次选渠应取得探测名额")
	}

	// 未释放名额时，第二次选渠不能再次取得该渠道的探测名额。
	second, err := sch.SelectChannel(context.Background(), "", map[int]bool{first.ChannelIndex: true}, ChannelKindMessages, "")
	if err == nil && second.Permit != nil && second.ChannelIndex == first.ChannelIndex {
		t.Fatal("探测名额被占用期间不得再次取得同渠道名额")
	}

	first.Permit.RecordHealthSuccess()
	first.Permit.Release()
}
