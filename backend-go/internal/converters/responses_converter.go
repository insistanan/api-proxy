package converters

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/insistanan/api-proxy/internal/types"
	"github.com/insistanan/api-proxy/internal/utils"
)

// ============== Responses → Claude Messages ==============

// ResponsesToClaudeMessagesWithOptions converts Responses history+input to Claude messages.
// includeHistoryThinking materializes type=reasoning as assistant text; default false skips it.
// When session history exists and client input already replays the same prefix, avoid double-append.
func ResponsesToClaudeMessagesWithOptions(sess *types.Session, newInput interface{}, instructions string, includeHistoryThinking bool) ([]types.ClaudeMessage, string, error) {
	newItems, err := parseResponsesInput(newInput)
	if err != nil {
		return nil, "", err
	}

	// 在 Responses item 层统一合并，再转换为目标协议。不能先转换成 Claude
	// message 再去重，因为转换会丢失 item id/call_id/encrypted_content 等
	// 能证明“这是历史重放”的身份信息。
	mergedItems := newItems
	if sess != nil && len(sess.Messages) > 0 {
		mergedItems = utils.MergeResponsesItemsDedup(sess.Messages, newItems)
	}
	if utils.ResponsesItemsContainCompaction(mergedItems) {
		return nil, "", fmt.Errorf("Responses compaction item 只能由 Responses 原生上游处理，无法转换为 Claude Messages")
	}

	messages := make([]types.ClaudeMessage, 0, len(mergedItems))
	for _, item := range mergedItems {
		msg, err := responsesItemToClaudeMessageWithOptions(item, includeHistoryThinking)
		if err != nil {
			return nil, "", fmt.Errorf("转换 Responses item 失败: %w", err)
		}
		if msg != nil {
			messages = append(messages, *msg)
		}
	}

	return messages, instructions, nil
}

// responsesItemToClaudeMessage 单个 ResponsesItem 转换为 Claude Message
func responsesItemToClaudeMessage(item types.ResponsesItem) (*types.ClaudeMessage, error) {
	return responsesItemToClaudeMessageWithOptions(item, false)
}

func responsesItemToClaudeMessageWithOptions(item types.ResponsesItem, includeHistoryThinking bool) (*types.ClaudeMessage, error) {
	switch item.Type {
	case "message":
		role := normalizeResponsesMessageRole(item.Role)

		contentBlocks := buildClaudeMessageContent(item.Content)
		if len(contentBlocks) == 0 {
			return nil, nil
		}

		return &types.ClaudeMessage{
			Role:    role,
			Content: contentBlocks,
		}, nil

	case "text":
		contentBlocks := buildClaudeMessageContent(item.Content)
		if len(contentBlocks) == 0 {
			return nil, fmt.Errorf("text 类型的 content 不能为空")
		}

		role := normalizeResponsesMessageRole(item.Role)

		return &types.ClaudeMessage{
			Role:    role,
			Content: contentBlocks,
		}, nil

	case "tool_call":
		return responsesItemToClaudeToolMessage(item)

	case "tool_result":
		return responsesItemToClaudeToolMessage(item)

	case "function_call", "function_call_output", "custom_tool_call", "custom_tool_call_output", "tool_search_call", "tool_search_output":
		return responsesItemToClaudeToolMessage(item)

	case "reasoning":
		if !includeHistoryThinking {
			return nil, nil
		}
		text := ExtractResponsesReasoningText(item)
		if text == "" {
			return nil, nil
		}
		return &types.ClaudeMessage{
			Role: "assistant",
			Content: []map[string]interface{}{
				{"type": "text", "text": "[thinking]\n" + text},
			},
		}, nil

	default:
		return nil, fmt.Errorf("未知的 item type: %s", item.Type)
	}
}

// ============== Claude Response → Responses ==============

