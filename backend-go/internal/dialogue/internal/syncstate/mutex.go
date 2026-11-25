// Package syncstate 提供会话状态使用的可取消读写锁。
package syncstate

import (
	"context"
	"sync"
)

// Mutex 的零值可用。锁等待与数据库等待都受请求 Context 控制。
// 写等待者阻止新读者插队；不为每次等待创建 goroutine。
type Mutex struct {
	mu             sync.Mutex
	changed        chan struct{}
	readers        int
	writer         bool
	waitingWriters int
}

func (m *Mutex) signal() {
	if m.changed != nil {
		close(m.changed)
	}
	m.changed = nil
}

func (m *Mutex) acquire(ctx context.Context, read bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !read {
		m.waitingWriters++
		defer func() { m.waitingWriters--; m.signal() }()
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !m.writer && (read && m.waitingWriters == 0 || !read && m.readers == 0) {
			if read {
				m.readers++
			} else {
				m.writer = true
			}
			return nil
		}
		if m.changed == nil {
			m.changed = make(chan struct{})
		}
		changed := m.changed
		m.mu.Unlock()
		select {
		case <-ctx.Done():
		case <-changed:
		}
		m.mu.Lock()
	}
}
func (m *Mutex) LockContext(ctx context.Context) error  { return m.acquire(ctx, false) }
func (m *Mutex) RLockContext(ctx context.Context) error { return m.acquire(ctx, true) }
func (m *Mutex) Lock()                                  { _ = m.LockContext(context.Background()) }
func (m *Mutex) RLock()                                 { _ = m.RLockContext(context.Background()) }
func (m *Mutex) Unlock() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.writer {
		panic("syncstate: unlock of unlocked writer")
	}
	m.writer = false
	m.signal()
}
func (m *Mutex) RUnlock() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.readers == 0 {
		panic("syncstate: unlock of unlocked reader")
	}
	m.readers--
	m.signal()
}
