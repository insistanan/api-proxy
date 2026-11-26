package messages

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"insistanan/api-proxy/internal/infra/config"
	"insistanan/api-proxy/internal/protocol/providers"
	"insistanan/api-proxy/internal/protocol/types"
)

func TestProcessStreamEventsDoesNotLoseBufferedError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	envCfg := &config.EnvConfig{}
	streamErr := providers.NewUpstreamStreamFailedError(false, "上游没有回复正文")
	// 两个通道均已关闭时，select 的选择顺序不确定；循环覆盖关闭信号先到的情况。
	for range 64 {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
		events := make(chan string)
		close(events)
		failures := make(chan error, 1)
		failures <- streamErr
		close(failures)

		usage, err := ProcessStreamEvents(c, c.Writer, c.Writer, events, failures, NewStreamContext(context.Background(), envCfg), envCfg, time.Now(), nil)
		require.ErrorIs(t, err, streamErr)
		require.Nil(t, usage)
		require.Contains(t, recorder.Body.String(), "event: error")
		require.Contains(t, recorder.Body.String(), "上游没有回复正文")
	}
}

func TestMessagesStreamFailurePreservesReportedUsage(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	envCfg := &config.EnvConfig{}
	ctx := NewStreamContext(context.Background(), envCfg)
	event := "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"m\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"usage\":{\"input_tokens\":100,\"output_tokens\":0}}}\n\n"
	require.NoError(t, ProcessStreamEvent(c, c.Writer, c.Writer, event, ctx, envCfg, nil))
	events := make(chan string)
	close(events)
	failures := make(chan error, 1)
	upstreamErr := fmt.Errorf("上游连接中断")
	failures <- upstreamErr
	close(failures)
	usage, err := ProcessStreamEvents(c, c.Writer, c.Writer, events, failures, ctx, envCfg, time.Now(), nil)
	require.ErrorIs(t, err, upstreamErr)
	require.NotNil(t, usage)
	require.Equal(t, 100, usage.InputTokens)
	require.Zero(t, usage.OutputTokens)
}

func TestProcessStreamErrorClosesDeliveredReasoningAndText(t *testing.T) {
	for _, blockType := range []string{"thinking", "text", "tool_use"} {
		for _, alreadyClosed := range []bool{false, true} {
			t.Run(blockType+fmt.Sprint(alreadyClosed), func(t *testing.T) {
				recorder := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(recorder)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
				envCfg := &config.EnvConfig{} // 关闭日志时也必须跟踪块生命周期。
				ctx := NewStreamContext(context.Background(), envCfg)
				start := fmt.Sprintf("event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":3,\"content_block\":{\"type\":%q}}\n\n", blockType)
				require.NoError(t, ProcessStreamEvent(c, c.Writer, c.Writer, start, ctx, envCfg, nil))
				stop := "event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":3}\n\n"
				if alreadyClosed {
					require.NoError(t, ProcessStreamEvent(c, c.Writer, c.Writer, stop, ctx, envCfg, nil))
				}
				events := make(chan string)
				close(events)
				failures := make(chan error, 1)
				upstreamErr := providers.NewUpstreamStreamFailedError(false, "模拟上游 502")
				failures <- upstreamErr
				close(failures)
				_, err := ProcessStreamEvents(c, c.Writer, c.Writer, events, failures, ctx, envCfg, time.Now(), nil)
				require.ErrorIs(t, err, upstreamErr)
				output := recorder.Body.String()
				if alreadyClosed || blockType != "tool_use" {
					require.Equal(t, 1, strings.Count(output, "event: content_block_stop"))
					require.Less(t, strings.Index(output, "event: content_block_stop"), strings.Index(output, "event: error"))
				} else {
					require.NotContains(t, output, "event: content_block_stop", "错误流不能将未完成的工具调用定稿")
				}
				require.NotContains(t, output, "event: message_delta")
				require.NotContains(t, output, "event: message_stop", "仍须保留失败语义，不能伪造成功")
			})
		}
	}
}

