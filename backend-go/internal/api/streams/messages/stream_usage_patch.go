// 本文件负责把估算/补齐后的 usage "写回" SSE 事件：token 字段修补、message_start 的
// input_tokens 校验、message_delta 形态的 usage 事件构造，以及异常 usage 元组的重建。
// 读取（检测）侧在 stream_usage_detect.go。
package messages

import (
	"encoding/json"
	"fmt"
	"insistanan/api-proxy/internal/protocol/codec"
	"insistanan/api-proxy/internal/protocol/tokenusage"
	"log"
	"strings"
)

// PatchTokensInEvent 只修补输入、输出量，保留上游明确报告的缓存字段。
func PatchTokensInEvent(event string, estimatedInputTokens, estimatedOutputTokens int, enableLog bool) string {
	patched, _ := codec.RewriteSSEDataLines(event, func(payload string) (string, bool) {
		var data map[string]interface{}
		if err := json.Unmarshal([]byte(payload), &data); err != nil {
			return "", false
		}
		PatchTokensInEventData(data, estimatedInputTokens, estimatedOutputTokens, enableLog)
		patchedJSON, err := json.Marshal(data)
		if err != nil {
			return "", false
		}
		return string(patchedJSON), true
	})
	return patched
}

func PatchTokensInEventData(data map[string]interface{}, estimatedInputTokens, estimatedOutputTokens int, enableLog bool) {
	if usage, ok := data["usage"].(map[string]interface{}); ok {
		patchUsageFieldsWithLog(usage, estimatedInputTokens, estimatedOutputTokens, enableLog, "顶层usage")
	}
	if msg, ok := data["message"].(map[string]interface{}); ok {
		if usage, ok := msg["usage"].(map[string]interface{}); ok {
			patchUsageFieldsWithLog(usage, estimatedInputTokens, estimatedOutputTokens, enableLog, "message.usage")
		}
	}
}

func patchUsageFieldsWithLog(usage map[string]interface{}, estimatedInput, estimatedOutput int, enableLog bool, location string) {
	originalInput := usage["input_tokens"]
	originalOutput := usage["output_tokens"]
	inputPatched := false
	outputPatched := false

	// 仅补全缺失或接近零的输入量，避免用全量历史估算扩大客户端上下文计数。
	// 指标与管理日志读取独立的 UpstreamUsage，不使用此处修补后的值。

	if v, ok := usage["input_tokens"].(float64); ok {
		currentInput := int(v)
		if currentInput <= 1 && estimatedInput > 1 {
			// 估算仍走公共约束，避免调用方传入全量历史导致计数膨胀。
			safeEstimate := tokenusage.SafeEstimatedInputTokens(estimatedInput)
			if safeEstimate > 1 {
				usage["input_tokens"] = safeEstimate
				inputPatched = true
			}
		}
	} else if usage["input_tokens"] == nil {
		// 没有可靠补全值时保留缺失字段。
		safeEstimate := tokenusage.SafeEstimatedInputTokens(estimatedInput)
		if safeEstimate > 1 {
			usage["input_tokens"] = safeEstimate
			inputPatched = true
		}
	}

	if v, ok := usage["output_tokens"].(float64); ok {
		currentOutput := int(v)
		if currentOutput <= 1 && estimatedOutput > currentOutput && estimatedOutput > 1 {
			usage["output_tokens"] = estimatedOutput
			outputPatched = true
		}
	} else if usage["output_tokens"] == nil && estimatedOutput > 0 {
		usage["output_tokens"] = estimatedOutput
		outputPatched = true
	}

	if enableLog {
		if inputPatched || outputPatched {
			log.Printf("[Messages-Stream-Token-Patch] %s: InputTokens %v -> %v, OutputTokens %v -> %v",
				location, originalInput, usage["input_tokens"], originalOutput, usage["output_tokens"])
		}
	}
}

