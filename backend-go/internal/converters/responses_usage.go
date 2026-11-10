// Responses 用量转换：统一解析 OpenAI、Claude 与 Gemini 的缓存统计。
package converters

import (
	"github.com/insistanan/api-proxy/internal/types"
)

// getIntFromMap 从 map 中安全提取整数值
// 支持 float64（JSON 反序列化）和 int/int64（内部构造）两种类型
func getIntFromMap(m map[string]interface{}, key string) (int, bool) {
	v, exists := m[key]
	if !exists {
		return 0, false
	}
	switch val := v.(type) {
	case float64:
		return int(val), true
	case int:
		return val, true
	case int64:
		return int(val), true
	case int32:
		return int(val), true
	default:
		return 0, false
	}
}

// parseResponsesUsage 解析 Responses API 的 usage 字段
// 完整支持 OpenAI Responses API 的详细 usage 结构
func parseResponsesUsage(usageRaw interface{}) types.ResponsesUsage {
	usage := types.ResponsesUsage{}

	usageMap, ok := usageRaw.(map[string]interface{})
	if !ok {
		return usage
	}

	// 解析基础字段（兼容两种命名风格）
	// OpenAI Responses API: input_tokens / output_tokens
	// OpenAI Chat API: prompt_tokens / completion_tokens
	if v, ok := getIntFromMap(usageMap, "input_tokens"); ok {
		usage.InputTokens = v
	} else if v, ok := getIntFromMap(usageMap, "prompt_tokens"); ok {
		usage.InputTokens = v
	}

	if v, ok := getIntFromMap(usageMap, "output_tokens"); ok {
		usage.OutputTokens = v
	} else if v, ok := getIntFromMap(usageMap, "completion_tokens"); ok {
		usage.OutputTokens = v
	}

	if v, ok := getIntFromMap(usageMap, "total_tokens"); ok {
		usage.TotalTokens = v
	} else {
		usage.TotalTokens = usage.InputTokens + usage.OutputTokens
	}

	// 解析 input_tokens_details（兼容 prompt_tokens_details）
	inputDetailsRaw := usageMap["input_tokens_details"]
	if inputDetailsRaw == nil {
		inputDetailsRaw = usageMap["prompt_tokens_details"]
	}
	if detailsMap, ok := inputDetailsRaw.(map[string]interface{}); ok {
		usage.InputTokensDetails = &types.InputTokensDetails{}
		if v, ok := getIntFromMap(detailsMap, "cached_tokens"); ok {
			if v < 0 {
				v = 0
			}
			usage.InputTokensDetails.CachedTokens = v
			usage.CacheReadInputTokens = v
			// OpenAI 的 cached_tokens 是 input_tokens 的子集。
			// 必须覆盖相等和 cached_tokens 超过 input_tokens 的异常回包，
			// 否则客户端会继续看到一份虚高的上下文计数。
			usage.InputTokens -= v
			if usage.InputTokens < 0 {
				usage.InputTokens = 0
			}
			usage.TotalTokens = usage.InputTokens + usage.OutputTokens
		}
	}

	// 解析 output_tokens_details（兼容 completion_tokens_details）
	outputDetailsRaw := usageMap["output_tokens_details"]
	if outputDetailsRaw == nil {
		outputDetailsRaw = usageMap["completion_tokens_details"]
	}
	if detailsMap, ok := outputDetailsRaw.(map[string]interface{}); ok {
		usage.OutputTokensDetails = &types.OutputTokensDetails{}
		if v, ok := getIntFromMap(detailsMap, "reasoning_tokens"); ok {
			usage.OutputTokensDetails.ReasoningTokens = v
		}
	}

	return usage
}

// parseClaudeUsage 解析 Claude API 的 usage 字段
// 完整支持 Claude 的缓存统计，包括 TTL 细分 (5m/1h)
// 参考 claude-code-hub 的 extractUsageMetrics 实现
func parseClaudeUsage(usageRaw interface{}) types.ResponsesUsage {
	usage := types.ResponsesUsage{}

	usageMap, ok := usageRaw.(map[string]interface{})
	if !ok {
		return usage
	}

	// 基础字段
	if v, ok := getIntFromMap(usageMap, "input_tokens"); ok {
		usage.InputTokens = v
	}
	if v, ok := getIntFromMap(usageMap, "output_tokens"); ok {
		usage.OutputTokens = v
	}
	usage.TotalTokens = usage.InputTokens + usage.OutputTokens

	// Claude 缓存创建统计（区分 TTL）
	var cacheCreation, cacheCreation5m, cacheCreation1h int
	var has5m, has1h bool

	// 总缓存创建量
	if v, ok := getIntFromMap(usageMap, "cache_creation_input_tokens"); ok {
		cacheCreation = v
		usage.CacheCreationInputTokens = cacheCreation
	}

	// 5分钟 TTL 缓存创建
	if v, ok := getIntFromMap(usageMap, "cache_creation_5m_input_tokens"); ok {
		cacheCreation5m = v
		usage.CacheCreation5mInputTokens = cacheCreation5m
		has5m = cacheCreation5m > 0
	}

	// 1小时 TTL 缓存创建
	if v, ok := getIntFromMap(usageMap, "cache_creation_1h_input_tokens"); ok {
		cacheCreation1h = v
		usage.CacheCreation1hInputTokens = cacheCreation1h
		has1h = cacheCreation1h > 0
	}

	// 缓存读取
	var cacheRead int
	if v, ok := getIntFromMap(usageMap, "cache_read_input_tokens"); ok {
		cacheRead = v
		usage.CacheReadInputTokens = cacheRead
	}

	// 设置缓存 TTL 标识
	if has5m && has1h {
		usage.CacheTTL = "mixed"
	} else if has1h {
		usage.CacheTTL = "1h"
	} else if has5m {
		usage.CacheTTL = "5m"
	}

	// 同时设置 InputTokensDetails（兼容 OpenAI 格式）
	// CachedTokens = cache_read（仅缓存读取，不包含缓存创建）
	// 注意：cache_creation 是新创建的缓存，不是"已缓存的 token"
	if cacheRead > 0 {
		usage.InputTokensDetails = &types.InputTokensDetails{
			CachedTokens: cacheRead,
		}
	}

	return usage
}

