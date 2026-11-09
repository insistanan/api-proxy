package responses

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/insistanan/api-proxy/internal/config"
	"github.com/insistanan/api-proxy/internal/converters"
	"github.com/insistanan/api-proxy/internal/session"
	"github.com/insistanan/api-proxy/internal/types"
	"github.com/stretchr/testify/require"
)

type responsesResetBody struct {
	*strings.Reader
	err error
}

func (b *responsesResetBody) Read(p []byte) (int, error) {
	n, err := b.Reader.Read(p)
	if err == io.EOF {
		return n, b.err
	}
	return n, err
}
func (b *responsesResetBody) Close() error { return nil }

func runResponsesLifecycleStream(t *testing.T, c *gin.Context, upstream string, body io.ReadCloser) (*types.Usage, error) {
	t.Helper()
	manager, err := session.NewPersistentSessionManager(filepath.Join(t.TempDir(), "sessions.db"), time.Hour, 100, 10000)
	require.NoError(t, err)
	t.Cleanup(manager.Stop)
	requestJSON := []byte(`{"model":"m","input":"hello"}`)
	return handleStreamSuccess(c, &http.Response{StatusCode: 200, Header: make(http.Header), Body: body}, upstream, &config.EnvConfig{}, manager, time.Now(), &types.ResponsesRequest{Model: "m", Input: "hello"}, requestJSON, requestJSON, nil, "")
}

func TestResponsesStreamLifecycle(t *testing.T) {
	resetErr := errors.New("read tcp: wsarecv: An existing connection was forcibly closed by the remote host.")
	prefix := "data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\n"
	for _, tc := range []struct {
		name, tail string
		complete   bool
	}{
		{"截断", "", false},
		{"完成后额外内容", "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"r1\",\"output\":[],\"usage\":{\"input_tokens\":10,\"output_tokens\":8}}}\n\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"late\"}\n\n", true},
		{"incomplete没有末尾换行", `data: {"type":"response.incomplete","response":{"id":"r1","status":"incomplete","output":[],"usage":{"input_tokens":10,"output_tokens":8}}}`, true},
		{"失败事件", "data: {\"type\":\"response.failed\",\"response\":{\"error\":{\"message\":\"boom\"}}}\n\n", false},
		{"完成类型中的失败状态", "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"failed\",\"error\":{\"message\":\"boom\"}}}\n\n", false},
		{"非法JSON", "data: {broken\n\n", false},
	} {
		for _, readErr := range []error{io.EOF, resetErr} {
			t.Run(tc.name+"/"+readErr.Error(), func(t *testing.T) {
				recorder := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(recorder)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
				usage, err := runResponsesLifecycleStream(t, c, converters.ResponsesUpstreamResponses, &responsesResetBody{Reader: strings.NewReader(prefix + tc.tail), err: readErr})
				if tc.complete {
					require.NoError(t, err)
					require.Equal(t, 8, usage.OutputTokens)
					require.True(t, strings.HasSuffix(recorder.Body.String(), "\n\n"))
				} else {
					require.Error(t, err)
				}
				require.NotContains(t, recorder.Body.String(), "late")
			})
		}
	}
}

func TestResponsesConvertedStreamUsageTail(t *testing.T) {
	for _, tc := range []struct{ upstream, sse string }{
		{converters.ResponsesUpstreamGemini, "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"hello\"}]},\"finishReason\":\"STOP\"}]}\n\ndata: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"late\"}]}}],\"usageMetadata\":{\"promptTokenCount\":10,\"candidatesTokenCount\":8}}"},
		{converters.ResponsesUpstreamOpenAI, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hello\"},\"finish_reason\":\"stop\"}]}\n\ndata: {\"choices\":[],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":8}}"},
	} {
		t.Run(tc.upstream, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			usage, err := runResponsesLifecycleStream(t, c, tc.upstream, io.NopCloser(strings.NewReader(tc.sse)))
			require.NoError(t, err)
			require.Equal(t, 8, usage.OutputTokens)
			require.Equal(t, 1, strings.Count(recorder.Body.String(), `"type":"response.completed"`))
			require.Contains(t, recorder.Body.String(), `"output_tokens":8`)
			require.NotContains(t, recorder.Body.String(), "late")
		})
	}
}

type responsesLifecycleWriter struct {
	gin.ResponseWriter
	cancel context.CancelFunc
	fail   error
}

func (w *responsesLifecycleWriter) Write(data []byte) (int, error) {
	if w.fail != nil {
		return 0, w.fail
	}
	n, err := w.ResponseWriter.Write(data)
	if strings.Contains(string(data), `"type":"response.output_item.done"`) {
		w.cancel()
	}
	return n, err
}

func TestResponsesToolDeliveryCancellationAndWriteFailure(t *testing.T) {
	for _, completed := range []bool{false, true} {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil).WithContext(ctx)
		c.Writer = &responsesLifecycleWriter{ResponseWriter: c.Writer, cancel: cancel}
		status := "in_progress"
		if completed {
			status = "completed"
		}
		sse := "data: {\"type\":\"response.output_item.done\",\"item\":{\"id\":\"fc1\",\"type\":\"function_call\",\"status\":\"" + status + "\",\"name\":\"tool\",\"call_id\":\"c1\",\"arguments\":\"{}\"}}\n\n"
		_, err := runResponsesLifecycleStream(t, c, converters.ResponsesUpstreamResponses, io.NopCloser(strings.NewReader(sse)))
		if completed {
			require.NoError(t, err)
		} else {
			require.ErrorIs(t, err, context.Canceled)
		}
	}
	writeErr := errors.New("downstream write failed")
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	c.Writer = &responsesLifecycleWriter{ResponseWriter: c.Writer, fail: writeErr}
	_, err := runResponsesLifecycleStream(t, c, converters.ResponsesUpstreamResponses, io.NopCloser(strings.NewReader("data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\n")))
	require.ErrorIs(t, err, writeErr)
}
