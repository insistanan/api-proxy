// 本文件负责把估算/补齐后的 usage "写回" SSE 事件：token 字段修补、message_start 的
// input_tokens 校验、message_delta 形态的 usage 事件构造，以及异常 usage 元组的重建。
// 读取（检测）侧在 stream_usage_detect.go。
package streams

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/BenedictKing/api-proxy/internal/utils"
)

// PatchTokensInEventWithCache 修补事件中的 token 字段。
// inferredCacheRead 当前只参与日志：cache_read_input_tokens 有意不写进下发客户端的 SSE
// （见函数内 "never write cache_read into client SSE"），所以传 0 与传正数对输出等价。
func PatchTokensInEventWithCache(event string, estimatedInputTokens, estimatedOutputTokens, inferredCacheRead int, hasCacheTokens bool, enableLog bool, lowQuality bool) string {
	patched, _ := utils.RewriteSSEDataLines(event, func(payload string) (string, bool) {
		var data map[string]interface{}
		if err := json.Unmarshal([]byte(payload), &data); err != nil {
			return "", false
		}

		// 修补顶层 usage
		if usage, ok := data["usage"].(map[string]interface{}); ok {
			patchUsageFieldsWithLog(usage, estimatedInputTokens, estimatedOutputTokens, hasCacheTokens, enableLog, "顶层usage", lowQuality)
			// 写入推断的 cache_read_input_tokens（仅当字段不存在时）
			if inferredCacheRead > 0 {
				if _, exists := usage["cache_read_input_tokens"]; !exists {
					_ = inferredCacheRead // never write cache_read into client SSE
					if enableLog {
						log.Printf("[Messages-Stream-Token] 顶层usage: 写入推断的 cache_read_input_tokens=%d", inferredCacheRead)
					}
				}
			}
		}

		// 修补 message.usage
		if msg, ok := data["message"].(map[string]interface{}); ok {
			if usage, ok := msg["usage"].(map[string]interface{}); ok {
				patchUsageFieldsWithLog(usage, estimatedInputTokens, estimatedOutputTokens, hasCacheTokens, enableLog, "message.usage", lowQuality)
				// 写入推断的 cache_read_input_tokens（仅当字段不存在时）
				if inferredCacheRead > 0 {
					if _, exists := usage["cache_read_input_tokens"]; !exists {
						_ = inferredCacheRead // never write cache_read into client SSE
						if enableLog {
							log.Printf("[Messages-Stream-Token] message.usage: 写入推断的 cache_read_input_tokens=%d", inferredCacheRead)
						}
					}
				}
			}
		}

		patchedJSON, err := json.Marshal(data)
		if err != nil {
			return "", false
		}
		return string(patchedJSON), true
	})
	return patched
}

func PatchTokensInEventDataWithCache(data map[string]interface{}, estimatedInputTokens, estimatedOutputTokens, inferredCacheRead int, hasCacheTokens bool, enableLog bool, lowQuality bool) {
	if data == nil {
		return
	}

	// 修补顶层 usage
	if usage, ok := data["usage"].(map[string]interface{}); ok {
		patchUsageFieldsWithLog(usage, estimatedInputTokens, estimatedOutputTokens, hasCacheTokens, enableLog, "顶层usage", lowQuality)
		// 写入推断的 cache_read_input_tokens（仅当字段不存在时）
		if inferredCacheRead > 0 {
			if _, exists := usage["cache_read_input_tokens"]; !exists {
				_ = inferredCacheRead // never write cache_read into client SSE
				if enableLog {
					log.Printf("[Messages-Stream-Token] 顶层usage: 写入推断的 cache_read_input_tokens=%d", inferredCacheRead)
				}
			}
		}
	}

	// 修补 message.usage
	if msg, ok := data["message"].(map[string]interface{}); ok {
		if usage, ok := msg["usage"].(map[string]interface{}); ok {
			patchUsageFieldsWithLog(usage, estimatedInputTokens, estimatedOutputTokens, hasCacheTokens, enableLog, "message.usage", lowQuality)
			// 写入推断的 cache_read_input_tokens（仅当字段不存在时）
			if inferredCacheRead > 0 {
				if _, exists := usage["cache_read_input_tokens"]; !exists {
					_ = inferredCacheRead // never write cache_read into client SSE
					if enableLog {
						log.Printf("[Messages-Stream-Token] message.usage: 写入推断的 cache_read_input_tokens=%d", inferredCacheRead)
					}
				}
			}
		}
	}
}

