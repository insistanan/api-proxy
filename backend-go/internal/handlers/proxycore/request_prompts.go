package proxycore

import (
	"encoding/json"
	"strings"

	"github.com/insistanan/api-proxy/internal/conversation"
	"github.com/insistanan/api-proxy/internal/types"
	"github.com/insistanan/api-proxy/internal/utils"
)

func ExtractPromptsFromClaude(messages []types.ClaudeMessage) []string {
	prompts := make([]string, 0, 3)
	for _, msg := range messages {
		if strings.EqualFold(msg.Role, "user") {
			appendPromptsFromContent(&prompts, msg.Content, 3)
		}
	}
	return prompts
}

func ExtractPromptsFromOpenAI(messages []types.OpenAIMessage) []string {
	prompts := make([]string, 0, 3)
	for _, msg := range messages {
		if strings.EqualFold(msg.Role, "user") {
			appendPromptsFromContent(&prompts, msg.Content, 3)
		}
	}
	return prompts
}

func ExtractPromptsFromResponsesInput(input interface{}) []string {
	prompts := make([]string, 0, 3)
	appendResponsesInputPrompts(&prompts, input, 3)
	return prompts
}

func ExtractPromptsFromGemini(contents []types.GeminiContent) []string {
	prompts := make([]string, 0, 3)
	for _, content := range contents {
		if content.Role != "" && !strings.EqualFold(content.Role, "user") {
			continue
		}
		for _, part := range content.Parts {
			appendPrompt(&prompts, part.Text, 3)
		}
	}
	return prompts
}

// NormalizePromptTexts 把已经从**非 JSON** 载荷（例如 multipart/form-data 表单字段）
// 里取出的原始文本，按会话观测的统一口径归一化：清洗噪声 → 去重 → 至多 3 条。
// 目的是让这类入口的观测 prompt 与 ExtractPrompts* 系列输出同口径，
// 而不是各自 TrimSpace 出一份"看起来差不多"的结果。
func NormalizePromptTexts(texts ...string) []string {
	prompts := make([]string, 0, len(texts))
	for _, text := range texts {
		appendPrompt(&prompts, text, 3)
	}
	return prompts
}

func ExtractPromptJSONFieldPrompts(bodyBytes []byte, field string) []string {
	var payload map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &payload); err != nil {
		return nil
	}
	prompts := make([]string, 0, 3)
	appendPromptsFromContent(&prompts, payload[field], 3)
	return prompts
}

func appendResponsesInputPrompts(prompts *[]string, input interface{}, limit int) {
	if len(*prompts) >= limit {
		return
	}
	switch value := input.(type) {
	case []types.ResponsesItem:
		for _, item := range value {
			if len(*prompts) >= limit {
				return
			}
			if item.Role != "" && !strings.EqualFold(item.Role, "user") {
				continue
			}
			appendPromptsFromContent(prompts, item.Content, limit)
		}
	case []interface{}:
		for _, item := range value {
			if len(*prompts) >= limit {
				return
			}
			if msg, ok := item.(map[string]interface{}); ok {
				if role, _ := msg["role"].(string); role != "" && !strings.EqualFold(role, "user") {
					continue
				}
			}
			appendPromptsFromContent(prompts, item, limit)
		}
	default:
		appendPromptsFromContent(prompts, input, limit)
	}
}

func appendPromptsFromContent(prompts *[]string, content interface{}, limit int) {
	if len(*prompts) >= limit {
		return
	}
	switch value := content.(type) {
	case string:
		appendPrompt(prompts, value, limit)
	case []types.ClaudeContent:
		for _, block := range utils.NormalizeContentBlocks(value) {
			if text, ok := utils.ExtractTextFromBlock(block); ok {
				appendPrompt(prompts, text, limit)
			}
		}
	case []types.ContentBlock:
		for _, block := range utils.NormalizeContentBlocks(value) {
			if text, ok := utils.ExtractTextFromBlock(block); ok {
				appendPrompt(prompts, text, limit)
			}
		}
	case []interface{}:
		for _, item := range value {
			appendPromptsFromContent(prompts, item, limit)
			if len(*prompts) >= limit {
				return
			}
		}
	case map[string]interface{}:
		if text, ok := utils.ExtractTextFromBlock(value); ok {
			appendPrompt(prompts, text, limit)
		}
		for _, key := range []string{"content", "text", "input_text"} {
			appendPromptsFromContent(prompts, value[key], limit)
			if len(*prompts) >= limit {
				return
			}
		}
	}
}

func appendPrompt(prompts *[]string, prompt string, limit int) {
	prompt = normalizePrompt(prompt)
	if prompt == "" || len(*prompts) >= limit {
		return
	}
	for _, current := range *prompts {
		if current == prompt {
			return
		}
	}
	*prompts = append(*prompts, prompt)
}

func normalizePrompt(prompt string) string {
	prompt = cleanAndExtractRealPrompt(prompt)
	return strings.TrimSpace(strings.ReplaceAll(prompt, "\r\n", "\n"))
}

func cleanAndExtractRealPrompt(prompt string) string {
	return conversation.NormalizeUserPrompt(prompt)
}
