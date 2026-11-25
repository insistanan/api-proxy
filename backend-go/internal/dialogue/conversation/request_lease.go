package conversation

import "context"

type requestLeaseKey struct{}

// RequestLease 仅允许仍属于当前会话的请求补充可重建缓存；删除与回填共用注册表锁。
type RequestLease struct {
	registry *Registry
	id       string
}

func (r *Registry) WithRequestLease(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestLeaseKey{}, &RequestLease{registry: r, id: id})
}

func RequestLeaseFrom(ctx context.Context) *RequestLease {
	if ctx == nil {
		return nil
	}
	lease, _ := ctx.Value(requestLeaseKey{}).(*RequestLease)
	return lease
}

func (l *RequestLease) Store(write func()) bool {
	if l == nil {
		write()
		return true
	}
	l.registry.mu.Lock()
	defer l.registry.mu.Unlock()
	if l.registry.stopped || l.registry.records[l.id] == nil {
		return false
	}
	write()
	return true
}
