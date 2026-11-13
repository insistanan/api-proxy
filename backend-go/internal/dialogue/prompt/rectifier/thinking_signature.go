package rectifier

import (
	"bytes"
	"encoding/json"
	"strings"
)

// SignatureRectifyStats 记录签名与 thinking 块整流的影响统计。
type SignatureRectifyStats struct {
	RemovedThinkingBlocks         int
	RemovedRedactedThinkingBlocks int
	RemovedSignatureFields        int
	RemovedTopLevelThinking       bool
}

// ShouldRectifyThinkingSignature 检测上游错误是否与 thinking signature 校验或块结构不符合规范相关。
func ShouldRectifyThinkingSignature(responseBodyBytes []byte) bool {
	if len(responseBodyBytes) == 0 {
		return false
	}

	lowerMessage := strings.ToLower(string(responseBodyBytes))

	// 场景 1: thinking block 中的 signature 校验失败
	// 示例: "Invalid 'signature' in 'thinking' block"
	if strings.Contains(lowerMessage, "invalid") &&
		strings.Contains(lowerMessage, "signature") &&
		strings.Contains(lowerMessage, "thinking") &&
		strings.Contains(lowerMessage, "block") {
		return true
	}

	// 场景 1b: Gemini 或第三方渠道返回 "Thought signature is not valid"
	// 示例: "Unable to submit request because Thought signature is not valid"
	if strings.Contains(lowerMessage, "thought signature") &&
		(strings.Contains(lowerMessage, "not valid") || strings.Contains(lowerMessage, "invalid")) {
		return true
	}

	// 场景 2: 助理消息必须以 thinking block 开头
	// 示例: "must start with a thinking block"
	if strings.Contains(lowerMessage, "must start with a thinking block") {
		return true
	}

	// 场景 3: 期望 thinking/redacted_thinking 但遇到了 tool_use
	// 示例: "Expected `thinking` or `redacted_thinking`, but found `tool_use`"
	if strings.Contains(lowerMessage, "expected") &&
		(strings.Contains(lowerMessage, "thinking") || strings.Contains(lowerMessage, "redacted_thinking")) &&
		strings.Contains(lowerMessage, "found") &&
		strings.Contains(lowerMessage, "tool_use") {
		return true
	}

	// 场景 4: signature 字段缺失
	// 示例: "signature: Field required"
	if strings.Contains(lowerMessage, "signature") && strings.Contains(lowerMessage, "field required") {
		return true
	}

	// 场景 5: signature 字段不被接受
	// 示例: "signature: Extra inputs are not permitted"
	if strings.Contains(lowerMessage, "signature") && strings.Contains(lowerMessage, "extra inputs are not permitted") {
		return true
	}

	// 场景 6: thinking/redacted_thinking 块被修改
	// 示例: "thinking or redacted_thinking blocks ... cannot be modified"
	if (strings.Contains(lowerMessage, "thinking") || strings.Contains(lowerMessage, "redacted_thinking")) &&
		strings.Contains(lowerMessage, "cannot be modified") {
		return true
	}

	// 场景 7: 明确提到非法/无效请求且包含 signature 或 thinking
	if (strings.Contains(lowerMessage, "invalid request") ||
		strings.Contains(lowerMessage, "illegal request") ||
		strings.Contains(lowerMessage, "非法请求")) &&
		(strings.Contains(lowerMessage, "signature") || strings.Contains(lowerMessage, "thinking")) {
		return true
	}

	return false
}

