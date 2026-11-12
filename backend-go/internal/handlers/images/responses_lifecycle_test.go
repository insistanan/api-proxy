package images

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/insistanan/api-proxy/internal/infra/config"
	"github.com/insistanan/api-proxy/internal/infra/httpclient"
	"github.com/stretchr/testify/require"
)

// 首次 Flush 通知写入已完成，测试线程无需并发读取 ResponseRecorder。
type notifiedImagesResponseWriter struct {
	*httptest.ResponseRecorder
	flushed chan struct{}
	once    sync.Once
}

func (writer *notifiedImagesResponseWriter) Flush() {
	writer.ResponseRecorder.Flush()
	writer.once.Do(func() { close(writer.flushed) })
}

func notifyImagesFlush(c *gin.Context, recorder *httptest.ResponseRecorder) <-chan struct{} {
	writer := &notifiedImagesResponseWriter{ResponseRecorder: recorder, flushed: make(chan struct{})}
	wrapped, _ := gin.CreateTestContext(writer)
	c.Writer = wrapped.Writer
	return writer.flushed
}

func TestResponsesImageLifecyclePreservesDeadlineAndCancellation(t *testing.T) {
	parent, cancel := context.WithDeadline(context.Background(), time.Now().Add(time.Minute))
	defer cancel()
	lifecycle := newResponsesImageLifecycle(parent)
	defer lifecycle.close()
	parentDeadline, _ := parent.Deadline()
	deadline, ok := lifecycle.ctx.Deadline()
	require.True(t, ok)
	require.Equal(t, parentDeadline, deadline)
	cancel()
	select {
	case <-lifecycle.ctx.Done():
		require.ErrorIs(t, context.Cause(lifecycle.ctx), context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("图片交付前未传播取消")
	}
	bounded := newResponsesImageLifecycle(context.Background())
	defer bounded.close()
	deadline, ok = bounded.ctx.Deadline()
	require.True(t, ok)
	require.WithinDuration(t, time.Now().Add(imagesRequestTimeout), deadline, time.Second)
}

func TestResponsesImageDeadlineStillAppliesAfterDelivery(t *testing.T) {
	parent, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	lifecycle := newResponsesImageLifecycle(parent)
	defer lifecycle.close()
	lifecycle.markDelivered()
	select {
	case <-lifecycle.ctx.Done():
		require.ErrorIs(t, context.Cause(lifecycle.ctx), context.DeadlineExceeded)
	case <-time.After(time.Second):
		t.Fatal("图片交付后丢失父 deadline")
	}
}

func TestResponsesImageRetryKeepsParentRequestBudget(t *testing.T) {
	c, _ := imageStreamContext()
	parent, cancel := context.WithTimeout(c.Request.Context(), time.Minute)
	defer cancel()
	c.Request = c.Request.WithContext(parent)
	first := resetResponsesLifecycle(c)
	second := resetResponsesLifecycle(c)
	defer finishResponsesLifecycle(c)
	require.ErrorIs(t, first.ctx.Err(), context.Canceled)
	deadline, _ := parent.Deadline()
	retryDeadline, ok := second.ctx.Deadline()
	require.True(t, ok)
	require.Equal(t, deadline, retryDeadline)
	require.NoError(t, second.ctx.Err())
}

func TestResponsesImageClientCloseDoesNotTurnTruncatedTailIntoSuccess(t *testing.T) {
	c, recorder := imageStreamContext()
	flushed := notifyImagesFlush(c, recorder)
	ctx, cancel := context.WithCancel(c.Request.Context())
	defer cancel()
	c.Request = c.Request.WithContext(ctx)
	reader, writer := io.Pipe()
	defer writer.Close()
	result := make(chan error, 1)
	go func() {
		_, err := convertResponsesImageStream(c, reader, make(http.Header), "/images/generations", true, 1024*1024)
		result <- err
	}()
	_, err := io.WriteString(writer, "event: response.output_item.done\ndata: {\"type\":\"response.output_item.done\",\"item\":{\"id\":\"img\",\"type\":\"image_generation_call\",\"result\":\"AQID\"}}\n\n")
	require.NoError(t, err)
	select {
	case <-flushed:
	case <-time.After(2 * time.Second):
		t.Fatal("完整图片没有写出")
	}
	cancel()
	require.NoError(t, writer.Close())
	select {
	case err := <-result:
		require.ErrorContains(t, err, "未收到 response.completed")
	case <-time.After(2 * time.Second):
		t.Fatal("上游截断后未返回错误")
	}
}

func TestImagesResponsesSendingStopsOnClientCancellation(t *testing.T) {
	connected, stopped := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		close(connected)
		<-r.Context().Done()
		close(stopped)
	}))
	defer server.Close()
	c, _ := imageStreamContext()
	ctx, cancel := context.WithCancel(c.Request.Context())
	defer cancel()
	c.Request = c.Request.WithContext(ctx)
	c.Request.Header.Set("Content-Type", "application/json")
	req, err := buildImagesUpstreamRequest(c, &config.UpstreamConfig{
		BaseURL: server.URL, ServiceType: "responses", ResponsesModel: "gpt-5.6",
	}, "test-key", "/images/generations", []byte(`{"model":"gpt-image-2.6-sunburst","prompt":"猫","stream":true}`))
	require.NoError(t, err)
	defer finishResponsesLifecycle(c)
	result := make(chan error, 1)
	go func() {
		resp, err := httpclient.Send(req, httpclient.SendOptions{IsStream: true})
		if resp != nil {
			resp.Body.Close()
		}
		result <- err
	}()
	select {
	case <-connected:
	case <-time.After(2 * time.Second):
		t.Fatal("请求没有到达测试上游")
	}
	cancel()
	select {
	case err := <-result:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(2 * time.Second):
		t.Fatal("客户端取消后发送未结束")
	}
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("上游未收到取消")
	}
}

