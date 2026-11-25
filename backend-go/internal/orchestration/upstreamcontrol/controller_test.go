package upstreamcontrol

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"
)

type acquireResult struct {
	lease *Lease
	err   error
}

func asyncAcquire(ctx context.Context, c *Controller, id Identity) <-chan acquireResult {
	result := make(chan acquireResult, 1)
	go func() {
		lease, _, err := c.Acquire(ctx, id)
		result <- acquireResult{lease: lease, err: err}
	}()
	return result
}

func awaitQueue(t *testing.T, c *Controller, id Identity, length int) {
	t.Helper()
	require.Eventually(t, func() bool {
		c.mu.Lock()
		defer c.mu.Unlock()
		return c.entries[id] != nil && len(c.entries[id].queue) == length
	}, time.Second, time.Millisecond)
}

func receiveAcquire(t *testing.T, result <-chan acquireResult) acquireResult {
	t.Helper()
	select {
	case acquired := <-result:
		return acquired
	case <-time.After(time.Second):
		t.Fatal("等待准入结果超时")
		return acquireResult{}
	}
}

// 重试必须排在已有排队请求之后；满队列拒绝新请求；重复归还不超发。
func TestRetryGoesBehindExistingQueue(t *testing.T) {
	id := NewIdentity("https://example.com", "responses", "test-key")
	c := NewController()
	c.Reconcile(map[Identity]int{id: 1})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	first, _, err := c.Acquire(ctx, id)
	require.NoError(t, err)
	defer first.Release()
	queue := make([]<-chan acquireResult, 3)
	for i := range queue {
		queue[i] = asyncAcquire(ctx, c, id)
		awaitQueue(t, c, id, i+1)
	}
	_, _, err = c.Acquire(ctx, id)
	require.ErrorIs(t, err, ErrQueueFull)
	first.Release()
	first.Release()
	q1 := receiveAcquire(t, queue[0])
	require.NoError(t, q1.err)
	defer q1.lease.Release()
	retry := asyncAcquire(ctx, c, id)
	awaitQueue(t, c, id, 3)
	q1.lease.Release()
	q2 := receiveAcquire(t, queue[1])
	require.NoError(t, q2.err)
	q2.lease.Release()
	q3 := receiveAcquire(t, queue[2])
	require.NoError(t, q3.err)
	defer q3.lease.Release()
	select {
	case <-retry:
		t.Fatal("重试插到了已有排队请求前面")
	default:
	}
	q3.lease.Release()
	retried := receiveAcquire(t, retry)
	require.NoError(t, retried.err)
	retried.lease.Release()
}

// 排队中的请求取消或超时后立即让出位置。
func TestQueuedWaitersLeaveQueueOnCancelAndTimeout(t *testing.T) {
	t.Run("取消", func(t *testing.T) {
		id := NewIdentity("https://example.com", "openai", "test-key")
		c := NewController()
		c.Reconcile(map[Identity]int{id: 1})
		active, _, err := c.Acquire(context.Background(), id)
		require.NoError(t, err)
		defer active.Release()
		ctx, cancel := context.WithCancel(context.Background())
		queued := asyncAcquire(ctx, c, id)
		awaitQueue(t, c, id, 1)
		cancel()
		result := receiveAcquire(t, queued)
		require.ErrorIs(t, result.err, context.Canceled)
		awaitQueue(t, c, id, 0)
	})
	t.Run("超时", func(t *testing.T) {
		id := NewIdentity("https://example.com", "openai", "test-key")
		c := NewController()
		c.queueTimeout = 20 * time.Millisecond
		c.Reconcile(map[Identity]int{id: 1})
		first, _, err := c.Acquire(context.Background(), id)
		require.NoError(t, err)
		defer first.Release()
		started := time.Now()
		_, waited, err := c.Acquire(context.Background(), id)
		require.ErrorIs(t, err, ErrQueueTimeout)
		require.GreaterOrEqual(t, waited, 20*time.Millisecond)
		require.Less(t, time.Since(started), time.Second)
		awaitQueue(t, c, id, 0)
	})
}

