package providers

import (
	"testing"

	"github.com/tidwall/gjson"
)

// 上游用 subset 式字段名（cached_tokens ⊆ prompt_tokens）报缓存时，
// Anthropic 出口的 input_tokens 必须是扣除后的余量。
func TestNormalizeOpenAIUsage_SubsetCachedTokensSubtracted(t *testing.T) {
	got := normalizeOpenAIUsage(nil, &openAIUsageDetails{
		PromptTokens:       1000,
		CompletionTokens:   20,
		PromptTokenDetails: openAITokenDetails{CachedTokens: 900},
	})
	if got.InputTokens != 100 {
		t.Errorf("input_tokens 应为 1000-900=100，实际 %d", got.InputTokens)
	}
	if got.CacheReadInputTokens != 900 {
		t.Errorf("cache_read_input_tokens 应为 900，实际 %d", got.CacheReadInputTokens)
	}
}

// 上游用 Anthropic 式字段名 cache_read_input_tokens 报缓存时，input_tokens 本就是
// uncached 余量，再减一次就是双减。判据是字段名来源，不是数值大小。
func TestNormalizeOpenAIUsage_AnthropicStyleCacheReadNotSubtracted(t *testing.T) {
	got := normalizeOpenAIUsage(nil, &openAIUsageDetails{
		InputTokens:          100,
		OutputTokens:         20,
		CacheReadInputTokens: 900,
	})
	if got.InputTokens != 100 {
		t.Errorf("input_tokens 应保持 100（不得双减），实际 %d", got.InputTokens)
	}
}

// 上游异常回包 cached > prompt 时钳制到 0，不让负数流进客户端与指标。
func TestNormalizeOpenAIUsage_NegativeInputClampedToZero(t *testing.T) {
	got := normalizeOpenAIUsage(nil, &openAIUsageDetails{
		PromptTokens:       10,
		PromptTokenDetails: openAITokenDetails{CachedTokens: 99},
	})
	if got.InputTokens != 0 {
		t.Errorf("input_tokens 应钳制为 0，实际 %d", got.InputTokens)
	}
}

// 流式路径会对每个带 usage 的 chunk 反复调用 normalizeOpenAIUsage，且把上一轮结果
// 当作 base 传回。减法绑定"本次 details 报出的 input"，因此必须幂等。
func TestNormalizeOpenAIUsage_RepeatedMergeIsIdempotent(t *testing.T) {
	details := &openAIUsageDetails{
		PromptTokens:       1000,
		CompletionTokens:   20,
		PromptTokenDetails: openAITokenDetails{CachedTokens: 900},
	}
	acc := normalizeOpenAIUsage(nil, details)
	acc = normalizeOpenAIUsage(&acc, details)
	if acc.InputTokens != 100 {
		t.Fatalf("重复合并后 input_tokens 应仍为 100（不得二次相减），实际 %d", acc.InputTokens)
	}

	// 后续 chunk 只报缓存、不报 input 时，保留上一轮已减的值。
	acc = normalizeOpenAIUsage(&acc, &openAIUsageDetails{
		PromptTokenDetails: openAITokenDetails{CachedTokens: 900},
	})
	if acc.InputTokens != 100 {
		t.Fatalf("缺 input 的 chunk 不应改动已减值，实际 %d", acc.InputTokens)
	}
}

// Responses 上游 → Claude 流式：subset 式缓存字段要减。
func TestCaptureResponsesUsage_SubsetCachedTokensSubtracted(t *testing.T) {
	s := &responsesToClaudeStreamState{}
	s.captureResponsesUsage(gjson.Parse(
		`{"response":{"usage":{"input_tokens":1000,"output_tokens":20,"input_tokens_details":{"cached_tokens":900}}}}`,
	))
	if s.inputTokens != 100 {
		t.Errorf("input_tokens 应为 1000-900=100，实际 %d", s.inputTokens)
	}
	if s.cacheReadInputTokens != 900 {
		t.Errorf("cache_read 应为 900，实际 %d", s.cacheReadInputTokens)
	}
}

// Anthropic 式字段名不减；一条流里多次抓取 usage 必须幂等。
func TestCaptureResponsesUsage_AnthropicStyleNotSubtractedAndIdempotent(t *testing.T) {
	s := &responsesToClaudeStreamState{}
	line := `{"response":{"usage":{"input_tokens":100,"output_tokens":20,"cache_read_input_tokens":900}}}`
	s.captureResponsesUsage(gjson.Parse(line))
	if s.inputTokens != 100 {
		t.Fatalf("input_tokens 应保持 100（不得双减），实际 %d", s.inputTokens)
	}

	subset := `{"usage":{"input_tokens":1000,"output_tokens":20,"prompt_tokens_details":{"cached_tokens":900}}}`
	s2 := &responsesToClaudeStreamState{}
	s2.captureResponsesUsage(gjson.Parse(subset))
	s2.captureResponsesUsage(gjson.Parse(subset))
	if s2.inputTokens != 100 {
		t.Fatalf("重复抓取后 input_tokens 应仍为 100，实际 %d", s2.inputTokens)
	}
}