// parseGeminiUsage 解析 Gemini API 的 usage 字段
// Gemini 使用 promptTokenCount/candidatesTokenCount，需要特殊处理缓存去重
// 参考 claude-code-hub: Gemini 的 promptTokenCount 已包含 cachedContentTokenCount，需要扣除避免重复计费
func parseGeminiUsage(usageRaw interface{}) types.ResponsesUsage {
	usage := types.ResponsesUsage{}

	usageMap, ok := usageRaw.(map[string]interface{})
	if !ok {
		return usage
	}

	var promptTokens, cachedTokens, outputTokens, thoughtsTokens int

	// Gemini 字段名
	if v, ok := getIntFromMap(usageMap, "promptTokenCount"); ok {
		promptTokens = v
	}
	if v, ok := getIntFromMap(usageMap, "cachedContentTokenCount"); ok {
		cachedTokens = v
	}
	if v, ok := getIntFromMap(usageMap, "candidatesTokenCount"); ok {
		outputTokens = v
	}
	if v, ok := getIntFromMap(usageMap, "thoughtsTokenCount"); ok {
		thoughtsTokens = v
	}

	// 关键处理：Gemini 的 promptTokenCount 已包含 cachedContentTokenCount
	// 为避免重复计费，实际输入 token = promptTokenCount - cachedContentTokenCount
	actualInputTokens := promptTokens - cachedTokens
	if actualInputTokens < 0 {
		actualInputTokens = 0
	}

	usage.InputTokens = actualInputTokens
	// candidatesTokenCount 不含 thoughtsTokenCount，两者相加才是完整输出；而 Responses
	// 语义下 output_tokens 含 reasoning（官方三家语义对照见 types.ClaudeOutputTokensDetails）。
	// 不加就把 thinking 模型的输出系统性低报，reasoning 重的场景缺口可达 86%。
	usage.OutputTokens = outputTokens + thoughtsTokens
	usage.TotalTokens = actualInputTokens + usage.OutputTokens

	// 缓存读取统计
	if cachedTokens > 0 {
		usage.CacheReadInputTokens = cachedTokens
		usage.InputTokensDetails = &types.InputTokensDetails{
			CachedTokens: cachedTokens,
		}
	}

	// 推理明细。已计入 output_tokens，这里只作拆分展示。
	if thoughtsTokens > 0 {
		usage.OutputTokensDetails = &types.OutputTokensDetails{
			ReasoningTokens: thoughtsTokens,
		}
	}

	return usage
}

// ExtractUsageMetrics 多格式 Token 提取统一入口
// 自动检测并解析 Claude/Gemini/OpenAI 三种格式的 usage
// 参考 claude-code-hub 的 extractUsageMetrics 实现
func ExtractUsageMetrics(usageRaw interface{}) types.ResponsesUsage {
	usageMap, ok := usageRaw.(map[string]interface{})
	if !ok {
		return types.ResponsesUsage{}
	}

	// 1. 检测 Claude 格式：有 cache_creation_input_tokens 或 cache_read_input_tokens
	if _, hasCacheCreation := usageMap["cache_creation_input_tokens"]; hasCacheCreation {
		return parseClaudeUsage(usageRaw)
	}
	if _, hasCacheRead := usageMap["cache_read_input_tokens"]; hasCacheRead {
		return parseClaudeUsage(usageRaw)
	}

	// 2. 检测 Gemini 格式：有 promptTokenCount
	if _, hasPromptTokenCount := usageMap["promptTokenCount"]; hasPromptTokenCount {
		return parseGeminiUsage(usageRaw)
	}

	// 3. 默认 OpenAI 格式
	return parseResponsesUsage(usageRaw)
}