func patchUsageFieldsWithLog(usage map[string]interface{}, estimatedInput, estimatedOutput int, hasCacheTokens bool, enableLog bool, location string, lowQuality bool) {
	originalInput := usage["input_tokens"]
	originalOutput := usage["output_tokens"]
	inputPatched := false
	outputPatched := false

	// CRITICAL for Cursor compact thrash:
	// estimatedInput often comes from EstimateRequestTokens(full requestBody) which is the
	// entire Cursor transcript (100k-300k). NEVER replace a real positive input_tokens with
	// a larger estimate. Only fill missing/zero/nil input. Admin logs still use CollectedUsage.
	_ = hasCacheTokens
	_ = lowQuality

	if v, ok := usage["input_tokens"].(float64); ok {
		currentInput := int(v)
		if currentInput <= 1 && estimatedInput > 1 {
			// Only fill near-zero missing values, never inflate.
			// Cap estimate so a buggy caller cannot push the whole long context into the client meter.
			safeEstimate := utils.SafeEstimatedInputTokens(estimatedInput)
			if safeEstimate > 1 {
				usage["input_tokens"] = safeEstimate
				inputPatched = true
			}
		}
	} else if usage["input_tokens"] == nil {
		// nil: leave unset or use tiny safe fill only
		safeEstimate := utils.SafeEstimatedInputTokens(estimatedInput)
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

// PatchMessageStartInputTokensIfNeeded 在首个 message_start 事件中尽早补全 input_tokens。
//
// 部分客户端（例如终端工具）只读取首个 usage 来累计 prompt tokens；如果 message_start 的 input_tokens 为 0/极小值，
// 即便后续顶层 usage 给出正确值，也可能导致累计失败。
func PatchMessageStartInputTokensIfNeeded(event string, requestBody []byte, needInputPatch bool, usageData CollectedUsageData, hasUsage bool, enableLog bool, lowQuality bool) string {
	if !IsMessageStartEvent(event) {
		return event
	}
	if !hasUsage {
		return event
	}

	// Do NOT replace real/missing input with EstimateRequestTokens(requestBody).
	// requestBody is Cursor full history (often 100k-300k tokens). Writing that into
	// message_start makes Cursor Conversation full immediately after compact and
	// triggers compact thrash (admin may show in~21k while client saw estimated 200k+).
	// Real usage arrives on message_delta; leave message_start alone when risky.
	_ = requestBody
	_ = needInputPatch
	_ = usageData
	_ = enableLog
	_ = lowQuality
	return event
}

// BuildUsageEvent 构建带 usage 的 message_delta SSE 事件
func BuildUsageEvent(requestBody []byte, outputText string) string {
	inputTokens := 0 // do not estimate full Cursor body for client
	outputTokens := utils.EstimateTokens(outputText)

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

// StripCacheFieldsFromClaudeSSE removes cache_creation_* fields from Claude SSE.
//
// Deprecated: Anthropic 客户端需要完整缓存字段计算上下文规模，生产出口不得调用本函数。
// 保留它只用于兼容已有包内特征测试和可能的定向诊断工具。
//
// cache_read_input_tokens is preserved so clients can correctly assess
// cached context size and trigger conversation compaction.
// Admin metrics have already collected the full usage earlier in the pipeline.
// Note the resulting asymmetry: an Anthropic client computing
// total = input_tokens + cache_read + cache_creation now sees cache_creation as absent,
// so it under-counts on the turn that first writes a cache entry. That is the pre-existing
// behaviour of this function and is deliberately left as-is; handlers/messages mirrors it
// for non-streaming responses so both exits present one contract.
func StripCacheFieldsFromClaudeSSE(event string) string {
	if event == "" {
		return event
	}
	// Only check for cache_creation fields; cache_read_input_tokens is preserved
	// so clients (e.g. Cursor) can correctly assess cached context for compaction.
	if !strings.Contains(event, "cache_creation") &&
		!strings.Contains(event, "cache_ttl") {
		return event
	}
	lines := strings.Split(event, "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		payload, isData := utils.SSEDataJSON(trimmed)
		if !isData {
			continue
		}
		var root map[string]interface{}
		if err := json.Unmarshal([]byte(payload), &root); err != nil {
			continue
		}
		changed := false
		stripUsage := func(usage map[string]interface{}) {
			for _, key := range []string{
				"cache_creation_input_tokens",
				"cache_creation_5m_input_tokens",
				"cache_creation_1h_input_tokens",
				"cache_ttl",
			} {
				if _, exists := usage[key]; exists {
					delete(usage, key)
					changed = true
				}
			}
		}
		if usage, ok := root["usage"].(map[string]interface{}); ok {
			stripUsage(usage)
		}
		if message, ok := root["message"].(map[string]interface{}); ok {
			if usage, ok := message["usage"].(map[string]interface{}); ok {
				stripUsage(usage)
			}
		}
		if !changed {
			continue
		}
		encoded, err := json.Marshal(root)
		if err != nil {
			continue
		}
		// Preserve "data: " prefix style
		if strings.HasPrefix(line, "data: ") {
			lines[i] = "data: " + string(encoded)
		} else {
			lines[i] = "data:" + string(encoded)
		}
	}
	return strings.Join(lines, "\n")
}

// sanityCheckMessageStreamInputTokens 校验 message_start.message.usage 与顶层 usage 中的
// input_tokens 是否被上游错报，需要时重建客户端副本。
//
// Anthropic 语义：input_tokens 不含缓存，上游声明的总量 = input + cache_read + cache_creation
// 缓存创建总量优先使用 cache_creation_input_tokens；5m/1h 只是总量明细，只有总字段
// 缺失时才求和。若缓存量本身不可信，则连同缓存明细一起从客户端副本清除。
//
// 只改返回的事件字符串；ctx.CollectedUsage 与指标路径一律保持上游原值。
func sanityCheckMessageStreamInputTokens(event string, requestBody []byte, enableLog bool) (string, bool) {
	if len(requestBody) == 0 {
		return event, false
	}
	if !strings.Contains(event, "input_tokens") {
		return event, false
	}
	rewritten, changed := utils.RewriteSSEDataLines(event, func(payload string) (string, bool) {
		var root map[string]interface{}
		if err := json.Unmarshal([]byte(payload), &root); err != nil {
			return "", false
		}
		usage := claudeUsageFromEvent(root)
		if usage == nil {
			return "", false
		}
		upstreamUsage := extractUsageFromMap(usage)
		correction, need := utils.SanityCheckedAnthropicUsageForRequest(
			requestBody,
			utils.EstimateRequestTokens(requestBody),
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
				correctedCached = utils.AnthropicCachedInputTokens(
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
