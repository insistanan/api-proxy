package ratelimit

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestLimiter_RPM(t *testing.T) {
	limiter := NewLimiter()
	ctx := context.Background()
	key := "test-channel-rpm"

	// 允许 2 RPM
	_, allowed1 := limiter.WaitOrAcquire(ctx, key, 2, 0, 0, 100*time.Millisecond)
	assert.True(t, allowed1)

	_, allowed2 := limiter.WaitOrAcquire(ctx, key, 2, 0, 0, 100*time.Millisecond)
	assert.True(t, allowed2)

	// 第 3 个请求超限且等待时间不足，应被拒绝
	_, allowed3 := limiter.WaitOrAcquire(ctx, key, 2, 0, 0, 50*time.Millisecond)
	assert.False(t, allowed3)
}

func TestLimiter_TPM(t *testing.T) {
	limiter := NewLimiter()
	ctx := context.Background()
	key := "test-channel-tpm"

	// 允许 1000 TPM
	_, allowed1 := limiter.WaitOrAcquire(ctx, key, 0, 1000, 600, 100*time.Millisecond)
	assert.True(t, allowed1)

	// 累计 600 + 500 = 1100 > 1000，应被拒绝
	_, allowed2 := limiter.WaitOrAcquire(ctx, key, 0, 1000, 500, 50*time.Millisecond)
	assert.False(t, allowed2)
}

func TestLimiter_Record429Backoff(t *testing.T) {
	limiter := NewLimiter()
	ctx := context.Background()
	key := "test-channel-429"

	// 记录 200ms 冷却
	limiter.Record429(key, 200*time.Millisecond)

	// 立即请求，若等待时间只有 50ms 则返回 false
	_, allowedImmediate := limiter.WaitOrAcquire(ctx, key, 0, 0, 0, 50*time.Millisecond)
	assert.False(t, allowedImmediate)

	// 若允许等待 300ms，则排队后顺利放行
	waited, allowedAfterWait := limiter.WaitOrAcquire(ctx, key, 0, 0, 0, 350*time.Millisecond)
	assert.True(t, allowedAfterWait)
	assert.GreaterOrEqual(t, waited, 100*time.Millisecond)
}