func TestProcessStreamEventsClosedErrorChannelPreservesEvents(t *testing.T) {
	gin.SetMode(gin.TestMode)
	envCfg := &config.EnvConfig{}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	events := make(chan string, 1)
	events <- "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"正常回复\"}}\n\n"
	close(events)
	failures := make(chan error)
	close(failures)

	_, err := ProcessStreamEvents(c, c.Writer, c.Writer, events, failures, NewStreamContext(context.Background(), envCfg), envCfg, time.Now(), nil)
	require.NoError(t, err)
	require.Contains(t, recorder.Body.String(), "正常回复")
}

func TestProcessStreamEventsCanCancelAfterEventsClose(t *testing.T) {
	gin.SetMode(gin.TestMode)
	envCfg := &config.EnvConfig{}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	requestCtx, cancel := context.WithCancel(context.Background())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil).WithContext(requestCtx)
	events := make(chan string)
	close(events)
	failures := make(chan error)
	defer close(failures)
	cancel()
	streamCtx := NewStreamContext(context.Background(), envCfg)

	_, err := ProcessStreamEvents(c, c.Writer, c.Writer, events, failures, streamCtx, envCfg, time.Now(), nil)
	require.ErrorIs(t, err, context.Canceled)
	require.True(t, streamCtx.ClientGone)
}

type mockPrimeStreamProvider struct {
	events  []string
	errChan chan error
	initErr error
}

func (m *mockPrimeStreamProvider) ConvertToProviderRequest(c *gin.Context, upstream *config.UpstreamConfig, apiKey string) (*http.Request, []byte, error) {
	return nil, nil, nil
}

func (m *mockPrimeStreamProvider) BuildRequest(input providers.RequestInput, upstream *config.UpstreamConfig, apiKey string) (*providers.RequestOutcome, error) {
	return nil, nil
}

func (m *mockPrimeStreamProvider) ConvertToClaudeResponse(providerResp *types.ProviderResponse) (*types.ClaudeResponse, error) {
	return nil, nil
}

func (m *mockPrimeStreamProvider) HandleStreamResponseCtx(ctx context.Context, body io.ReadCloser) (<-chan string, <-chan error, error) {
	if m.initErr != nil {
		return nil, nil, m.initErr
	}
	eventChan := make(chan string, len(m.events))
	for _, e := range m.events {
		eventChan <- e
	}
	close(eventChan)
	return eventChan, m.errChan, nil
}

