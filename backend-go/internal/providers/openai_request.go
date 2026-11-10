// OpenAI 请求内容转换：消息、工具、Token 限制与提示词缓存键。
package providers

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/insistanan/api-proxy/internal/config"
	"github.com/insistanan/api-proxy/internal/types"
	"github.com/insistanan/api-proxy/internal/utils"
)

// convertMessages 转换消息
func (p *OpenAIProvider) convertMessages(claudeReq *types.ClaudeRequest, forceReasoningContent bool) ([]types.OpenAIMessage, error) {
	messages := []types.OpenAIMessage{}

	// 添加系统消息
	if claudeReq.System != nil {
		systemText := extractSystemText(claudeReq.System)
		if systemText != "" {
			messages = append(messages, types.OpenAIMessage{
				Role:    "system",
				Content: systemText,
			})
		}
	}

	for index, msg := range claudeReq.Messages {
		openaiMsg := p.convertMessage(msg)
		requiresOriginal := forceReasoningContent
		for _, block := range utils.NormalizeContentBlocks(msg.Content) {
			if block["type"] == "redacted_thinking" {
				requiresOriginal = true
			}
		}
		if normalizeRole(msg.Role) == "assistant" && requiresOriginal && len(openaiMsg) == 0 {
			return nil, &types.ReasoningHistoryError{Message: fmt.Sprintf("assistant 历史消息 %d 的原始 reasoning_content 无法恢复；请回传原始 thinking 或开启新会话", index+1)}
		}
		for _, converted := range openaiMsg {
			if converted.Role == "assistant" && requiresOriginal && converted.ReasoningContent == "" {
				log.Printf("[Reasoning-History] 必需的原始推理无法恢复，拒绝构造上游请求")
				return nil, &types.ReasoningHistoryError{Message: fmt.Sprintf("assistant 历史消息 %d 的原始 reasoning_content 无法恢复；请回传原始 thinking 或开启新会话", index+1)}
			}
		}
		messages = append(messages, openaiMsg...)
	}

	return messages, nil
}

