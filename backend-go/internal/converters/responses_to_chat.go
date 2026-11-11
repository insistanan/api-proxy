package converters

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/insistanan/api-proxy/internal/types"
	"github.com/insistanan/api-proxy/internal/utils"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// ValidateResponsesToOpenAIChatRequest 对 raw JSON 快路径做协议兼容校验。
func ValidateResponsesToOpenAIChatRequest(inputRawJSON []byte) (validationErr error) {
	defer func() {
		if validationErr != nil && !types.IsUnsupportedProtocol(validationErr) {
			validationErr = &types.InvalidRequestError{Err: validationErr}
		}
	}()
	if !json.Valid(inputRawJSON) {
		return fmt.Errorf("Responses -> OpenAI Chat 请求不是合法 JSON")
	}
	root := gjson.ParseBytes(inputRawJSON)
	if !gjsonResultIsObject(root) {
		return fmt.Errorf("Responses -> OpenAI Chat 请求必须是对象")
	}

	if tools := root.Get("tools"); tools.Exists() {
		if err := validateResponsesToolsArray(tools, "Responses -> OpenAI Chat 的 tools"); err != nil {
			return err
		}
	}

	if toolChoice := root.Get("tool_choice"); toolChoice.Exists() {
		if err := validateOpenAIChatToolChoice(toolChoice); err != nil {
			return err
		}
	}

	if reasoning := root.Get("reasoning"); reasoning.Exists() {
		if !gjsonResultIsObject(reasoning) {
			return fmt.Errorf("Responses -> OpenAI Chat 的 reasoning 必须是对象")
		}
		if effort := reasoning.Get("effort"); effort.Exists() {
			if _, err := NormalizeReasoningEffortForConstrainedUpstream(effort.String()); err != nil {
				return fmt.Errorf("OpenAI Chat %w", err)
			}
		}
	}

	if input := root.Get("input"); input.Exists() && input.IsArray() {
		var err error
		input.ForEach(func(key, item gjson.Result) bool {
			itemType := item.Get("type").String()
			switch itemType {
			case "function_call", "custom_tool_call", "tool_search_call":
				if item.Get("call_id").String() == "" {
					err = fmt.Errorf("OpenAI Chat input 第 %d 项 %s 缺少 call_id", key.Int(), itemType)
					return false
				}
				if itemType != "tool_search_call" && item.Get("name").String() == "" {
					err = fmt.Errorf("OpenAI Chat input 第 %d 项 %s 缺少 name", key.Int(), itemType)
					return false
				}
				if itemType != "custom_tool_call" {
					if args := item.Get("arguments"); args.Exists() {
						if err = validateResponsesCallArguments(args, key.Int(), itemType); err != nil {
							return false
						}
					}
				}
			case "tool_search_output":
				if tools := item.Get("tools"); tools.Exists() {
					if err = validateResponsesToolsArray(tools, fmt.Sprintf("OpenAI Chat input 第 %d 项 tool_search_output.tools", key.Int())); err != nil {
						return false
					}
				}
			}
			return true
		})
		if err != nil {
			return err
		}
	}

	return nil
}

func validateResponsesToolsArray(tools gjson.Result, label string) error {
	if !tools.IsArray() {
		return fmt.Errorf("%s 必须是数组", label)
	}

	var err error
	tools.ForEach(func(key, tool gjson.Result) bool {
		if !gjsonResultIsObject(tool) {
			err = fmt.Errorf("%s 第 %d 项必须是对象", label, key.Int())
			return false
		}
		if toolType := tool.Get("type"); toolType.Exists() && toolType.Type != gjson.String {
			err = fmt.Errorf("%s 第 %d 项 type 必须是字符串", label, key.Int())
			return false
		}
		toolType := tool.Get("type").String()
		if toolType != "" && toolType != "function" && toolType != "custom" && toolType != "namespace" && toolType != "tool_search" && toolType != "web_search" && toolType != "web_search_preview" {
			err = &types.UnsupportedProtocolError{Err: fmt.Errorf("OpenAI Chat 不支持第 %d 个 Responses tool type %q", key.Int(), toolType)}
			return false
		}
		if toolType == "web_search" || toolType == "web_search_preview" {
			err = &types.UnsupportedProtocolError{Err: fmt.Errorf("OpenAI Chat 转换不支持 Responses 内置 web_search 的服务器执行与回程，请使用原生 Responses 渠道")}
			return false
		}
		if toolType == "tool_search" {
			return true
		}
		if toolType == "namespace" {
			if name := tool.Get("name"); name.Type != gjson.String || name.String() == "" {
				err = fmt.Errorf("%s 第 %d 项缺少 name", label, key.Int())
				return false
			}
			if nested := tool.Get("tools"); nested.Exists() {
				err = validateResponsesToolsArray(nested, fmt.Sprintf("%s 第 %d 项 namespace.tools", label, key.Int()))
				if err != nil {
					return false
				}
			}
			return true
		}
		for _, field := range []string{"name", "function.name"} {
			if name := tool.Get(field); name.Exists() && name.Type != gjson.String {
				err = fmt.Errorf("%s 第 %d 项 %s 必须是字符串", label, key.Int(), field)
				return false
			}
		}
		name := tool.Get("name").String()
		if nested := tool.Get("function.name"); nested.Exists() && nested.String() != "" {
			name = nested.String()
		}
		if name == "" {
			err = fmt.Errorf("%s 第 %d 项缺少 function.name", label, key.Int())
			return false
		}
		return true
	})
	return err
}

