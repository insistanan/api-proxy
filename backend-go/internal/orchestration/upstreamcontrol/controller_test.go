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
	first.Release() // 重复归还不能造成超发。
	q1 := receiveAcquire(t, queue[0])
	require.NoError(t, q1.err)
	defer q1.lease.Release()
	retry := asyncAcquire(ctx, c, id)
	awaitQueue(t, c, id, 3)
	q1.lease.Release()
	q2 := receiveAcquire(t, queue[1])
	require.NoError(t, q2.err)
	defer q2.lease.Release()
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

func TestQueuedCancelImmediatelyFreesPosition(t *testing.T) {
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
}

func TestReconcileKeepsRunningCountAndWakesRemovedQueue(t *testing.T) {
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
	second.Release()
	result := receiveAcquire(t, readded)
	require.NoError(t, result.err)
	result.lease.Release()
}

func TestRaisedLimitWakesQueueWithoutRelease(t *testing.T) {
	id := NewIdentity("https://example.com", "openai", "test-key")
	c := NewController()
	c.Reconcile(map[Identity]int{id: 1})
	first, _, err := c.Acquire(context.Background(), id)
	require.NoError(t, err)
	defer first.Release()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	queued := asyncAcquire(ctx, c, id)
	awaitQueue(t, c, id, 1)
	c.Reconcile(map[Identity]int{id: 2})
	result := receiveAcquire(t, queued)
	require.NoError(t, result.err)
	result.lease.Release()
}

func TestQueueTimeoutReturnsErrQueueTimeout(t *testing.T) {
	// 生产上限固定 10 秒；测试注入毫秒级值来验证超时路径，避免让整套测试空等。
	require.Equal(t, 10*time.Second, QueueTimeout)
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
}

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

func TestBudgetSharedAcrossCandidatesAndAuxiliaryContexts(t *testing.T) {
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
}

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
	// 日期格式只能延长已有冷却，不能用更短的值突破一分钟限制。
	Record429(ctx, http.Header{"Retry-After": []string{time.Now().Add(5 * time.Second).UTC().Format(http.TimeFormat)}})
	c.mu.Lock()
	unchanged := c.entries[first].cooldown
	c.mu.Unlock()
	require.Equal(t, until, unchanged)
}

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
					// 先取消非队首，再排空其余等待者，覆盖移动与过滤后的尾部清理。
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
