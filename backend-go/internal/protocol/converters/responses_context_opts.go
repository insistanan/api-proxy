package converters

import (
	"strings"

	"github.com/insistanan/api-proxy/internal/infra/utils"
	"github.com/insistanan/api-proxy/internal/protocol/types"
)

// ExtractResponsesReasoningText 从 Responses reasoning item 的 summary/content 抽出文本。
func ExtractResponsesReasoningText(item types.ResponsesItem) string {
	if original := ExtractResponsesOriginalReasoningText(item); original != "" {
		return original
	}
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

// ExtractResponsesOriginalReasoningText 仅提取有明确类型的原始推理，保留全部空白。
// 展示摘要和不透明 encrypted_content 不能作为 Chat 推理历史的原文。
func ExtractResponsesOriginalReasoningText(item types.ResponsesItem) string {
	var original strings.Builder
	for _, block := range utils.NormalizeContentBlocks(item.Content) {
		if block["type"] == "reasoning_text" {
			if text, ok := block["text"].(string); ok {
				original.WriteString(text)
			}
		}
	}
	return original.String()
}