func validateResponsesCallArguments(args gjson.Result, index int64, itemType string) error {
	switch args.Type {
	case gjson.String:
		if !json.Valid([]byte(args.String())) {
			return fmt.Errorf("OpenAI Chat input 第 %d 项 %s arguments 不是合法 JSON", index, itemType)
		}
	case gjson.JSON:
		if !json.Valid([]byte(args.Raw)) {
			return fmt.Errorf("OpenAI Chat input 第 %d 项 %s arguments 不是合法 JSON", index, itemType)
		}
	default:
		raw := strings.TrimSpace(args.Raw)
		if raw != "" && !json.Valid([]byte(raw)) {
			return fmt.Errorf("OpenAI Chat input 第 %d 项 %s arguments 不是合法 JSON", index, itemType)
		}
	}
	return nil
}

// ConvertResponsesToOpenAIChatRequest 将 OpenAI Responses 请求格式转换为 OpenAI Chat Completions 格式
// 转换内容包括:
// 1. model 和 stream 配置
// 2. instructions → system message
// 3. input 数组 → messages 数组
// 4. tools 定义转换
// 5. function_call 和 function_call_output 处理
// 6. 生成参数映射 (max_tokens, reasoning 等)
//
// 参数:
//   - modelName: 要使用的模型名称
//   - inputRawJSON: Responses 格式的原始 JSON 请求
//   - stream: 是否为流式请求
//
// 返回:
//   - []byte: Chat Completions 格式的请求 JSON
func ConvertResponsesToOpenAIChatRequest(modelName string, inputRawJSON []byte, stream bool) []byte {
	return ConvertResponsesToOpenAIChatRequestWithOptions(modelName, inputRawJSON, stream, false)
}

