package responses

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/BenedictKing/api-proxy/internal/config"
	"github.com/BenedictKing/api-proxy/internal/types"
	"github.com/BenedictKing/api-proxy/internal/utils"
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
	needInputPatch := resp.Usage.InputTokens <= 1 && !hasClaudeCache
	needOutputPatch := resp.Usage.OutputTokens <= 1

	// 如果 usage 完全为空，只对可由请求体代表的完整上下文估算 input；链式/Codex
	// 请求的 input 由服务端掌握，不能用单轮 body 覆盖。output 仍可按已返回内容估算。
	if resp.Usage.InputTokens == 0 && resp.Usage.OutputTokens == 0 && resp.Usage.TotalTokens == 0 {
		estimatedInput, _ := estimateResponsesInputForClient(requestBody)
		estimatedOutput := estimateResponsesOutputFromItems(resp.Output)
		resp.Usage.InputTokens = estimatedInput
		resp.Usage.OutputTokens = estimatedOutput
		resp.Usage.TotalTokens = estimatedInput + estimatedOutput
		if envCfg.EnableResponseLogs {
			log.Printf("[Responses-Token] 上游无Usage, 本地估算: input=%d, output=%d", estimatedInput, estimatedOutput)
		}
		return
	}

	// 修补虚假值
	originalInput := resp.Usage.InputTokens
	originalOutput := resp.Usage.OutputTokens
	patched := false

	inputPatched := false
	if needInputPatch {
		if estimatedInput, ok := estimateResponsesInputForClient(requestBody); ok {
			resp.Usage.InputTokens = estimatedInput
			inputPatched = true
			patched = true
		}
	}
	if needOutputPatch {
		resp.Usage.OutputTokens = estimateResponsesOutputFromItems(resp.Output)
		patched = true
	}

	// 重新计算 TotalTokens（修补时或 total_tokens 为 0 但 input/output 有效时）
	if inputPatched || (resp.Usage.TotalTokens == 0 && (resp.Usage.InputTokens > 0 || resp.Usage.OutputTokens > 0)) {
		resp.Usage.TotalTokens = resp.Usage.InputTokens + resp.Usage.OutputTokens
	}

	if envCfg.EnableResponseLogs {
		if patched {
			log.Printf("[Responses-Token] 虚假值修补: InputTokens=%d->%d, OutputTokens=%d->%d",
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

		// 检查 response.completed 事件中的 usage
		if eventType == "response.completed" {
			if response, ok := data["response"].(map[string]interface{}); ok {
				if usage, ok := response["usage"].(map[string]interface{}); ok {
					usageData := extractResponsesUsageFromMap(usage)
					needPatch := usageData.InputTokens <= 1 || usageData.OutputTokens <= 1

					// 仅当检测到 Claude 原生缓存字段时，才跳过 input_tokens 补全
					// OpenAI 的 input_tokens_details.cached_tokens 不应阻止补全
					if usageData.HasClaudeCache && usageData.InputTokens <= 1 {
						needPatch = usageData.OutputTokens <= 1 // 有 Claude 缓存时只检查 output
					}

					// 检查 total_tokens 是否需要补全（有效 input/output 但 total=0）
					if !needPatch && usageData.TotalTokens == 0 && (usageData.InputTokens > 0 || usageData.OutputTokens > 0) {
						needPatch = true
					}

					if enableLog {
						log.Printf("[Responses-Stream-Token] response.completed: InputTokens=%d, OutputTokens=%d, TotalTokens=%d, CacheCreation=%d, CacheRead=%d, HasClaudeCache=%v, 需补全=%v",
							usageData.InputTokens, usageData.OutputTokens, usageData.TotalTokens, usageData.CacheCreationInputTokens, usageData.CacheReadInputTokens, usageData.HasClaudeCache, needPatch)
					}
					return true, needPatch, usageData
				} else if enableLog {
					log.Printf("[Responses-Stream-Token] response.completed 事件中无 usage 字段")
				}
			} else if enableLog {
				log.Printf("[Responses-Stream-Token] response.completed 事件中无 response 字段")
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
	inputTokens := 0
	if v, ok := usage["input_tokens"].(float64); ok {
		inputTokens = int(v)
	}
	return openAICacheLooksAccumulated(openAICachedTokensFromUsageMap(usage), inputTokens)
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

// injectResponsesUsageToCompletedEvent 向 response.completed 事件注入 usage
// 返回: 修改后的事件字符串, 估算的 inputTokens, 估算的 outputTokens
func injectResponsesUsageToCompletedEvent(event string, requestBody []byte, outputText string, envCfg *config.EnvConfig) (string, int, int) {
	return injectResponsesUsageToCompletedEventWithTokens(event, requestBody, utils.EstimateTokens(outputText), envCfg)
}

func injectResponsesUsageToCompletedEventWithTokens(event string, requestBody []byte, outputTokens int, envCfg *config.EnvConfig) (string, int, int) {
	// 仅对非链式、非 Codex 请求使用请求体估算；链式请求的 body 只是增量，Codex
	// 还可能包含服务端注入内容，写入估算值会把客户端的上下文占用提示改错。
	inputTokens, _ := estimateResponsesInputForClient(requestBody)
	totalTokens := inputTokens + outputTokens

	debugLog := envCfg.EnableResponseLogs && envCfg.ShouldLog("debug")

	// 调试日志：记录估算开始
	if debugLog {
		log.Printf("[Responses-Stream-Token] injectUsage 开始: inputTokens=%d, outputTokens=%d, event长度=%d",
			inputTokens, outputTokens, len(event))
	}

	rewritten, injected := utils.RewriteSSEDataLines(event, func(payload string) (string, bool) {
		var data map[string]interface{}
		if err := json.Unmarshal([]byte(payload), &data); err != nil {
			// 调试日志：JSON 解析失败
			if debugLog {
				log.Printf("[Responses-Stream-Token] JSON解析失败: %v, 内容前200字符: %.200s", err, utils.RedactSensitivePlaceholdersForLog(payload))
			}
			return "", false
		}

		if eventType, _ := data["type"].(string); eventType != "response.completed" {
			return "", false
		}

		response, ok := data["response"].(map[string]interface{})
		if !ok {
			// response 字段缺失或类型错误，创建一个新的
			if debugLog {
				log.Printf("[Responses-Stream-Token] response字段缺失, 创建新的response对象")
			}
			response = make(map[string]interface{})
			data["response"] = response
		}

		response["usage"] = map[string]interface{}{
			"input_tokens":  inputTokens,
			"output_tokens": outputTokens,
			"total_tokens":  totalTokens,
		}

		patchedJSON, err := json.Marshal(data)
		if err != nil {
			if debugLog {
				log.Printf("[Responses-Stream-Token] JSON序列化失败: %v", err)
			}
			return "", false
		}

		if debugLog {
			log.Printf("[Responses-Stream-Token] 注入本地估算成功: InputTokens=%d, OutputTokens=%d, TotalTokens=%d",
				inputTokens, outputTokens, totalTokens)
		}
		return string(patchedJSON), true
	})

	if injected {
		return rewritten, inputTokens, outputTokens
	}

	// 逐行未命中：可能是 JSON 被拆到多个连续 data 行，合并后再试
	if debugLog {
		log.Printf("[Responses-Stream-Token] 逐行解析未找到, 尝试整体解析 event")
	}

	if merged, ok := injectUsageIntoMultiLineDataEvent(event, inputTokens, outputTokens, totalTokens); ok {
		if debugLog {
			log.Printf("[Responses-Stream-Token] 整体解析注入成功: InputTokens=%d, OutputTokens=%d",
				inputTokens, outputTokens)
		}
		return merged, inputTokens, outputTokens
	}

	// 仍然没有成功注入，记录警告并打印 event 内容
	if debugLog {
		// 打印 event 的前500个字符帮助调试
		eventPreview := event
		if len(eventPreview) > 500 {
			eventPreview = eventPreview[:500] + "..."
		}
		log.Printf("[Responses-Stream-Token] 警告: 未找到 response.completed 事件进行注入, event内容: %s", utils.RedactSensitivePlaceholdersForLog(eventPreview))
	}
	return event, inputTokens, outputTokens
}

// injectUsageIntoMultiLineDataEvent 处理 JSON 被拆到多个连续 data 行的 response.completed 事件：
// 把这段连续 data 行的载荷拼成完整 JSON，注入 usage 后用一行 data 替换整段，其余行原样保留。
// 返回 ok=false 表示事件不是这种形态（无 data 行 / 拼不出合法 JSON / 不是 response.completed），调用方应保留原事件。
func injectUsageIntoMultiLineDataEvent(event string, inputTokens, outputTokens, totalTokens int) (string, bool) {
	lines := strings.Split(event, "\n")

	// 定位连续 data 行区间 [dataStart, dataEnd)
	dataStart := -1
	for i, line := range lines {
		if _, isData := utils.ParseSSEDataLine(line); isData {
			dataStart = i
			break
		}
	}
	if dataStart < 0 {
		return "", false
	}

	dataEnd := len(lines)
	var jsonBuilder strings.Builder
	for i := dataStart; i < len(lines); i++ {
		payload, isData := utils.ParseSSEDataLine(lines[i])
		if !isData {
			// 区间到首个非 data 行为止；dataEnd 保持 len(lines) 会把尾部行吞掉，必须显式记录
			dataEnd = i
			break
		}
		jsonBuilder.WriteString(payload)
	}

	fullJSON := jsonBuilder.String()
	if fullJSON == "" {
		return "", false
	}

	var data map[string]interface{}
	if err := json.Unmarshal([]byte(fullJSON), &data); err != nil {
		return "", false
	}
	if eventType, _ := data["type"].(string); eventType != "response.completed" {
		return "", false
	}

	response, ok := data["response"].(map[string]interface{})
	if !ok {
		response = make(map[string]interface{})
		data["response"] = response
	}
	response["usage"] = map[string]interface{}{
		"input_tokens":  inputTokens,
		"output_tokens": outputTokens,
		"total_tokens":  totalTokens,
	}

	patchedJSON, err := json.Marshal(data)
	if err != nil {
		return "", false
	}

	rebuilt := make([]string, 0, len(lines))
	rebuilt = append(rebuilt, lines[:dataStart]...)
	rebuilt = append(rebuilt, utils.SSEDataLinePrefix+string(patchedJSON))
	rebuilt = append(rebuilt, lines[dataEnd:]...)
	return strings.Join(rebuilt, "\n"), true
}

// patchResponsesCompletedEventUsage 修补 response.completed 事件中的 usage
func patchResponsesCompletedEventUsage(event string, requestBody []byte, outputText string, collected *responsesStreamUsage, envCfg *config.EnvConfig) string {
	return patchResponsesCompletedEventUsageWithTokens(event, requestBody, utils.EstimateTokens(outputText), collected, envCfg)
}

func patchResponsesCompletedEventUsageWithTokens(event string, requestBody []byte, outputTokens int, collected *responsesStreamUsage, envCfg *config.EnvConfig) string {
	rewritten, _ := utils.RewriteSSEDataLines(event, func(payload string) (string, bool) {
		var data map[string]interface{}
		if err := json.Unmarshal([]byte(payload), &data); err != nil {
			return "", false
		}
		if data["type"] != "response.completed" {
			return "", false
		}

		if response, ok := data["response"].(map[string]interface{}); ok {
			if usage, ok := response["usage"].(map[string]interface{}); ok {
				originalInput := collected.InputTokens
				originalOutput := collected.OutputTokens
				patched := false
				inputPatched := false

				// 修补 input_tokens（仅当没有 Claude 原生缓存且请求体可代表完整上下文时）。
				// OpenAI 的 cached_tokens 不应阻止 input_tokens 补全，但链式/Codex 请求
				// 仍不能用本地 request body 估算全量上下文。
				if collected.InputTokens <= 1 && !collected.HasClaudeCache {
					if estimatedInput, ok := estimateResponsesInputForClient(requestBody); ok {
						usage["input_tokens"] = estimatedInput
						collected.InputTokens = estimatedInput
						inputPatched = true
						patched = true
					}
				}

				// 修补 output_tokens
				if collected.OutputTokens <= 1 {
					estimatedOutput := outputTokens
					usage["output_tokens"] = estimatedOutput
					collected.OutputTokens = estimatedOutput
					patched = true
				}

				// 重新计算 total_tokens（修补时或 total_tokens 为 0 但 input/output 有效时）
				currentTotal := 0
				if t, ok := usage["total_tokens"].(float64); ok {
					currentTotal = int(t)
				}
				if inputPatched || (currentTotal == 0 && (collected.InputTokens > 0 || collected.OutputTokens > 0)) {
					collected.TotalTokens = collected.InputTokens + collected.OutputTokens
					usage["total_tokens"] = collected.TotalTokens
				}

				if envCfg.EnableResponseLogs && envCfg.ShouldLog("debug") && patched {
					log.Printf("[Responses-Stream-Token] 虚假值修补: InputTokens=%d->%d, OutputTokens=%d->%d",
						originalInput, collected.InputTokens, originalOutput, collected.OutputTokens)
				}
			}
		}

		patchedJSON, err := json.Marshal(data)
		if err != nil {
			return "", false
		}
		return string(patchedJSON), true
	})
	return rewritten
}

// stripAccumulatedCacheFromCompletedEvent 从 response.completed 事件里剥离累积式缓存统计。
//
// 背景：上游 grok-4.6（OpenAI 兼容格式）返回的 input_tokens_details.cached_tokens 是
// 跨请求单调递增的累积缓存命中统计（204800、205056 ...），不是"当前请求已缓存的上下文大小"。
// responses 透传分支原样转发该字段，Cursor 把它当成当前已缓存上下文大小，误判上下文一直满
// 200k，反复触发压缩、压不下去（日志证据：压缩后请求体仅 30KB，上游仍回 cached_tokens=204800）。
//
// 真正 Claude 上游的 cache_read_input_tokens 是本次请求的真实缓存命中，不在剥离范围。
// 本函数只针对 OpenAI 格式的 input_tokens_details / prompt_tokens_details，以及由它
// 派生的 cache_read_input_tokens：把这两类字段从下发给客户端的 usage 里删掉，让客户端
// 用 input_tokens（已扣除缓存的 uncached 真实值）判断上下文大小。
//
// 注意：如果请求携带 previous_response_id，usage 可能包含服务端链上历史，
// 此时不能凭本地请求体大小推断缓存语义。但 cached_tokens 超过本次 input_tokens
// 是自证的累积式证据（OpenAI 语义下缓存必须是 input 的子集），这类请求仍然剥离——
// 否则 Cursor 从第二轮起就一直看到满缓存，压缩后数字不降，压缩会无限循环。
// client_metadata 只代表客户端身份，不能用来推断上游缓存字段的语义，不参与判断。
func stripAccumulatedCacheFromCompletedEvent(event string, requestBody []byte) string {
	chained := hasPreviousResponseID(requestBody)
	if !strings.Contains(event, "cached_tokens") &&
		!strings.Contains(event, "cache_read_input_tokens") {
		return event
	}
	rewritten, _ := utils.RewriteSSEDataLines(event, func(payload string) (string, bool) {
		var data map[string]interface{}
		if err := json.Unmarshal([]byte(payload), &data); err != nil {
			return "", false
		}
		if data["type"] != "response.completed" {
			return "", false
		}
		response, ok := data["response"].(map[string]interface{})
		if !ok {
			return "", false
		}
		usage, ok := response["usage"].(map[string]interface{})
		if !ok {
			return "", false
		}
		if chained && !usageMapHasAccumulatedOpenAICache(usage) {
			return "", false
		}
		changed := false
		// 删除 OpenAI 累积式缓存统计字段
		if _, exists := usage["input_tokens_details"]; exists {
			delete(usage, "input_tokens_details")
			changed = true
		}
		if _, exists := usage["prompt_tokens_details"]; exists {
			delete(usage, "prompt_tokens_details")
			changed = true
		}
		// 删除由 cached_tokens 派生的 cache_read_input_tokens（extractResponsesUsageFromMap 会把它
		// 从 input_tokens_details.cached_tokens 拷贝过来，对累积式上游而言这个值是假大）。
		// 仅当没有 Claude 原生缓存创建字段时才删（有 cache_creation_* 说明是 Claude 上游，保留）。
		_, hasCacheCreation := usage["cache_creation_input_tokens"]
		_, hasCacheCreation5m := usage["cache_creation_5m_input_tokens"]
		_, hasCacheCreation1h := usage["cache_creation_1h_input_tokens"]
		isClaudeNativeCache := hasCacheCreation || hasCacheCreation5m || hasCacheCreation1h
		if _, exists := usage["cache_read_input_tokens"]; exists && !isClaudeNativeCache {
			delete(usage, "cache_read_input_tokens")
			changed = true
		}
		if !changed {
			return "", false
		}
		patchedJSON, err := json.Marshal(data)
		if err != nil {
			return "", false
		}
		return string(patchedJSON), true
	})
	return rewritten
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
	rewritten, _ := utils.RewriteSSEDataLines(event, func(payload string) (string, bool) {
		var data map[string]interface{}
		if err := json.Unmarshal([]byte(payload), &data); err != nil {
			return "", false
		}
		if data["type"] != "response.completed" {
			return "", false
		}
		response, ok := data["response"].(map[string]interface{})
		if !ok {
			return "", false
		}
		usage, ok := response["usage"].(map[string]interface{})
		if !ok {
			return "", false
		}
		contextInput, ok := contextDetailsInputTokensFromUsageMap(usage)
		if !ok {
			return "", false
		}
		upstreamInput, _ := usage["input_tokens"].(float64)
		outputTokens, _ := usage["output_tokens"].(float64)
		usage["input_tokens"] = contextInput
		usage["total_tokens"] = contextInput + int(outputTokens)
		if enableLog && int(upstreamInput) != contextInput {
			log.Printf("[Responses-Stream-Token] 使用上游 context_details.input_tokens: %d -> %d（仅改下发值）",
				int(upstreamInput), contextInput)
		}
		patchedJSON, err := json.Marshal(data)
		if err != nil {
			return "", false
		}
		return string(patchedJSON), true
	})
	return rewritten
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
	if envCfg == nil || !envCfg.CorrectResponsesInputTokens || hasPreviousResponseID(requestBody) || isCodexResponsesRequest(requestBody) {
		return event
	}
	rewritten, _ := utils.RewriteSSEDataLines(event, func(payload string) (string, bool) {
		var data map[string]interface{}
		if err := json.Unmarshal([]byte(payload), &data); err != nil {
			return "", false
		}
		if data["type"] != "response.completed" {
			return "", false
		}
		response, ok := data["response"].(map[string]interface{})
		if !ok {
			return "", false
		}
		usage, ok := response["usage"].(map[string]interface{})
		if !ok {
			return "", false
		}
		upstreamInput := 0
		if v, ok := usage["input_tokens"].(float64); ok {
			upstreamInput = int(v)
		}
		corrected, need := correctedInputTokens(requestBody, upstreamInput, upstreamCachedTokensFromUsageMap(usage))
		if !need {
			return "", false
		}
		outputTokens := 0
		if v, ok := usage["output_tokens"].(float64); ok {
			outputTokens = int(v)
		}
		usage["input_tokens"] = corrected
		usage["total_tokens"] = corrected + outputTokens
		if envCfg != nil && envCfg.EnableResponseLogs {
			log.Printf("[Responses-Stream-Token] 上游 input_tokens 错报校正: %d -> %d（本地估算，仅改下发值）",
				upstreamInput, corrected)
		}
		patchedJSON, err := json.Marshal(data)
		if err != nil {
			return "", false
		}
		return string(patchedJSON), true
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
	if envCfg != nil && envCfg.EnableResponseLogs {
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