// RectifyThinkingSignature 对 Anthropic 格式的请求体进行最小侵入整流：
// 1. 移除 messages[*].content 中的 thinking / redacted_thinking 块。
// 2. 移除非 thinking 块上遗留的 signature 字段。
// 3. 若启用 thinking 且 tool-use 轮次缺少前置 thinking 块，移除顶层 thinking。
func RectifyThinkingSignature(requestBodyBytes []byte) ([]byte, bool, SignatureRectifyStats) {
	var stats SignatureRectifyStats
	if len(requestBodyBytes) == 0 {
		return requestBodyBytes, false, stats
	}

	var payloadMap map[string]interface{}
	decoder := json.NewDecoder(bytes.NewReader(requestBodyBytes))
	decoder.UseNumber()
	if err := decoder.Decode(&payloadMap); err != nil {
		return requestBodyBytes, false, stats
	}

	var modified bool

	// 1 & 2: 遍历 messages 中的 blocks
	if rawMessages, exists := payloadMap["messages"]; exists {
		if messagesSlice, ok := rawMessages.([]interface{}); ok {
			for _, rawMsg := range messagesSlice {
				msgMap, ok := rawMsg.(map[string]interface{})
				if !ok {
					continue
				}
				rawContent, hasContent := msgMap["content"]
				if !hasContent {
					continue
				}
				blocksSlice, ok := rawContent.([]interface{})
				if !ok {
					continue
				}

				var newBlocks []interface{}
				var contentChanged bool

				for _, rawBlock := range blocksSlice {
					blockMap, ok := rawBlock.(map[string]interface{})
					if !ok {
						newBlocks = append(newBlocks, rawBlock)
						continue
					}

					blockType, _ := blockMap["type"].(string)
					if blockType == "thinking" {
						stats.RemovedThinkingBlocks++
						contentChanged = true
						continue
					}
					if blockType == "redacted_thinking" {
						stats.RemovedRedactedThinkingBlocks++
						contentChanged = true
						continue
					}

					if _, hasSig := blockMap["signature"]; hasSig {
						delete(blockMap, "signature")
						stats.RemovedSignatureFields++
						contentChanged = true
					}

					newBlocks = append(newBlocks, blockMap)
				}

				if contentChanged {
					msgMap["content"] = newBlocks
					modified = true
				}
			}
		}
	}

	// 3: 顶层 thinking 兜底清理
	if shouldRemoveTopLevelThinking(payloadMap) {
		delete(payloadMap, "thinking")
		stats.RemovedTopLevelThinking = true
		modified = true
	}

	if !modified {
		return requestBodyBytes, false, stats
	}

	modifiedBytes, err := json.Marshal(payloadMap)
	if err != nil {
		return requestBodyBytes, false, stats
	}

	return modifiedBytes, true, stats
}

func shouldRemoveTopLevelThinking(payloadMap map[string]interface{}) bool {
	rawThinking, exists := payloadMap["thinking"]
	if !exists {
		return false
	}
	thinkingMap, ok := rawThinking.(map[string]interface{})
	if !ok {
		return false
	}
	thinkingType, _ := thinkingMap["type"].(string)
	if thinkingType != "enabled" {
		return false
	}

	rawMessages, exists := payloadMap["messages"]
	if !exists {
		return false
	}
	messagesSlice, ok := rawMessages.([]interface{})
	if !ok || len(messagesSlice) == 0 {
		return false
	}

	// 从后向前寻找最后一条 assistant 消息
	var lastAssistantMap map[string]interface{}
	for i := len(messagesSlice) - 1; i >= 0; i-- {
		msgMap, ok := messagesSlice[i].(map[string]interface{})
		if !ok {
			continue
		}
		if role, _ := msgMap["role"].(string); role == "assistant" {
			lastAssistantMap = msgMap
			break
		}
	}

	if lastAssistantMap == nil {
		return false
	}

	rawContent, exists := lastAssistantMap["content"]
	if !exists {
		return false
	}
	blocksSlice, ok := rawContent.([]interface{})
	if !ok || len(blocksSlice) == 0 {
		return false
	}

	firstBlock, ok := blocksSlice[0].(map[string]interface{})
	if !ok {
		return false
	}
	firstType, _ := firstBlock["type"].(string)
	missingThinkingPrefix := firstType != "thinking" && firstType != "redacted_thinking"
	if !missingThinkingPrefix {
		return false
	}

	// 检查该 assistant 消息中是否包含 tool_use
	hasToolUse := false
	for _, rawBlock := range blocksSlice {
		if bMap, ok := rawBlock.(map[string]interface{}); ok {
			if bType, _ := bMap["type"].(string); bType == "tool_use" {
				hasToolUse = true
				break
			}
		}
	}

	return hasToolUse
}