// ConvertResponsesToOpenAIChatRequestWithOptions converts Responses JSON to Chat Completions.
// reasoning_text 原文总是映射为 reasoning_content；历史摘要不再转成正文。
func ConvertResponsesToOpenAIChatRequestWithOptions(modelName string, inputRawJSON []byte, stream bool, includeHistoryThinking bool) []byte {
	// 基础 Chat Completions 模板
	out := `{"model":"","messages":[],"stream":false}`

	root := gjson.ParseBytes(inputRawJSON)

	// 设置 model
	out, _ = sjson.Set(out, "model", modelName)

	// 设置 stream
	out, _ = sjson.Set(out, "stream", stream)

	// 如果是流式请求，添加 stream_options 以获取 usage 信息
	if stream {
		out, _ = sjson.Set(out, "stream_options.include_usage", true)
	}

	// 映射生成参数
	if maxTokens := root.Get("max_output_tokens"); maxTokens.Exists() {
		out, _ = sjson.Set(out, "max_tokens", maxTokens.Int())
	} else if maxTokens := root.Get("max_tokens"); maxTokens.Exists() {
		out, _ = sjson.Set(out, "max_tokens", maxTokens.Int())
	}

	if temperature := root.Get("temperature"); temperature.Exists() {
		out, _ = sjson.Set(out, "temperature", temperature.Float())
	}

	if topP := root.Get("top_p"); topP.Exists() {
		out, _ = sjson.Set(out, "top_p", topP.Float())
	}

	// 频率/存在惩罚直传（Responses 与 Chat 同名同语义）。
	if v := root.Get("frequency_penalty"); v.Exists() && v.Float() != 0 {
		out, _ = sjson.Set(out, "frequency_penalty", v.Float())
	}
	if v := root.Get("presence_penalty"); v.Exists() && v.Float() != 0 {
		out, _ = sjson.Set(out, "presence_penalty", v.Float())
	}

	// stop：Responses 的 stop（string 或数组）直传 Chat stop。
	if stop := root.Get("stop"); stop.Exists() {
		if stop.Type == gjson.String {
			if stop.String() != "" {
				out, _ = sjson.Set(out, "stop", []string{stop.String()})
			}
		} else if stop.IsArray() && len(stop.Array()) > 0 {
			stops := make([]string, 0, len(stop.Array()))
			for _, item := range stop.Array() {
				stops = append(stops, item.String())
			}
			out, _ = sjson.Set(out, "stop", stops)
		}
	}

	if user := root.Get("user"); user.Exists() {
		out, _ = sjson.Set(out, "user", user.String())
	}

	// 转换 instructions → system message
	if instructions := root.Get("instructions"); instructions.Exists() && instructions.String() != "" {
		systemMessage := `{"role":"system","content":""}`
		systemMessage, _ = sjson.Set(systemMessage, "content", instructions.String())
		out, _ = sjson.SetRaw(out, "messages.-1", systemMessage)
	}

	// 转换 input 数组 → messages
	if input := root.Get("input"); input.Exists() {
		if input.IsArray() {
			out = convertInputArrayToMessagesWithOptions(input, out, includeHistoryThinking)
		} else if input.Type == gjson.String {
			// 简单字符串输入
			msg := `{"role":"user","content":""}`
			msg, _ = sjson.Set(msg, "content", input.String())
			out, _ = sjson.SetRaw(out, "messages.-1", msg)
		}
	}

	// 转换 tools（包含 input 里的 tool_search_output 动态已加载工具）
	toolsConverted := false
	var request map[string]interface{}
	if err := json.Unmarshal(inputRawJSON, &request); err == nil {
		if toolDefinitions, err := collectResponsesToolDefinitions(request["tools"], request["input"]); err == nil && len(toolDefinitions) > 0 {
			if chatTools, err := responsesToolDefinitionsToOpenAIChatTools(toolDefinitions); err == nil && len(chatTools) > 0 {
				out, _ = sjson.Set(out, "tools", chatTools)
				toolsConverted = true
			}
		}
	} else if tools := root.Get("tools"); tools.Exists() && tools.IsArray() {
		out, toolsConverted = convertToolsToOpenAIFormat(tools, out)
	}

	// 转换 reasoning.effort → reasoning_effort
	if reasoningEffort := root.Get("reasoning.effort"); reasoningEffort.Exists() {
		// 调用方已通过 ValidateResponsesToOpenAIChatRequest 校验该值。
		effort, _ := NormalizeReasoningEffortForConstrainedUpstream(reasoningEffort.String())
		switch effort {
		case "none":
			out, _ = sjson.Set(out, "reasoning_effort", "none")
		case "auto":
			// Chat Completions 没有 auto 值；省略字段即使用上游默认值。
		case "minimal":
			out, _ = sjson.Set(out, "reasoning_effort", "minimal")
		case "low":
			out, _ = sjson.Set(out, "reasoning_effort", "low")
		case "medium":
			out, _ = sjson.Set(out, "reasoning_effort", "medium")
		case "high":
			out, _ = sjson.Set(out, "reasoning_effort", "high")
		case "xhigh":
			out, _ = sjson.Set(out, "reasoning_effort", "xhigh")
		case "max":
			out, _ = sjson.Set(out, "reasoning_effort", "max")
		}
	}

	// 转换 tool_choice
	if toolsConverted {
		if parallelToolCalls := root.Get("parallel_tool_calls"); parallelToolCalls.Exists() {
			out, _ = sjson.Set(out, "parallel_tool_calls", parallelToolCalls.Bool())
		}
		if toolChoice := root.Get("tool_choice"); toolChoice.Exists() {
			if normalized, ok := normalizeResponsesToolChoiceForChat(toolChoice); ok {
				out, _ = sjson.Set(out, "tool_choice", normalized)
			}
		}
	}

	out = pruneOpenAIChatToolControlFields(out)

	return []byte(out)
}

