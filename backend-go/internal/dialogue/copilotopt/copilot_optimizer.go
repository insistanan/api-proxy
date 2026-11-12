package copilotopt

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/insistanan/api-proxy/internal/infra/utils"
)

// SubagentMarker 是 Claude Code 子代理请求中的标识符。
const SubagentMarker = "__SUBAGENT_MARKER__"

// CopilotClassification 表示对请求交互类型的分类结果。
type CopilotClassification struct {
	// Initiator 为 "user" 或 "agent"，映射到 x-initiator 请求头。
	Initiator string
	// IsWarmup 是否为探针/预热请求（适合降级或走极速模型）。
	IsWarmup bool
	// IsCompact 是否为 Claude Code 内部上下文压缩请求。
	IsCompact bool
	// IsSubagent 是否为子代理请求。
	IsSubagent bool
}

// ClassifyRequest 评估 Anthropic 格式请求体并判定 Copilot 请求头。
func ClassifyRequest(
	bodyBytes []byte,
	hasAnthropicBeta bool,
	compactDetection bool,
	subagentDetection bool,
) CopilotClassification {
	if len(bodyBytes) == 0 {
		return CopilotClassification{
			Initiator: "user",
		}
	}

	var payload map[string]interface{}
	decoder := json.NewDecoder(bytes.NewReader(bodyBytes))
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil {
		return CopilotClassification{Initiator: "user"}
	}

	isCompact := compactDetection && isCompactRequest(payload)
	isSubagent := subagentDetection && detectSubagent(payload)

	rawMessages, exists := payload["messages"]
	if !exists {
		return CopilotClassification{
			Initiator:  "user",
			IsWarmup:   isWarmupRequest(payload, hasAnthropicBeta, false),
			IsCompact:  false,
			IsSubagent: isSubagent,
		}
	}

	messagesSlice, ok := rawMessages.([]interface{})
	if !ok || len(messagesSlice) == 0 {
		return CopilotClassification{
			Initiator:  "user",
			IsWarmup:   isWarmupRequest(payload, hasAnthropicBeta, false),
			IsCompact:  false,
			IsSubagent: isSubagent,
		}
	}

	lastMsg, ok := messagesSlice[len(messagesSlice)-1].(map[string]interface{})
	if !ok {
		return CopilotClassification{
			Initiator:  "user",
			IsCompact:  isCompact,
			IsSubagent: isSubagent,
		}
	}

	role, _ := lastMsg["role"].(string)
	if role != "user" {
		initiator := "user"
		if isSubagent {
			initiator = "agent"
		}
		return CopilotClassification{
			Initiator:  initiator,
			IsWarmup:   false,
			IsCompact:  isCompact,
			IsSubagent: isSubagent,
		}
	}

	// 最后一条为 user 消息：检查是否为纯用户发起
	isUserInitiated := true
	if rawContent, hasContent := lastMsg["content"]; hasContent {
		if blocks, ok := rawContent.([]interface{}); ok {
			// 只要含有 tool_result 就归为 agent 工具续写
			hasToolResult := false
			for _, rawBlock := range blocks {
				if bMap, ok := rawBlock.(map[string]interface{}); ok {
					if bType, _ := bMap["type"].(string); bType == "tool_result" {
						hasToolResult = true
						break
					}
				}
			}
			if hasToolResult {
				isUserInitiated = false
			}
		}
	}

	initiator := "user"
	if isSubagent || !isUserInitiated || isCompact {
		initiator = "agent"
	}

	return CopilotClassification{
		Initiator:  initiator,
		IsWarmup:   initiator == "user" && isWarmupRequest(payload, hasAnthropicBeta, isCompact),
		IsCompact:  isCompact,
		IsSubagent: isSubagent,
	}
}

func isWarmupRequest(payload map[string]interface{}, hasAnthropicBeta bool, isCompact bool) bool {
	if !hasAnthropicBeta || isCompact {
		return false
	}
	rawTools, exists := payload["tools"]
	if !exists {
		return true
	}
	toolsSlice, ok := rawTools.([]interface{})
	return !ok || len(toolsSlice) == 0
}