// ClaudeResponseToResponses 将 Claude 响应转换为 Responses 格式
func ClaudeResponseToResponses(claudeResp map[string]interface{}, sessionID string) (*types.ResponsesResponse, error) {
	// 提取字段
	model, _ := claudeResp["model"].(string)
	content, _ := claudeResp["content"].([]interface{})

	// 转换 output
	output := []types.ResponsesItem{}
	for _, c := range content {
		contentBlock, ok := c.(map[string]interface{})
		if !ok {
			continue
		}

		item, ok, err := claudeContentBlockToResponsesItem(contentBlock)
		if err != nil {
			return nil, err
		}
		if ok {
			output = append(output, item)
		}
	}

	// 提取 usage（使用统一入口自动检测格式）
	usage := ExtractUsageMetrics(claudeResp["usage"])
	status := "completed"
	if stopReason, _ := claudeResp["stop_reason"].(string); stopReason != "" {
		status = OpenAIFinishReasonToResponses(AnthropicStopReasonToOpenAI(stopReason))
	}

	// 生成 response ID
	responseID := generateResponseID()

	return &types.ResponsesResponse{
		ID:         responseID,
		Object:     "response",
		Model:      model,
		Output:     output,
		Status:     status,
		PreviousID: "", // 将在外部设置
		Usage:      usage,
	}, nil
}

// ============== Responses → OpenAI Chat ==============

// ResponsesToOpenAIChatMessages 将 Responses 格式转换为 OpenAI Chat 格式
func ResponsesToOpenAIChatMessages(sess *types.Session, newInput interface{}, instructions string) ([]map[string]interface{}, error) {
	newItems, err := parseResponsesInput(newInput)
	if err != nil {
		return nil, err
	}

	mergedItems := newItems
	if sess != nil && len(sess.Messages) > 0 {
		mergedItems = utils.MergeResponsesItemsDedup(sess.Messages, newItems)
	}
	if utils.ResponsesItemsContainCompaction(mergedItems) {
		return nil, fmt.Errorf("Responses compaction item 只能由 Responses 原生上游处理，无法转换为 OpenAI Chat")
	}

	messages := make([]map[string]interface{}, 0, len(mergedItems)+1)
	if instructions != "" {
		messages = append(messages, map[string]interface{}{
			"role":    "system",
			"content": instructions,
		})
	}
	for _, item := range mergedItems {
		if msg := responsesItemToOpenAIMessage(item); msg != nil {
			messages = append(messages, msg)
		}
	}
	return collapseSystemMessagesToHead(messages), nil
}

// responsesItemToOpenAIMessage 单个 ResponsesItem 转换为 OpenAI Message
func responsesItemToOpenAIMessage(item types.ResponsesItem) map[string]interface{} {
	switch item.Type {
	case "message":
		role := normalizeResponsesMessageRole(item.Role)

		content := buildOpenAIMessageContent(item.Content)
		if content == nil {
			return nil
		}

		return map[string]interface{}{
			"role":    role,
			"content": content,
		}

	case "text":
		content := buildOpenAIMessageContent(item.Content)
		if content == nil {
			return nil
		}

		role := normalizeResponsesMessageRole(item.Role)

		return map[string]interface{}{
			"role":    role,
			"content": content,
		}

	case "tool_call":
		if item.ToolUse == nil {
			return nil
		}
		arguments, err := json.Marshal(item.ToolUse.Input)
		if err != nil {
			return nil
		}
		return map[string]interface{}{
			"role": "assistant",
			"tool_calls": []map[string]interface{}{{
				"id":   item.ToolUse.ID,
				"type": "function",
				"function": map[string]interface{}{
					"name":      item.ToolUse.Name,
					"arguments": string(arguments),
				},
			}},
		}

	case "function_call", "custom_tool_call", "tool_search_call":
		callID := item.CallID
		if callID == "" {
			callID = item.ID
		}
		arguments := item.Arguments
		name := item.Name
		if item.Type == "custom_tool_call" {
			arguments = customToolArgumentsJSON(item)
		} else if item.Type == "tool_search_call" {
			name = openAIChatToolSearchName
			if strings.TrimSpace(arguments) == "" {
				arguments = "{}"
			}
		} else if arguments == "" {
			arguments = "{}"
		}
		if item.Namespace != "" {
			name = flattenNamespaceToolName(item.Namespace, name)
		}
		return map[string]interface{}{
			"role": "assistant",
			"tool_calls": []map[string]interface{}{
				{
					"id":   callID,
					"type": "function",
					"function": map[string]interface{}{
						"name":      name,
						"arguments": arguments,
					},
				},
			},
		}
	}

	if isResponsesToolOutputType(item.Type) {
		content := stringifyResponsesToolOutput(item.Content)
		if content == "" && item.Type == "tool_search_output" && item.Tools != nil {
			content = stringifyResponsesToolOutput(item.Tools)
		}
		return map[string]interface{}{
			"role":         "tool",
			"tool_call_id": item.CallID,
			"content":      content,
		}
	}

	return nil
}

