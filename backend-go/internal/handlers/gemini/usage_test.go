package gemini

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/insistanan/api-proxy/internal/infra/config"
	"github.com/insistanan/api-proxy/internal/protocol/types"
)

// geminiUsageBody 是一份带 thinking 的 Gemini 原生非流式回包：
// promptTokenCount 含缓存 900，candidatesTokenCount(20) 不含 thoughtsTokenCount(7)。
const geminiUsageBody = `{"candidates":[{"content":{"parts":[{"text":"ok"}],"role":"model"},` +
	`"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":1000,` +
	`"cachedContentTokenCount":900,"candidatesTokenCount":20,"thoughtsTokenCount":7,` +
	`"totalTokenCount":1027}}`

// /v1beta 原生出口的指标必须按完整输出记（candidates + thoughts），否则 Gemini 渠道的
// 用量统计比其它渠道系统性偏低、跨渠道不可比。三家 output 侧语义与官方证据见
// types.ClaudeOutputTokensDetails。
func TestHandleSuccess_GeminiThoughtsCountedIntoUsage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost,
		"/v1beta/models/gemini-2.5-pro:generateContent", nil)

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(geminiUsageBody)),
	}

	usage, err := handleSuccess(c, resp, "gemini", &config.EnvConfig{}, time.Now(),
		&types.GeminiRequest{}, "gemini-2.5-pro", false)
	if err != nil {
		t.Fatalf("handleSuccess 失败: %v", err)
	}
	if usage == nil {
		t.Fatal("usage 为空")
	}
	if usage.OutputTokens != 27 {
		t.Errorf("指标 output_tokens 应为 20+7=27，实际 %d", usage.OutputTokens)
	}
	if usage.InputTokens != 100 {
		t.Errorf("指标 input_tokens 应为 1000-900=100，实际 %d", usage.InputTokens)
	}
	if usage.OutputTokensDetails == nil || usage.OutputTokensDetails.ThinkingTokens != 7 {
		t.Errorf("thinking_tokens 应单列为 7，实际 %+v", usage.OutputTokensDetails)
	}

	// 透传给客户端的响应体保持 Gemini 原生语义：candidatesTokenCount 不含 thoughts，
	// 原生客户端自行求和。只改指标口径、不改响应体是刻意的选择。
	if body := recorder.Body.String(); !strings.Contains(body, `"candidatesTokenCount":20`) {
		t.Errorf("原生响应体的 candidatesTokenCount 不应被改写为含 thoughts 的值；实际: %s", body)
	}
}

// 流式指标与非流式同口径。
func TestStreamGeminiToGemini_ThoughtsCountedIntoUsage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost,
		"/v1beta/models/gemini-2.5-pro:streamGenerateContent?alt=sse", nil)

	sse := `data: {"candidates":[{"content":{"parts":[{"text":"ok"}],"role":"model"},"finishReason":"STOP","index":0}],"usageMetadata":{"promptTokenCount":1000,"cachedContentTokenCount":900,"candidatesTokenCount":20,"thoughtsTokenCount":7,"totalTokenCount":1027}}
`
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(sse)),
	}

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		t.Fatal("测试 ResponseWriter 不支持 Flusher")
	}

	usage, err := streamGeminiToGemini(c, resp, flusher, &config.EnvConfig{})
	if err != nil {
		t.Fatalf("streamGeminiToGemini 失败: %v", err)
	}
	if usage == nil {
		t.Fatal("usage 为空")
	}
	if usage.OutputTokens != 27 {
		t.Errorf("指标 output_tokens 应为 20+7=27，实际 %d", usage.OutputTokens)
	}
	if usage.OutputTokensDetails == nil || usage.OutputTokensDetails.ThinkingTokens != 7 {
		t.Errorf("thinking_tokens 应单列为 7，实际 %+v", usage.OutputTokensDetails)
	}
	if body := recorder.Body.String(); !strings.Contains(body, `"candidatesTokenCount":20`) {
		t.Errorf("转发的 SSE 应保持原生 usageMetadata 不变；实际: %s", body)
	}
}
