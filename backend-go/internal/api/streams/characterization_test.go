// 验证流式用量修正、缓存计数校验和上游响应体转发。
package streams

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/insistanan/api-proxy/internal/protocol/tokenusage"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/insistanan/api-proxy/internal/infra/config"
)

func TestProcessStreamEventCorrectsNestedMessageStartUsageWithoutChangingMetrics(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ginContext, _ := gin.CreateTestContext(recorder)
	ginContext.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	streamContext := NewStreamContext(&config.EnvConfig{})
	requestBody := []byte(fmt.Sprintf(
		`{"messages":[{"role":"user","content":%q}]}`,
		strings.Repeat("a", 28000),
	))
	estimatedInputTokens := tokenusage.EstimateRequestTokens(requestBody)
	event := "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"usage\":{\"input_tokens\":209736,\"output_tokens\":1}}}\n\n"

	if err := ProcessStreamEvent(
		ginContext,
		ginContext.Writer,
		recorder,
		event,
		streamContext,
		&config.EnvConfig{},
		requestBody,
	); err != nil {
		t.Fatalf("ProcessStreamEvent returned error: %v", err)
	}

	if streamContext.UpstreamUsage.InputTokens != 209736 {
		t.Fatalf("上游指标快照应保留原值, got %d", streamContext.UpstreamUsage.InputTokens)
	}
	writtenEvent := recorder.Body.String()
	writtenData, ok := ParseSSEEventData(writtenEvent)
	if !ok {
		t.Fatalf("客户端事件应保持可解析: %q", writtenEvent)
	}
	usage := writtenData["message"].(map[string]interface{})["usage"].(map[string]interface{})
	if got := int(usage["input_tokens"].(float64)); got != estimatedInputTokens {
		t.Fatalf("message_start 客户端 input_tokens = %d, want %d", got, estimatedInputTokens)
	}
}

func TestSanityCheckMessageStreamUsageClearsImpossibleCacheTuple(t *testing.T) {
	requestBody := []byte(fmt.Sprintf(
		`{"messages":[{"role":"user","content":%q}]}`,
		strings.Repeat("a", 28000),
	))
	estimatedInputTokens := tokenusage.EstimateRequestTokens(requestBody)
	event := "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":500,\"output_tokens\":1,\"cache_read_input_tokens\":204800,\"cache_creation_input_tokens\":1000,\"cache_creation_5m_input_tokens\":400,\"cache_creation_1h_input_tokens\":600,\"cache_ttl\":\"mixed\",\"input_tokens_details\":{\"cached_tokens\":204800}}}}\n\n"

	correctedEvent, changed := sanityCheckMessageStreamInputTokens(event, requestBody, false)
	if !changed {
		t.Fatal("不可能的缓存元组应被校正")
	}
	data, ok := ParseSSEEventData(correctedEvent)
	if !ok {
		t.Fatalf("校正后事件应保持可解析: %q", correctedEvent)
	}
	usage := data["message"].(map[string]interface{})["usage"].(map[string]interface{})
	if got := int(usage["input_tokens"].(float64)); got != estimatedInputTokens {
		t.Fatalf("input_tokens = %d, want %d", got, estimatedInputTokens)
	}
	for _, key := range []string{
		"cache_read_input_tokens",
		"cache_creation_input_tokens",
		"cache_creation_5m_input_tokens",
		"cache_creation_1h_input_tokens",
		"cache_ttl",
		"input_tokens_details",
	} {
		if _, exists := usage[key]; exists {
			t.Fatalf("异常客户端 usage 不应保留 %s", key)
		}
	}
}

