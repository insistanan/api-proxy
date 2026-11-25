package streams

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestForwardUpstreamResponseBody(t *testing.T) {
	gin.SetMode(gin.TestMode)

	newCtx := func() (*gin.Context, *httptest.ResponseRecorder) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil)
		return c, w
	}

	t.Run("forwards body and applies default content type", func(t *testing.T) {
		c, w := newCtx()
		resp := &http.Response{
			StatusCode: 200,
			Header:     http.Header{},
			Body:       io.NopCloser(strings.NewReader("hello")),
		}
		if err := ForwardUpstreamResponseBody(c, resp, "application/json", false); err != nil {
			t.Fatalf("err = %v", err)
		}
		if w.Body.String() != "hello" {
			t.Fatalf("body = %q", w.Body.String())
		}
		if got := w.Header().Get("Content-Type"); got != "application/json" {
			t.Fatalf("缺省 Content-Type 未生效, got %q", got)
		}
		if w.Code != 200 {
			t.Fatalf("status = %d", w.Code)
		}
	})

	t.Run("preserves upstream content type over default", func(t *testing.T) {
		c, w := newCtx()
		resp := &http.Response{
			StatusCode: 200,
			Header:     http.Header{},
			Body:       io.NopCloser(strings.NewReader("x")),
		}
		resp.Header.Set("Content-Type", "image/png")
		if err := ForwardUpstreamResponseBody(c, resp, "application/json", false); err != nil {
			t.Fatalf("err = %v", err)
		}
		if got := w.Header().Get("Content-Type"); got != "image/png" {
			t.Fatalf("上游 Content-Type 应优先, got %q", got)
		}
	})

	t.Run("sse response forces streaming headers", func(t *testing.T) {
		c, w := newCtx()
		resp := &http.Response{
			StatusCode: 200,
			Header:     http.Header{},
			Body:       io.NopCloser(strings.NewReader("data: {}\n\n")),
		}
		resp.Header.Set("Content-Type", "text/event-stream")
		if err := ForwardUpstreamResponseBody(c, resp, "", false); err != nil {
			t.Fatalf("err = %v", err)
		}
		if got := w.Header().Get("Cache-Control"); got != "no-cache" {
			t.Fatalf("SSE 响应应强制 no-cache, got %q", got)
		}
		if got := w.Header().Get("X-Accel-Buffering"); got != "no" {
			t.Fatalf("SSE 响应应强制 X-Accel-Buffering: no, got %q", got)
		}
	})

	t.Run("propagates read error", func(t *testing.T) {
		c, _ := newCtx()
		resp := &http.Response{
			StatusCode: 200,
			Header:     http.Header{},
			Body: io.NopCloser(io.MultiReader(
				strings.NewReader("partial"),
				errReader{},
			)),
		}
		if err := ForwardUpstreamResponseBody(c, resp, "", false); err == nil || err.Error() != "read failed" {
			t.Fatalf("读错误应向上传播, got %v", err)
		}
	})

	t.Run("missing response is an explicit error", func(t *testing.T) {
		if err := ForwardUpstreamResponseBody(nil, nil, "", false); err == nil {
			t.Fatal("缺少响应体不能被当作转发成功")
		}
	})
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("read failed") }
