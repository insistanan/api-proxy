// Chat → Responses 非流式转换与工具输出构造。
package converters

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

func extractCustomToolContextFromRawRequest(raw []byte) map[string]customToolSpec {
	return buildResponseToolContextFromRawRequest(raw)
}

func classifyChatToolCall(name string, callID string, arguments string, toolContext map[string]customToolSpec) (string, string, string, string, string, interface{}) {
	itemID := fmt.Sprintf("fc_%s", callID)
	itemType := "function_call"
	itemName := name
	namespace := ""
	execution := ""
	argumentValue := interface{}(arguments)

	if spec, ok := toolContext[name]; ok {
		switch spec.Kind {
		case responseToolKindCustom:
			return fmt.Sprintf("ctc_%s", callID), "custom_tool_call", spec.OriginalNameOr(name), "", "", extractCustomToolInput(arguments)
		case responseToolKindToolSearch:
			return fmt.Sprintf("tsc_%s", callID), "tool_search_call", "", "", spec.ExecutionOrDefault(), parseToolSearchArguments(arguments)
		case responseToolKindFunction:
			return itemID, itemType, spec.OriginalNameOr(name), spec.Namespace, "", argumentValue
		}
	}

	if name == openAIChatToolSearchName {
		return fmt.Sprintf("tsc_%s", callID), "tool_search_call", "", "", "client", parseToolSearchArguments(arguments)
	}

	return itemID, itemType, itemName, namespace, execution, argumentValue
}

func buildChatToolCallOutputItem(callID string, name string, arguments string, toolContext map[string]customToolSpec) map[string]interface{} {
	return buildChatToolCallOutputItemWithLock(callID, name, arguments, toolContext, false)
}

// buildChatToolCallOutputItemWithLock forceFunction=true 时无视分类结果一律按
// function_call 输出（流式锁定类型的退化场景：completed 的 item 类型必须与
// 流中事件一致，否则内容安全流式检查按"参数类型变化"中断响应）。
func buildChatToolCallOutputItemWithLock(callID string, name string, arguments string, toolContext map[string]customToolSpec, forceFunction bool) map[string]interface{} {
	itemID, itemType, itemName, namespace, execution, argumentValue := classifyChatToolCall(name, callID, arguments, toolContext)
	if forceFunction && itemType == "custom_tool_call" {
		itemID, itemType, itemName, namespace, execution, argumentValue = fmt.Sprintf("fc_%s", callID), "function_call", name, "", "", interface{}(arguments)
	}
	item := map[string]interface{}{
		"id":      itemID,
		"type":    itemType,
		"status":  "completed",
		"call_id": callID,
	}

	switch itemType {
	case "custom_tool_call":
		item["name"] = itemName
		item["input"] = argumentValue
	case "tool_search_call":
		item["execution"] = execution
		item["arguments"] = argumentValue
	default:
		item["name"] = itemName
		item["arguments"] = argumentValue
		if namespace != "" {
			item["namespace"] = namespace
		}
	}

	return item
}