func TestProcessStreamEventPreservesTrustedCacheUsage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ginContext, _ := gin.CreateTestContext(recorder)
	ginContext.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	requestBody := []byte(fmt.Sprintf(
		`{"messages":[{"role":"user","content":%q}]}`,
		strings.Repeat("a", 28000),
	))
	estimatedInputTokens := tokenusage.EstimateRequestTokens(requestBody)
	uncachedInputTokens := estimatedInputTokens - 600
	event := fmt.Sprintf(
		"event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"usage\":{\"input_tokens\":%d,\"output_tokens\":2,\"cache_read_input_tokens\":100,\"cache_creation_input_tokens\":500,\"cache_creation_5m_input_tokens\":200,\"cache_creation_1h_input_tokens\":300,\"cache_ttl\":\"mixed\"}}}\n\n",
		uncachedInputTokens,
	)

	if err := ProcessStreamEvent(
		ginContext,
		ginContext.Writer,
		recorder,
		event,
		NewStreamContext(&config.EnvConfig{}),
		&config.EnvConfig{},
		requestBody,
	); err != nil {
		t.Fatalf("ProcessStreamEvent returned error: %v", err)
	}

	data, ok := ParseSSEEventData(recorder.Body.String())
	if !ok {
		t.Fatalf("客户端事件应保持可解析: %q", recorder.Body.String())
	}
	usage := data["message"].(map[string]interface{})["usage"].(map[string]interface{})
	for key, want := range map[string]interface{}{
		"input_tokens":                   float64(uncachedInputTokens),
		"cache_read_input_tokens":        float64(100),
		"cache_creation_input_tokens":    float64(500),
		"cache_creation_5m_input_tokens": float64(200),
		"cache_creation_1h_input_tokens": float64(300),
		"cache_ttl":                      "mixed",
	} {
		if got := usage[key]; got != want {
			t.Fatalf("%s = %v, want %v", key, got, want)
		}
	}
}

func TestForwardUpstreamResponseBody(t *testing.T) {
	gin.SetMode(gin.TestMode)

	newCtx := func() (*gin.Context, *httptest.ResponseRecorder) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
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

	t.Run("nil arguments are safe", func(t *testing.T) {
		if err := ForwardUpstreamResponseBody(nil, nil, "", false); err != nil {
			t.Fatalf("nil 参数应返回 nil, got %v", err)
		}
	})
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("read failed") }

// makeLargeMessagesRequestBody 造一个长上下文 messages 请求体，保证本地估算超过校验下限。
func makeLargeMessagesRequestBody(t *testing.T) []byte {
	t.Helper()
	body := []byte(`{"model":"claude-opus-4-6","messages":[{"role":"user","content":"` + strings.Repeat("abcdefgh ", 17500) + `"}]}`)
	if estimated := tokenusage.EstimateRequestTokens(body); estimated < tokenusage.UsageSanityMinTokensForTest {
		t.Fatalf("测试请求体估算 %d 未达校验下限 %d", estimated, tokenusage.UsageSanityMinTokensForTest)
	}
	return body
}

