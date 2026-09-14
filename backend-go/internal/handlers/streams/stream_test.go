package streams

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/BenedictKing/api-proxy/internal/config"
	"github.com/BenedictKing/api-proxy/internal/providers"
	"github.com/BenedictKing/api-proxy/internal/types"
	"github.com/BenedictKing/api-proxy/internal/utils"
	"github.com/gin-gonic/gin"
)

func TestPatchUsageFieldsWithLog_NilInputTokens(t *testing.T) {
	tests := []struct {
		name           string
		usage          map[string]interface{}
		estimatedInput int
		hasCacheTokens bool
		wantPatched    bool
		wantValue      int
	}{
		{
			name:           "large estimate without cache should stay unset",
			usage:          map[string]interface{}{"input_tokens": nil, "output_tokens": float64(100)},
			estimatedInput: 10920,
			hasCacheTokens: false,
			wantPatched:    false,
			wantValue:      0,
		},
		{
			name:           "large estimate with cache should stay unset",
			usage:          map[string]interface{}{"input_tokens": nil, "output_tokens": float64(100)},
			estimatedInput: 10920,
			hasCacheTokens: true,
			wantPatched:    false,
			wantValue:      0,
		},
		{
			name:           "valid input_tokens - should not patch",
			usage:          map[string]interface{}{"input_tokens": float64(5000), "output_tokens": float64(100)},
			estimatedInput: 10920,
			hasCacheTokens: true,
			wantPatched:    false,
			wantValue:      5000,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			patchUsageFieldsWithLog(tt.usage, tt.estimatedInput, 100, tt.hasCacheTokens, false, "test", false)

			if tt.wantPatched {
				if v, ok := tt.usage["input_tokens"].(int); !ok || v != tt.wantValue {
					t.Errorf("expected input_tokens=%d, got %v", tt.wantValue, tt.usage["input_tokens"])
				}
			} else if tt.usage["input_tokens"] == nil {
				// nil case - expected to remain nil
			} else if v, ok := tt.usage["input_tokens"].(float64); ok && int(v) != tt.wantValue {
				t.Errorf("expected input_tokens=%d, got %v", tt.wantValue, tt.usage["input_tokens"])
			}
		})
	}
}

