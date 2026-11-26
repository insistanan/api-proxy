package providers

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestNormalizeOpenAIUsagePreservesCacheAccounting(t *testing.T) {
	for _, tc := range []struct {
		name          string
		details       openAIUsageDetails
		input, cached int
	}{
		{"缓存包含在输入中", openAIUsageDetails{PromptTokens: 1000, CompletionTokens: 20, PromptTokenDetails: openAITokenDetails{CachedTokens: 900}}, 100, 900},
		{"缓存单独计数", openAIUsageDetails{InputTokens: 100, OutputTokens: 20, CacheReadInputTokens: 900}, 100, 900},
		{"输入不能为负", openAIUsageDetails{PromptTokens: 10, PromptTokenDetails: openAITokenDetails{CachedTokens: 99}}, 0, 99},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := normalizeOpenAIUsage(nil, &tc.details)
			for i := 0; i < 2; i++ {
				require.Equal(t, tc.input, got.InputTokens)
				require.Equal(t, tc.cached, got.CacheReadInputTokens)
				got = normalizeOpenAIUsage(&got, &tc.details)
			}
			// 后续 chunk 缺少输入总量时，不应再次扣减缓存。
			got = normalizeOpenAIUsage(&got, &openAIUsageDetails{PromptTokenDetails: openAITokenDetails{CachedTokens: tc.cached}})
			require.Equal(t, tc.input, got.InputTokens)
		})
	}
}

func TestCaptureResponsesUsagePreservesCacheAccounting(t *testing.T) {
	for _, body := range []string{
		`{"response":{"usage":{"input_tokens":1000,"output_tokens":20,"input_tokens_details":{"cached_tokens":900}}}}`,
		`{"response":{"usage":{"input_tokens":100,"output_tokens":20,"cache_read_input_tokens":900}}}`,
		`{"usage":{"input_tokens":1000,"output_tokens":20,"prompt_tokens_details":{"cached_tokens":900}}}`,
	} {
		state := &responsesToClaudeStreamState{}
		for i := 0; i < 2; i++ {
			state.captureResponsesUsage(gjson.Parse(body))
			require.Equal(t, 100, state.inputTokens)
			require.Equal(t, 900, state.cacheReadInputTokens)
			require.Equal(t, 20, state.outputTokens)
		}
	}
}
