package providers

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/insistanan/api-proxy/internal/types"
)

// Gemini 的 candidatesTokenCount 不含 thoughtsTokenCount，而 Anthropic 的 output_tokens
// **含** thinking token（官方三家语义对照见 types.ClaudeOutputTokensDetails）。
// 非流式出口必须相加，否则 thinking 模型的输出被系统性低报。
func TestGeminiConvertToClaudeResponse_ThoughtsCountedIntoOutput(t *testing.T) {
	p := &GeminiProvider{}
	resp, err := p.ConvertToClaudeResponse(&types.ProviderResponse{
		StatusCode: 200,
		Body: []byte(`{"candidates":[{"content":{"parts":[{"text":"ok"}],"role":"model"},` +
			`"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":1000,` +
			`"cachedContentTokenCount":900,"candidatesTokenCount":20,"thoughtsTokenCount":7,` +
			`"totalTokenCount":1027}}`),
	})
	if err != nil {
		t.Fatalf("转换失败: %v", err)
	}
	if resp.Usage == nil {
		t.Fatal("usage 为空")
	}
	if resp.Usage.OutputTokens != 27 {
		t.Errorf("output_tokens 应为 20+7=27，实际 %d", resp.Usage.OutputTokens)
	}
	if resp.Usage.InputTokens != 100 {
		t.Errorf("input_tokens 应为 1000-900=100，实际 %d", resp.Usage.InputTokens)
	}
	if resp.Usage.OutputTokensDetails == nil || resp.Usage.OutputTokensDetails.ThinkingTokens != 7 {
		t.Errorf("thinking_tokens 应单列为 7，实际 %+v", resp.Usage.OutputTokensDetails)
	}
}

// 非 thinking 模型不报 thoughts 时不得凭空生成明细字段。
func TestGeminiConvertToClaudeResponse_NoThoughtsOmitsDetails(t *testing.T) {
	p := &GeminiProvider{}
	resp, err := p.ConvertToClaudeResponse(&types.ProviderResponse{
		StatusCode: 200,
		Body: []byte(`{"candidates":[{"content":{"parts":[{"text":"ok"}],"role":"model"},` +
			`"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":11,` +
			`"candidatesTokenCount":124,"totalTokenCount":135}}`),
	})
	if err != nil {
		t.Fatalf("转换失败: %v", err)
	}
	if resp.Usage.OutputTokens != 124 {
		t.Errorf("output_tokens 应为 124，实际 %d", resp.Usage.OutputTokens)
	}
	if resp.Usage.OutputTokensDetails != nil {
		t.Errorf("未报 thoughts 时不应生成 output_tokens_details，实际 %+v", resp.Usage.OutputTokensDetails)
	}
}

// collectGeminiClaudeStream 把 Gemini SSE 喂给流式转换器，返回全部 Claude 事件文本。
func collectGeminiClaudeStream(t *testing.T, sse string) string {
	t.Helper()
	p := &GeminiProvider{}
	eventChan, errChan, err := p.HandleStreamResponseCtx(
		context.Background(), io.NopCloser(strings.NewReader(sse)),
	)
	if err != nil {
		t.Fatalf("启动流式转换失败: %v", err)
	}
	var sb strings.Builder
	for ev := range eventChan {
		sb.WriteString(ev)
	}
	for e := range errChan {
		if e != nil {
			t.Fatalf("流式转换报错: %v", e)
		}
	}
	return sb.String()
}

// 流式出口与非流式同口径：message_delta 的 output_tokens 含 thoughts，
// 并单列 output_tokens_details.thinking_tokens。
func TestGeminiStreamUsage_ThoughtsCountedIntoOutput(t *testing.T) {
	got := collectGeminiClaudeStream(t, `data: {"candidates":[{"content":{"parts":[{"text":"ok"}],"role":"model"},"finishReason":"STOP","index":0}],"usageMetadata":{"promptTokenCount":1000,"cachedContentTokenCount":900,"candidatesTokenCount":20,"thoughtsTokenCount":7,"totalTokenCount":1027}}
`)

	for _, want := range []string{
		`"output_tokens":27`,
		`"input_tokens":100`,
		`"cache_read_input_tokens":900`,
		`"thinking_tokens":7`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("流式输出缺少 %s；实际:\n%s", want, got)
		}
	}
}

// Gemini 原生每个 chunk 都带**累积** usageMetadata，且 thinking 模型的早期 chunk 可能
// 只报 promptTokenCount/thoughtsTokenCount 而没有 candidatesTokenCount。
// 此时 output 不能记 0（输出确实已经产生），终值仍以最后一个 chunk 为准（last-wins）。
func TestGeminiStreamUsage_EarlyThoughtsOnlyChunkThenFinalValue(t *testing.T) {
	got := collectGeminiClaudeStream(t, `data: {"candidates":[{"content":{"parts":[{"text":"a"}],"role":"model"},"index":0}],"usageMetadata":{"promptTokenCount":10,"thoughtsTokenCount":5}}

data: {"candidates":[{"content":{"parts":[{"text":"b"}],"role":"model"},"finishReason":"STOP","index":0}],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":8,"thoughtsTokenCount":9,"totalTokenCount":27}}
`)

	if !strings.Contains(got, `"output_tokens":17`) {
		t.Errorf("output_tokens 应为末次 8+9=17，实际:\n%s", got)
	}
	if !strings.Contains(got, `"thinking_tokens":9`) {
		t.Errorf("thinking_tokens 应为末次值 9，实际:\n%s", got)
	}
	if strings.Contains(got, `"output_tokens":0`) {
		t.Errorf("不得出现 output_tokens=0（早期只报 thoughts 的 chunk 被当成 0）：\n%s", got)
	}
}

// 反证切片 3 修复：第三方兼容网关把 usage 终值放在**没有 candidates** 的纯
// usageMetadata 尾 chunk。抓取点必须在 candidates 守卫之前，否则读不到。
// message_delta 在流尾统一发送，必须包含尾包终值且不重复终止。
func TestGeminiStreamUsage_TrailingUsageOnlyChunkCaptured(t *testing.T) {
	got := collectGeminiClaudeStream(t, `data: {"candidates":[{"content":{"parts":[{"text":"hi"}],"role":"model"},"finishReason":"STOP","index":0}],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":3,"thoughtsTokenCount":2}}

data: {"usageMetadata":{"promptTokenCount":10,"cachedContentTokenCount":4,"candidatesTokenCount":6,"thoughtsTokenCount":2,"totalTokenCount":18}}
`)

	for _, want := range []string{`"output_tokens":8`, `"input_tokens":6`, `"cache_read_input_tokens":4`} {
		if !strings.Contains(got, want) {
			t.Errorf("尾包 usage 缺少 %s，实际:\n%s", want, got)
		}
	}
	if strings.Count(got, "event: message_delta\n") != 1 || strings.Count(got, "event: message_stop\n") != 1 {
		t.Fatalf("流必须恰好定稿一次:\n%s", got)
	}
}
