// OpenAI 用量字段解析与归一化，供流式及非流式共用。
package providers

import (
	"encoding/json"

	"github.com/insistanan/api-proxy/internal/types"
)

type openAIUsageEnvelope struct {
	Usage *openAIUsageDetails `json:"usage"`
}

type openAIUsageDetails struct {
	InputTokens                int                `json:"input_tokens"`
	OutputTokens               int                `json:"output_tokens"`
	PromptTokens               int                `json:"prompt_tokens"`
	CompletionTokens           int                `json:"completion_tokens"`
	CacheCreationInputTokens   int                `json:"cache_creation_input_tokens"`
	CacheReadInputTokens       int                `json:"cache_read_input_tokens"`
	CacheCreation5mInputTokens int                `json:"cache_creation_5m_input_tokens"`
	CacheCreation1hInputTokens int                `json:"cache_creation_1h_input_tokens"`
	CacheTTL                   string             `json:"cache_ttl"`
	CachedContentTokenCount    int                `json:"cachedContentTokenCount"`
	PromptTokenDetails         openAITokenDetails `json:"prompt_tokens_details"`
	InputTokenDetails          openAITokenDetails `json:"input_tokens_details"`
}

type openAITokenDetails struct {
	CachedTokens int `json:"cached_tokens"`
}

func normalizeOpenAIUsage(base *types.Usage, details *openAIUsageDetails) types.Usage {
	var usage types.Usage
	if base != nil {
		usage = *base
	}
	if details != nil {
		usage.InputTokens = firstPositiveInt(details.InputTokens, details.PromptTokens, usage.InputTokens, usage.PromptTokens)
		usage.OutputTokens = firstPositiveInt(details.OutputTokens, details.CompletionTokens, usage.OutputTokens, usage.CompletionTokens)
		usage.CacheCreationInputTokens = firstPositiveInt(details.CacheCreationInputTokens, usage.CacheCreationInputTokens)
		cacheReadFromDetails := firstPositiveInt(
			details.InputTokenDetails.CachedTokens,
			details.PromptTokenDetails.CachedTokens,
			details.CachedContentTokenCount,
		)
		usage.CacheReadInputTokens = firstPositiveInt(
			details.CacheReadInputTokens,
			cacheReadFromDetails,
			usage.CacheReadInputTokens,
		)
		usage.CacheCreation5mInputTokens = firstPositiveInt(details.CacheCreation5mInputTokens, usage.CacheCreation5mInputTokens)
		usage.CacheCreation1hInputTokens = firstPositiveInt(details.CacheCreation1hInputTokens, usage.CacheCreation1hInputTokens)
		if details.CacheTTL != "" {
			usage.CacheTTL = details.CacheTTL
		}
		// input_tokens 收敛到 Anthropic uncached 口径。
		// Anthropic 契约由客户端自行求和：
		//   total_input = input_tokens + cache_read_input_tokens + cache_creation_input_tokens
		// 所以在下发 cache_read 的同时把总量塞进 input_tokens，规范客户端会把缓存前缀算两遍，
		// 上下文满度虚高，表现为 compact 之后立刻又触发 compact。
		//
		// 减不减由 cache 量的**字段名来源**决定，不看数值：
		//   - cached_tokens / prompt_tokens_details.cached_tokens / cachedContentTokenCount
		//     是 subset 式命名（OpenAI、Gemini 语义下 cached ⊆ prompt/input），此时上游报的
		//     input 是总量，必须减。
		//   - cache_read_input_tokens 是 Anthropic 式命名，上游本就按 uncached 报 input，
		//     再减一次就是双减（对照 converters.TestExtractUsageMetrics_ClaudeCacheReadDoesNotDoubleSubtract）。
		// 与 converters.ExtractUsageMetrics 同一判据，两条路径不允许分叉。
		//
		// 减法绑定在"本次 details 报出的 input 量"而非最终 usage.InputTokens，因此对流式
		// mergeOpenAIUsageFromChunk 的反复调用幂等：本 chunk 报了 input 就重算一次完整减法，
		// 没报就保留上一轮已减的值，不会二次相减。
		reportedInput := firstPositiveInt(details.InputTokens, details.PromptTokens)
		if reportedInput > 0 && cacheReadFromDetails > 0 && details.CacheReadInputTokens == 0 {
			usage.InputTokens = reportedInput - cacheReadFromDetails
			if usage.InputTokens < 0 {
				// 上游异常回包（cached > prompt）。钳制到 0 而不是放行负数：
				// 负数会流进客户端的上下文估算与 sqlite 指标日志。
				usage.InputTokens = 0
			}
		}
	}
	return usage
}

func mergeOpenAIUsageFromChunk(chunk map[string]interface{}, dst *types.Usage) bool {
	if chunk == nil || dst == nil {
		return false
	}
	usageRaw, ok := chunk["usage"]
	if !ok || usageRaw == nil {
		return false
	}

	payload, err := json.Marshal(map[string]interface{}{"usage": usageRaw})
	if err != nil {
		return false
	}
	var envelope openAIUsageEnvelope
	if err := json.Unmarshal(payload, &envelope); err != nil || envelope.Usage == nil {
		return false
	}
	normalized := normalizeOpenAIUsage(dst, envelope.Usage)
	*dst = normalized
	return openAIUsageHasData(normalized)
}

func openAIUsageHasData(usage types.Usage) bool {
	return usage.InputTokens > 0 ||
		usage.OutputTokens > 0 ||
		usage.CacheCreationInputTokens > 0 ||
		usage.CacheReadInputTokens > 0 ||
		usage.CacheCreation5mInputTokens > 0 ||
		usage.CacheCreation1hInputTokens > 0
}

func firstPositiveInt(values ...int) int {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}