// 降低上限不驱逐在途请求；移除身份唤醒排队者；重新加入后提高上限可直接唤醒队列。
func TestReconcileLimitChangesAndRemovalWakeQueue(t *testing.T) {
	id := NewIdentity("https://example.com", "claude", "test-key")
	c := NewController()
	c.Reconcile(map[Identity]int{id: 2})
	first, _, err := c.Acquire(context.Background(), id)
	require.NoError(t, err)
	defer first.Release()
	second, _, err := c.Acquire(context.Background(), id)
	require.NoError(t, err)
	defer second.Release()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	queued := asyncAcquire(ctx, c, id)
	awaitQueue(t, c, id, 1)
	c.Reconcile(map[Identity]int{id: 1})
	first.Release()
	select {
	case <-queued:
		t.Fatal("降低上限后仍超额准入")
	default:
	}
	c.Reconcile(map[Identity]int{})
	removed := receiveAcquire(t, queued)
	require.ErrorIs(t, removed.err, ErrRemoved)
	c.Reconcile(map[Identity]int{id: 1})
	readded := asyncAcquire(ctx, c, id)
	awaitQueue(t, c, id, 1)
	c.Reconcile(map[Identity]int{id: 2})
	result := receiveAcquire(t, readded)
	require.NoError(t, result.err)
	result.lease.Release()
}

// 两个请求同时完成 Acquire 与 cancel/grant 竞争时，不泄漏名额或排队项。
func TestCancelAndGrantRaceDoesNotLeak(t *testing.T) {
	for range 50 {
		id := NewIdentity("https://example.com", "responses", "test-key")
		c := NewController()
		c.Reconcile(map[Identity]int{id: 1})
		first, _, err := c.Acquire(context.Background(), id)
		require.NoError(t, err)
		ctx, cancel := context.WithCancel(context.Background())
		queued := asyncAcquire(ctx, c, id)
		awaitQueue(t, c, id, 1)
		go cancel()
		first.Release()
		result := receiveAcquire(t, queued)
		require.True(t, result.err == nil || errors.Is(result.err, context.Canceled))
		result.lease.Release()
		cancel()
		c.mu.Lock()
		running, length := c.entries[id].running, len(c.entries[id].queue)
		c.mu.Unlock()
		require.Zero(t, running)
		require.Zero(t, length)
	}
}

// 辅助上下文：同一请求的多个候选共享总预算；后台准入只使用空闲名额且不排队。
func TestAuxiliaryAdmissionSharesBudgetAndYieldsToForeground(t *testing.T) {
	t.Run("预算跨候选共享", func(t *testing.T) {
		first := NewIdentity("https://example.com", "openai", "first-key")
		second := NewIdentity("https://example.com", "responses", "second-key")
		c := NewController()
		c.Reconcile(map[Identity]int{first: 1, second: 1})
		ctx := WithScope(context.Background())
		for i := range MaxAttempts {
			id := first
			if i%2 == 1 {
				id = second
			}
			child := WithCandidate(WithScope(ctx), c, id, false)
			lease, _, attempts, err := AcquireRequest(child)
			require.NoError(t, err)
			require.Equal(t, i+1, attempts)
			lease.Release()
		}
		_, _, _, err := AcquireRequest(WithCandidate(ctx, c, first, false))
		require.ErrorIs(t, err, ErrBudget)
	})
	t.Run("后台只用空闲名额", func(t *testing.T) {
		c := NewController()
		id := NewIdentity("https://example.test/v1/messages", "claude", "key")
		c.Reconcile(map[Identity]int{id: 2})
		background := WithBackgroundAdmission(WithCandidate(WithScope(context.Background()), c, id, false))
		foreground, _, err := c.Acquire(context.Background(), id)
		require.NoError(t, err)
		if lease, _, _, err := AcquireRequest(background); !errors.Is(err, ErrBackgroundBusy) || lease != nil {
			t.Fatalf("前台使用上游时后台不应抢占剩余名额: %v", err)
		}
		foreground.Release()
		lease, _, _, err := AcquireRequest(background)
		require.NoError(t, err)
		foreground, _, err = c.Acquire(context.Background(), id)
		require.NoError(t, err)
		foreground.Release()
		lease.Release()
		c.CoolDown(id, time.Now().Add(time.Minute))
		if lease, _, _, err := AcquireRequest(background); !errors.Is(err, ErrBackgroundBusy) || lease != nil {
			t.Fatalf("后台不应进入冷却队列: %v", err)
		}
	})
}