func TestPatchMessageStartInputTokensIfNeeded(t *testing.T) {
	requestBody := []byte(`{"messages":[{"role":"user","content":[{"type":"text","text":"hello world hello world hello world"}]}]}`)
	estimated := utils.EstimateRequestTokens(requestBody)
	if estimated <= 0 {
		t.Fatalf("expected estimated input tokens > 0, got %d", estimated)
	}

	extractInputTokens := func(t *testing.T, event string) float64 {
		t.Helper()
		for _, line := range strings.Split(event, "\n") {
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			var data map[string]interface{}
			if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &data); err != nil {
				t.Fatalf("failed to unmarshal data: %v", err)
			}
			msg, ok := data["message"].(map[string]interface{})
			if !ok {
				t.Fatalf("missing message field")
			}
			usage, ok := msg["usage"].(map[string]interface{})
			if !ok {
				t.Fatalf("missing message.usage field")
			}
			v, ok := usage["input_tokens"].(float64)
			if !ok {
				t.Fatalf("missing input_tokens field")
			}
			return v
		}
		t.Fatalf("no data line found")
		return 0
	}

	t.Run("input_tokens=0 should remain unchanged in message_start", func(t *testing.T) {
		event := "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":0,\"output_tokens\":0}}}\n\n"
		hasUsage, needInputPatch, _, usageData := CheckEventUsageStatus(event, false)
		if !hasUsage {
			t.Fatalf("expected hasUsage=true")
		}
		if !needInputPatch {
			t.Fatalf("expected needInputPatch=true")
		}

		patched := PatchMessageStartInputTokensIfNeeded(event, requestBody, needInputPatch, usageData, true, false, false)
		got := extractInputTokens(t, patched)
		if got != 0 {
			t.Fatalf("expected input_tokens=0, got %v", got)
		}
	})

	t.Run("input_tokens<10 should remain unchanged in message_start", func(t *testing.T) {
		event := "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":5,\"output_tokens\":0}}}\n\n"
		hasUsage, needInputPatch, _, usageData := CheckEventUsageStatus(event, false)
		if !hasUsage {
			t.Fatalf("expected hasUsage=true")
		}
		if needInputPatch {
			t.Fatalf("expected needInputPatch=false")
		}

		patched := PatchMessageStartInputTokensIfNeeded(event, requestBody, needInputPatch, usageData, true, false, false)
		got := extractInputTokens(t, patched)
		if got != 5 {
			t.Fatalf("expected input_tokens=5, got %v", got)
		}
	})

	t.Run("cache hit should not patch input_tokens", func(t *testing.T) {
		event := "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":0,\"output_tokens\":0,\"cache_read_input_tokens\":100}}}\n\n"
		hasUsage, needInputPatch, _, usageData := CheckEventUsageStatus(event, false)
		if !hasUsage {
			t.Fatalf("expected hasUsage=true")
		}
		if needInputPatch {
			t.Fatalf("expected needInputPatch=false")
		}

		patched := PatchMessageStartInputTokensIfNeeded(event, requestBody, needInputPatch, usageData, false, false, false)
		got := extractInputTokens(t, patched)
		if got != 0 {
			t.Fatalf("expected input_tokens=0, got %v", got)
		}
	})

	t.Run("valid input_tokens should not patch", func(t *testing.T) {
		event := "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":50,\"output_tokens\":0}}}\n\n"
		hasUsage, needInputPatch, _, usageData := CheckEventUsageStatus(event, false)
		if !hasUsage {
			t.Fatalf("expected hasUsage=true")
		}
		if needInputPatch {
			t.Fatalf("expected needInputPatch=false")
		}

		patched := PatchMessageStartInputTokensIfNeeded(event, requestBody, needInputPatch, usageData, false, false, false)
		got := extractInputTokens(t, patched)
		if got != 50 {
			t.Fatalf("expected input_tokens=50, got %v", got)
		}
	})
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