// convertMessage 转换单个消息
func (p *OpenAIProvider) convertMessage(msg types.ClaudeMessage) []types.OpenAIMessage {
	messages := []types.OpenAIMessage{}

	// 如果是字符串内容
	if str, ok := msg.Content.(string); ok {
		if msg.Role != "tool" {
			openaiMsg := types.OpenAIMessage{
				Role:    normalizeRole(msg.Role),
				Content: str,
			}
			if openaiMsg.Role == "assistant" {
				openaiMsg.ReasoningContent = reasoningContentForAssistantMessage(openaiMsg, p.reasoningScope)
			}
			messages = append(messages, openaiMsg)
		}
		return messages
	}

	contents := utils.NormalizeContentBlocks(msg.Content)
	if len(contents) == 0 {
		return messages
	}

	textContents := []string{}
	reasoningContents := []string{}
	toolCalls := []types.OpenAIToolCall{}
	multimodalContents := []map[string]interface{}{}
	hasVisionContent := false
	flushAssistantOrUserMessage := func() {
		if len(textContents) == 0 && len(reasoningContents) == 0 && len(toolCalls) == 0 && len(multimodalContents) == 0 {
			return
		}
		role := normalizeRole(msg.Role)
		if role == "tool" {
			textContents = nil
			toolCalls = nil
			multimodalContents = nil
			hasVisionContent = false
			return
		}

		openaiMsg := types.OpenAIMessage{
			Role: role,
		}
		if hasVisionContent {
			openaiMsg.Content = multimodalContents
		} else if len(textContents) > 0 {
			openaiMsg.Content = strings.Join(textContents, "")
		} else {
			openaiMsg.Content = nil
		}
		if len(toolCalls) > 0 {
			openaiMsg.ToolCalls = toolCalls
		}
		// 明文优先；只从当前会话与来源缓存恢复原文，不生成占位推理。
		if role == "assistant" && len(reasoningContents) == 0 {
			if reasoning := reasoningContentForAssistantMessage(openaiMsg, p.reasoningScope); reasoning != "" {
				reasoningContents = append(reasoningContents, reasoning)
			}
		}
		if role == "assistant" && len(reasoningContents) > 0 {
			openaiMsg.ReasoningContent = strings.Join(reasoningContents, "")
		}

		messages = append(messages, openaiMsg)
		textContents = nil
		reasoningContents = nil
		toolCalls = nil
		multimodalContents = nil
		hasVisionContent = false
	}

	for _, content := range contents {
		contentType, _ := content["type"].(string)

		switch contentType {
		case "thinking":
			if normalizeRole(msg.Role) == "assistant" {
				if thinking, _ := content["thinking"].(string); thinking != "" {
					reasoningContents = append(reasoningContents, thinking)
				}
			}
		case "redacted_thinking":
			// 不透明推理无法转成明文；由 convertMessages 校验是否可恢复。
		case "text":
			if text, ok := content["text"].(string); ok {
				textContents = append(textContents, text)
				multimodalContents = append(multimodalContents, map[string]interface{}{
					"type": "text",
					"text": text,
				})
			}
		case "image", "image_url", "input_image":
			if imageBlock, ok := utils.ToOpenAIImageContentBlock(content); ok {
				hasVisionContent = true
				multimodalContents = append(multimodalContents, imageBlock)
			}

		case "tool_use":
			id, _ := content["id"].(string)
			name, _ := content["name"].(string)
			input := content["input"]

			inputJSON, _ := json.Marshal(input)
			toolCalls = append(toolCalls, types.OpenAIToolCall{
				ID:   id,
				Type: "function",
				Function: types.OpenAIToolCallFunction{
					Name:      name,
					Arguments: string(inputJSON),
				},
			})

		case "tool_result":
			if normalizeRole(msg.Role) != "user" {
				flushAssistantOrUserMessage()
			}

			toolUseID, _ := content["tool_use_id"].(string)
			resultContent := content["content"]

			var contentStr string
			if str, ok := resultContent.(string); ok {
				contentStr = str
			} else {
				contentJSON, _ := json.Marshal(resultContent)
				contentStr = string(contentJSON)
			}

			messages = append(messages, types.OpenAIMessage{
				Role:       "tool",
				ToolCallID: toolUseID,
				Content:    contentStr,
			})
		}
	}

	flushAssistantOrUserMessage()

	return messages
}

// convertTools 转换工具
func (p *OpenAIProvider) convertTools(claudeTools []types.ClaudeTool) []types.OpenAITool {
	tools := []types.OpenAITool{}

	for _, tool := range claudeTools {
		if tool.Name == "" || tool.Name == "BatchTool" {
			continue
		}
		tools = append(tools, types.OpenAITool{
			Type: "function",
			Function: types.OpenAIToolFunction{
				Name:        tool.Name,
				Description: tool.Description,
				Parameters:  cleanJsonSchema(tool.InputSchema),
			},
		})
	}

	return tools
}

// cleanJsonSchema 清理 JSON Schema，移除某些上游不支持的字段
func cleanJsonSchema(schema interface{}) interface{} {
	cleaned := cleanJsonSchemaFields(schema)
	// 根级缺 type/properties 时补最小 object schema（cc-switch clean_schema 同语义）：
	// 部分严格 OpenAI 兼容网关对缺根 type 的 function parameters 直接 400。
	if schemaMap, ok := cleaned.(map[string]interface{}); ok {
		if _, hasType := schemaMap["type"]; !hasType {
			schemaMap["type"] = "object"
			if _, hasProps := schemaMap["properties"]; !hasProps {
				schemaMap["properties"] = map[string]interface{}{}
			}
		}
	}
	return cleaned
}

