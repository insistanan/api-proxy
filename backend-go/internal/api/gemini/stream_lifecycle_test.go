package gemini

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/insistanan/api-proxy/internal/infra/config"
	"github.com/stretchr/testify/require"
)

type geminiResetBody struct {
	*strings.Reader
	err error
}

func (b *geminiResetBody) Read(p []byte) (int, error) {
	n, err := b.Reader.Read(p)
	if err == io.EOF {
		return n, b.err
	}
	return n, err
}
func (b *geminiResetBody) Close() error { return nil }

func TestGeminiEntryStreamLifecycle(t *testing.T) {
	resetErr := errors.New("read tcp: wsarecv: An existing connection was forcibly closed by the remote host.")
	for _, protocol := range []struct{ name, prefix, terminal string }{
		{"gemini", "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"hello\"}]}}]}\n\n", `data: {"candidates":[{"finishReason":"STOP"}]}`},
		{"claude", "data: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"hello\"}}\n\n", `data: {"type":"message_stop"}`},
		{"openai", "data: {\"choices\":[{\"delta\":{\"content\":\"hello\"}}]}\n\n", `data: {"choices":[{"finish_reason":"stop"}]}`},
	} {
		for _, readErr := range []error{io.EOF, resetErr} {
			for _, complete := range []bool{false, true} {
				t.Run(protocol.name+"/"+readErr.Error()+"/"+map[bool]string{false: "截断", true: "完成"}[complete], func(t *testing.T) {
					recorder := httptest.NewRecorder()
					c, _ := gin.CreateTestContext(recorder)
					c.Request = httptest.NewRequest(http.MethodPost, "/v1beta/models/m:streamGenerateContent", nil)
					sse := protocol.prefix
					if complete {
						sse += protocol.terminal
					}
					_, err := handleStreamSuccess(c, &http.Response{StatusCode: 200, Header: make(http.Header), Body: &geminiResetBody{Reader: strings.NewReader(sse), err: readErr}}, protocol.name, &config.EnvConfig{}, time.Now(), "m")
					if complete {
						require.NoError(t, err)
						require.Equal(t, 1, strings.Count(recorder.Body.String(), `"finishReason":"STOP"`))
						require.True(t, strings.HasSuffix(recorder.Body.String(), "\n\n"))
					} else {
						if readErr == io.EOF {
							require.ErrorIs(t, err, io.ErrUnexpectedEOF)
						} else {
							require.ErrorIs(t, err, readErr)
						}
						require.NotContains(t, recorder.Body.String(), `"finishReason"`)
					}
				})
			}
		}
	}
}

func TestGeminiEntryLateUsageAndInvalidPayload(t *testing.T) {
	for _, tc := range []struct{ upstream, sse string }{
		{"gemini", "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"hello\"}]},\"finishReason\":\"STOP\"}]}\n\ndata: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"late\"}]}}],\"usageMetadata\":{\"promptTokenCount\":10,\"candidatesTokenCount\":8}}"},
		{"openai", "data: {\"choices\":[{\"delta\":{\"content\":\"hello\"},\"finish_reason\":\"stop\"}]}\n\ndata: {\"choices\":[{\"delta\":{\"content\":\"late\"}}],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":8}}\n\ndata: [DONE]"},
	} {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1beta/models/m:streamGenerateContent", nil)
		usage, err := handleStreamSuccess(c, &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(tc.sse))}, tc.upstream, &config.EnvConfig{}, time.Now(), "m")
		require.NoError(t, err)
		require.Equal(t, 8, usage.OutputTokens)
		require.Contains(t, recorder.Body.String(), `"candidatesTokenCount":8`)
		require.NotContains(t, recorder.Body.String(), "late")
		require.Equal(t, 1, strings.Count(recorder.Body.String(), `"finishReason":"STOP"`))
	}
	for _, upstream := range []string{"gemini", "claude", "openai"} {
		for _, payload := range []string{`{broken`, `null`, `{"error":{"message":"boom"}}`} {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1beta/models/m:streamGenerateContent", nil)
			_, err := handleStreamSuccess(c, &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("data: " + payload))}, upstream, &config.EnvConfig{}, time.Now(), "m")
			require.Error(t, err)
			require.NotContains(t, recorder.Body.String(), `"finishReason"`)
		}
	}
}

type geminiBlockingBody struct {
	started   chan struct{}
	closed    chan struct{}
	startOnce sync.Once
	closeOnce sync.Once
}

func (b *geminiBlockingBody) Read([]byte) (int, error) {
	b.startOnce.Do(func() { close(b.started) })
	<-b.closed
	return 0, io.ErrClosedPipe
}
func (b *geminiBlockingBody) Close() error { b.closeOnce.Do(func() { close(b.closed) }); return nil }

func TestGeminiEntryCancellationUnblocksRead(t *testing.T) {
	for _, encoding := range []string{"", "gzip"} {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1beta/models/m:streamGenerateContent", nil).WithContext(ctx)
		body := &geminiBlockingBody{started: make(chan struct{}), closed: make(chan struct{})}
		header := make(http.Header)
		if encoding != "" {
			header.Set("Content-Encoding", encoding)
		}
		done := make(chan error, 1)
		go func() {
			_, err := handleStreamSuccess(c, &http.Response{StatusCode: 200, Header: header, Body: body}, "gemini", &config.EnvConfig{}, time.Now(), "m")
			done <- err
		}()
		select {
		case <-body.started:
		case <-time.After(time.Second):
			t.Fatal("读取未开始")
		}
		cancel()
		select {
		case err := <-done:
			require.ErrorIs(t, err, context.Canceled)
		case <-time.After(time.Second):
			t.Fatal("取消未解除读取阻塞")
		}
	}
}

type geminiFailWriter struct {
	gin.ResponseWriter
	err error
}

func (w *geminiFailWriter) Write([]byte) (int, error) { return 0, w.err }

func TestGeminiEntryPropagatesWriteFailure(t *testing.T) {
	writeErr := errors.New("downstream write failed")
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1beta/models/m:streamGenerateContent", nil)
	c.Writer = &geminiFailWriter{ResponseWriter: c.Writer, err: writeErr}
	_, err := handleStreamSuccess(c, &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`data: {"candidates":[{"content":{"parts":[{"text":"hello"}]}}]}`))}, "gemini", &config.EnvConfig{}, time.Now(), "m")
	require.ErrorIs(t, err, writeErr)
}