func normalizeResponsesMessageRole(role string) string {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "", "user":
		return "user"
	case "assistant":
		return "assistant"
	case "system", "developer":
		return "system"
	case "tool":
		return "tool"
	default:
		return "user"
	}
}

// ============== OpenAI Chat Response → Responses ==============

// OpenAIChatResponseToResponses 将 OpenAI Chat 响应转换为 Responses 格式
func OpenAIChatResponseToResponses(openaiResp map[string]interface{}, sessionID string) (*types.ResponsesResponse, error) {
	return openAIChatResponseToResponsesWithToolContext(openaiResp, sessionID, nil)
}

func openAIChatResponseToResponsesWithCustomTools(openaiResp map[string]interface{}, sessionID string, customTools map[string]customToolSpec) (*types.ResponsesResponse, error) {
	for name, spec := range customTools {
		if spec.Kind != "" {
			continue
		}
		spec.Kind = responseToolKindCustom
		if strings.TrimSpace(spec.OriginalName) == "" {
			spec.OriginalName = name
		}
		customTools[name] = spec
	}
	return openAIChatResponseToResponsesWithToolContext(openaiResp, sessionID, customTools)
}

func openAIChatResponseToResponsesWithToolContext(openaiResp map[string]interface{}, sessionID string, toolContext map[string]responseToolSpec) (*types.ResponsesResponse, error) {
	// 提取字段
	model, _ := openaiResp["model"].(string)
	choices, _ := openaiResp["choices"].([]interface{})

	// 提取第一个 choice 的 message
	output := []types.ResponsesItem{}
	status := "completed"
	if len(choices) > 0 {
		choice, ok := choices[0].(map[string]interface{})
		if ok {
			if finishReason, _ := choice["finish_reason"].(string); finishReason != "" {
				status = OpenAIFinishReasonToResponses(finishReason)
			}
			message, _ := choice["message"].(map[string]interface{})
			output = append(output, openAIChatMessageToResponsesItems(message, toolContext)...)
		}
	}

	// 提取 usage（使用统一入口自动检测格式）
	usage := ExtractUsageMetrics(openaiResp["usage"])

	// 生成 response ID
	responseID := generateResponseID()

	return &types.ResponsesResponse{
		ID:         responseID,
		Object:     "response",
		Model:      model,
		Output:     output,
		Status:     status,
		PreviousID: "",
		Usage:      usage,
	}, nil
}

func openAIChatMessageToResponsesItems(message map[string]interface{}, toolContext map[string]responseToolSpec) []types.ResponsesItem {
	items := []types.ResponsesItem{}

	if reasoning, _ := message["reasoning_content"].(string); reasoning != "" {
		items = append(items, types.ResponsesItem{
			Type:   "reasoning",
			Status: "completed",
			Summary: []map[string]interface{}{
				{
					"type": "summary_text",
					"text": reasoning,
				},
			},
			Content: []map[string]interface{}{{"type": "reasoning_text", "text": reasoning}},
		})
	}

	if content, _ := message["content"].(string); content != "" {
		items = append(items, types.ResponsesItem{
			Type:   "message",
			Status: "completed",
			Role:   "assistant",
			Content: []map[string]interface{}{
				{
					"type":        "output_text",
					"text":        content,
					"annotations": []interface{}{},
					"logprobs":    []interface{}{},
				},
			},
		})
	}

	if toolCalls, ok := message["tool_calls"].([]interface{}); ok {
		for i, rawToolCall := range toolCalls {
			toolCall, ok := rawToolCall.(map[string]interface{})
			if !ok {
				continue
			}

			callID, _ := toolCall["id"].(string)
			if callID == "" {
				callID = fmt.Sprintf("call_%d", i)
			}
			function, _ := toolCall["function"].(map[string]interface{})
			name, _ := function["name"].(string)
			arguments, _ := function["arguments"].(string)
			if arguments == "" {
				arguments = "{}"
			}

			if spec, ok := toolContext[name]; ok {
				switch spec.Kind {
				case responseToolKindCustom:
					items = append(items, types.ResponsesItem{
						ID:      fmt.Sprintf("ctc_%s", callID),
						Type:    "custom_tool_call",
						Status:  "completed",
						CallID:  callID,
						Name:    spec.OriginalName,
						Content: extractCustomToolInput(arguments),
					})
					continue
				case responseToolKindToolSearch:
					items = append(items, types.ResponsesItem{
						ID:        fmt.Sprintf("tsc_%s", callID),
						Type:      "tool_search_call",
						Status:    "completed",
						CallID:    callID,
						Execution: spec.ExecutionOrDefault(),
						Arguments: arguments,
					})
					continue
				case responseToolKindFunction:
					items = append(items, types.ResponsesItem{
						ID:        fmt.Sprintf("fc_%s", callID),
						Type:      "function_call",
						Status:    "completed",
						CallID:    callID,
						Name:      spec.OriginalNameOr(name),
						Namespace: spec.Namespace,
						Arguments: arguments,
					})
					continue
				}
			}

			if name == openAIChatToolSearchName {
				items = append(items, types.ResponsesItem{
					ID:        fmt.Sprintf("tsc_%s", callID),
					Type:      "tool_search_call",
					Status:    "completed",
					CallID:    callID,
					Execution: "client",
					Arguments: arguments,
				})
				continue
			}

			items = append(items, types.ResponsesItem{
				ID:        fmt.Sprintf("fc_%s", callID),
				Type:      "function_call",
				Status:    "completed",
				CallID:    callID,
				Name:      name,
				Arguments: arguments,
			})
		}
	}

	return items
}