func TestImagesResponsesBlockedBodyStopsOnCancellationAndDeadline(t *testing.T) {
	for _, deadline := range []bool{false, true} {
		t.Run(map[bool]string{false: "取消", true: "deadline"}[deadline], func(t *testing.T) {
			c, _ := imageStreamContext()
			ctx, cancel := context.WithCancel(c.Request.Context())
			if deadline {
				cancel()
				ctx, cancel = context.WithTimeout(c.Request.Context(), 30*time.Millisecond)
			}
			defer cancel()
			c.Request = c.Request.WithContext(ctx)
			reader, writer := io.Pipe()
			defer writer.Close()
			result := make(chan error, 1)
			go func() {
				_, err := handleImagesResponsesSuccess(c, &http.Response{
					Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: reader,
				}, &config.EnvConfig{MaxRequestBodySize: 1024 * 1024}, "/images/generations", true)
				result <- err
			}()
			if !deadline {
				cancel()
			}
			select {
			case err := <-result:
				if deadline {
					require.ErrorIs(t, err, context.DeadlineExceeded)
				} else {
					require.ErrorIs(t, err, context.Canceled)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("取消未解除响应体阻塞读取")
			}
		})
	}
}

func TestResponsesImageUsageDrainTimeoutIsExplicit(t *testing.T) {
	c, recorder := imageStreamContext()
	flushed := notifyImagesFlush(c, recorder)
	lifecycle := responsesLifecycle(c)
	reader, writer := io.Pipe()
	defer writer.Close()
	result := make(chan error, 1)
	go func() {
		_, err := convertResponsesImageStream(c, reader, make(http.Header), "/images/generations", true, 1024*1024)
		result <- err
	}()
	_, err := io.WriteString(writer, "event: response.output_item.done\ndata: {\"type\":\"response.output_item.done\",\"item\":{\"id\":\"img\",\"type\":\"image_generation_call\",\"result\":\"AQID\"}}\n\n")
	require.NoError(t, err)
	select {
	case <-flushed:
	case <-time.After(2 * time.Second):
		t.Fatal("图片未写出")
	}
	// 缩短同一计时器，验证生产超时回调与阻塞读取关闭，而不等待 15 秒。
	lifecycle.mu.Lock()
	require.NotNil(t, lifecycle.drainTimer)
	lifecycle.drainTimer.Reset(20 * time.Millisecond)
	lifecycle.mu.Unlock()
	select {
	case err := <-result:
		require.ErrorIs(t, err, errResponsesImageUsageDrainTimeout)
		require.Contains(t, recorder.Body.String(), "event: error")
	case <-time.After(2 * time.Second):
		t.Fatal("图片交付后上游等待无界")
	}
}