func isCompactRequest(payload map[string]interface{}) bool {
	// 信号 1: 专用 system prompt 前缀
	systemText := extractSystemText(payload)
	if strings.HasPrefix(systemText, "You are a helpful AI assistant tasked with summarizing conversations") {
		return true
	}

	// 信号 2 & 3: 检查最后一条用户消息中的机器生成特征
	rawMessages, exists := payload["messages"]
	if !exists {
		return false
	}
	messagesSlice, ok := rawMessages.([]interface{})
	if !ok || len(messagesSlice) == 0 {
		return false
	}

	lastMsg, ok := messagesSlice[len(messagesSlice)-1].(map[string]interface{})
	if !ok {
		return false
	}
	if role, _ := lastMsg["role"].(string); role != "user" {
		return false
	}

	msgText := extractTextFromMessage(lastMsg)
	if strings.Contains(msgText, "CRITICAL: Respond with TEXT ONLY. Do NOT call any tools.") {
		return true
	}
	if strings.Contains(msgText, "Pending Tasks:") && strings.Contains(msgText, "Current Work:") {
		return true
	}

	return false
}

func detectSubagent(payload map[string]interface{}) bool {
	rawMessages, exists := payload["messages"]
	if !exists {
		return false
	}
	messagesSlice, ok := rawMessages.([]interface{})
	if !ok || len(messagesSlice) == 0 {
		return false
	}
	firstMsg, ok := messagesSlice[0].(map[string]interface{})
	if !ok {
		return false
	}
	return strings.Contains(extractTextFromMessage(firstMsg), SubagentMarker)
}

func extractSystemText(payload map[string]interface{}) string {
	rawSystem, exists := payload["system"]
	if !exists {
		return ""
	}
	if str, ok := rawSystem.(string); ok {
		return str
	}
	if slice, ok := rawSystem.([]interface{}); ok {
		var sb strings.Builder
		for _, rawBlock := range slice {
			if bMap, ok := rawBlock.(map[string]interface{}); ok {
				if text, ok := bMap["text"].(string); ok {
					sb.WriteString(text)
				}
			}
		}
		return sb.String()
	}
	return ""
}

func extractTextFromMessage(msg map[string]interface{}) string {
	rawContent, exists := msg["content"]
	if !exists {
		return ""
	}
	if str, ok := rawContent.(string); ok {
		return str
	}
	if slice, ok := rawContent.([]interface{}); ok {
		var sb strings.Builder
		for _, rawBlock := range slice {
			if bMap, ok := rawBlock.(map[string]interface{}); ok {
				if text, ok := bMap["text"].(string); ok {
					sb.WriteString(text)
				}
			}
		}
		return sb.String()
	}
	return ""
}

