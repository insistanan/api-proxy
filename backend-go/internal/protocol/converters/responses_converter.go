package converters

import (
	"fmt"
	"strings"

	"github.com/insistanan/api-proxy/internal/protocol/items"
	"github.com/insistanan/api-proxy/internal/protocol/types"
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
		mergedItems = respitems.MergeResponsesItemsDedup(sess.Messages, newItems)
	}
	if respitems.ResponsesItemsContainCompaction(mergedItems) {
		return nil, "", &types.UnsupportedProtocolError{Err: fmt.Errorf("Responses compaction item 只能由 Responses 原生上游处理，无法转换为 Claude Messages")}
	}

	mergedItems, instructions, err = extractResponsesSystem(mergedItems, instructions)
	if err != nil {
		return nil, "", err
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
