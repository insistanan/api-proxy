package responses

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/insistanan/api-proxy/internal/infra/config"
	"github.com/insistanan/api-proxy/internal/infra/utils"
	"github.com/insistanan/api-proxy/internal/protocol/types"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// prepareResponsesUsageRequestBody 返回用于客户端 usage 修正的请求体。
//
// provider 在压缩/完整历史边界上会从实际上游请求中删除 previous_response_id，
// 但 handler 仍保留原始客户端请求体用于会话持久化和响应转换。若这里继续使用原始
// 请求体，usage 处理会把已经切断的边界误判为链式增量，从而跳过缓存剥离和本地
// 上下文规模校正，客户端就会在压缩后继续看到旧的上下文占用。
func prepareResponsesUsageRequestBody(requestBody []byte, historyBoundary bool) ([]byte, error) {
	if !historyBoundary || !hasPreviousResponseID(requestBody) {
		return requestBody, nil
	}
	trimmed, err := sjson.DeleteBytes(requestBody, "previous_response_id")
	if err != nil {
		return nil, fmt.Errorf("删除 usage 修正请求体中的 previous_response_id 失败: %w", err)
	}
	return trimmed, nil
}

func hasPreviousResponseID(requestBody []byte) bool {
	if len(requestBody) == 0 {
		return false
	}
	return strings.TrimSpace(gjson.GetBytes(requestBody, "previous_response_id").String()) != ""
}

// isCodexResponsesRequest 判断请求是否带有 Codex 专用的 client_metadata。
// Codex 的 input_tokens 可能包含服务端注入的工具定义、缓存上下文和内部历史，
// 不能用代理对请求体的近似估算覆盖上游 usage；否则客户端会看到远小于真实值的上下文，
// 自动压缩永远不会按真实窗口触发。
func isCodexResponsesRequest(requestBody []byte) bool {
	if len(requestBody) == 0 {
		return false
	}

	var request struct {
		ClientMetadata map[string]json.RawMessage `json:"client_metadata"`
	}
	if err := json.Unmarshal(requestBody, &request); err != nil {
		return false
	}

	for _, key := range []string{
		"x-codex-window-id",
		"x-codex-installation-id",
		"x-codex-turn-metadata",
	} {
		raw, exists := request.ClientMetadata[key]
		if !exists {
			continue
		}
		value := strings.TrimSpace(string(raw))
		if value != "" && value != "null" && value != `""` {
			return true
		}
	}
	return false
}

// estimateResponsesInputForClient 仅在请求体代表本次完整上下文时提供安全估算。
// previous_response_id 的 input 只是一轮增量，Codex 请求还可能由服务端注入工具
// 定义和内部历史；这两类都不能拿本地 request body 冒充全量 input_tokens。
func estimateResponsesInputForClient(requestBody []byte) (int, bool) {
	if hasPreviousResponseID(requestBody) || isCodexResponsesRequest(requestBody) {
		return 0, false
	}
	estimated := utils.SafeEstimatedInputTokens(utils.EstimateResponsesRequestTokens(requestBody))
	if estimated <= 0 {
		return 0, false
	}
	return estimated, true
}

