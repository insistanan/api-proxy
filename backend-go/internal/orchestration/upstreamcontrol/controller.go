// Package upstreamcontrol 管理 URL、实际协议和凭据组合的共享准入。
// 配置只更新规则；执行中的名额和 FIFO 队列不随热重载重建。
package upstreamcontrol

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	DefaultConcurrency = 5
	MaxQueue           = 3
	QueueTimeout       = 10 * time.Second
)

var (
	ErrQueueFull    = errors.New("上游共享队列已满（最多排队 3 个请求）")
	ErrQueueTimeout = errors.New("上游共享队列等待超过 10 秒")
	ErrRemoved      = errors.New("上游组合已从有效配置中移除")
)

// Identity 不保留明文 Key，也不推断不同 Key 是否属于同一账号。
type Identity struct {
	URL        string
	Protocol   string
	Credential [32]byte
}

func NewIdentity(baseURL, protocol, key string) Identity {
	baseURL = strings.TrimSpace(baseURL)
	if parsed, err := url.Parse(baseURL); err == nil {
		parsed.Scheme = strings.ToLower(parsed.Scheme)
		parsed.Host = strings.ToLower(parsed.Host)
		baseURL = parsed.String()
	}
	return Identity{URL: baseURL, Protocol: strings.ToLower(strings.TrimSpace(protocol)), Credential: sha256.Sum256([]byte(key))}
}

// Fingerprint 用于持久化 response ID 的归属，不记录明文凭据。
func (id Identity) Fingerprint() string {
	payload := append([]byte(id.URL+"\x00"+id.Protocol+"\x00"), id.Credential[:]...)
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

func IsAdmissionError(err error) bool {
	return errors.Is(err, ErrQueueFull) || errors.Is(err, ErrQueueTimeout) || errors.Is(err, ErrRemoved)
}

type waiter struct {
	ctx      context.Context
	deadline time.Time
	ready    chan struct{}
	granted  bool
	err      error
}

type entry struct {
	limit    int
	running  int
	active   bool
	queue    []*waiter
	cooldown time.Time
	wake     *time.Timer
}

type Controller struct {
	mu      sync.Mutex
	entries map[Identity]*entry
}

func NewController() *Controller {
	return &Controller{entries: make(map[Identity]*entry)}
}

// Reconcile 发布去重后的完整规则。删除再添加时仍复用未收尾的执行计数。
func (c *Controller) Reconcile(rules map[Identity]int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for id, state := range c.entries {
		limit, exists := rules[id]
		state.active = exists
		if exists {
			state.limit = limit
		}
		c.dispatchLocked(id, state)
		if !exists && state.running == 0 {
			delete(c.entries, id)
		}
	}
	for id, limit := range rules {
		if _, exists := c.entries[id]; !exists {
			c.entries[id] = &entry{limit: limit, active: true}
		}
	}
}

type Lease struct {
	once       sync.Once
	controller *Controller
	id         Identity
	state      *entry
}

func (l *Lease) Release() {
	if l == nil {
		return
	}
	l.once.Do(func() {
		c := l.controller
		c.mu.Lock()
		defer c.mu.Unlock()
		l.state.running--
		c.dispatchLocked(l.id, l.state)
		if !l.state.active && l.state.running == 0 && c.entries[l.id] == l.state {
			delete(c.entries, l.id)
		}
	})
}

// Acquire 的十秒截止只覆盖等待阶段，不改变上游请求的 context。
func (c *Controller) Acquire(ctx context.Context, id Identity) (*Lease, time.Duration, error) {
	started := time.Now()
	c.mu.Lock()
	state, exists := c.entries[id]
	if err := ctx.Err(); err != nil {
		c.mu.Unlock()
		return nil, 0, err
	}
	if !exists || !state.active {
		c.mu.Unlock()
		return nil, 0, ErrRemoved
	}
	// 先清除已经取消、过期的队首，再决定是否准入，后来者不能插队。
	c.dispatchLocked(id, state)
	if len(state.queue) == 0 && state.running < state.limit && !time.Now().Before(state.cooldown) {
		state.running++
		c.mu.Unlock()
		return &Lease{controller: c, id: id, state: state}, 0, nil
	}
	if len(state.queue) >= MaxQueue {
		c.mu.Unlock()
		return nil, 0, ErrQueueFull
	}
	w := &waiter{ctx: ctx, deadline: started.Add(QueueTimeout), ready: make(chan struct{})}
	state.queue = append(state.queue, w)
	c.dispatchLocked(id, state)
	c.mu.Unlock()
	timer := time.NewTimer(time.Until(w.deadline))
	defer timer.Stop()
	var waitErr error
	select {
	case <-w.ready:
	case <-ctx.Done():
		waitErr = ctx.Err()
	case <-timer.C:
		waitErr = ErrQueueTimeout
	}
	c.mu.Lock()
	// 同时获得名额和取消时，以取消优先；已分配的名额恰好归还一次。
	if err := ctx.Err(); err != nil {
		waitErr = err
	}
	if waitErr == nil {
		waitErr = w.err
	}
	if waitErr != nil {
		if w.granted {
			state.running--
		} else {
			for i, queued := range state.queue {
				if queued == w {
					state.queue = append(state.queue[:i], state.queue[i+1:]...)
					break
				}
			}
		}
		c.dispatchLocked(id, state)
		if !state.active && state.running == 0 && c.entries[id] == state {
			delete(c.entries, id)
		}
		c.mu.Unlock()
		return nil, time.Since(started), waitErr
	}
	c.mu.Unlock()
	return &Lease{controller: c, id: id, state: state}, time.Since(started), nil
}

func (c *Controller) dispatchLocked(id Identity, state *entry) {
	if state.wake != nil {
		state.wake.Stop()
		state.wake = nil
	}
	now := time.Now()
	// 扫描整个小队列，让已取消的非队首请求也立即失去占位。
	kept := state.queue[:0]
	for _, w := range state.queue {
		err := w.ctx.Err()
		if err == nil && !state.active {
			err = ErrRemoved
		}
		if err == nil && !now.Before(w.deadline) {
			err = ErrQueueTimeout
		}
		if err != nil {
			w.err = err
			close(w.ready)
		} else {
			kept = append(kept, w)
		}
	}
	state.queue = kept
	if now.Before(state.cooldown) && state.active && len(state.queue) > 0 {
		state.wake = time.AfterFunc(time.Until(state.cooldown), func() {
			c.mu.Lock()
			defer c.mu.Unlock()
			if c.entries[id] == state {
				c.dispatchLocked(id, state)
			}
		})
		return
	}
	for state.active && state.running < state.limit && len(state.queue) > 0 {
		w := state.queue[0]
		state.queue = state.queue[1:]
		state.running++
		w.granted = true
		close(w.ready)
	}
}

// CoolDown 只延长指定组合的冷却；不会把供应商要求的等待缩短。
func (c *Controller) CoolDown(id Identity, until time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if state := c.entries[id]; state != nil && state.cooldown.Before(until) {
		state.cooldown = until
		c.dispatchLocked(id, state)
	}
}
