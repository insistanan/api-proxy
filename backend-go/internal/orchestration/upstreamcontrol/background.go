package upstreamcontrol

import (
	"context"
	"errors"
	"time"
)

var ErrBackgroundBusy = errors.New("上游正忙或处于冷却，后台保活不进入等待队列")

type backgroundKey struct{}

// WithBackgroundAdmission 只允许后台请求使用空闲组合，不与真实请求排队竞争。
func WithBackgroundAdmission(ctx context.Context) context.Context {
	return context.WithValue(ctx, backgroundKey{}, true)
}

func (c *Controller) acquireBackground(ctx context.Context, id Identity) (*Lease, time.Duration, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	state := c.entries[id]
	if state == nil || !state.active {
		return nil, 0, ErrRemoved
	}
	c.dispatchLocked(id, state)
	if state.running != 0 || len(state.queue) != 0 || time.Now().Before(state.cooldown) {
		return nil, 0, ErrBackgroundBusy
	}
	state.running++
	return &Lease{controller: c, id: id, state: state}, 0, nil
}
