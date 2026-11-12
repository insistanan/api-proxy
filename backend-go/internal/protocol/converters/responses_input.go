// Responses 输入解析与消息内容构造，保留历史角色及多模态表示。
package converters

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/insistanan/api-proxy/internal/infra/utils"
	"github.com/insistanan/api-proxy/internal/protocol/types"
)

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
				Action:           itemMap["action"],
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