func cleanJsonSchemaFields(schema interface{}) interface{} {
	if schema == nil {
		return schema
	}

	// 如果是 map，递归清理
	if schemaMap, ok := schema.(map[string]interface{}); ok {
		cleaned := make(map[string]interface{})

		for key, value := range schemaMap {
			// 移除不需要的字段
			if key == "$schema" || key == "title" || key == "examples" || key == "additionalProperties" {
				continue
			}
			// 移除 format 字段（当类型为 string 时）
			if key == "format" {
				if schemaType, hasType := schemaMap["type"]; hasType && schemaType == "string" {
					continue
				}
			}
			// 递归处理嵌套对象
			if key == "properties" || key == "items" {
				cleaned[key] = cleanJsonSchemaFields(value)
			} else if valueMap, isMap := value.(map[string]interface{}); isMap {
				cleaned[key] = cleanJsonSchemaFields(valueMap)
			} else if valueSlice, isSlice := value.([]interface{}); isSlice {
				cleanedSlice := make([]interface{}, len(valueSlice))
				for i, item := range valueSlice {
					cleanedSlice[i] = cleanJsonSchemaFields(item)
				}
				cleaned[key] = cleanedSlice
			} else {
				cleaned[key] = value
			}
		}

		return cleaned
	}

	// 如果是数组，递归清理每个元素
	if schemaSlice, ok := schema.([]interface{}); ok {
		cleaned := make([]interface{}, len(schemaSlice))
		for i, item := range schemaSlice {
			cleaned[i] = cleanJsonSchemaFields(item)
		}
		return cleaned
	}

	// 其他类型直接返回
	return schema
}

func (p *OpenAIProvider) applyTokenLimit(openaiReq *types.OpenAIRequest, claudeReq *types.ClaudeRequest, upstream *config.UpstreamConfig) {
	tokenValue := 65535
	if claudeReq.MaxCompletionTokens > 0 {
		tokenValue = claudeReq.MaxCompletionTokens
	} else if claudeReq.MaxTokens > 0 {
		tokenValue = claudeReq.MaxTokens
	}

	if shouldUseMaxCompletionTokens(openaiReq.Model, claudeReq, upstream) {
		openaiReq.MaxCompletionTokens = tokenValue
		return
	}

	openaiReq.MaxTokens = tokenValue
}

func shouldUseMaxCompletionTokens(targetModel string, claudeReq *types.ClaudeRequest, upstream *config.UpstreamConfig) bool {
	if claudeReq.MaxCompletionTokens > 0 {
		return true
	}

	// Kimi/Moonshot 兼容网关更容易接受 max_completion_tokens。
	if looksLikeKimiOrMoonshot(targetModel) {
		return true
	}

	// OpenAI o-series / gpt-5 推理模型只接受 max_completion_tokens
	//（cc-switch transform.rs 同判据；对官方与兼容网关都发 max_tokens 会被 400 拒绝）。
	if isOpenAIReasoningModelFamily(targetModel) {
		return true
	}

	if upstream == nil {
		return false
	}

	if looksLikeKimiOrMoonshot(upstream.Name) || looksLikeKimiOrMoonshot(upstream.BaseURL) {
		return true
	}

	for sourceModel, mappedModels := range upstream.ModelMapping {
		if sourceModel == claudeReq.Model && len(mappedModels) > 0 && looksLikeKimiOrMoonshot(mappedModels[0]) {
			return true
		}
	}

	return false
}

// isOpenAIReasoningModelFamily 判断目标模型是否属于只接受 max_completion_tokens 的
// OpenAI 推理模型家族（o1/o3/o4 系列与 gpt-5 系列）。子串匹配与 cc-switch 的
// is_openai_o_series 判据一致。
func isOpenAIReasoningModelFamily(model string) bool {
	value := strings.ToLower(model)
	return strings.Contains(value, "o1") || strings.Contains(value, "o3") ||
		strings.Contains(value, "o4") || strings.Contains(value, "gpt-5")
}

func looksLikeKimiOrMoonshot(value string) bool {
	value = strings.ToLower(value)
	return strings.Contains(value, "kimi") || strings.Contains(value, "moonshot")
}