// ConvertOpenAIChatToResponsesNonStream 将 OpenAI Chat Completions 响应转换为 Responses 格式（非流式）
func ConvertOpenAIChatToResponsesNonStream(_ context.Context, _ string, originalRequestRawJSON, requestRawJSON, rawJSON []byte, _ *any) string {
	root := gjson.ParseBytes(rawJSON)
	toolContext := extractCustomToolContextFromRawRequest(originalRequestRawJSON)

	// 基础响应模板
	out := `{"id":"","object":"response","created_at":0,"status":"completed","background":false,"error":null,"incomplete_details":null,"output":[],"usage":{"input_tokens":0,"input_tokens_details":{"cached_tokens":0},"output_tokens":0,"output_tokens_details":{},"total_tokens":0}}`

	// 提取基本字段
	responseID := root.Get("id").String()
	if responseID == "" {
		responseID = fmt.Sprintf("resp_%d", time.Now().UnixNano())
	}
	createdAt := root.Get("created").Int()
	if createdAt == 0 {
		createdAt = time.Now().Unix()
	}

	out, _ = sjson.Set(out, "id", responseID)
	out, _ = sjson.Set(out, "created_at", createdAt)

	// 注入原始请求字段
	if originalRequestRawJSON != nil {
		req := gjson.ParseBytes(originalRequestRawJSON)
		if v := req.Get("instructions"); v.Exists() {
			out, _ = sjson.Set(out, "instructions", v.String())
		}
		if v := req.Get("max_output_tokens"); v.Exists() {
			out, _ = sjson.Set(out, "max_output_tokens", v.Int())
		}
		if v := req.Get("model"); v.Exists() {
			out, _ = sjson.Set(out, "model", v.String())
		}
		if v := req.Get("parallel_tool_calls"); v.Exists() {
			out, _ = sjson.Set(out, "parallel_tool_calls", v.Bool())
		}
		if v := req.Get("previous_response_id"); v.Exists() {
			out, _ = sjson.Set(out, "previous_response_id", v.String())
		}
		if v := req.Get("reasoning"); v.Exists() {
			out, _ = sjson.Set(out, "reasoning", v.Value())
		}
		if v := req.Get("temperature"); v.Exists() {
			out, _ = sjson.Set(out, "temperature", v.Float())
		}
		if v := req.Get("tool_choice"); v.Exists() {
			out, _ = sjson.Set(out, "tool_choice", v.Value())
		}
		if v := req.Get("tools"); v.Exists() {
			out, _ = sjson.Set(out, "tools", v.Value())
		}
		if v := req.Get("top_p"); v.Exists() {
			out, _ = sjson.Set(out, "top_p", v.Float())
		}
		if v := req.Get("metadata"); v.Exists() {
			out, _ = sjson.Set(out, "metadata", v.Value())
		}
	}

	// 解析 choices
	choices := root.Get("choices")
	if !choices.Exists() || !choices.IsArray() || len(choices.Array()) == 0 {
		return out
	}

	var outputs []interface{}
	var textBuf strings.Builder
	var reasoningBuf strings.Builder
	currentMsgID := fmt.Sprintf("msg_%s_0", responseID)

	for _, choice := range choices.Array() {
		message := choice.Get("message")
		if !message.Exists() {
			continue
		}

		// 处理 reasoning_content
		if reasoning := message.Get("reasoning_content"); reasoning.Exists() && reasoning.String() != "" {
			reasoningBuf.WriteString(reasoning.String())
		}

		// 处理 content
		if content := message.Get("content"); content.Exists() && content.String() != "" {
			textBuf.WriteString(content.String())
		}

		// 处理 tool_calls
		if toolCalls := message.Get("tool_calls"); toolCalls.Exists() && toolCalls.IsArray() {
			for _, tc := range toolCalls.Array() {
				callID := tc.Get("id").String()
				if callID == "" {
					callID = fmt.Sprintf("call_%d", len(outputs))
				}
				funcName := tc.Get("function.name").String()
				funcArgs := tc.Get("function.arguments").String()
				if funcArgs == "" {
					funcArgs = "{}"
				}
				outputs = append(outputs, buildChatToolCallOutputItem(callID, funcName, funcArgs, toolContext))
			}
		}
	}

	// 构建 output 数组
	outputIndex := 0

	// reasoning item（如果有）
	if reasoningBuf.Len() > 0 {
		reasoningItemID := fmt.Sprintf("rs_%s_0", responseID)
		r := map[string]interface{}{
			"id":     reasoningItemID,
			"type":   "reasoning",
			"status": "completed",
			"summary": []interface{}{map[string]interface{}{
				"type": "summary_text",
				"text": reasoningBuf.String(),
			}},
			"content": []interface{}{map[string]interface{}{"type": "reasoning_text", "text": reasoningBuf.String()}},
		}
		// 在开头插入
		outputs = append([]interface{}{r}, outputs...)
		outputIndex = 1
		currentMsgID = fmt.Sprintf("msg_%s_%d", responseID, outputIndex)
	}

	// message item（如果有文本）
	if textBuf.Len() > 0 {
		m := map[string]interface{}{
			"id":     currentMsgID,
			"type":   "message",
			"status": "completed",
			"content": []interface{}{map[string]interface{}{
				"type":        "output_text",
				"annotations": []interface{}{},
				"logprobs":    []interface{}{},
				"text":        textBuf.String(),
			}},
			"role": "assistant",
		}
		// 在 reasoning 之后，tool_calls 之前插入
		if outputIndex > 0 {
			// 有 reasoning，插入到位置 1
			newOutputs := make([]interface{}, 0, len(outputs)+1)
			newOutputs = append(newOutputs, outputs[0]) // reasoning
			newOutputs = append(newOutputs, m)          // message
			newOutputs = append(newOutputs, outputs[1:]...)
			outputs = newOutputs
		} else {
			// 没有 reasoning，插入到开头
			outputs = append([]interface{}{m}, outputs...)
		}
	}

	if len(outputs) > 0 {
		out, _ = sjson.Set(out, "output", outputs)
	}

	// 处理 usage（完整支持多格式详细字段，参考 claude-code-hub）
	if usage := root.Get("usage"); usage.Exists() {
		var inputTokens, outputTokens, totalTokens, cachedTokens int64
		var cacheCreation, cacheCreation5m, cacheCreation1h int64
		var cacheTTL string

		// OpenAI 格式
		var openaiPromptTokens, openaiCachedTokens int64
		if v := usage.Get("prompt_tokens"); v.Exists() {
			openaiPromptTokens = v.Int()
		}
		if v := usage.Get("completion_tokens"); v.Exists() {
			outputTokens = v.Int()
		}
		if v := usage.Get("total_tokens"); v.Exists() {
			totalTokens = v.Int()
		}
		if v := usage.Get("prompt_tokens_details.cached_tokens"); v.Exists() {
			openaiCachedTokens = v.Int()
		}
		// OpenAI 的 prompt_tokens 包含缓存，需要扣除计算实际计费 input
		subsetSubtracted := false
		if openaiPromptTokens > 0 {
			inputTokens = openaiPromptTokens - openaiCachedTokens
			if inputTokens < 0 {
				inputTokens = 0
			}
			cachedTokens = openaiCachedTokens
			subsetSubtracted = openaiCachedTokens > 0
		}
		reasoningTokensFromUsage := usage.Get("completion_tokens_details.reasoning_tokens").Int()

		// Claude 格式（优先级高于 OpenAI），但 input_tokens 不能无条件覆盖上面减好的值：
		// 少数兼容网关两套字段一起发（prompt_tokens 与 cached_tokens 之外还有 input_tokens），
		// 此时 input_tokens 与 prompt_tokens 是同一个含缓存总量，覆盖回去等于作废减法，
		// 客户端再按契约求和 input_tokens + cache_read 就又双计了。
		// 只有 subset 式减法没生效时（上游是纯 Anthropic 语义，input_tokens 本就是 uncached）
		// 才采用 input_tokens。
		if v := usage.Get("input_tokens"); v.Exists() && !subsetSubtracted {
			inputTokens = v.Int()
		}
		if v := usage.Get("output_tokens"); v.Exists() {
			outputTokens = v.Int()
		}
		if v := usage.Get("cache_read_input_tokens"); v.Exists() {
			cachedTokens = v.Int()
		}
		if v := usage.Get("cache_creation_input_tokens"); v.Exists() {
			cacheCreation = v.Int()
		}
		if v := usage.Get("cache_creation_5m_input_tokens"); v.Exists() {
			cacheCreation5m = v.Int()
		}
		if v := usage.Get("cache_creation_1h_input_tokens"); v.Exists() {
			cacheCreation1h = v.Int()
		}

		// 设置缓存 TTL 标识
		if cacheCreation5m > 0 && cacheCreation1h > 0 {
			cacheTTL = "mixed"
		} else if cacheCreation1h > 0 {
			cacheTTL = "1h"
		} else if cacheCreation5m > 0 {
			cacheTTL = "5m"
		}

		// Gemini 格式（自动去重）
		if v := usage.Get("promptTokenCount"); v.Exists() {
			promptTokens := v.Int()
			geminiCached := usage.Get("cachedContentTokenCount").Int()
			// Gemini 的 promptTokenCount 已包含 cachedContentTokenCount，需要扣除
			actualInput := promptTokens - geminiCached
			if actualInput < 0 {
				actualInput = 0
			}
			inputTokens = actualInput
			cachedTokens = geminiCached
		}
		if v := usage.Get("candidatesTokenCount"); v.Exists() {
			outputTokens = v.Int()
		}

		// 计算总量
		if totalTokens == 0 {
			totalTokens = inputTokens + outputTokens
		}

		// 写入基础字段
		out, _ = sjson.Set(out, "usage.input_tokens", inputTokens)
		out, _ = sjson.Set(out, "usage.output_tokens", outputTokens)
		out, _ = sjson.Set(out, "usage.total_tokens", totalTokens)

		// input_tokens_details
		if cachedTokens > 0 {
			out, _ = sjson.Set(out, "usage.input_tokens_details.cached_tokens", cachedTokens)
		}

		// output_tokens_details
		reasoningTokens := reasoningTokensFromUsage
		if reasoningTokens == 0 && reasoningBuf.Len() > 0 {
			reasoningTokens = int64(reasoningBuf.Len() / 4)
		}
		if reasoningTokens > 0 {
			out, _ = sjson.Set(out, "usage.output_tokens_details.reasoning_tokens", reasoningTokens)
		}

		// Claude 缓存 TTL 细分字段
		if cacheCreation > 0 {
			out, _ = sjson.Set(out, "usage.cache_creation_input_tokens", cacheCreation)
		}
		if cacheCreation5m > 0 {
			out, _ = sjson.Set(out, "usage.cache_creation_5m_input_tokens", cacheCreation5m)
		}
		if cacheCreation1h > 0 {
			out, _ = sjson.Set(out, "usage.cache_creation_1h_input_tokens", cacheCreation1h)
		}
		if cachedTokens > 0 {
			out, _ = sjson.Set(out, "usage.cache_read_input_tokens", cachedTokens)
		}
		if cacheTTL != "" {
			out, _ = sjson.Set(out, "usage.cache_ttl", cacheTTL)
		}
	}

	return out
}