// ============== 工具函数 ==============

type responseToolKind string

const (
	responseToolKindFunction   responseToolKind = "function"
	responseToolKindCustom     responseToolKind = "custom"
	responseToolKindToolSearch responseToolKind = "tool_search"
)

type responseToolSpec struct {
	Kind         responseToolKind
	OriginalName string
	Namespace    string
	Execution    string
}

type customToolSpec = responseToolSpec

func (s responseToolSpec) OriginalNameOr(fallback string) string {
	if strings.TrimSpace(s.OriginalName) != "" {
		return strings.TrimSpace(s.OriginalName)
	}
	return strings.TrimSpace(fallback)
}

func (s responseToolSpec) ExecutionOrDefault() string {
	if strings.TrimSpace(s.Execution) != "" {
		return strings.TrimSpace(s.Execution)
	}
	return "client"
}

func buildResponseToolContextFromRawRequest(raw []byte) map[string]responseToolSpec {
	if len(raw) == 0 {
		return nil
	}

	var request map[string]interface{}
	if err := json.Unmarshal(raw, &request); err != nil {
		return nil
	}
	return buildResponseToolContext(request["tools"], request["input"])
}

func buildCustomToolContextFromResponsesTools(raw interface{}) map[string]customToolSpec {
	return buildResponseToolContext(raw, nil)
}

func buildResponseToolContext(toolsRaw interface{}, inputRaw interface{}) map[string]responseToolSpec {
	context := map[string]responseToolSpec{}
	collectResponseToolContextFromTools(context, toolsRaw)
	collectResponseToolContextFromToolSearchOutputs(context, inputRaw)
	if len(context) == 0 {
		return nil
	}
	return context
}

func collectResponseToolContextFromTools(context map[string]responseToolSpec, raw interface{}) {
	tools, err := normalizeMapSlice(raw)
	if err != nil || len(tools) == 0 {
		return
	}

	for _, tool := range tools {
		spec := normalizeResponsesToolDefinition(tool)
		switch spec.ToolType {
		case "", "function":
			if spec.Name == "" {
				continue
			}
			context[spec.Name] = responseToolSpec{
				Kind:         responseToolKindFunction,
				OriginalName: spec.Name,
			}
		case "custom":
			name, _ := tool["name"].(string)
			name = strings.TrimSpace(name)
			if name == "" {
				name = spec.Name
			}
			if name == "" {
				continue
			}
			context[name] = responseToolSpec{
				Kind:         responseToolKindCustom,
				OriginalName: name,
			}
		case "tool_search":
			execution, _ := tool["execution"].(string)
			context[openAIChatToolSearchName] = responseToolSpec{
				Kind:         responseToolKindToolSearch,
				OriginalName: openAIChatToolSearchName,
				Execution:    execution,
			}
		case "namespace":
			namespace, _ := tool["name"].(string)
			namespace = strings.TrimSpace(namespace)
			if namespace == "" {
				continue
			}
			rawChildren := tool["tools"]
			if rawChildren == nil {
				rawChildren = tool["children"]
			}
			children, err := normalizeMapSlice(rawChildren)
			if err != nil {
				continue
			}
			for _, child := range children {
				childSpec := normalizeResponsesToolDefinition(child)
				if childSpec.ToolType != "" && childSpec.ToolType != "function" {
					continue
				}
				if childSpec.Name == "" {
					continue
				}
				context[flattenNamespaceToolName(namespace, childSpec.Name)] = responseToolSpec{
					Kind:         responseToolKindFunction,
					OriginalName: childSpec.Name,
					Namespace:    namespace,
				}
			}
		}
	}
}