// convertToolChoice 转换 tool_choice
func (p *OpenAIProvider) convertToolChoice(toolChoice interface{}) interface{} {
	if toolChoice == nil {
		return nil
	}

	// 字符串格式：Claude "auto"/"any" → OpenAI "auto"/"required"
	if str, ok := toolChoice.(string); ok {
		switch str {
		case "any":
			return "required"
		case "auto":
			return "auto"
		case "none":
			return "none"
		default:
			return "auto"
		}
	}

	// 对象格式：Claude {type: "auto"/"any"/"tool", name: "..."} → OpenAI 对应格式
	if obj, ok := toolChoice.(map[string]interface{}); ok {
		tcType, _ := obj["type"].(string)
		switch tcType {
		case "auto":
			return "auto"
		case "any":
			return "required"
		case "none":
			return "none"
		case "tool":
			name, _ := obj["name"].(string)
			if name != "" {
				return map[string]interface{}{
					"type": "function",
					"function": map[string]string{
						"name": name,
					},
				}
			}
			return "auto"
		default:
			return "auto"
		}
	}

	return nil
}

// convertThinkingToReasoningEffort 保留给测试/兼容调用；实现统一走 resolveClaudeReasoningEffort。
func (p *OpenAIProvider) convertThinkingToReasoningEffort(thinking interface{}) string {
	return resolveClaudeReasoningEffort(&types.ClaudeRequest{Thinking: thinking})
}

// buildClaudeChatPromptCacheKey builds a stable prompt_cache_key for Messages->Chat.
func buildClaudeChatPromptCacheKey(claudeReq *types.ClaudeRequest, upstream *config.UpstreamConfig, model string) string {
	if claudeReq == nil {
		return ""
	}
	channel := ""
	baseURL := ""
	if upstream != nil {
		channel = strings.TrimSpace(upstream.Name)
		baseURL = strings.TrimRight(upstream.GetEffectiveBaseURL(), "/#")
	}
	stableParts := map[string]interface{}{
		"protocol": "claude-messages-to-openai-chat-v1",
		"model":    model,
		"channel":  channel,
		"baseURL":  baseURL,
		"system":   extractSystemText(claudeReq.System),
		"tools":    normalizeToolsForPromptCacheKey(claudeReq.Tools),
	}
	sum := sha256.Sum256([]byte(utils.CanonicalJSON(stableParts)))
	return "claude-chat-" + hex.EncodeToString(sum[:])[:24]
}

func extractSystemText(system interface{}) string {
	if str, ok := system.(string); ok {
		return str
	}

	// 可能是数组
	arr, ok := system.([]interface{})
	if !ok {
		return ""
	}

	parts := []string{}
	for _, item := range arr {
		obj, ok := item.(map[string]interface{})
		if !ok {
			continue
		}

		if obj["type"] == "text" {
			if text, ok := obj["text"].(string); ok {
				parts = append(parts, text)
			}
		}
	}

	return strings.Join(parts, "\n")
}

func normalizeRole(role string) string {
	role = strings.ToLower(role)
	switch role {
	case "user", "assistant", "system", "tool":
		return role
	default:
		return "user"
	}
}

// extractOpenAIMessageText 提取 OpenAI 响应消息中的纯文本（用于 reasoning 缓存键）。
// Content 可能是 string，也可能是部分 OpenAI 兼容网关返回的内容块数组。
func extractOpenAIMessageText(msg types.OpenAIMessage) string {
	switch v := msg.Content.(type) {
	case string:
		return v
	case []interface{}:
		var b strings.Builder
		for _, item := range v {
			obj, ok := item.(map[string]interface{})
			if !ok {
				continue
			}
			if t, _ := obj["type"].(string); t == "text" {
				if text, ok := obj["text"].(string); ok {
					b.WriteString(text)
				}
			}
		}
		return b.String()
	}
	return ""
}