// patchResponsesUsage 补全 Responses 响应的 Token 统计
func patchResponsesUsage(resp *types.ResponsesResponse, requestBody []byte, envCfg *config.EnvConfig) {
	// 检查是否有 Claude 原生缓存 token（有时才跳过 input_tokens 修补）
	// 仅检测 Claude 原生字段：cache_creation_input_tokens, cache_read_input_tokens,
	// cache_creation_5m_input_tokens, cache_creation_1h_input_tokens
	// 注意：不检测 input_tokens_details.cached_tokens（OpenAI 格式），避免错误跳过
	hasClaudeCache := resp.Usage.CacheCreationInputTokens > 0 ||
		resp.Usage.CacheReadInputTokens > 0 ||
		resp.Usage.CacheCreation5mInputTokens > 0 ||
		resp.Usage.CacheCreation1hInputTokens > 0

	// 检查是否需要补全
	needInputPatch := resp.Usage.InputTokens <= 0 && !hasClaudeCache
	needOutputPatch := resp.Usage.OutputTokens <= 0

	// 如果 usage 完全为空，只对可由请求体代表的完整上下文估算 input；链式/Codex
	// 请求的 input 由服务端掌握，不能用单轮 body 覆盖。output 仍可按已返回内容估算。
	if resp.Usage.InputTokens == 0 && resp.Usage.OutputTokens == 0 && resp.Usage.TotalTokens == 0 && !hasClaudeCache {
		estimatedInput, _ := estimateResponsesInputForClient(requestBody)
		estimatedOutput := estimateResponsesOutputFromItems(resp.Output)
		resp.Usage.InputTokens = estimatedInput
		resp.Usage.OutputTokens = estimatedOutput
		resp.Usage.TotalTokens = estimatedInput + estimatedOutput
		if envCfg.Current().EnableResponseLogs {
			log.Printf("[Responses-Token] 上游无Usage, 本地估算: input=%d, output=%d", estimatedInput, estimatedOutput)
		}
		return
	}

	// 只补全缺失统计，保留上游的有效正值（包括 1 token）。
	originalInput := resp.Usage.InputTokens
	originalOutput := resp.Usage.OutputTokens
	patched := false

	if needInputPatch {
		if estimatedInput, ok := estimateResponsesInputForClient(requestBody); ok {
			resp.Usage.InputTokens = estimatedInput
			patched = true
		}
	}
	if needOutputPatch {
		resp.Usage.OutputTokens = estimateResponsesOutputFromItems(resp.Output)
		patched = true
	}

	// 重新计算 TotalTokens（修补时或 total_tokens 为 0 但 input/output 有效时）
	if patched || (resp.Usage.TotalTokens == 0 && (resp.Usage.InputTokens > 0 || resp.Usage.OutputTokens > 0)) {
		resp.Usage.TotalTokens = resp.Usage.InputTokens + resp.Usage.OutputTokens
	}

	if envCfg.Current().EnableResponseLogs {
		if patched {
			log.Printf("[Responses-Token] 缺失统计补全: InputTokens=%d->%d, OutputTokens=%d->%d",
				originalInput, resp.Usage.InputTokens, originalOutput, resp.Usage.OutputTokens)
		}
		log.Printf("[Responses-Token] InputTokens=%d, OutputTokens=%d, TotalTokens=%d, CacheCreation=%d, CacheRead=%d, CacheCreation5m=%d, CacheCreation1h=%d, CacheTTL=%s",
			resp.Usage.InputTokens, resp.Usage.OutputTokens, resp.Usage.TotalTokens,
			resp.Usage.CacheCreationInputTokens, resp.Usage.CacheReadInputTokens,
			resp.Usage.CacheCreation5mInputTokens, resp.Usage.CacheCreation1hInputTokens,
			resp.Usage.CacheTTL)
	}
}

// estimateResponsesOutputFromItems 从 ResponsesItem 的语义字段估算输出 token。
// 该函数只用于上游漏报/错报时给客户端做有限补全，不是计费数据源。尤其不能
// 把完整 response.completed JSON envelope 当作正文估算，否则 envelope 的 id、
// status、usage 等控制字段会被误算成模型输出。
func estimateResponsesOutputFromItems(output []types.ResponsesItem) int {
	total := 0
	for _, item := range output {
		switch item.Type {
		case "message", "text":
			total += estimateResponsesContentTokens(item.Content)
		case "reasoning":
			total += estimateResponsesContentTokens(item.Summary)
			total += estimateResponsesContentTokens(item.Content)
		case "function_call", "custom_tool_call", "tool_search_call":
			total += estimateResponsesStringTokens(item.Name)
			total += estimateResponsesStringTokens(item.Arguments)
			if item.Type == "custom_tool_call" {
				total += estimateResponsesContentTokens(item.Content)
			}
		case "function_call_output", "custom_tool_call_output", "tool_search_output", "tool_result":
			total += estimateResponsesContentTokens(item.Content)
			if item.Type == "tool_search_output" {
				total += estimateResponsesContentTokens(item.Tools)
			}
		default:
			total += estimateResponsesContentTokens(item.Content)
			total += estimateResponsesContentTokens(item.Summary)
			total += estimateResponsesStringTokens(item.Arguments)
			total += estimateResponsesContentTokens(item.Tools)
		}

		if item.ToolUse != nil {
			total += estimateResponsesStringTokens(item.ToolUse.Name)
			total += estimateResponsesContentTokens(item.ToolUse.Input)
		}
	}
	return total
}

func estimateResponsesStringTokens(value string) int {
	if strings.TrimSpace(value) == "" {
		return 0
	}
	return utils.EstimateTokens(value)
}