func collectResponseToolContextFromToolSearchOutputs(context map[string]responseToolSpec, raw interface{}) {
	switch value := raw.(type) {
	case []interface{}:
		for _, item := range value {
			collectResponseToolContextFromToolSearchOutputs(context, item)
		}
	case map[string]interface{}:
		itemType, _ := value["type"].(string)
		if itemType == "tool_search_output" {
			collectResponseToolContextFromTools(context, value["tools"])
		}
		for _, nested := range value {
			collectResponseToolContextFromToolSearchOutputs(context, nested)
		}
	case []types.ResponsesItem:
		for _, item := range value {
			collectResponseToolContextFromToolSearchOutputs(context, item)
		}
	case types.ResponsesItem:
		if value.Type == "tool_search_output" {
			collectResponseToolContextFromTools(context, value.Tools)
		}
	}
}

func parseToolSearchArguments(arguments string) interface{} {
	arguments = strings.TrimSpace(arguments)
	if arguments == "" {
		return map[string]interface{}{}
	}

	var parsed interface{}
	if err := json.Unmarshal([]byte(arguments), &parsed); err == nil {
		return parsed
	}
	return map[string]interface{}{"query": arguments}
}

// extractTextFromContent 从 content 中提取文本内容
// 支持三种格式：
// 1. string - 直接返回
// 2. []ContentBlock - 提取 input_text/output_text 类型的 text 字段
// 3. []interface{} - 动态解析为 ContentBlock
func extractTextFromContent(content interface{}) string {
	// 1. 如果是 string，直接返回
	if str, ok := content.(string); ok {
		return str
	}

	// 2. 如果是 []ContentBlock（已解析类型）
	if blocks, ok := content.([]types.ContentBlock); ok {
		texts := []string{}
		for _, block := range blocks {
			if block.Type == "input_text" || block.Type == "output_text" {
				texts = append(texts, block.Text)
			}
		}
		return strings.Join(texts, "\n")
	}

	// 3. 如果是 []interface{}（未解析类型）
	if arr, ok := content.([]interface{}); ok {
		texts := []string{}
		for _, c := range arr {
			if block, ok := c.(map[string]interface{}); ok {
				blockType, _ := block["type"].(string)
				if blockType == "input_text" || blockType == "output_text" {
					if text, ok := block["text"].(string); ok {
						texts = append(texts, text)
					}
				}
			}
		}
		return strings.Join(texts, "\n")
	}

	return ""
}

type normalizedResponsesToolDefinition struct {
	ToolType    string
	Name        string
	Description string
	Parameters  interface{}
	Strict      interface{}
	Function    map[string]interface{}
}

func copyToolField(dst, src map[string]interface{}, key string) {
	if value, ok := src[key]; ok {
		dst[key] = value
	}
}

func normalizeResponsesToolDefinition(tool map[string]interface{}) normalizedResponsesToolDefinition {
	spec := normalizedResponsesToolDefinition{
		Function: map[string]interface{}{},
	}
	spec.ToolType, _ = tool["type"].(string)
	spec.ToolType = strings.TrimSpace(spec.ToolType)

	if nested, ok := tool["function"].(map[string]interface{}); ok {
		for k, v := range nested {
			spec.Function[k] = v
		}
	} else {
		copyToolField(spec.Function, tool, "name")
		copyToolField(spec.Function, tool, "description")
		copyToolField(spec.Function, tool, "parameters")
		copyToolField(spec.Function, tool, "strict")
	}

	if spec.ToolType == "custom" {
		spec.Function = map[string]interface{}{
			"name":        extractNamedToolChoice(tool),
			"description": customResponsesToolDescription(tool),
			"parameters":  customResponsesToolParameters(tool),
		}
	}

	if _, ok := spec.Function["parameters"]; !ok || spec.Function["parameters"] == nil {
		spec.Function["parameters"] = emptyOpenAIChatToolParameters()
	}

	spec.Name, _ = spec.Function["name"].(string)
	spec.Name = strings.TrimSpace(spec.Name)
	spec.Description, _ = spec.Function["description"].(string)
	spec.Parameters = spec.Function["parameters"]
	spec.Strict = spec.Function["strict"]
	return spec
}