// Retry-After 只冷却匹配的身份组合；日期格式不能缩短已有冷却。
func TestRetryAfterOnlyCoolsMatchingTuple(t *testing.T) {
	first := NewIdentity("https://example.com", "responses", "first-key")
	second := NewIdentity("https://example.com", "responses", "second-key")
	c := NewController()
	c.Reconcile(map[Identity]int{first: 1, second: 1})
	ctx := WithCandidate(WithScope(context.Background()), c, first, false)
	started := time.Now()
	Record429(ctx, http.Header{"Retry-After": []string{"60"}})
	c.mu.Lock()
	until := c.entries[first].cooldown
	other := c.entries[second].cooldown
	c.mu.Unlock()
	require.GreaterOrEqual(t, until.Sub(started), time.Minute)
	require.True(t, other.IsZero())
	Record429(ctx, http.Header{"Retry-After": []string{time.Now().Add(5 * time.Second).UTC().Format(http.TimeFormat)}})
	c.mu.Lock()
	unchanged := c.entries[first].cooldown
	c.mu.Unlock()
	require.Equal(t, until, unchanged)
}

// 队列中的请求以任一方式结束后，底层数组不得继续保留其 context 或等待者引用。
func TestQueueRemoval_ReleasesBackingReferences(t *testing.T) {
	for _, mode := range []string{"cancel", "timeout", "grant", "remove"} {
		t.Run(mode, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				id := NewIdentity("https://example.com", "responses", "key")
				controller := NewController()
				controller.Reconcile(map[Identity]int{id: 1})
				active, _, err := controller.Acquire(context.Background(), id)
				require.NoError(t, err)
				defer active.Release()
				var cancels []context.CancelFunc
				var results []<-chan acquireResult
				for range MaxQueue {
					ctx, cancel := context.WithCancel(context.Background())
					defer cancel()
					cancels = append(cancels, cancel)
					results = append(results, asyncAcquire(ctx, controller, id))
					synctest.Wait()
				}
				controller.mu.Lock()
				state := controller.entries[id]
				backing := state.queue[:cap(state.queue)]
				controller.mu.Unlock()
				switch mode {
				case "cancel":
					cancels[1]()
					synctest.Wait()
					controller.mu.Lock()
					tail := backing[MaxQueue-1]
					controller.mu.Unlock()
					require.Nil(t, tail)
					cancels[0]()
					cancels[2]()
				case "timeout":
					time.Sleep(QueueTimeout + time.Second)
				case "grant":
					active.Release()
				case "remove":
					controller.Reconcile(nil)
				}
				for _, result := range results {
					acquired := <-result
					if mode == "grant" {
						require.NoError(t, acquired.err)
						require.NotNil(t, acquired.lease)
					} else {
						require.Error(t, acquired.err)
					}
					acquired.lease.Release()
				}
				synctest.Wait()
				controller.mu.Lock()
				queue := state.queue
				retained := append([]*waiter(nil), backing...)
				controller.mu.Unlock()
				require.Nil(t, queue)
				for _, reference := range retained {
					require.Nil(t, reference, "底层数组不能继续保留已结束请求的 context")
				}
			})
		})
	}
}
