package httpclient

import (
	"context"
	"io"
	"sync"

	"github.com/insistanan/api-proxy/internal/orchestration/upstreamcontrol"
)

// admissionBody 持有名额直到 EOF、Close 或取消后的网络收尾，恰好释放一次。
type admissionBody struct {
	body     io.ReadCloser
	lease    *upstreamcontrol.Lease
	once     sync.Once
	err      error
	mu       sync.Mutex
	stop     func() bool
	finished bool
}

func newAdmissionBody(ctx context.Context, body io.ReadCloser, lease *upstreamcontrol.Lease) io.ReadCloser {
	wrapped := &admissionBody{body: body, lease: lease}
	stop := context.AfterFunc(ctx, func() { _ = wrapped.Close() })
	wrapped.mu.Lock()
	if wrapped.finished {
		stop()
	} else {
		wrapped.stop = stop
	}
	wrapped.mu.Unlock()
	return wrapped
}

func (b *admissionBody) Read(p []byte) (int, error) {
	n, err := b.body.Read(p)
	if err != nil {
		_ = b.Close()
	}
	return n, err
}

func (b *admissionBody) Close() error {
	b.once.Do(func() {
		b.err = b.body.Close()
		b.lease.Release()
		b.mu.Lock()
		b.finished = true
		if b.stop != nil {
			b.stop()
		}
		b.mu.Unlock()
	})
	return b.err
}