func validateOpenAIChatToolChoice(toolChoice gjson.Result) error {
	if toolChoice.Type == gjson.String {
		switch toolChoice.String() {
		case "auto", "none", "required":
			return nil
		default:
			return fmt.Errorf("OpenAI Chat 不支持 tool_choice=%q", toolChoice.String())
		}
	}
	if !gjsonResultIsObject(toolChoice) {
		return fmt.Errorf("Responses -> OpenAI Chat 的 tool_choice 必须是字符串或对象")
	}
	if choiceType := toolChoice.Get("type"); choiceType.Exists() && choiceType.Type != gjson.String {
		return fmt.Errorf("OpenAI Chat tool_choice.type 必须是字符串")
	}
	choiceType := toolChoice.Get("type").String()
	if choiceType == "" {
		choiceType = "function"
	}
	if choiceType != "function" && choiceType != "custom" && choiceType != "tool_search" && choiceType != "web_search" && choiceType != "web_search_preview" {
		return &types.UnsupportedProtocolError{Err: fmt.Errorf("OpenAI Chat 不支持 tool_choice.type=%q", choiceType)}
	}
	if choiceType == "web_search" || choiceType == "web_search_preview" {
		return &types.UnsupportedProtocolError{Err: fmt.Errorf("OpenAI Chat 转换不支持 Responses 内置 web_search tool_choice，请使用原生 Responses 渠道")}
	}
	if choiceType == "tool_search" {
		return nil
	}
	for _, field := range []string{"name", "function.name"} {
		if name := toolChoice.Get(field); name.Exists() && name.Type != gjson.String {
			return fmt.Errorf("OpenAI Chat tool_choice.%s 必须是字符串", field)
		}
	}
	name := toolChoice.Get("name").String()
	if nested := toolChoice.Get("function.name"); nested.Exists() && nested.String() != "" {
		name = nested.String()
	}
	if name == "" {
		return fmt.Errorf("OpenAI Chat tool_choice 对象缺少 function.name 或 name")
	}
	return nil
}

func gjsonResultIsObject(result gjson.Result) bool {
	return result.Type == gjson.JSON && strings.HasPrefix(strings.TrimSpace(result.Raw), "{")
}

func normalizeResponsesToolChoiceForChat(toolChoice gjson.Result) (interface{}, bool) {
	if toolChoice.Type == gjson.String {
		return toolChoice.String(), true
	}
	if !gjsonResultIsObject(toolChoice) {
		return nil, false
	}
	choiceType := toolChoice.Get("type").String()
	if choiceType == "tool_search" {
		return map[string]interface{}{
			"type": "function",
			"function": map[string]interface{}{
				"name": openAIChatToolSearchName,
			},
		}, true
	}
	if choiceType == "web_search" || choiceType == "web_search_preview" {
		// 生产入口会先移除内置搜索约束；底层也不能把服务器搜索变成客户端函数。
		return nil, false
	}
	name := toolChoice.Get("function.name").String()
	if name == "" {
		name = toolChoice.Get("name").String()
	}
	if name == "" {
		return nil, false
	}
	if namespace := strings.TrimSpace(toolChoice.Get("namespace").String()); namespace != "" {
		name = flattenNamespaceToolName(namespace, name)
	}
	return map[string]interface{}{
		"type": "function",
		"function": map[string]interface{}{
			"name": name,
		},
	}, true
}

// convertInputArrayToMessages 将 input 数组转换为 messages 数组
func convertInputArrayToMessages(input gjson.Result, out string) string {
	return convertInputArrayToMessagesWithOptions(input, out, false)
}