func estimateResponsesContentTokens(content interface{}) int {
	if content == nil {
		return 0
	}
	if text, ok := content.(string); ok {
		return estimateResponsesStringTokens(text)
	}

	blocks := utils.NormalizeContentBlocks(content)
	if len(blocks) > 0 {
		total := 0
		for _, block := range blocks {
			if text, ok := utils.ExtractTextFromBlock(block); ok {
				total += estimateResponsesStringTokens(text)
				continue
			}
			data, err := json.Marshal(block)
			if err == nil {
				total += utils.EstimateTokens(string(data))
			}
		}
		return total
	}

	data, err := json.Marshal(content)
	if err != nil || string(data) == "null" {
		return 0
	}
	return utils.EstimateTokens(string(data))
}

// checkResponsesEventUsage 检测 Responses 事件是否包含 usage
func checkResponsesEventUsage(event string, enableLog bool) (bool, bool, responsesStreamUsage) {
	lines := strings.Split(event, "\n")
	for _, line := range lines {
		jsonStr, isData := utils.SSEDataJSON(line)
		if !isData {
			continue
		}

		var data map[string]interface{}
		if err := json.Unmarshal([]byte(jsonStr), &data); err != nil {
			continue
		}

		eventType, _ := data["type"].(string)

		// 完成与达到输出限制都携带最终 usage，不能把 incomplete 的真实值丢掉。
		if eventType == "response.completed" || eventType == "response.incomplete" {
			if response, ok := data["response"].(map[string]interface{}); ok {
				if usage, ok := response["usage"].(map[string]interface{}); ok {
					usageData := extractResponsesUsageFromMap(usage)
					// 拆分缓存后，未缓存输入可以合法地为 0；是否缺失应看上游原字段。
					rawInput, _ := usage["input_tokens"].(float64)
					needPatch := rawInput <= 0 && !usageData.HasClaudeCache || usageData.OutputTokens <= 0

					// 检查 total_tokens 是否需要补全（有效 input/output 但 total=0）
					if !needPatch && usageData.TotalTokens == 0 && (usageData.InputTokens > 0 || usageData.OutputTokens > 0) {
						needPatch = true
					}

					if enableLog {
						log.Printf("[Responses-Stream-Token] %s: InputTokens=%d, OutputTokens=%d, TotalTokens=%d, CacheCreation=%d, CacheRead=%d, HasClaudeCache=%v, 需补全=%v",
							eventType, usageData.InputTokens, usageData.OutputTokens, usageData.TotalTokens, usageData.CacheCreationInputTokens, usageData.CacheReadInputTokens, usageData.HasClaudeCache, needPatch)
					}
					return true, needPatch, usageData
				} else if enableLog {
					log.Printf("[Responses-Stream-Token] %s 事件中无 usage 字段", eventType)
				}
			} else if enableLog {
				log.Printf("[Responses-Stream-Token] %s 事件中无 response 字段", eventType)
			}
		}
	}
	return false, false, responsesStreamUsage{}
}

// extractResponsesUsageFromMap 从 usage map 中提取数据
func extractResponsesUsageFromMap(usage map[string]interface{}) responsesStreamUsage {
	var data responsesStreamUsage

	if v, ok := usage["input_tokens"].(float64); ok {
		data.InputTokens = int(v)
	}
	if v, ok := usage["output_tokens"].(float64); ok {
		data.OutputTokens = int(v)
	}
	if v, ok := usage["total_tokens"].(float64); ok {
		data.TotalTokens = int(v)
	}
	if v, ok := usage["cache_creation_input_tokens"].(float64); ok {
		data.CacheCreationInputTokens = int(v)
		if v > 0 {
			data.HasClaudeCache = true
		}
	}
	hasExplicitCacheRead := false
	if v, ok := usage["cache_read_input_tokens"].(float64); ok {
		data.CacheReadInputTokens = int(v)
		hasExplicitCacheRead = v > 0
		if v > 0 {
			data.HasClaudeCache = true
		}
	}
	if v, ok := usage["cache_creation_5m_input_tokens"].(float64); ok {
		data.CacheCreation5mInputTokens = int(v)
		if v > 0 {
			data.HasClaudeCache = true
		}
	}
	if v, ok := usage["cache_creation_1h_input_tokens"].(float64); ok {
		data.CacheCreation1hInputTokens = int(v)
		if v > 0 {
			data.HasClaudeCache = true
		}
	}

	// 检查 input_tokens_details.cached_tokens (OpenAI 格式，不设置 HasClaudeCache)
	openAICachedTokens := openAICachedTokensFromUsageMap(usage)
	if openAICachedTokens > 0 {
		// 仅当 CacheReadInputTokens 未被设置时才使用 OpenAI 的 cached_tokens
		if data.CacheReadInputTokens == 0 {
			data.CacheReadInputTokens = openAICachedTokens
		}
		// cache_read_input_tokens=0 只是零值占位，不能阻止 OpenAI
		// cached_tokens 的子集拆分；只有明确的非零 Anthropic cache-read
		// 才表示 input_tokens 已经按“未缓存部分”上报。
		// 累积式上游（cached_tokens 超过本次 input_tokens）不满足子集前提，
		// 按子集去减会把上报的输入量压到 0，指标与计费都会失真。
		if !hasExplicitCacheRead && !openAICacheLooksAccumulated(openAICachedTokens, data.InputTokens) {
			data.InputTokens -= openAICachedTokens
			if data.InputTokens < 0 {
				data.InputTokens = 0
			}
			// cached_tokens 已从 input_tokens 中拆出；total_tokens 也必须
			// 使用同一口径，不能保留包含缓存子集的原始总量。
			data.TotalTokens = data.InputTokens + data.OutputTokens
		}
		// 注意：不设置 HasClaudeCache，因为这是 OpenAI 格式
	}

	// 设置 CacheTTL
	var has5m, has1h bool
	if data.CacheCreation5mInputTokens > 0 {
		has5m = true
	}
	if data.CacheCreation1hInputTokens > 0 {
		has1h = true
	}
	if has5m && has1h {
		data.CacheTTL = "mixed"
	} else if has1h {
		data.CacheTTL = "1h"
	} else if has5m {
		data.CacheTTL = "5m"
	}

	return data
}