// BuildUsageEvent 构建带 usage 的 message_delta SSE 事件
func BuildUsageEvent(outputText string) string {
	inputTokens := 0 // 上游未提供输入量，不把全量历史估算写给客户端。
	outputTokens := tokenusage.EstimateTokens(outputText)

	event := map[string]interface{}{
		"type": "message_delta",
		"usage": map[string]int{
			"input_tokens":  inputTokens,
			"output_tokens": outputTokens,
		},
	}
	eventJSON, _ := json.Marshal(event)
	return fmt.Sprintf("event: message_delta\ndata: %s\n\n", eventJSON)
}

// sanityCheckMessageStreamInputTokens 校验 message_start.message.usage 与顶层 usage 中的
// input_tokens 是否被上游错报，需要时重建客户端副本。
//
// Anthropic 语义：input_tokens 不含缓存，上游声明的总量 = input + cache_read + cache_creation
// 缓存创建总量优先使用 cache_creation_input_tokens；5m/1h 只是总量明细，只有总字段
// 缺失时才求和。若缓存量本身不可信，则连同缓存明细一起从客户端副本清除。
//
// 只改返回的事件字符串；ctx.UpstreamUsage 与指标路径保持上游原值。
func sanityCheckMessageStreamInputTokens(event string, requestBody []byte, enableLog bool) (string, bool) {
	if len(requestBody) == 0 {
		return event, false
	}
	if !strings.Contains(event, "input_tokens") {
		return event, false
	}
	rewritten, changed := codec.RewriteSSEDataLines(event, func(payload string) (string, bool) {
		var root map[string]interface{}
		if err := json.Unmarshal([]byte(payload), &root); err != nil {
			return "", false
		}
		usage := claudeUsageFromEvent(root)
		if usage == nil {
			return "", false
		}
		upstreamUsage := extractUsageFromMap(usage)
		correction, need := tokenusage.SanityCheckedAnthropicUsageForRequest(
			requestBody,
			tokenusage.EstimateRequestTokens(requestBody),
			upstreamUsage.InputTokens,
			upstreamUsage.CacheReadInputTokens,
			upstreamUsage.CacheCreationInputTokens,
			upstreamUsage.CacheCreation5mInputTokens,
			upstreamUsage.CacheCreation1hInputTokens,
		)
		if !need {
			return "", false
		}
		usage["input_tokens"] = correction.InputTokens
		if correction.ClearCache {
			clearAnthropicCacheUsageFields(usage)
		}
		// Anthropic usage 无 total_tokens 字段；若上游带了就同步，避免留下与 input 矛盾的值
		if _, hasTotal := usage["total_tokens"]; hasTotal {
			correctedCached := 0
			if !correction.ClearCache {
				correctedCached = tokenusage.AnthropicCachedInputTokens(
					upstreamUsage.CacheReadInputTokens,
					upstreamUsage.CacheCreationInputTokens,
					upstreamUsage.CacheCreation5mInputTokens,
					upstreamUsage.CacheCreation1hInputTokens,
				)
			}
			usage["total_tokens"] = correction.InputTokens + correctedCached + upstreamUsage.OutputTokens
		}
		if enableLog {
			log.Printf("[Messages-Stream-Token] 上游 usage 错报校正: input_tokens=%d->%d, clear_cache=%v（本地估算，仅改下发值）",
				upstreamUsage.InputTokens, correction.InputTokens, correction.ClearCache)
		}
		encoded, err := json.Marshal(root)
		if err != nil {
			return "", false
		}
		return string(encoded), true
	})
	return rewritten, changed
}

func claudeUsageFromEvent(root map[string]interface{}) map[string]interface{} {
	if usage, ok := root["usage"].(map[string]interface{}); ok {
		return usage
	}
	message, ok := root["message"].(map[string]interface{})
	if !ok {
		return nil
	}
	usage, _ := message["usage"].(map[string]interface{})
	return usage
}

func clearAnthropicCacheUsageFields(usage map[string]interface{}) {
	for _, key := range []string{
		"cache_read_input_tokens",
		"cache_creation_input_tokens",
		"cache_creation_5m_input_tokens",
		"cache_creation_1h_input_tokens",
		"cache_ttl",
		"input_tokens_details",
		"prompt_tokens_details",
	} {
		delete(usage, key)
	}
}
