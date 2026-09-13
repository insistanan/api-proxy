package logger

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sync"

	"github.com/BenedictKing/api-proxy/internal/utils"
	"github.com/google/uuid"
)

type requestIDContextKey struct{}

func NewRequestID() string {
	return uuid.NewString()
}

func ContextWithRequestID(ctx context.Context, requestID string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if requestID == "" {
		return ctx
	}
	return context.WithValue(ctx, requestIDContextKey{}, requestID)
}

func RequestIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	value := ctx.Value(requestIDContextKey{})
	requestID, _ := value.(string)
	return requestID
}

func RecordTraffic(entry TrafficLog) {
	store := Default()
	if store == nil {
		return
	}
	// RecordTraffic 会直接报告存储错误，不能再经标准 log 回写失败的队列。
	_ = store.RecordTraffic(entry)
}

func RecordStreamSynth(ctx context.Context, apiType string, body string) {
	if body == "" {
		return
	}
	RecordTraffic(TrafficLog{
		RequestID: RequestIDFromContext(ctx),
		Phase:     PhaseStreamSynth,
		APIType:   apiType,
		Body:      body,
		Stream:    true,
	})
}

func MaskedHeaderJSON(header http.Header) string {
	if header == nil {
		return ""
	}
	plain := make(map[string]string, len(header))
	for key, values := range header {
		if len(values) > 0 {
			plain[key] = values[0]
		}
	}
	masked := utils.MaskSensitiveHeaders(plain)
	encoded, err := json.Marshal(masked)
	if err != nil {
		return ""
	}
	return string(encoded)
}

func CaptureResponseBody(inner io.ReadCloser, entry TrafficLog) io.ReadCloser {
	if inner == nil {
		return inner
	}
	return &responseCapture{
		inner:     inner,
		entry:     entry,
		remaining: MaxBodyBytes,
	}
}

type responseCapture struct {
	inner     io.ReadCloser
	entry     TrafficLog
	mu        sync.Mutex
	buf       []byte
	remaining int
	closed    bool
	once      sync.Once
	closeErr  error
}

func (c *responseCapture) Read(p []byte) (int, error) {
	n, err := c.inner.Read(p)
	if n > 0 {
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.closed {
			return n, err
		}
		c.entry.OriginalBytes += n
		take := n
		if take > c.remaining {
			take = c.remaining
		}
		c.buf = append(c.buf, p[:take]...)
		c.remaining -= take
		if take < n {
			c.entry.Truncated = true
		}
	}
	return n, err
}

func (c *responseCapture) Close() error {
	c.once.Do(func() {
		// 先打断网络读取；日志背压不能阻止人工否决关闭上游连接。
		if c.inner != nil {
			c.closeErr = c.inner.Close()
		}
		c.mu.Lock()
		c.closed = true
		entry := c.entry
		entry.Phase = PhaseUpstreamResponse
		if len(c.buf) > 0 {
			entry.Body = string(c.buf)
		}
		c.buf = nil
		c.mu.Unlock()
		RecordTraffic(entry)
	})
	return c.closeErr
}