// openAICachedTokensFromUsageMap 读取 OpenAI 格式的缓存命中量。
// Anthropic 的 cache_read_input_tokens 语义不同（input_tokens 已扣除缓存），不在此列。
func openAICachedTokensFromUsageMap(usage map[string]interface{}) int {
	for _, key := range []string{"input_tokens_details", "prompt_tokens_details"} {
		details, ok := usage[key].(map[string]interface{})
		if !ok {
			continue
		}
		if cached, ok := details["cached_tokens"].(float64); ok && cached > 0 {
			return int(cached)
		}
	}
	return 0
}

// openAICacheLooksAccumulated 判断 OpenAI 格式的 cached_tokens 是否是跨请求累积值。
//
// OpenAI 语义下 input_tokens 已经包含缓存命中部分，cached_tokens 必须是它的子集。
// 累积式上游（grok-4.6 等）把 cached_tokens 当成跨请求单调递增的命中总量上报，很快
// 就会超过本次 input_tokens。该不等式是自证的：不依赖本地估算，也不依赖请求是否
// 携带 previous_response_id，因此链式增量请求同样可以用它判断缓存字段的语义。
//
// inputTokens <= 0 时无从比较（上游漏报），按“无法判定”处理，保留上游原值。
func openAICacheLooksAccumulated(cachedTokens, inputTokens int) bool {
	return cachedTokens > 0 && inputTokens > 0 && cachedTokens > inputTokens
}

func usageMapHasAccumulatedOpenAICache(usage map[string]interface{}) bool {
	return openAICacheLooksAccumulated(openAICachedTokensFromUsageMap(usage), usageTokenCount(usage, "input_tokens"))
}

func responsesUsageHasAccumulatedOpenAICache(usage *types.ResponsesUsage) bool {
	if usage == nil || usage.InputTokensDetails == nil {
		return false
	}
	return openAICacheLooksAccumulated(usage.InputTokensDetails.CachedTokens, usage.InputTokens)
}

