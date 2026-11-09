// 本文件负责从上游 SSE 事件里"读出" usage：判定事件是否带 usage、是否需要修补，
// 提取各类 token 字段并累积到 CollectedUsageData，以及隐式缓存读取的推断。
// 修补（写回）侧在 stream_usage_patch.go。
package streams

import (
	"encoding/json"
	"log"
	"strings"

	"github.com/insistanan/api-proxy/internal/utils"
)

// updateCollectedUsage 更新收集的 usage 数据
func updateCollectedUsage(collected *CollectedUsageData, usageData CollectedUsageData) {
	if usageData.InputTokens > collected.InputTokens {
		collected.InputTokens = usageData.InputTokens
	}
	if usageData.OutputTokens > collected.OutputTokens {
		collected.OutputTokens = usageData.OutputTokens
	}
	if usageData.CacheCreationInputTokens > 0 {
		collected.CacheCreationInputTokens = usageData.CacheCreationInputTokens
	}
	if usageData.CacheReadInputTokens > 0 {
		collected.CacheReadInputTokens = usageData.CacheReadInputTokens
	}
	if usageData.CacheCreation5mInputTokens > 0 {
		collected.CacheCreation5mInputTokens = usageData.CacheCreation5mInputTokens
	}
	if usageData.CacheCreation1hInputTokens > 0 {
		collected.CacheCreation1hInputTokens = usageData.CacheCreation1hInputTokens
	}
	if usageData.CacheTTL != "" {
		collected.CacheTTL = usageData.CacheTTL
	}
}

// inferImplicitCacheRead 推断隐式缓存读取
//
// 当 message_start 中的 input_tokens 与 message_delta 中的最终 input_tokens 存在显著差异时，
// 差额可能是上游 prompt caching 命中但未明确返回 cache_read_input_tokens 的情况。
// 触发条件：差额 > 10% 或差额 > 10000 tokens，且上游未返回 cache_read_input_tokens。
func inferImplicitCacheRead(ctx *Context, enableLog bool) {
	// 前置条件检查
	if ctx.MessageStartInputTokens == 0 || ctx.CollectedUsage.InputTokens == 0 {
		return
	}

	// 上游已明确返回 cache_read，无需推断
	if ctx.CollectedUsage.CacheReadInputTokens > 0 {
		return
	}

	// 计算差额
	diff := ctx.MessageStartInputTokens - ctx.CollectedUsage.InputTokens
	if diff <= 0 {
		return
	}

	// 计算差额比例
	ratio := float64(diff) / float64(ctx.MessageStartInputTokens)

	// 触发条件：差额 > 10% 或差额 > 10000 tokens
	if ratio > 0.10 || diff > 10000 {
		ctx.CollectedUsage.CacheReadInputTokens = diff
		if enableLog {
			log.Printf("[Messages-Stream-Token] 推断隐式缓存: message_start=%d, final=%d, cache_read=%d (%.1f%%)",
				ctx.MessageStartInputTokens, ctx.CollectedUsage.InputTokens, diff, ratio*100)
		}
	}
}

// CheckEventUsageStatus 检测事件是否包含 usage 字段
func CheckEventUsageStatus(event string, enableLog bool) (bool, bool, bool, CollectedUsageData) {
	for _, line := range strings.Split(event, "\n") {
		jsonStr, isData := utils.ParseSSEDataLine(line)
		if !isData {
			continue
		}

		var data map[string]interface{}
		if err := json.Unmarshal([]byte(jsonStr), &data); err != nil {
			continue
		}

		return CheckEventUsageStatusFromData(data, enableLog)
	}
	return false, false, false, CollectedUsageData{}
}

// CheckEventUsageStatusFromData 检测已解析事件中是否包含 usage 字段。
func CheckEventUsageStatusFromData(data map[string]interface{}, enableLog bool) (bool, bool, bool, CollectedUsageData) {
	if data == nil {
		return false, false, false, CollectedUsageData{}
	}

	// 检查顶层 usage 字段
	if hasUsage, needInputPatch, needOutputPatch := checkUsageFieldsWithPatch(data["usage"]); hasUsage {
		var usageData CollectedUsageData
		if usage, ok := data["usage"].(map[string]interface{}); ok {
			if enableLog {
				logUsageDetection("顶层usage", usage, needInputPatch || needOutputPatch)
			}
			usageData = extractUsageFromMap(usage)
		}
		return true, needInputPatch, needOutputPatch, usageData
	}

	// 检查 message.usage
	if msg, ok := data["message"].(map[string]interface{}); ok {
		if hasUsage, needInputPatch, needOutputPatch := checkUsageFieldsWithPatch(msg["usage"]); hasUsage {
			var usageData CollectedUsageData
			if usage, ok := msg["usage"].(map[string]interface{}); ok {
				if enableLog {
					logUsageDetection("message.usage", usage, needInputPatch || needOutputPatch)
				}
				usageData = extractUsageFromMap(usage)
			}
			return true, needInputPatch, needOutputPatch, usageData
		}
	}

	return false, false, false, CollectedUsageData{}
}

