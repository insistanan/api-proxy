package upstreamcontrol

import (
	"context"
	"math/rand/v2"
	"net/http"
	"time"

	"github.com/insistanan/api-proxy/internal/infra/upstreamreq"
)

// Record429 是共享凭据冷却的唯一写入策略；无有效 Retry-After 时采用有界指数退避。
func Record429(ctx context.Context, header http.Header) {
	candidate, ok := CandidateFromContext(ctx)
	if !ok || candidate.Controller == nil {
		return
	}
	now := time.Now()
	until, explicit := upstreamreq.ParseRetryAfter(header.Get("Retry-After"), now)
	if !explicit {
		attempts := 1
		if scope := ScopeFromContext(ctx); scope != nil {
			scope.mu.Lock()
			attempts = scope.attempts
			scope.mu.Unlock()
		}
		exponent := min(max(attempts-1, 0), 5)
		base := time.Second * time.Duration(1<<exponent)
		until = now.Add(base + time.Duration(rand.Int64N(int64(base/2)+1)))
	}
	candidate.Controller.CoolDown(candidate.Identity, until)
}

// Cooldowns 返回仍生效的共享冷却快照，供观测读取；历史日志不能替代当前准入状态。
func (c *Controller) Cooldowns() map[Identity]time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	result := make(map[Identity]time.Time)
	for id, state := range c.entries {
		if state.active && state.cooldown.After(now) {
			result[id] = state.cooldown
		}
	}
	return result
}