// TestSanityCheckMessageStreamInputTokens 锁定 messages 流式出口的 usage 合理性校验契约。
// 与 tokenusage.SanityCheckedInputTokens 共用同一套判定，这里只验证 SSE 层改写不破坏结构、
// 缓存字段不被覆盖、客户端可读字段的语义与 responses 侧保持一致。
func TestSanityCheckMessageStreamInputTokens(t *testing.T) {
	largeBody := makeLargeMessagesRequestBody(t)
	estimated := tokenusage.EstimateRequestTokens(largeBody)

	t.Run("少报: 校正后 input 回到估算量级", func(t *testing.T) {
		event := "event: message_delta\ndata: {\"type\":\"message_delta\",\"usage\":{\"input_tokens\":7723,\"output_tokens\":188,\"cache_read_input_tokens\":0}}\n\n"
		out, ok := sanityCheckMessageStreamInputTokens(event, largeBody, false)
		if !ok {
			t.Fatalf("应触发校正")
		}
		usage := extractStreamUsage(t, out)
		if got, _ := usage["input_tokens"].(float64); int(got) != estimated {
			t.Errorf("input_tokens 应校正为 %d, got %v", estimated, usage["input_tokens"])
		}
		if got, _ := usage["output_tokens"].(float64); int(got) != 188 {
			t.Errorf("output_tokens 不应被改动: %v", usage["output_tokens"])
		}
		if got, _ := usage["cache_read_input_tokens"].(float64); int(got) != 0 {
			t.Errorf("cache_read_input_tokens 不应被改动: %v", usage["cache_read_input_tokens"])
		}
	})

	t.Run("多报: 压缩后假大值应被压回估算量级", func(t *testing.T) {
		smallBody := []byte(`{"model":"claude-opus-4-6","messages":[{"role":"user","content":"summary + new"}]}`)
		event := "event: message_delta\ndata: {\"type\":\"message_delta\",\"usage\":{\"input_tokens\":209736,\"output_tokens\":188}}\n\n"
		out, ok := sanityCheckMessageStreamInputTokens(event, smallBody, false)
		if !ok {
			t.Fatalf("多报场景应触发校正")
		}
		usage := extractStreamUsage(t, out)
		if got, _ := usage["input_tokens"].(float64); int(got) > 20000 {
			t.Errorf("假大值应被压回估算量级, got %v", usage["input_tokens"])
		}
	})

	t.Run("上游回报可信: 原样返回", func(t *testing.T) {
		event := "event: message_delta\ndata: {\"type\":\"message_delta\",\"usage\":{\"input_tokens\":" + strconv.Itoa(estimated) + ",\"output_tokens\":188}}\n\n"
		if out, ok := sanityCheckMessageStreamInputTokens(event, largeBody, false); ok || out != event {
			t.Fatalf("可信回报应原样返回 (changed=%v)", ok)
		}
	})

	t.Run("Anthropic 语义分开报缓存: 不得误判", func(t *testing.T) {
		cacheRead := estimated - 1000
		event := "event: message_delta\ndata: {\"type\":\"message_delta\",\"usage\":{\"input_tokens\":271,\"output_tokens\":188,\"cache_read_input_tokens\":" + strconv.Itoa(cacheRead) + ",\"cache_creation_input_tokens\":500}}\n\n"
		if out, ok := sanityCheckMessageStreamInputTokens(event, largeBody, false); ok || out != event {
			t.Fatalf("Claude 语义分开报缓存应原样返回 (changed=%v)", ok)
		}
	})

	t.Run("上游确实少报且有缓存: 校正值扣掉缓存", func(t *testing.T) {
		event := "event: message_delta\ndata: {\"type\":\"message_delta\",\"usage\":{\"input_tokens\":100,\"output_tokens\":188,\"cache_read_input_tokens\":1000,\"cache_creation_input_tokens\":0}}\n\n"
		out, ok := sanityCheckMessageStreamInputTokens(event, largeBody, false)
		if !ok {
			t.Fatalf("应触发校正")
		}
		usage := extractStreamUsage(t, out)
		want := estimated - 1000
		if got, _ := usage["input_tokens"].(float64); int(got) != want {
			t.Errorf("校正值应扣掉缓存量: got %d, want %d", int(got), want)
		}
		if got, _ := usage["cache_read_input_tokens"].(float64); int(got) != 1000 {
			t.Errorf("cache_read 字段不应被改动: %v", usage["cache_read_input_tokens"])
		}
		// input + cache 之和回到真实上下文规模，不双倍
		if int(usage["input_tokens"].(float64))+int(usage["cache_read_input_tokens"].(float64)) != estimated {
			t.Errorf("input+cache 应等于估算总量 %d", estimated)
		}
	})

	t.Run("不带 input_tokens: 原样返回", func(t *testing.T) {
		event := "event: message_delta\ndata: {\"type\":\"message_delta\",\"usage\":{\"output_tokens\":188}}\n\n"
		if out, ok := sanityCheckMessageStreamInputTokens(event, largeBody, false); ok || out != event {
			t.Fatalf("无 input_tokens 时不应触发 (changed=%v)", ok)
		}
	})
}

func extractStreamUsage(t *testing.T, event string) map[string]interface{} {
	t.Helper()
	for _, line := range strings.Split(event, "\n") {
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimPrefix(strings.TrimPrefix(line, "data: "), "data:")
		var root map[string]interface{}
		if err := json.Unmarshal([]byte(payload), &root); err != nil {
			continue
		}
		if u, ok := root["usage"].(map[string]interface{}); ok {
			return u
		}
	}
	t.Fatalf("未找到 usage 字段: %q", event)
	return nil
}