// checkUsageFieldsWithPatch 检查 usage 对象是否包含 token 字段
func checkUsageFieldsWithPatch(usage interface{}) (bool, bool, bool) {
	if u, ok := usage.(map[string]interface{}); ok {
		inputTokens, hasInput := u["input_tokens"]
		outputTokens, hasOutput := u["output_tokens"]
		if hasInput || hasOutput {
			needInputPatch := false
			needOutputPatch := false

			cacheCreation := usageTokenValue(u, "cache_creation_input_tokens")
			cacheCreation5m := usageTokenValue(u, "cache_creation_5m_input_tokens")
			cacheCreation1h := usageTokenValue(u, "cache_creation_1h_input_tokens")
			cacheRead := usageTokenValue(u, "cache_read_input_tokens")
			if cacheRead <= 0 {
				cacheRead = int(nestedCachedTokens(u["input_tokens_details"]))
			}
			if cacheRead <= 0 {
				cacheRead = int(nestedCachedTokens(u["prompt_tokens_details"]))
			}
			hasCacheTokens := utils.AnthropicCachedInputTokens(
				cacheRead,
				cacheCreation,
				cacheCreation5m,
				cacheCreation1h,
			) > 0

			if hasInput {
				if inputTokens == nil {
					// input_tokens 为 nil 时需要修补
					needInputPatch = true
				} else if v, ok := inputTokens.(float64); ok && v <= 1 && !hasCacheTokens {
					needInputPatch = true
				}
			}
			if hasOutput {
				if v, ok := outputTokens.(float64); ok && v <= 1 {
					needOutputPatch = true
				}
			}
			return true, needInputPatch, needOutputPatch
		}
	}
	return false, false, false
}

// extractUsageFromMap 从 usage map 中提取 token 数据
func extractUsageFromMap(usage map[string]interface{}) CollectedUsageData {
	var data CollectedUsageData

	data.InputTokens = usageTokenValue(usage, "input_tokens")
	data.OutputTokens = usageTokenValue(usage, "output_tokens")
	data.CacheCreationInputTokens = usageTokenValue(usage, "cache_creation_input_tokens")
	data.CacheReadInputTokens = usageTokenValue(usage, "cache_read_input_tokens")
	if data.CacheReadInputTokens == 0 {
		if cached := nestedCachedTokens(usage["input_tokens_details"]); cached > 0 {
			data.CacheReadInputTokens = int(cached)
		}
	}
	if data.CacheReadInputTokens == 0 {
		if cached := nestedCachedTokens(usage["prompt_tokens_details"]); cached > 0 {
			data.CacheReadInputTokens = int(cached)
		}
	}

	var has5m, has1h bool
	data.CacheCreation5mInputTokens = usageTokenValue(usage, "cache_creation_5m_input_tokens")
	data.CacheCreation1hInputTokens = usageTokenValue(usage, "cache_creation_1h_input_tokens")
	has5m = data.CacheCreation5mInputTokens > 0
	has1h = data.CacheCreation1hInputTokens > 0

	if has5m && has1h {
		data.CacheTTL = "mixed"
	} else if has1h {
		data.CacheTTL = "1h"
	} else if has5m {
		data.CacheTTL = "5m"
	}

	return data
}

func usageTokenValue(usage map[string]interface{}, key string) int {
	switch value := usage[key].(type) {
	case float64:
		return int(value)
	case int:
		return value
	case int64:
		return int(value)
	case json.Number:
		parsed, err := value.Int64()
		if err == nil {
			return int(parsed)
		}
	}
	return 0
}

func nestedCachedTokens(raw interface{}) float64 {
	if details, ok := raw.(map[string]interface{}); ok {
		if cached, ok := details["cached_tokens"].(float64); ok {
			return cached
		}
	}
	return 0
}

// logUsageDetection 统一格式输出 usage 检测日志
func logUsageDetection(location string, usage map[string]interface{}, needPatch bool) {
	inputTokens := usage["input_tokens"]
	outputTokens := usage["output_tokens"]
	cacheCreation, _ := usage["cache_creation_input_tokens"].(float64)
	cacheRead, _ := usage["cache_read_input_tokens"].(float64)

	log.Printf("[Messages-Stream-Token] %s: InputTokens=%v, OutputTokens=%v, CacheCreation=%.0f, CacheRead=%.0f, 需补全=%v",
		location, inputTokens, outputTokens, cacheCreation, cacheRead, needPatch)
}

// HasEventWithUsage 检查事件是否包含 usage 字段
func HasEventWithUsage(event string) bool {
	for _, line := range strings.Split(event, "\n") {
		jsonStr, isData := utils.ParseSSEDataLine(line)
		if !isData {
			continue
		}

		var data map[string]interface{}
		if err := json.Unmarshal([]byte(jsonStr), &data); err != nil {
			continue
		}

		if _, ok := data["usage"].(map[string]interface{}); ok {
			return true
		}

		if msg, ok := data["message"].(map[string]interface{}); ok {
			if _, ok := msg["usage"].(map[string]interface{}); ok {
				return true
			}
		}
	}
	return false
}

// ExtractInputTokensFromEvent 从 SSE 事件中提取 input_tokens
// 支持 message_start 事件的 message.usage.input_tokens 和顶层 usage.input_tokens
func ExtractInputTokensFromEvent(event string) int {
	for _, line := range strings.Split(event, "\n") {
		jsonStr, isData := utils.ParseSSEDataLine(line)
		if !isData {
			continue
		}

		var data map[string]interface{}
		if err := json.Unmarshal([]byte(jsonStr), &data); err != nil {
			continue
		}

		// 检查 message.usage.input_tokens (message_start 事件)
		if msg, ok := data["message"].(map[string]interface{}); ok {
			if usage, ok := msg["usage"].(map[string]interface{}); ok {
				if v, ok := usage["input_tokens"].(float64); ok && v > 0 {
					return int(v)
				}
			}
		}

		// 检查顶层 usage.input_tokens (message_delta 事件)
		if usage, ok := data["usage"].(map[string]interface{}); ok {
			if v, ok := usage["input_tokens"].(float64); ok && v > 0 {
				return int(v)
			}
		}
	}
	return 0
}