// MergeToolResults 将单条 user 消息内的 [tool_result, text] 混合形态吸收合并为纯 tool_result 块，
// 并将连续的 tool_result 用户消息合并为单条，避免 Copilot 将工具续写判定为新的计费交互。
func MergeToolResults(requestBodyBytes []byte) ([]byte, bool) {
	if len(requestBodyBytes) == 0 {
		return requestBodyBytes, false
	}

	var payload map[string]interface{}
	decoder := json.NewDecoder(bytes.NewReader(requestBodyBytes))
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil {
		return requestBodyBytes, false
	}

	rawMessages, exists := payload["messages"]
	if !exists {
		return requestBodyBytes, false
	}
	messagesSlice, ok := rawMessages.([]interface{})
	if !ok || len(messagesSlice) == 0 {
		return requestBodyBytes, false
	}

	var modified bool

	// Phase 1: 消息内部合并 — 将 text 块吸收进前置或首个 tool_result 中
	for _, rawMsg := range messagesSlice {
		msgMap, ok := rawMsg.(map[string]interface{})
		if !ok || msgMap["role"] != "user" {
			continue
		}
		rawContent, hasContent := msgMap["content"]
		if !hasContent {
			continue
		}
		blocksSlice, ok := rawContent.([]interface{})
		if !ok || len(blocksSlice) <= 1 {
			continue
		}

		var toolResults []map[string]interface{}
		var textBlocks []string
		allRecognized := true

		for _, rawBlock := range blocksSlice {
			bMap, ok := rawBlock.(map[string]interface{})
			if !ok {
				allRecognized = false
				break
			}
			bType, _ := bMap["type"].(string)
			if bType == "tool_result" {
				toolResults = append(toolResults, bMap)
			} else if bType == "text" {
				if textStr, ok := bMap["text"].(string); ok {
					textBlocks = append(textBlocks, textStr)
				}
			} else {
				allRecognized = false
				break
			}
		}

		if allRecognized && len(toolResults) > 0 && len(textBlocks) > 0 {
			mergedText := strings.Join(textBlocks, "\n\n")
			// 吸收进最后一个 tool_result
			lastToolResult := toolResults[len(toolResults)-1]
			absorbTextIntoToolResult(lastToolResult, mergedText)

			var newContent []interface{}
			for _, tr := range toolResults {
				newContent = append(newContent, tr)
			}
			msgMap["content"] = newContent
			modified = true
		}
	}

	// Phase 2: 跨消息合并 — 连续的纯 tool_result 用户消息合并为一条
	if len(messagesSlice) > 1 {
		var mergedMessages []interface{}
		i := 0
		for i < len(messagesSlice) {
			currentMsg, ok := messagesSlice[i].(map[string]interface{})
			if !ok || !isToolResultOnlyMessage(currentMsg) {
				mergedMessages = append(mergedMessages, messagesSlice[i])
				i++
				continue
			}

			var combinedContent []interface{}
			runStart := i
			for i < len(messagesSlice) {
				nextMsg, ok := messagesSlice[i].(map[string]interface{})
				if !ok || !isToolResultOnlyMessage(nextMsg) {
					break
				}
				if rawContent, ok := nextMsg["content"].([]interface{}); ok {
					combinedContent = append(combinedContent, rawContent...)
				}
				i++
			}

			if i-runStart > 1 {
				modified = true
			}
			mergedMessages = append(mergedMessages, map[string]interface{}{
				"role":    "user",
				"content": combinedContent,
			})
		}
		payload["messages"] = mergedMessages
	}

	if !modified {
		return requestBodyBytes, false
	}

	modifiedBytes, err := utils.MarshalJSONNoEscape(payload)
	if err != nil {
		return requestBodyBytes, false
	}
	return modifiedBytes, true
}

func absorbTextIntoToolResult(toolResult map[string]interface{}, textToAbsorb string) {
	rawContent, exists := toolResult["content"]
	if !exists {
		toolResult["content"] = textToAbsorb
		return
	}
	if currentStr, ok := rawContent.(string); ok {
		toolResult["content"] = fmt.Sprintf("%s\n\n%s", currentStr, textToAbsorb)
		return
	}
	if contentBlocks, ok := rawContent.([]interface{}); ok {
		contentBlocks = append(contentBlocks, map[string]interface{}{
			"type": "text",
			"text": textToAbsorb,
		})
		toolResult["content"] = contentBlocks
	}
}

func isToolResultOnlyMessage(msg map[string]interface{}) bool {
	if role, _ := msg["role"].(string); role != "user" {
		return false
	}
	rawContent, exists := msg["content"]
	if !exists {
		return false
	}
	blocks, ok := rawContent.([]interface{})
	if !ok || len(blocks) == 0 {
		return false
	}
	for _, rawBlock := range blocks {
		bMap, ok := rawBlock.(map[string]interface{})
		if !ok {
			return false
		}
		if bType, _ := bMap["type"].(string); bType != "tool_result" {
			return false
		}
	}
	return true
}

// ApplyCopilotHeaders 依据分类结果设置发往 Copilot 上游的专属头部。
func ApplyCopilotHeaders(headers http.Header, classification CopilotClassification) {
	if classification.Initiator != "" {
		headers.Set("x-initiator", classification.Initiator)
	}
	if classification.IsSubagent {
		headers.Set("x-interaction-type", "conversation-subagent")
	}
}