func (m *mockPrimeStreamProvider) HandleStreamResponse(body io.ReadCloser) (<-chan string, <-chan error, error) {
	return m.HandleStreamResponseCtx(context.Background(), body)
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

// TestInferImplicitCacheRead 测试隐式缓存推断逻辑
func TestInferImplicitCacheRead(t *testing.T) {
	tests := []struct {
		name                    string
		messageStartInputTokens int
		collectedInputTokens    int
		existingCacheRead       int
		wantCacheRead           int
	}{
		{
			name:                    "large diff ratio (>10%) should infer cache",
			messageStartInputTokens: 100000,
			collectedInputTokens:    20000,
			existingCacheRead:       0,
			wantCacheRead:           80000,
		},
		{
			name:                    "large diff value (>10k) should infer cache",
			messageStartInputTokens: 50000,
			collectedInputTokens:    38000,
			existingCacheRead:       0,
			wantCacheRead:           12000,
		},
		{
			name:                    "small diff should not infer cache",
			messageStartInputTokens: 10000,
			collectedInputTokens:    9500,
			existingCacheRead:       0,
			wantCacheRead:           0,
		},
		{
			name:                    "existing cache_read should not be overwritten",
			messageStartInputTokens: 100000,
			collectedInputTokens:    20000,
			existingCacheRead:       50000,
			wantCacheRead:           50000,
		},
		{
			name:                    "zero message_start should not infer",
			messageStartInputTokens: 0,
			collectedInputTokens:    20000,
			existingCacheRead:       0,
			wantCacheRead:           0,
		},
		{
			name:                    "zero collected should not infer",
			messageStartInputTokens: 100000,
			collectedInputTokens:    0,
			existingCacheRead:       0,
			wantCacheRead:           0,
		},
		{
			name:                    "negative diff should not infer",
			messageStartInputTokens: 10000,
			collectedInputTokens:    15000,
			existingCacheRead:       0,
			wantCacheRead:           0,
		},
		{
			name:                    "exactly 10% diff should not infer",
			messageStartInputTokens: 10000,
			collectedInputTokens:    9000,
			existingCacheRead:       0,
			wantCacheRead:           0,
		},
		{
			name:                    "just over 10% diff should infer",
			messageStartInputTokens: 10000,
			collectedInputTokens:    8900,
			existingCacheRead:       0,
			wantCacheRead:           1100,
		},
		{
			name:                    "10k diff but ratio <10% should infer (diff > 10k takes precedence)",
			messageStartInputTokens: 150000,
			collectedInputTokens:    139000,
			existingCacheRead:       0,
			wantCacheRead:           11000,
		},
		{
			name:                    "diff exactly 10k with ratio <10% should not infer",
			messageStartInputTokens: 150000,
			collectedInputTokens:    140000,
			existingCacheRead:       0,
			wantCacheRead:           0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := &Context{
				MessageStartInputTokens: tt.messageStartInputTokens,
				CollectedUsage: CollectedUsageData{
					InputTokens:          tt.collectedInputTokens,
					CacheReadInputTokens: tt.existingCacheRead,
				},
			}

			inferImplicitCacheRead(ctx, false)

			if ctx.CollectedUsage.CacheReadInputTokens != tt.wantCacheRead {
				t.Errorf("CacheReadInputTokens = %d, want %d",
					ctx.CollectedUsage.CacheReadInputTokens, tt.wantCacheRead)
			}
		})
	}
}

// TestPatchTokensInEventWithCache 测试带缓存推断的事件修补
func TestPatchTokensInEventWithCache(t *testing.T) {
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
		patched := PatchTokensInEventWithCache(event, 20000, 100, 80000, true, false, false)
		got := extractCacheRead(t, patched)
		if got != 0 {
			t.Errorf("expected cache_read_input_tokens=0, got %v", got)
		}
	})

	t.Run("should not overwrite existing cache_read", func(t *testing.T) {
		event := "event: message_delta\ndata: {\"type\":\"message_delta\",\"usage\":{\"input_tokens\":20000,\"output_tokens\":100,\"cache_read_input_tokens\":50000}}\n\n"
		patched := PatchTokensInEventWithCache(event, 20000, 100, 80000, true, false, false)
		got := extractCacheRead(t, patched)
		if got != 50000 {
			t.Errorf("expected cache_read_input_tokens=50000 (unchanged), got %v", got)
		}
	})

	t.Run("should not write when inferredCacheRead is 0", func(t *testing.T) {
		event := "event: message_delta\ndata: {\"type\":\"message_delta\",\"usage\":{\"input_tokens\":20000,\"output_tokens\":100}}\n\n"
		patched := PatchTokensInEventWithCache(event, 20000, 100, 0, false, false, false)
		got := extractCacheRead(t, patched)
		if got != 0 {
			t.Errorf("expected cache_read_input_tokens=0, got %v", got)
		}
	})

	t.Run("should not overwrite explicit zero from upstream", func(t *testing.T) {
		// 上游显式返回 cache_read_input_tokens: 0 表示"明确无缓存"，不应被推断值覆盖
		event := "event: message_delta\ndata: {\"type\":\"message_delta\",\"usage\":{\"input_tokens\":20000,\"output_tokens\":100,\"cache_read_input_tokens\":0}}\n\n"
		patched := PatchTokensInEventWithCache(event, 20000, 100, 80000, true, false, false)
		got := extractCacheRead(t, patched)
		if got != 0 {
			t.Errorf("expected cache_read_input_tokens=0 (explicit zero preserved), got %v", got)
		}
	})
}
