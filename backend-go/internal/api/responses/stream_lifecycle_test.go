package responses

import (
	"context"
	"errors"
	"fmt"
	"github.com/insistanan/api-proxy/internal/protocol/providers"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/insistanan/api-proxy/internal/dialogue/session"
	"github.com/insistanan/api-proxy/internal/infra/config"
	"github.com/insistanan/api-proxy/internal/protocol/converters"
	"github.com/insistanan/api-proxy/internal/protocol/types"
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
	require.NoError(t, beginResponsesTurn(c, manager, requestJSON, ""))
	defer closeResponsesTurn(c)
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
					require.Equal(t, 1, strings.Count(recorder.Body.String(), "event: error\n"))
					require.NotContains(t, recorder.Body.String(), `"type":"response.completed"`)
					if strings.Contains(tc.tail, "boom") {
						require.Contains(t, recorder.Body.String(), "boom")
					}
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
	cancel       context.CancelFunc
	cancelOnType string
	fail         error
	failOnType   string
}

func (w *responsesLifecycleWriter) Write(data []byte) (int, error) {
	if w.fail != nil && (w.failOnType == "" || strings.Contains(string(data), `"type":"`+w.failOnType+`"`)) {
		return 0, w.fail
	}
	n, err := w.ResponseWriter.Write(data)
	cancelOnType := w.cancelOnType
	if cancelOnType == "" {
		cancelOnType = "response.output_item.done"
	}
	if strings.Contains(string(data), `"type":"`+cancelOnType+`"`) {
		w.cancel()
	}
	return n, err
}

func TestResponsesStream_TerminalWriteFailurePreservesReportedUsage(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	writeErr := errors.New("broken pipe")
	c.Writer = &responsesLifecycleWriter{ResponseWriter: c.Writer, fail: writeErr, failOnType: "response.completed"}
	sse := "data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\n" +
		"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"r1\",\"output\":[],\"usage\":{\"input_tokens\":100,\"output_tokens\":15,\"input_tokens_details\":{\"cached_tokens\":40}}}}\n\n"
	usage, err := runResponsesLifecycleStream(t, c, converters.ResponsesUpstreamResponses, io.NopCloser(strings.NewReader(sse)))
	require.ErrorIs(t, err, writeErr)
	require.NotNil(t, usage)
	require.False(t, usage.Estimated)
	require.Equal(t, 60, usage.InputTokens)
	require.Equal(t, 40, usage.CacheReadInputTokens)
	require.Equal(t, 15, usage.OutputTokens)
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
		sse := "data: {\"type\":\"response.created\",\"response\":{\"id\":\"tool-response\"}}\n\ndata: {\"type\":\"response.output_item.done\",\"item\":{\"id\":\"fc1\",\"type\":\"function_call\",\"status\":\"" + status + "\",\"name\":\"tool\",\"call_id\":\"c1\",\"arguments\":\"{}\"}}\n\n"
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

func TestResponsesCancellationAtDeliveryBoundary(t *testing.T) {
	for _, eventType := range []string{"response.output_text.delta", "response.completed"} {
		t.Run(eventType, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil).WithContext(ctx)
			c.Writer = &responsesLifecycleWriter{ResponseWriter: c.Writer, cancel: cancel, cancelOnType: eventType}
			sse := "data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\n" +
				"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"r1\",\"output\":[],\"usage\":{\"input_tokens\":10,\"output_tokens\":8}}}\n\n"
			usage, err := runResponsesLifecycleStream(t, c, converters.ResponsesUpstreamResponses, io.NopCloser(strings.NewReader(sse)))
			if eventType == "response.completed" {
				require.NoError(t, err)
				require.Equal(t, 8, usage.OutputTokens)
			} else {
				require.ErrorIs(t, err, context.Canceled)
			}
		})
	}
}

func TestResponsesToolCancellationOnlyPersistsDeliveredCompletePrefix(t *testing.T) {
	for _, blockedPrefix := range []bool{false, true} {
		t.Run(fmt.Sprint(blockedPrefix), func(t *testing.T) {
			manager, err := session.NewPersistentSessionManager(filepath.Join(t.TempDir(), "history.db"), time.Hour, 0, 0)
			require.NoError(t, err)
			defer manager.Stop()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil).WithContext(ctx)
			request := []byte(`{"model":"m","input":"hello","stream":true}`)
			require.NoError(t, beginResponsesTurn(c, manager, request, ""))
			defer closeResponsesTurn(c)
			events := []string{`{"type":"response.created","response":{"id":"tool-prefix"}}`}
			if blockedPrefix {
				events = append(events, `{"type":"response.output_item.added","output_index":0,"item":{"type":"message","id":"unfinished","content":[]}}`,
					`{"type":"response.output_item.done","output_index":1,"item":{"type":"function_call","call_id":"complete","name":"exec","arguments":"{}"}}`)
				c.Writer = &responsesLifecycleWriter{ResponseWriter: c.Writer, cancel: cancel}
			} else {
				events = append(events, `{"type":"response.output_item.done","output_index":0,"item":{"type":"function_call","call_id":"complete","name":"exec","arguments":"{}"}}`,
					`{"type":"response.output_item.added","output_index":1,"item":{"type":"function_call","call_id":"half","name":"exec","arguments":""}}`,
					`{"type":"response.function_call_arguments.delta","output_index":1,"delta":"{"}`)
				c.Writer = &responsesLifecycleWriter{ResponseWriter: c.Writer, cancel: cancel, cancelOnType: "response.function_call_arguments.delta"}
			}
			body := "data: " + strings.Join(events, "\n\ndata: ") + "\n\n"
			_, err = handleStreamSuccess(c, &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, "responses", &config.EnvConfig{}, manager, time.Now(), &types.ResponsesRequest{Model: "m", Input: "hello", Stream: true}, request, request, nil, "")
			if blockedPrefix {
				require.ErrorContains(t, err, "未完成条目")
				_, known, readErr := manager.ResponseOwner(context.Background(), "tool-prefix")
				require.NoError(t, readErr)
				require.False(t, known)
			} else {
				require.NoError(t, err)
				history, err := manager.GetOrCreateSessionForConversation(context.Background(), "tool-prefix", "")
				require.NoError(t, err)
				require.Len(t, history.Messages, 2)
				require.Equal(t, "complete", history.Messages[1].CallID)
			}
		})
	}
}
func TestResponsesCommitFailurePreservesReportedUsage(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprint(stream), func(t *testing.T) {
			manager, err := session.NewPersistentSessionManager(filepath.Join(t.TempDir(), "history.db"), time.Hour, 0, 0)
			require.NoError(t, err)
			defer manager.Stop()
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			request := []byte(`{"model":"m","input":"hello"}`)
			require.NoError(t, beginResponsesTurn(c, manager, request, ""))
			defer closeResponsesTurn(c)
			manager.Stop()
			body := `{"id":"save-fails","status":"completed","model":"m","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"answer"}]}],"usage":{"input_tokens":11,"output_tokens":7}}`
			if stream {
				body = "data: {\"type\":\"response.completed\",\"response\":" + body + "}\n\n"
			}
			usage, err := handleSuccess(c, &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, &providers.ResponsesProvider{SessionManager: manager}, "responses", &config.EnvConfig{}, manager, time.Now(), &types.ResponsesRequest{Model: "m", Input: "hello", Stream: stream}, request, nil, "")
			var commitErr *sessionCommitError
			require.ErrorAs(t, err, &commitErr)
			require.NotNil(t, usage)
			require.Equal(t, 11, usage.InputTokens)
			require.Equal(t, 7, usage.OutputTokens)
		})
	}
}