func TestHandleStreamResponse_PrimeThenCommit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	envCfg := &config.EnvConfig{}
	upstream := &config.UpstreamConfig{Name: "test-upstream"}
	requestBody := []byte(`{"model":"claude-3-5-sonnet","messages":[{"role":"user","content":"hello"}]}`)

	t.Run("non-event-stream response fails without writing headers", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(requestBody))

		resp := &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"bad gateway"}}`)),
		}

		provider := &mockPrimeStreamProvider{}
		_, err := HandleStreamResponse(c, resp, provider, envCfg, time.Now(), upstream, requestBody, "claude-3-5-sonnet")
		if err == nil {
			t.Fatal("expected error for non-event-stream response")
		}
		if c.Writer.Written() {
			t.Fatal("c.Writer.Written() should be false when non-event-stream fails")
		}
	})

	t.Run("empty stream fails without writing headers", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(requestBody))

		resp := &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       io.NopCloser(strings.NewReader("")),
		}

		errChan := make(chan error, 1)
		close(errChan)
		provider := &mockPrimeStreamProvider{
			events:  nil,
			errChan: errChan,
		}

		_, err := HandleStreamResponse(c, resp, provider, envCfg, time.Now(), upstream, requestBody, "claude-3-5-sonnet")
		if err == nil {
			t.Fatal("expected error for empty stream")
		}
		if !strings.Contains(err.Error(), "closed without sending any events") {
			t.Fatalf("unexpected error message: %v", err)
		}
		if c.Writer.Written() {
			t.Fatal("c.Writer.Written() should be false when empty stream is primed")
		}
	})

	t.Run("initial error event fails without writing headers", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(requestBody))

		resp := &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       io.NopCloser(strings.NewReader("")),
		}

		errChan := make(chan error, 1)
		close(errChan)
		provider := &mockPrimeStreamProvider{
			events: []string{
				"event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"overloaded_error\",\"message\":\"Server Overloaded\"}}\n\n",
			},
			errChan: errChan,
		}

		_, err := HandleStreamResponse(c, resp, provider, envCfg, time.Now(), upstream, requestBody, "claude-3-5-sonnet")
		if err == nil {
			t.Fatal("expected error for initial error event")
		}
		if !strings.Contains(err.Error(), "Server Overloaded") {
			t.Fatalf("error should contain Server Overloaded, got: %v", err)
		}
		if c.Writer.Written() {
			t.Fatal("c.Writer.Written() should be false when error event is primed (allows failover)")
		}
	})

	t.Run("valid stream primes and commits successfully", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(requestBody))

		resp := &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       io.NopCloser(strings.NewReader("")),
		}

		errChan := make(chan error, 1)
		close(errChan)
		provider := &mockPrimeStreamProvider{
			events: []string{
				"event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_prime_1\",\"model\":\"claude-3-5-sonnet\",\"usage\":{\"input_tokens\":20,\"output_tokens\":1}}}\n\n",
				"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"Hello\"}}\n\n",
				"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\" world\"}}\n\n",
				"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n",
				"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":5}}\n\n",
				"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n",
			},
			errChan: errChan,
		}

		usage, err := HandleStreamResponse(c, resp, provider, envCfg, time.Now(), upstream, requestBody, "claude-3-5-sonnet")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !c.Writer.Written() {
			t.Fatal("c.Writer.Written() should be true after valid stream commits")
		}
		if usage == nil {
			t.Fatal("expected non-nil usage")
		}
	})
}

// TestPatchTokensInEventPreservesCache 测试 usage 修补保留缓存字段
func TestPatchTokensInEventPreservesCache(t *testing.T) {
	extractCacheRead := func(t *testing.T, event string) float64 {
		t.Helper()
		for _, line := range strings.Split(event, "\n") {
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			var data map[string]interface{}
			if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &data); err != nil {
				continue
			}
			if usage, ok := data["usage"].(map[string]interface{}); ok {
				if v, ok := usage["cache_read_input_tokens"].(float64); ok {
					return v
				}
			}
		}
		return 0
	}

	t.Run("should not expose inferred cache_read when not present", func(t *testing.T) {
		event := "event: message_delta\ndata: {\"type\":\"message_delta\",\"usage\":{\"input_tokens\":20000,\"output_tokens\":100}}\n\n"
		patched := PatchTokensInEvent(event, 20000, 100, false)
		got := extractCacheRead(t, patched)
		if got != 0 {
			t.Errorf("expected cache_read_input_tokens=0, got %v", got)
		}
	})

	t.Run("should not overwrite existing cache_read", func(t *testing.T) {
		event := "event: message_delta\ndata: {\"type\":\"message_delta\",\"usage\":{\"input_tokens\":20000,\"output_tokens\":100,\"cache_read_input_tokens\":50000}}\n\n"
		patched := PatchTokensInEvent(event, 20000, 100, false)
		got := extractCacheRead(t, patched)
		if got != 50000 {
			t.Errorf("expected cache_read_input_tokens=50000 (unchanged), got %v", got)
		}
	})

	t.Run("should not invent cache usage", func(t *testing.T) {
		event := "event: message_delta\ndata: {\"type\":\"message_delta\",\"usage\":{\"input_tokens\":20000,\"output_tokens\":100}}\n\n"
		patched := PatchTokensInEvent(event, 20000, 100, false)
		got := extractCacheRead(t, patched)
		if got != 0 {
			t.Errorf("expected cache_read_input_tokens=0, got %v", got)
		}
	})

	t.Run("should not overwrite explicit zero from upstream", func(t *testing.T) {
		// 上游显式返回 cache_read_input_tokens: 0 表示"明确无缓存"，不应被推断值覆盖
		event := "event: message_delta\ndata: {\"type\":\"message_delta\",\"usage\":{\"input_tokens\":20000,\"output_tokens\":100,\"cache_read_input_tokens\":0}}\n\n"
		patched := PatchTokensInEvent(event, 20000, 100, false)
		got := extractCacheRead(t, patched)
		if got != 0 {
			t.Errorf("expected cache_read_input_tokens=0 (explicit zero preserved), got %v", got)
		}
	})
}