// convertInputArrayToMessagesWithOptions converts Responses input items.
// 原始 reasoning_text 关联到同一 assistant 回合；summary 只用于展示，不冒充原文。
// includeHistoryThinking 的旧正文回灌选项不再用于 Chat 推理历史。
func convertInputArrayToMessagesWithOptions(input gjson.Result, out string, _ bool) string {
	var assistant map[string]interface{}
	var pendingReasoning strings.Builder
	pendingToolCalls := make([]interface{}, 0)
	flushAssistant := func() {
		if assistant == nil && pendingReasoning.Len() == 0 && len(pendingToolCalls) == 0 {
			return
		}
		if assistant == nil {
			assistant = map[string]interface{}{"role": "assistant", "content": nil}
		}
		if pendingReasoning.Len() > 0 {
			assistant["reasoning_content"] = pendingReasoning.String()
		}
		if len(pendingToolCalls) > 0 {
			assistant["tool_calls"] = pendingToolCalls
		}
		out, _ = sjson.Set(out, "messages.-1", assistant)
		assistant = nil
		pendingReasoning.Reset()
		pendingToolCalls = nil
	}

	input.ForEach(func(_, item gjson.Result) bool {
		itemType := item.Get("type").String()

		// 如果没有 type 但有 role，则视为 message
		if itemType == "" && item.Get("role").String() != "" {
			itemType = "message"
		}

		switch itemType {
		case "message":
			if normalizeResponsesMessageRole(item.Get("role").String()) == "assistant" {
				if assistant != nil {
					flushAssistant()
				}
				converted := convertMessageItem(item, `{"messages":[]}`)
				assistant, _ = gjson.Get(converted, "messages.0").Value().(map[string]interface{})
			} else {
				flushAssistant()
				out = convertMessageItem(item, out)
			}

		case "function_call", "custom_tool_call", "tool_search_call":
			pendingToolCalls = append(pendingToolCalls, convertFunctionCallItem(item))

		case "function_call_output", "custom_tool_call_output", "tool_search_output":
			flushAssistant()
			out = convertFunctionCallOutputItem(item, out)
		case "reasoning":
			if assistant != nil || len(pendingToolCalls) > 0 {
				flushAssistant()
			}
			pendingReasoning.WriteString(extractOriginalResponsesReasoning(item))
		default:
			flushAssistant()
		}

		return true
	})

	flushAssistant()
	return out
}

func extractOriginalResponsesReasoning(item gjson.Result) string {
	var original strings.Builder
	item.Get("content").ForEach(func(_, part gjson.Result) bool {
		if part.Get("type").String() == "reasoning_text" {
			original.WriteString(part.Get("text").String())
		}
		return true
	})
	return original.String()
}

// convertMessageItem 转换 message 类型的 item
func convertMessageItem(item gjson.Result, out string) string {
	role := normalizeResponsesMessageRole(item.Get("role").String())

	message := `{"role":"","content":""}`
	message, _ = sjson.Set(message, "role", role)

	content := item.Get("content")
	if content.Exists() {
		if content.IsArray() {
			messageContent := convertResponsesContentArrayToOpenAIContent(content)
			if messageContent != nil {
				message, _ = sjson.Set(message, "content", messageContent)
			}
		} else if content.Type == gjson.String {
			// content 是字符串
			message, _ = sjson.Set(message, "content", content.String())
		}
	}

	out, _ = sjson.SetRaw(out, "messages.-1", message)
	return out
}

func convertResponsesContentArrayToOpenAIContent(content gjson.Result) interface{} {
	blocks := make([]map[string]interface{}, 0)
	texts := make([]string, 0)
	hasImage := false

	content.ForEach(func(_, contentItem gjson.Result) bool {
		var block map[string]interface{}
		if err := json.Unmarshal([]byte(contentItem.Raw), &block); err != nil {
			return true
		}
		if text, ok := utils.ExtractTextFromBlock(block); ok {
			texts = append(texts, text)
			blocks = append(blocks, map[string]interface{}{"type": "text", "text": text})
			return true
		}
		if imageBlock, ok := utils.ToOpenAIImageContentBlock(block); ok {
			hasImage = true
			blocks = append(blocks, imageBlock)
		}
		return true
	})

	if len(blocks) == 0 {
		return nil
	}
	if hasImage {
		return blocks
	}
	return strings.Join(texts, "")
}

// convertFunctionCallItem 转换 function_call 类型的 item
func convertFunctionCallItem(item gjson.Result) interface{} {
	toolCall := `{"id":"","type":"function","function":{"name":"","arguments":"{}"}}`

	if callID := item.Get("call_id"); callID.Exists() {
		toolCall, _ = sjson.Set(toolCall, "id", callID.String())
	}

	name := item.Get("name").String()
	itemType := item.Get("type").String()
	if itemType == "tool_search_call" {
		name = openAIChatToolSearchName
	}
	if itemType == "function_call" {
		if namespace := strings.TrimSpace(item.Get("namespace").String()); namespace != "" {
			name = flattenNamespaceToolName(namespace, name)
		}
	}
	if strings.TrimSpace(name) != "" {
		toolCall, _ = sjson.Set(toolCall, "function.name", name)
	}

	if itemType == "custom_tool_call" {
		toolCall, _ = sjson.Set(toolCall, "function.arguments", buildCustomToolCallArguments(item))
	} else if arguments := stringifyResponsesArguments(item.Get("arguments")); arguments != "" {
		toolCall, _ = sjson.Set(toolCall, "function.arguments", arguments)
	}

	return gjson.Parse(toolCall).Value()
}