func extractNamedToolChoice(raw map[string]interface{}) string {
	name, _ := raw["name"].(string)
	if name != "" {
		return strings.TrimSpace(name)
	}
	if fn, ok := raw["function"].(map[string]interface{}); ok {
		if name, _ := fn["name"].(string); name != "" {
			return strings.TrimSpace(name)
		}
	}
	if tool, ok := raw["tool"].(map[string]interface{}); ok {
		if name, _ := tool["name"].(string); name != "" {
			return strings.TrimSpace(name)
		}
	}
	return ""
}

func isResponsesToolOutputType(itemType string) bool {
	return itemType == "function_call_output" || itemType == "custom_tool_call_output" || itemType == "tool_search_output"
}

func stringifyResponsesToolOutput(content interface{}) string {
	if content == nil {
		return ""
	}
	if text, ok := content.(string); ok {
		return text
	}
	if blocks := utils.NormalizeContentBlocks(content); len(blocks) > 0 {
		texts := make([]string, 0, len(blocks))
		for _, block := range blocks {
			if text, ok := utils.ExtractTextFromBlock(block); ok {
				texts = append(texts, text)
			}
		}
		if len(texts) > 0 {
			return strings.Join(texts, "\n")
		}
	}
	contentJSON, err := utils.MarshalJSONNoEscape(content)
	if err != nil {
		return fmt.Sprint(content)
	}
	return string(contentJSON)
}

func extractCustomToolInput(arguments string) string {
	arguments = strings.TrimSpace(arguments)
	if arguments == "" {
		return ""
	}

	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(arguments), &payload); err != nil {
		return arguments
	}
	input, _ := payload["input"].(string)
	if input == "" {
		return arguments
	}
	return input
}

func customToolArgumentsJSON(item types.ResponsesItem) string {
	var input interface{}
	if text := extractTextFromContent(item.Content); text != "" {
		input = text
	} else if item.Content != nil {
		// custom_tool_call 的 input 也可能是结构化值；不能只取文本，
		// 否则回灌到 Chat/Claude 时会把整个工具输入变成空字符串。
		input = item.Content
	} else {
		input = strings.TrimSpace(item.Arguments)
	}
	payload, err := json.Marshal(map[string]interface{}{"input": input})
	if err != nil {
		return "{}"
	}
	return string(payload)
}

func collapseSystemMessagesToHead(messages []map[string]interface{}) []map[string]interface{} {
	if len(messages) <= 1 {
		return messages
	}

	systemTexts := make([]string, 0)
	nonSystem := make([]map[string]interface{}, 0, len(messages))
	for _, msg := range messages {
		if msg == nil {
			continue
		}
		role, _ := msg["role"].(string)
		content, _ := msg["content"].(string)
		if role == "system" && strings.TrimSpace(content) != "" {
			systemTexts = append(systemTexts, content)
			continue
		}
		nonSystem = append(nonSystem, msg)
	}

	if len(systemTexts) == 0 {
		return nonSystem
	}

	merged := map[string]interface{}{
		"role":    "system",
		"content": strings.Join(systemTexts, "\n\n"),
	}
	return append([]map[string]interface{}{merged}, nonSystem...)
}

