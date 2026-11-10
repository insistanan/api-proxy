package converters

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/insistanan/api-proxy/internal/types"
	"github.com/insistanan/api-proxy/internal/utils"
)

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
		return nil, "", &types.UnsupportedProtocolError{Err: fmt.Errorf("Responses compaction item 只能由 Responses 原生上游处理，无法转换为 Claude Messages")}
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
		return nil, &types.UnsupportedProtocolError{Err: fmt.Errorf("Responses compaction item 只能由 Responses 原生上游处理，无法转换为 OpenAI Chat")}
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