func stringifyResponsesArguments(arguments gjson.Result) string {
	if !arguments.Exists() {
		return "{}"
	}
	if arguments.Type == gjson.String {
		text := strings.TrimSpace(arguments.String())
		if text == "" {
			return "{}"
		}
		return text
	}
	if raw := strings.TrimSpace(arguments.Raw); raw != "" {
		return raw
	}
	return "{}"
}

func buildCustomToolCallArguments(item gjson.Result) string {
	input := extractCustomToolCallInput(item.Get("content"))
	payload, err := json.Marshal(map[string]string{"input": input})
	if err != nil {
		return `{"input":""}`
	}
	return string(payload)
}

func extractCustomToolCallInput(content gjson.Result) string {
	if !content.Exists() {
		return ""
	}
	if content.Type == gjson.String {
		return content.String()
	}
	if content.IsArray() {
		lines := make([]string, 0, len(content.Array()))
		content.ForEach(func(_, block gjson.Result) bool {
			if text := strings.TrimSpace(block.Get("text").String()); text != "" {
				lines = append(lines, text)
			}
			return true
		})
		return strings.Join(lines, "\n")
	}
	return strings.TrimSpace(content.String())
}

// convertFunctionCallOutputItem 转换 function_call_output 类型的 item
func convertFunctionCallOutputItem(item gjson.Result, out string) string {
	// function_call_output → tool message
	toolMessage := `{"role":"tool","tool_call_id":"","content":""}`

	if callID := item.Get("call_id"); callID.Exists() {
		toolMessage, _ = sjson.Set(toolMessage, "tool_call_id", callID.String())
	}

	toolMessage, _ = sjson.Set(toolMessage, "content", stringifyResponsesToolOutputResult(item))

	out, _ = sjson.SetRaw(out, "messages.-1", toolMessage)
	return out
}

func stringifyResponsesToolOutputResult(item gjson.Result) string {
	if output := item.Get("output"); output.Exists() {
		if output.Type == gjson.String {
			return output.String()
		}
		if strings.TrimSpace(output.Raw) != "" {
			return output.Raw
		}
	}
	if content := item.Get("content"); content.Exists() {
		if content.Type == gjson.String {
			return content.String()
		}
		if strings.TrimSpace(content.Raw) != "" {
			return content.Raw
		}
	}
	if tools := item.Get("tools"); tools.Exists() && strings.TrimSpace(tools.Raw) != "" {
		return tools.Raw
	}
	return ""
}

// convertToolsToOpenAIFormat 将 Responses tools 转换为 OpenAI Chat Completions tools 格式
func convertToolsToOpenAIFormat(tools gjson.Result, out string) (string, bool) {
	var chatCompletionsTools []interface{}

	tools.ForEach(func(key, tool gjson.Result) bool {
		var toolMap map[string]interface{}
		if err := json.Unmarshal([]byte(tool.Raw), &toolMap); err != nil {
			return true
		}
		chatTools, err := buildOpenAIChatToolsFromResponsesTool(toolMap, int(key.Int()))
		if err != nil {
			return true
		}
		for _, chatTool := range chatTools {
			chatCompletionsTools = append(chatCompletionsTools, chatTool)
		}

		return true
	})

	if len(chatCompletionsTools) > 0 {
		out, _ = sjson.Set(out, "tools", chatCompletionsTools)
		return out, true
	}

	return out, false
}

func pruneOpenAIChatToolControlFields(out string) string {
	root := gjson.Parse(out)
	tools := root.Get("tools")
	hasTools := tools.Exists() && tools.IsArray() && len(tools.Array()) > 0
	if hasTools {
		return out
	}

	var err error
	out, err = sjson.Delete(out, "tool_choice")
	if err != nil {
		return out
	}
	out, _ = sjson.Delete(out, "parallel_tool_calls")
	return out
}