// updateResponsesStreamUsage 更新收集的 usage 数据
func updateResponsesStreamUsage(collected *responsesStreamUsage, usageData responsesStreamUsage) {
	if usageData.InputTokens > collected.InputTokens {
		collected.InputTokens = usageData.InputTokens
	}
	if usageData.OutputTokens > collected.OutputTokens {
		collected.OutputTokens = usageData.OutputTokens
	}
	if usageData.TotalTokens > collected.TotalTokens {
		collected.TotalTokens = usageData.TotalTokens
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
	// 传播 HasClaudeCache 标志
	if usageData.HasClaudeCache {
		collected.HasClaudeCache = true
	}
}

// rewriteResponsesCompletedEvent 统一处理结束事件的 JSON 解析、序列化与 SSE 保真。
// 先处理独立 data 行；没有完整结束事件时，再合并连续 data 行中的 JSON 分片。
func rewriteResponsesCompletedEvent(event string, mutate func(map[string]interface{}) bool) (string, bool) {
	found := false
	rewrite := func(payload string) (string, bool) {
		var data map[string]interface{}
		if json.Unmarshal([]byte(payload), &data) != nil || data["type"] != "response.completed" {
			return "", false
		}
		found = true
		response, ok := data["response"].(map[string]interface{})
		if !ok {
			return "", false
		}
		if !mutate(response) {
			return "", false
		}
		encoded, err := json.Marshal(data)
		if err != nil {
			log.Printf("[Responses-Stream-Token] 序列化结束事件失败: %v", err)
			return "", false
		}
		return string(encoded), true
	}
	rewritten, changed := utils.RewriteSSEDataLines(event, rewrite)
	if found {
		return rewritten, changed
	}

	lines := strings.Split(event, "\n")
	for start := 0; start < len(lines); start++ {
		if _, ok := utils.ParseSSEDataLine(lines[start]); !ok {
			continue
		}
		end := start
		var payload strings.Builder
		for end < len(lines) {
			part, ok := utils.ParseSSEDataLine(lines[end])
			if !ok {
				break
			}
			payload.WriteString(part)
			end++
		}
		if end-start > 1 {
			if encoded, ok := rewrite(payload.String()); ok {
				rebuilt := append([]string{}, lines[:start]...)
				rebuilt = append(rebuilt, utils.SSEDataLinePrefix+encoded)
				rebuilt = append(rebuilt, lines[end:]...)
				return strings.Join(rebuilt, "\n"), true
			}
		}
		start = end - 1
	}
	return event, false
}

func injectResponsesUsageToCompletedEvent(event string, requestBody []byte, outputText string, envCfg *config.EnvConfig) (string, int, int) {
	return injectResponsesUsageToCompletedEventWithTokens(event, requestBody, utils.EstimateTokens(outputText), envCfg)
}

func injectResponsesUsageToCompletedEventWithTokens(event string, requestBody []byte, outputTokens int, envCfg *config.EnvConfig) (string, int, int) {
	inputTokens, _ := estimateResponsesInputForClient(requestBody)
	rewritten, changed := injectUsageIntoMultiLineDataEvent(event, inputTokens, outputTokens, inputTokens+outputTokens)
	if !changed && envCfg.Current().EnableResponseLogs {
		log.Printf("[Responses-Stream-Token] 未能向结束事件注入缺失的 usage")
	}
	return rewritten, inputTokens, outputTokens
}

func injectUsageIntoMultiLineDataEvent(event string, inputTokens, outputTokens, totalTokens int) (string, bool) {
	return rewriteResponsesCompletedEvent(event, func(response map[string]interface{}) bool {
		response["usage"] = map[string]interface{}{
			"input_tokens": inputTokens, "output_tokens": outputTokens, "total_tokens": totalTokens,
		}
		return true
	})
}

func patchResponsesCompletedEventUsage(event string, requestBody []byte, outputText string, collected *responsesStreamUsage, envCfg *config.EnvConfig) string {
	return patchResponsesCompletedEventUsageWithTokens(event, requestBody, utils.EstimateTokens(outputText), collected, envCfg)
}

func patchResponsesCompletedEventUsageWithTokens(event string, requestBody []byte, outputTokens int, collected *responsesStreamUsage, envCfg *config.EnvConfig) string {
	rewritten, _ := rewriteResponsesCompletedEvent(event, func(response map[string]interface{}) bool {
		usage, ok := response["usage"].(map[string]interface{})
		return ok && patchCompletedUsage(usage, requestBody, outputTokens, collected, envCfg)
	})
	return rewritten
}

func patchCompletedUsage(usage map[string]interface{}, requestBody []byte, outputTokens int, collected *responsesStreamUsage, envCfg *config.EnvConfig) bool {
	originalInput, originalOutput := collected.InputTokens, collected.OutputTokens
	rawInput := usageTokenCount(usage, "input_tokens")
	patched := false
	if rawInput <= 0 && collected.InputTokens <= 0 && !collected.HasClaudeCache {
		if input, ok := estimateResponsesInputForClient(requestBody); ok {
			usage["input_tokens"] = input
			rawInput = input
			collected.InputTokens = input
			patched = true
		}
	}
	if collected.OutputTokens <= 0 {
		usage["output_tokens"] = outputTokens
		collected.OutputTokens = outputTokens
		patched = true
	}
	changed := patched || (usageTokenCount(usage, "total_tokens") == 0 && (collected.InputTokens > 0 || collected.OutputTokens > 0))
	if changed {
		collected.TotalTokens = collected.InputTokens + collected.OutputTokens
		// 下发事件使用上游输入口径；采集值可能已拆分缓存，不能混用。
		usage["total_tokens"] = rawInput + collected.OutputTokens
	}
	if patched && envCfg.Current().EnableResponseLogs && envCfg.ShouldLog("debug") {
		log.Printf("[Responses-Stream-Token] 缺失统计补全: InputTokens=%d->%d, OutputTokens=%d->%d",
			originalInput, collected.InputTokens, originalOutput, collected.OutputTokens)
	}
	return changed
}

// usageTokenCount 同时读取 JSON 解码后的 float64 和本地补全写入的 int，
// 让多个修正规则可共享同一份 map，避免每条规则之间重新编解码 JSON。
func usageTokenCount(usage map[string]interface{}, key string) int {
	switch value := usage[key].(type) {
	case float64:
		return int(value)
	case int:
		return value
	default:
		return 0
	}
}

// stripAccumulatedCacheFromCompletedEvent 仅修改客户端副本，保留原始采集值。
// 链式请求只剥离 cached_tokens 超过 input_tokens 的自证累积缓存；
// Claude 原生缓存创建字段表示真实缓存语义，必须保留 cache_read_input_tokens。
func stripAccumulatedCacheFromCompletedEvent(event string, requestBody []byte) string {
	rewritten, _ := rewriteResponsesCompletedEvent(event, func(response map[string]interface{}) bool {
		usage, ok := response["usage"].(map[string]interface{})
		return ok && stripCompletedCache(usage, hasPreviousResponseID(requestBody))
	})
	return rewritten
}

func stripCompletedCache(usage map[string]interface{}, chained bool) bool {
	_, hasCache := usage["cache_read_input_tokens"]
	for _, key := range []string{"input_tokens_details", "prompt_tokens_details"} {
		if details, ok := usage[key].(map[string]interface{}); ok {
			_, cached := details["cached_tokens"]
			hasCache = hasCache || cached
		}
	}
	if !hasCache {
		return false
	}
	if chained && !usageMapHasAccumulatedOpenAICache(usage) {
		return false
	}
	changed := false
	for _, key := range []string{"input_tokens_details", "prompt_tokens_details"} {
		if _, exists := usage[key]; exists {
			delete(usage, key)
			changed = true
		}
	}
	_, creation := usage["cache_creation_input_tokens"]
	_, creation5m := usage["cache_creation_5m_input_tokens"]
	_, creation1h := usage["cache_creation_1h_input_tokens"]
	if _, exists := usage["cache_read_input_tokens"]; exists && !creation && !creation5m && !creation1h {
		delete(usage, "cache_read_input_tokens")
		changed = true
	}
	return changed
}

// contextDetailsInputTokensFromUsageMap 读取上游明确给出的当前上下文输入量。
//
// 部分 OpenAI 兼容上游（例如 Grok）把 usage.input_tokens 用作累计计费用量，
// 同时在 context_details.input_tokens 中给出本次请求实际占用的上下文。该字段
// 比代理根据请求体做的估算可靠，且不影响 usage 原始值的计费与指标用途。
func contextDetailsInputTokensFromUsageMap(usage map[string]interface{}) (int, bool) {
	details, ok := usage["context_details"].(map[string]interface{})
	if !ok {
		return 0, false
	}
	value, ok := details["input_tokens"].(float64)
	if !ok || value <= 0 || value != float64(int(value)) {
		return 0, false
	}
	return int(value), true
}

// normalizeContextDetailsInputTokensInCompletedEvent 将上游明确的当前上下文
// 统计同步到客户端 usage。原始 upstreamUsage 在调用此函数前已经单独收集，
// 因此这里只改变下发给客户端的副本。
func normalizeContextDetailsInputTokensInCompletedEvent(event string, enableLog bool) string {
	rewritten, _ := rewriteResponsesCompletedEvent(event, func(response map[string]interface{}) bool {
		usage, ok := response["usage"].(map[string]interface{})
		return ok && normalizeCompletedContext(usage, enableLog)
	})
	return rewritten
}

func normalizeCompletedContext(usage map[string]interface{}, enableLog bool) bool {
	contextInput, ok := contextDetailsInputTokensFromUsageMap(usage)
	if !ok {
		return false
	}
	upstreamInput := usageTokenCount(usage, "input_tokens")
	usage["input_tokens"] = contextInput
	usage["total_tokens"] = contextInput + usageTokenCount(usage, "output_tokens")
	if enableLog && upstreamInput != contextInput {
		log.Printf("[Responses-Stream-Token] 使用上游 context_details.input_tokens: %d -> %d（仅改下发值）",
			upstreamInput, contextInput)
	}
	return true
}

// normalizeContextDetailsInputTokensInResponse 是非流式响应对应的客户端副本
// 修正。resp.Extra 保留了上游原始响应，不能从已归一化的 ResponsesUsage 反推。
func normalizeContextDetailsInputTokensInResponse(resp *types.ResponsesResponse, clientUsage *types.ResponsesUsage, enableLog bool) {
	if resp == nil || clientUsage == nil || resp.Extra == nil {
		return
	}
	rawUsage, ok := resp.Extra["usage"].(map[string]interface{})
	if !ok {
		return
	}
	contextInput, ok := contextDetailsInputTokensFromUsageMap(rawUsage)
	if !ok {
		return
	}
	if enableLog && clientUsage.InputTokens != contextInput {
		log.Printf("[Responses-Token] 使用上游 context_details.input_tokens: %d -> %d（仅改下发值）",
			clientUsage.InputTokens, contextInput)
	}
	clientUsage.InputTokens = contextInput
	clientUsage.TotalTokens = contextInput + clientUsage.OutputTokens
}

// correctedInputTokens 是 Responses 透传分支的 usage 合理性校正入口。
// 具体阈值和双向判定统一由 utils.SanityCheckedInputTokens 管理，避免各协议各自维护
// 一套容易漂移的规则。

// correctedInputTokens 判断上游回报的上下文规模是否错报，返回校正后的 input_tokens。
// 薄封装：把本地估算交给 utils.SanityCheckedInputTokens（全代理共用的合理性校验，
// 双向判定 + 语义归一，契约见该函数注释与 utils/usage_sanity_test.go）。
func correctedInputTokens(requestBody []byte, upstreamInput, upstreamCached int) (int, bool) {
	if len(requestBody) == 0 {
		return 0, false
	}
	return utils.SanityCheckedInputTokensForRequest(
		requestBody,
		utils.EstimateResponsesRequestTokens(requestBody),
		upstreamInput,
		upstreamCached,
	)
}

// upstreamCachedTokensFromUsageMap 汇总 usage 里上游单独回报的缓存 token 总量。
// 注意本函数在累积式缓存剥离之后调用：OpenAI 的 cached_tokens 派生字段此时已被删除，
// 剩下的只有 Claude 原生缓存字段（真实的本次缓存命中/创建量）。
func upstreamCachedTokensFromUsageMap(usage map[string]interface{}) int {
	total := 0
	for _, key := range []string{
		"cache_read_input_tokens",
		"cache_creation_input_tokens",
		"cache_creation_5m_input_tokens",
		"cache_creation_1h_input_tokens",
	} {
		if v, ok := usage[key].(float64); ok && v > 0 {
			total += int(v)
		}
	}
	return total
}

// correctUnderreportedInputTokensInCompletedEvent 校正下发给客户端的 response.completed 事件里
// 被上游错报的 input_tokens，让 Cursor 这类依据回包 usage 做压缩决策的客户端看到真实上下文规模。
//
// 注意：携带 previous_response_id 或 Codex client_metadata 的请求不能用本地请求体估算校正。
// 前者的 requestBody 仅包含单轮增量，后者的 input_tokens 还可能包含服务端注入内容。
//
// 只改下发给客户端的 SSE，不动 collectedUsage——计费、熔断、性能画像仍用上游原值。
func correctUnderreportedInputTokensInCompletedEvent(event string, requestBody []byte, envCfg *config.EnvConfig) string {
	rewritten, _ := rewriteResponsesCompletedEvent(event, func(response map[string]interface{}) bool {
		usage, ok := response["usage"].(map[string]interface{})
		return ok && correctCompletedInput(usage, requestBody, envCfg)
	})
	return rewritten
}

func correctCompletedInput(usage map[string]interface{}, requestBody []byte, envCfg *config.EnvConfig) bool {
	if envCfg == nil || !envCfg.CorrectResponsesInputTokens || hasPreviousResponseID(requestBody) || isCodexResponsesRequest(requestBody) {
		return false
	}
	upstreamInput := usageTokenCount(usage, "input_tokens")
	corrected, need := correctedInputTokens(requestBody, upstreamInput, upstreamCachedTokensFromUsageMap(usage))
	if !need {
		return false
	}
	usage["input_tokens"] = corrected
	usage["total_tokens"] = corrected + usageTokenCount(usage, "output_tokens")
	if envCfg.Current().EnableResponseLogs {
		log.Printf("[Responses-Stream-Token] 上游 input_tokens 错报校正: %d -> %d（本地估算，仅改下发值）",
			upstreamInput, corrected)
	}
	return true
}

// finalizeResponsesCompletedUsage 按固定顺序修改客户端副本，只编解码一次 JSON。
// 原始用量已由调用方采集；上下文校正只影响下发值，不回写计费与指标。
func finalizeResponsesCompletedUsage(event string, requestBody []byte, outputTokens int, collected *responsesStreamUsage, hasUsage, needPatch, native bool, envCfg *config.EnvConfig) string {
	rewritten, _ := rewriteResponsesCompletedEvent(event, func(response map[string]interface{}) bool {
		usage, ok := response["usage"].(map[string]interface{})
		changed := false
		if !hasUsage {
			inputTokens, _ := estimateResponsesInputForClient(requestBody)
			usage = map[string]interface{}{
				"input_tokens": inputTokens, "output_tokens": outputTokens, "total_tokens": inputTokens + outputTokens,
			}
			response["usage"] = usage
			collected.InputTokens, collected.OutputTokens = inputTokens, outputTokens
			collected.TotalTokens = inputTokens + outputTokens
			changed = true
		} else if ok && needPatch {
			changed = patchCompletedUsage(usage, requestBody, outputTokens, collected, envCfg)
		}
		if usage == nil {
			return changed
		}
		if native {
			changed = stripCompletedCache(usage, hasPreviousResponseID(requestBody)) || changed
			changed = correctCompletedInput(usage, requestBody, envCfg) || changed
			changed = normalizeCompletedContext(usage, envCfg.Current().EnableResponseLogs) || changed
		}
		return changed
	})
	return rewritten
}

// correctUnderreportedInputTokensInResponse 非流式版本，语义同
// correctUnderreportedInputTokensInCompletedEvent。
//
// 注意：本函数改的是传入的 clientUsage 副本（调用方先做 `clientUsage := *resp.Usage` 再传入），
// resp 原件保持上游原值——handler 随后仍要拿原件上报计费与请求日志。
func correctUnderreportedInputTokensInResponse(resp *types.ResponsesResponse, clientUsage *types.ResponsesUsage, requestBody []byte, envCfg *config.EnvConfig) {
	if resp == nil || clientUsage == nil || envCfg == nil || !envCfg.CorrectResponsesInputTokens || len(requestBody) == 0 || hasPreviousResponseID(requestBody) || isCodexResponsesRequest(requestBody) {
		return
	}
	// 同流式：把上游单独回报的缓存量计入判定基数，避免误伤 Claude 语义的分开报数。
	upstreamCached := resp.Usage.CacheReadInputTokens + resp.Usage.CacheCreationInputTokens +
		resp.Usage.CacheCreation5mInputTokens + resp.Usage.CacheCreation1hInputTokens
	corrected, need := correctedInputTokens(requestBody, resp.Usage.InputTokens, upstreamCached)
	if !need {
		return
	}
	if envCfg != nil && envCfg.Current().EnableResponseLogs {
		log.Printf("[Responses-Token] 上游 input_tokens 错报校正: %d -> %d（本地估算，仅改下发值）",
			resp.Usage.InputTokens, corrected)
	}
	clientUsage.InputTokens = corrected
	clientUsage.TotalTokens = corrected + clientUsage.OutputTokens
}

// stripAccumulatedCacheFromResponse 从非流式 ResponsesResponse 的 usage 里剥离累积式缓存统计。
// 语义同 stripAccumulatedCacheFromCompletedEvent，作用于结构体字段而非 SSE 事件。
func stripAccumulatedCacheFromResponse(resp *types.ResponsesResponse, requestBody []byte) {
	if resp == nil {
		return
	}
	if hasPreviousResponseID(requestBody) && !responsesUsageHasAccumulatedOpenAICache(&resp.Usage) {
		return
	}
	// 有 Claude 原生缓存创建字段说明上游是 Claude，cache_read 是真实当前缓存，保留。
	isClaudeNativeCache := resp.Usage.CacheCreationInputTokens > 0 ||
		resp.Usage.CacheCreation5mInputTokens > 0 ||
		resp.Usage.CacheCreation1hInputTokens > 0
	resp.Usage.InputTokensDetails = nil
	if !isClaudeNativeCache {
		resp.Usage.CacheReadInputTokens = 0
	}
}
