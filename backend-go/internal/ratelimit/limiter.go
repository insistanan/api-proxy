// Package ratelimit 保留旧滑动窗口限流实现；生产请求准入由 upstreamcontrol 负责。
package ratelimit

import (
	"context"
	"sync"
	"time"
)

// Limiter 渠道级滑动窗口限流与平滑排队器
type Limiter struct {
	mu      sync.RWMutex
	windows map[string]*slidingWindow
}

type slidingWindow struct {
	mu            sync.Mutex
	requests      []time.Time
	tokens        []tokenEntry
	cooldownUntil time.Time
}

type tokenEntry struct {
	timestamp time.Time
	count     int
}

var defaultLimiter = NewLimiter()

// GetDefaultLimiter 获取全局默认限流器实例
func GetDefaultLimiter() *Limiter {
	return defaultLimiter
}

// NewLimiter 创建新的限流器实例
func NewLimiter() *Limiter {
	return &Limiter{
		windows: make(map[string]*slidingWindow),
	}
}

func (l *Limiter) getWindow(key string) *slidingWindow {
	l.mu.RLock()
	w, exists := l.windows[key]
	l.mu.RUnlock()
	if exists {
		return w
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	if w, exists = l.windows[key]; exists {
		return w
	}
	w = &slidingWindow{
		requests: make([]time.Time, 0, 64),
		tokens:   make([]tokenEntry, 0, 64),
	}
	l.windows[key] = w
	return w
}

// Record429 上游返回 429 时记录冷却期，让该 key 的后续请求自动进入退避排队
func (l *Limiter) Record429(key string, backoff time.Duration) {
	if l == nil || key == "" || backoff <= 0 {
		return
	}
	w := l.getWindow(key)
	w.mu.Lock()
	defer w.mu.Unlock()
	until := time.Now().Add(backoff)
	if until.After(w.cooldownUntil) {
		w.cooldownUntil = until
	}
}

// WaitOrAcquire 检查并获取配额，如处于冷却或超速则在 maxWait 允许范围内平滑等待
func (l *Limiter) WaitOrAcquire(ctx context.Context, key string, maxRPM int, maxTPM int, estimatedTokens int, maxWait time.Duration) (time.Duration, bool) {
	if l == nil || key == "" {
		return 0, true
	}
	w := l.getWindow(key)
	startTime := time.Now()

	for {
		w.mu.Lock()
		now := time.Now()
		// 1. 检查冷却
		if now.Before(w.cooldownUntil) {
			waitNeeded := w.cooldownUntil.Sub(now)
			w.mu.Unlock()
			if waitNeeded > maxWait || time.Since(startTime)+waitNeeded > maxWait {
				return 0, false
			}
			select {
			case <-ctx.Done():
				return 0, false
			case <-time.After(waitNeeded):
				continue
			}
		}

		// 2. 清理 1 分钟前的旧记录
		cutoff := now.Add(-1 * time.Minute)
		w.cleanup(cutoff)

		// 3. 检查 RPM
		rpmExceeded := maxRPM > 0 && len(w.requests) >= maxRPM
		// 4. 检查 TPM
		currentTokens := 0
		for _, t := range w.tokens {
			currentTokens += t.count
		}
		tpmExceeded := maxTPM > 0 && (currentTokens+estimatedTokens) > maxTPM

		if !rpmExceeded && !tpmExceeded {
			// 准予通行，记录窗口数据
			w.requests = append(w.requests, now)
			if estimatedTokens > 0 {
				w.tokens = append(w.tokens, tokenEntry{timestamp: now, count: estimatedTokens})
			}
			w.mu.Unlock()
			return time.Since(startTime), true
		}

		w.mu.Unlock()
		// 触发限速排队，轻量退避后重试检测
		backoff := 50 * time.Millisecond
		if time.Since(startTime)+backoff > maxWait {
			return 0, false
		}
		select {
		case <-ctx.Done():
			return 0, false
		case <-time.After(backoff):
		}
	}
}

func (w *slidingWindow) cleanup(cutoff time.Time) {
	// 清理过期请求
	validReqIdx := 0
	for i, ts := range w.requests {
		if ts.After(cutoff) {
			validReqIdx = i
			break
		}
		if i == len(w.requests)-1 {
			validReqIdx = len(w.requests)
		}
	}
	w.requests = w.requests[validReqIdx:]

	// 清理过期 Token
	validTokenIdx := 0
	for i, entry := range w.tokens {
		if entry.timestamp.After(cutoff) {
			validTokenIdx = i
			break
		}
		if i == len(w.tokens)-1 {
			validTokenIdx = len(w.tokens)
		}
	}
	w.tokens = w.tokens[validTokenIdx:]
}
