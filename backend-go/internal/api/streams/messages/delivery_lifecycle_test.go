package messages

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/insistanan/api-proxy/internal/infra/config"
	"github.com/insistanan/api-proxy/internal/protocol/providers"
	"github.com/insistanan/api-proxy/internal/protocol/types"
	"github.com/stretchr/testify/require"
)

type deliveryWriter struct {
	gin.ResponseWriter
	failOn string
	err    error
	cancel context.CancelFunc
}

func (w *deliveryWriter) Write(data []byte) (int, error) {
	if w.err != nil && strings.Contains(string(data), w.failOn) {
		return 0, w.err
	}
	n, err := w.ResponseWriter.Write(data)
	if w.cancel != nil && strings.Contains(string(data), `"type":"message_stop"`) {
		w.cancel()
	}
	return n, err
}

func TestMessagesDelivery_WriteFailureStopsEveryOutputBranch(t *testing.T) {
	for _, tc := range []struct {
		name, input, failOn string
		started             bool
	}{
		{"正文", `{"type":"content_block_delta","delta":{"type":"text_delta","text":"hello"}}`, "hello", true},
		{"合成开始", `{"type":"content_block_start","index":0,"content_block":{"type":"text"}}`, "message_start", false},
		{"估算usage", `{"type":"message_stop"}`, "message_delta", true},
		{"正常结束", `{"type":"message_stop"}`, "message_stop", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
			writeErr := errors.New("写入失败但 Context 未取消")
			c.Writer = &deliveryWriter{ResponseWriter: c.Writer, failOn: tc.failOn, err: writeErr}
			ctx := NewStreamContext(&config.EnvConfig{})
			ctx.SeenMessageStart = tc.started
			err := ProcessStreamEvent(c, c.Writer, c.Writer, "data: "+tc.input+"\n\n", ctx, &config.EnvConfig{}, nil)
			require.ErrorIs(t, err, writeErr)
			require.True(t, types.IsDownstreamWriteError(err))
			require.True(t, ctx.ClientGone)
			require.NoError(t, c.Request.Context().Err())
		})
	}
}

func TestMessagesDelivery_CancelAfterTerminalRemainsSuccessful(t *testing.T) {
	requestCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil).WithContext(requestCtx)
	c.Writer = &deliveryWriter{ResponseWriter: c.Writer, cancel: cancel}
	failures := make(chan error)
	close(failures)
	provider := &mockPrimeStreamProvider{events: []string{
		"data: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":10}}}\n\n",
		"data: {\"type\":\"message_stop\"}\n\n",
	}, errChan: failures}
	resp := &http.Response{Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(""))}
	usage, err := HandleStreamResponse(c, resp, provider, &config.EnvConfig{}, time.Now(), &config.UpstreamConfig{}, nil, "m")
	require.NoError(t, err)
	require.ErrorIs(t, requestCtx.Err(), context.Canceled)
	require.Equal(t, 10, usage.InputTokens)
}

func TestMessagesPrime_ControlFramesDoNotCommit(t *testing.T) {
	for _, tail := range []string{"", "event: error\ndata: {\"type\":\"error\",\"error\":{\"message\":\"boom\"}}\n\n"} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
		resp := &http.Response{Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(": keepalive\n\ndata: {\"type\":\"ping\"}\n\n" + tail))}
		_, err := HandleStreamResponse(c, resp, &providers.ClaudeProvider{}, &config.EnvConfig{}, time.Now(), &config.UpstreamConfig{}, nil, "m")
		require.Error(t, err)
		require.False(t, c.Writer.Written())
		if tail != "" {
			require.Contains(t, err.Error(), "boom")
		}
	}
}

func TestMessagesEvent_MidstreamErrorCannotBecomeSuccess(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	events := make(chan string, 2)
	events <- "data: {\"type\":\"error\",\"error\":{\"message\":\"boom\"}}\n\n"
	events <- "data: {\"type\":\"message_stop\"}\n\n"
	close(events)
	failures := make(chan error)
	close(failures)
	ctx := NewStreamContext(&config.EnvConfig{})
	_, err := ProcessStreamEvents(c, c.Writer, c.Writer, events, failures, ctx, &config.EnvConfig{}, time.Now(), nil)
	require.ErrorContains(t, err, "boom")
	require.Equal(t, 1, strings.Count(recorder.Body.String(), "event: error"))
	require.NotContains(t, recorder.Body.String(), "message_stop")
}

func TestMessagesEvent_ErrorNameRequiresExactSSEField(t *testing.T) {
	for _, event := range []string{
		"data: {\"type\":\"content_block_delta\",\"delta\":{\"text\":\"event: error 只是正文\"}}\n\n",
		"event: error_details\ndata: {\"type\":\"extension\",\"text\":\"extra\"}\n\n",
	} {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
		ctx := NewStreamContext(&config.EnvConfig{})
		require.NoError(t, ProcessStreamEvent(c, c.Writer, c.Writer, event, ctx, &config.EnvConfig{}, nil))
		require.Equal(t, event, recorder.Body.String())
	}
	for _, event := range []string{"event:error\ndata: {\"message\":\"boom\"}\n\n", "event: error\ndata: {\"message\":\"boom\"}\n\n"} {
		require.ErrorContains(t, streamEventError(event), "boom")
	}
}

func TestMessagesUsage_RealEntryPreservesInputAndReportedCache(t *testing.T) {
	for _, input := range []int{0, 5, 50} {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
		ctx := NewStreamContext(&config.EnvConfig{})
		event := fmt.Sprintf("data: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":%d,\"cache_read_input_tokens\":100}}}\n\n", input)
		err := ProcessStreamEvent(c, c.Writer, c.Writer, event, ctx, &config.EnvConfig{}, []byte(`{"messages":[{"role":"user","content":"hello"}]}`))
		require.NoError(t, err)
		require.Equal(t, input, ctx.UpstreamUsage.InputTokens)
		require.Equal(t, 100, ctx.UpstreamUsage.CacheReadInputTokens)
		data, ok := ParseSSEEventData(recorder.Body.String())
		require.True(t, ok)
		usage := data["message"].(map[string]interface{})["usage"].(map[string]interface{})
		require.EqualValues(t, input, usage["input_tokens"])
		require.EqualValues(t, 100, usage["cache_read_input_tokens"])
	}
}

func TestMessagesDelivery_WriteFailureClosesBlockedUpstream(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		reader, writer := io.Pipe()
		defer reader.Close()
		defer writer.Close()
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
		writeErr := errors.New("下游写入失败")
		c.Writer = &deliveryWriter{ResponseWriter: c.Writer, err: writeErr}
		go func() {
			_, _ = io.WriteString(writer, "data: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":10}}}\n\n")
		}()
		resp := &http.Response{Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: reader}
		_, err := HandleStreamResponse(c, resp, &providers.ClaudeProvider{}, &config.EnvConfig{}, time.Now(), &config.UpstreamConfig{}, nil, "m")
		require.ErrorIs(t, err, writeErr)
		synctest.Wait()
		_, err = writer.Write([]byte("late"))
		require.ErrorIs(t, err, io.ErrClosedPipe)
	})
}