// parseResponsesInput 解析 input 字段（可能是 string 或 []ResponsesItem）
func parseResponsesInput(input interface{}) ([]types.ResponsesItem, error) {
	switch v := input.(type) {
	case string:
		// 简单文本输入
		return []types.ResponsesItem{
			{
				Type:    "text",
				Content: v,
			},
		}, nil

	case []interface{}:
		// 数组输入
		items := []types.ResponsesItem{}
		for index, item := range v {
			itemMap, ok := item.(map[string]interface{})
			if !ok {
				return nil, fmt.Errorf("input 第 %d 项必须是对象", index)
			}

			itemType, _ := itemMap["type"].(string)
			role, _ := itemMap["role"].(string)
			content := itemMap["content"]
			summary := itemMap["summary"]
			id, _ := itemMap["id"].(string)
			status, _ := itemMap["status"].(string)
			callID, _ := itemMap["call_id"].(string)
			name, _ := itemMap["name"].(string)
			tools := itemMap["tools"]
			namespace, _ := itemMap["namespace"].(string)
			execution, _ := itemMap["execution"].(string)
			arguments, _ := itemMap["arguments"].(string)
			if arguments == "" {
				if rawArguments, ok := itemMap["arguments"]; ok && rawArguments != nil {
					if encoded, err := utils.MarshalJSONNoEscape(rawArguments); err == nil {
						arguments = string(encoded)
					}
				}
			}
			if itemType == "" && role != "" {
				itemType = "message"
			}
			if itemType == "" {
				return nil, fmt.Errorf("input 第 %d 项缺少 type", index)
			}
			if itemType == "custom_tool_call" {
				if input, ok := itemMap["input"]; ok && content == nil {
					content = input
				}
			}
			if output, ok := itemMap["output"]; ok && content == nil {
				content = output
			}

			parsed := types.ResponsesItem{
				ID:               id,
				Type:             itemType,
				Status:           status,
				Role:             role,
				Content:          content,
				Summary:          summary,
				CallID:           callID,
				Name:             name,
				Arguments:        arguments,
				Tools:            tools,
				Namespace:        namespace,
				Execution:        execution,
				EncryptedContent: itemMap["encrypted_content"],
				Signature:        stringValueFromMap(itemMap, "signature"),
			}
			if rawToolUse, ok := itemMap["tool_use"].(map[string]interface{}); ok {
				parsed.ToolUse = &types.ToolUse{
					ID:    stringValueFromMap(rawToolUse, "id"),
					Name:  stringValueFromMap(rawToolUse, "name"),
					Input: rawToolUse["input"],
				}
			}
			items = append(items, parsed)
		}
		return items, nil

	case []types.ResponsesItem:
		// 已经是正确类型
		return v, nil

	default:
		return nil, fmt.Errorf("不支持的 input 类型: %T", input)
	}
}

func stringValueFromMap(values map[string]interface{}, key string) string {
	value, _ := values[key].(string)
	return value
}

// generateResponseID 生成响应ID
func generateResponseID() string {
	return fmt.Sprintf("resp_%d", getCurrentTimestamp())
}

// getCurrentTimestamp 获取当前时间戳（毫秒）
func getCurrentTimestamp() int64 {
	return time.Now().UnixNano() / 1e6
}

func buildClaudeMessageContent(content interface{}) []map[string]interface{} {
	if text, ok := content.(string); ok && text != "" {
		return []map[string]interface{}{
			{
				"type": "text",
				"text": text,
			},
		}
	}

	blocks := utils.NormalizeContentBlocks(content)
	if len(blocks) == 0 {
		return nil
	}

	claudeBlocks := make([]map[string]interface{}, 0, len(blocks))
	for _, block := range blocks {
		if text, ok := utils.ExtractTextFromBlock(block); ok {
			claudeBlocks = append(claudeBlocks, map[string]interface{}{
				"type": "text",
				"text": text,
			})
			continue
		}

		if imageBlock, ok := utils.ToClaudeImageContentBlock(block); ok {
			claudeBlocks = append(claudeBlocks, imageBlock)
		}
	}

	return claudeBlocks
}

func buildOpenAIMessageContent(content interface{}) interface{} {
	if text, ok := content.(string); ok && text != "" {
		return text
	}

	blocks := utils.NormalizeContentBlocks(content)
	if len(blocks) == 0 {
		return nil
	}

	textParts := []string{}
	openAIBlocks := []map[string]interface{}{}
	hasVisionContent := false

	for _, block := range blocks {
		if text, ok := utils.ExtractTextFromBlock(block); ok {
			textParts = append(textParts, text)
			openAIBlocks = append(openAIBlocks, map[string]interface{}{
				"type": "text",
				"text": text,
			})
			continue
		}

		if imageBlock, ok := utils.ToOpenAIImageContentBlock(block); ok {
			hasVisionContent = true
			openAIBlocks = append(openAIBlocks, imageBlock)
		}
	}

	if len(openAIBlocks) == 0 {
		return nil
	}
	if hasVisionContent {
		return openAIBlocks
	}
	if len(textParts) > 0 {
		return strings.Join(textParts, "\n")
	}
	return nil
}

// JSONToMap 将 JSON 字节转为 map
func JSONToMap(data []byte) (map[string]interface{}, error) {
	var result map[string]interface{}
	err := json.Unmarshal(data, &result)
	return result, err
}

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
