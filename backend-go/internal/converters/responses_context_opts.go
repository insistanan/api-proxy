package converters

import (
	"encoding/json"
	"strings"

	"github.com/insistanan/api-proxy/internal/types"
)

// ExtractResponsesReasoningText 从 Responses reasoning item 的 summary/content 抽出文本。
func ExtractResponsesReasoningText(item types.ResponsesItem) string {
	parts := make([]string, 0)
	if item.Summary != nil {
		switch summary := item.Summary.(type) {
		case []interface{}:
			for _, raw := range summary {
				block, ok := raw.(map[string]interface{})
				if !ok {
					continue
				}
				if text, ok := block["text"].(string); ok && strings.TrimSpace(text) != "" {
					parts = append(parts, strings.TrimSpace(text))
				}
			}
		case []map[string]interface{}:
			for _, block := range summary {
				if text, ok := block["text"].(string); ok && strings.TrimSpace(text) != "" {
					parts = append(parts, strings.TrimSpace(text))
				}
			}
		}
	}
	if item.Content != nil {
		switch content := item.Content.(type) {
		case string:
			if strings.TrimSpace(content) != "" {
				parts = append(parts, strings.TrimSpace(content))
			}
		case []interface{}:
			for _, raw := range content {
				block, ok := raw.(map[string]interface{})
				if !ok {
					continue
				}
				if text, ok := block["text"].(string); ok && strings.TrimSpace(text) != "" {
					parts = append(parts, strings.TrimSpace(text))
				}
			}
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

// mergeClaudeHistoryMessagesDedup avoids double history when client input already contains
// session prefix.它保留完整前缀的确定性匹配；部分前缀只有跨过至少一轮
// assistant/user 边界时才裁剪，避免吞掉单条同文的新用户输入。
func mergeClaudeHistoryMessagesDedup(historyMessages []types.ClaudeMessage, currentMessages []types.ClaudeMessage) []types.ClaudeMessage {
	if len(historyMessages) == 0 {
		return currentMessages
	}
	if len(currentMessages) == 0 {
		return historyMessages
	}

	overlap := 0
	maxCheck := len(historyMessages)
	if len(currentMessages) < maxCheck {
		maxCheck = len(currentMessages)
	}
	for candidate := maxCheck; candidate > 0; candidate-- {
		if claudeMessagePrefixEqual(historyMessages[:candidate], currentMessages[:candidate]) {
			overlap = candidate
			break
		}
	}

	if overlap == len(historyMessages) {
		// Current fully includes history: use current only.
		return currentMessages
	}
	if overlap >= 2 && overlap < len(currentMessages) &&
		claudeReplayBoundary(historyMessages[overlap-1], currentMessages[overlap]) {
		out := make([]types.ClaudeMessage, 0, len(historyMessages)+len(currentMessages)-overlap)
		out = append(out, historyMessages...)
		out = append(out, currentMessages[overlap:]...)
		return out
	}

	out := make([]types.ClaudeMessage, 0, len(historyMessages)+len(currentMessages))
	out = append(out, historyMessages...)
	out = append(out, currentMessages...)
	return out
}

func claudeReplayBoundary(previous, next types.ClaudeMessage) bool {
	nextRole := strings.ToLower(strings.TrimSpace(next.Role))
	previousRole := strings.ToLower(strings.TrimSpace(previous.Role))
	if nextRole == "user" && previousRole == "assistant" {
		return true
	}
	return nextRole == "user" && containsClaudeToolUse(previous.Content)
}

func containsClaudeToolUse(content interface{}) bool {
	blocks, ok := canonicalContentBlockSlice(content)
	if !ok {
		return false
	}
	for _, block := range blocks {
		if block["type"] == "tool_use" {
			return true
		}
	}
	return false
}

func claudeMessagePrefixEqual(left []types.ClaudeMessage, right []types.ClaudeMessage) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if semanticJSON(canonicalClaudeMessage(left[index])) != semanticJSON(canonicalClaudeMessage(right[index])) {
			return false
		}
	}
	return true
}

// canonicalClaudeMessage 只保留会影响 Claude 对话语义的字段。Responses 转 Claude
// 时，历史消息通常由代理重新编码，而客户端重放的消息可能使用字符串、text block
// 或带 cache_control/signature 的 block；直接比较 JSON 会把这些表示差异误判成新消息。
func canonicalClaudeMessage(message types.ClaudeMessage) map[string]interface{} {
	role := strings.ToLower(strings.TrimSpace(message.Role))
	if role == "" {
		role = "user"
	}
	if role == "developer" {
		role = "system"
	}
	return map[string]interface{}{
		"role":    role,
		"content": canonicalClaudeContent(message.Content),
	}
}

func canonicalClaudeContent(content interface{}) interface{} {
	if text, ok := content.(string); ok {
		return text
	}
	blocks, ok := canonicalContentBlockSlice(content)
	if !ok {
		return canonicalValue(content)
	}

	allText := true
	var textBuilder strings.Builder
	for _, block := range blocks {
		blockType, _ := block["type"].(string)
		if blockType != "text" {
			allText = false
			break
		}
		text, _ := block["text"].(string)
		textBuilder.WriteString(text)
	}
	if allText {
		return textBuilder.String()
	}
	return blocks
}

func canonicalOpenAIChatMessage(message map[string]interface{}) map[string]interface{} {
	role, _ := message["role"].(string)
	role = strings.ToLower(strings.TrimSpace(role))
	if role == "developer" {
		role = "system"
	}
	if role == "" {
		role = "user"
	}

	out := map[string]interface{}{"role": role}
	if role == "tool" {
		out["tool_call_id"] = messageString(message, "tool_call_id")
		out["content"] = canonicalOpenAIContent(message["content"])
		return out
	}

	toolCalls := canonicalOpenAIToolCalls(message["tool_calls"])
	if len(toolCalls) > 0 {
		out["tool_calls"] = toolCalls
	}
	content := canonicalOpenAIContent(message["content"])
	// Chat assistant tool-call messages legally use null/omitted/empty content.
	// These forms are the same event and must not defeat prefix de-duplication.
	if !(role == "assistant" && len(toolCalls) > 0 && isEmptySemanticContent(content)) {
		out["content"] = content
	}
	return out
}

func canonicalOpenAIContent(content interface{}) interface{} {
	if text, ok := content.(string); ok {
		return text
	}
	blocks, ok := canonicalContentBlockSlice(content)
	if !ok {
		return canonicalValue(content)
	}

	allText := true
	textParts := make([]string, 0, len(blocks))
	for _, block := range blocks {
		blockType, _ := block["type"].(string)
		if blockType != "text" {
			allText = false
			break
		}
		text, _ := block["text"].(string)
		textParts = append(textParts, text)
	}
	if allText {
		// buildOpenAIMessageContent joins multiple text blocks with newlines.
		return strings.Join(textParts, "\n")
	}
	return blocks
}

func canonicalOpenAIToolCalls(raw interface{}) []map[string]interface{} {
	value := canonicalValue(raw)
	items, ok := value.([]interface{})
	if !ok {
		return nil
	}

	calls := make([]map[string]interface{}, 0, len(items))
	for _, item := range items {
		call, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		function, _ := call["function"].(map[string]interface{})
		if function == nil {
			function = map[string]interface{}{}
		}
		name, _ := function["name"].(string)
		arguments := function["arguments"]
		if arguments == nil {
			arguments = call["arguments"]
		}
		canonicalCall := map[string]interface{}{
			"id":   messageString(call, "id"),
			"type": messageString(call, "type"),
			"function": map[string]interface{}{
				"name":      name,
				"arguments": canonicalArguments(arguments),
			},
		}
		if canonicalCall["type"] == "" {
			canonicalCall["type"] = "function"
		}
		calls = append(calls, canonicalCall)
	}
	return calls
}

func canonicalContentBlockSlice(content interface{}) ([]map[string]interface{}, bool) {
	value := canonicalValue(content)
	items, ok := value.([]interface{})
	if !ok {
		return nil, false
	}

	blocks := make([]map[string]interface{}, 0, len(items))
	for _, item := range items {
		block, ok := item.(map[string]interface{})
		if !ok {
			return nil, false
		}
		blockType, _ := block["type"].(string)
		switch blockType {
		case "text", "input_text", "output_text":
			blocks = append(blocks, map[string]interface{}{
				"type": "text",
				"text": messageString(block, "text"),
			})
		case "tool_use":
			blocks = append(blocks, map[string]interface{}{
				"type":  "tool_use",
				"id":    messageString(block, "id"),
				"name":  messageString(block, "name"),
				"input": canonicalValue(block["input"]),
			})
		case "tool_result":
			blocks = append(blocks, map[string]interface{}{
				"type":        "tool_result",
				"tool_use_id": messageString(block, "tool_use_id"),
				"content":     canonicalClaudeContent(block["content"]),
			})
		case "thinking":
			blocks = append(blocks, map[string]interface{}{
				"type":     "thinking",
				"thinking": messageString(block, "thinking"),
			})
		case "redacted_thinking":
			blocks = append(blocks, map[string]interface{}{
				"type": "redacted_thinking",
				"data": messageString(block, "data"),
			})
		default:
			blocks = append(blocks, canonicalValue(block).(map[string]interface{}))
		}
	}
	return blocks, true
}

func canonicalValue(value interface{}) interface{} {
	if value == nil {
		return nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	var normalized interface{}
	if err := json.Unmarshal(data, &normalized); err != nil {
		return string(data)
	}
	return canonicalValueRecursive(normalized)
}

func canonicalValueRecursive(value interface{}) interface{} {
	switch typed := value.(type) {
	case []interface{}:
		out := make([]interface{}, 0, len(typed))
		for _, item := range typed {
			out = append(out, canonicalValueRecursive(item))
		}
		return out
	case map[string]interface{}:
		out := make(map[string]interface{}, len(typed))
		for key, item := range typed {
			switch key {
			case "annotations", "logprobs", "cache_control", "signature":
				continue
			}
			out[key] = canonicalValueRecursive(item)
		}
		return out
	default:
		return value
	}
}

func canonicalArguments(value interface{}) interface{} {
	if text, ok := value.(string); ok {
		trimmed := strings.TrimSpace(text)
		if trimmed == "" {
			return ""
		}
		var parsed interface{}
		if err := json.Unmarshal([]byte(trimmed), &parsed); err == nil {
			return canonicalValue(parsed)
		}
		return trimmed
	}
	return canonicalValue(value)
}

func messageString(message map[string]interface{}, key string) string {
	value, _ := message[key].(string)
	return value
}

func isEmptySemanticContent(value interface{}) bool {
	if value == nil {
		return true
	}
	text, ok := value.(string)
	return ok && text == ""
}

func semanticJSON(value interface{}) string {
	data, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return string(data)
}

// responsesItemToOpenAIMessageWithOptions converts a Responses item to Chat message.
// includeHistoryThinking materializes type=reasoning as assistant text; default false skips it.
func responsesItemToOpenAIMessageWithOptions(item types.ResponsesItem, includeHistoryThinking bool) map[string]interface{} {
	if item.Type == "reasoning" {
		if !includeHistoryThinking {
			return nil
		}
		text := ExtractResponsesReasoningText(item)
		if text == "" {
			return nil
		}
		return map[string]interface{}{
			"role":    "assistant",
			"content": "[thinking]\n" + text,
		}
	}
	// Reuse default conversion for non-reasoning items.
	return responsesItemToOpenAIMessage(item)
}
