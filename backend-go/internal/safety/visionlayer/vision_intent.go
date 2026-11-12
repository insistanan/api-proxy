package visionlayer

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"github.com/insistanan/api-proxy/internal/protocol/types"
)

func extractUserText(value interface{}) string {
	var parts []string
	collectText(value, &parts)
	result := strings.TrimSpace(strings.Join(parts, "\n"))
	if len(result) > 2000 {
		return result[len(result)-2000:]
	}
	return result
}

func resolveVisionAnalysisProfile(userText string) visionAnalysisProfile {
	userIntent := strings.TrimSpace(userText)
	if userIntent == "" {
		userIntent = "用户未提供文字问题，请根据图片内容进行可靠观察。"
	}
	return visionAnalysisProfile{
		intentFingerprint: visionIntentFingerprint(userIntent),
		userIntent:        userIntent,
	}
}

func visionIntentFingerprint(userText string) string {
	normalized := strings.ToLower(strings.Join(strings.Fields(userText), " "))
	runes := []rune(normalized)
	if len(runes) > 600 {
		normalized = string(runes[:600])
	}
	sum := sha256.Sum256([]byte(normalized))
	return hex.EncodeToString(sum[:])
}

func collectText(value interface{}, parts *[]string) {
	switch current := value.(type) {
	case map[string]interface{}:
		if typeValue, _ := current["type"].(string); strings.EqualFold(typeValue, "text") || strings.EqualFold(typeValue, "input_text") {
			if text, ok := current["text"].(string); ok && strings.TrimSpace(text) != "" {
				*parts = append(*parts, text)
			}
			return
		}
		// Gemini 格式: 无 type 字段, 直接 {"text": "..."}
		if _, hasType := current["type"]; !hasType {
			if text, ok := current["text"].(string); ok && strings.TrimSpace(text) != "" {
				*parts = append(*parts, text)
				return
			}
		}
		if role, ok := current["role"].(string); ok {
			if !strings.EqualFold(strings.TrimSpace(role), "user") {
				return
			}
			for _, key := range []string{"content", "parts"} {
				if child, exists := current[key]; exists {
					collectText(child, parts)
				}
			}
			return
		}
		for _, key := range []string{"messages", "contents", "input", "content", "parts"} {
			if child, exists := current[key]; exists {
				collectText(child, parts)
			}
		}
	case []interface{}:
		for _, child := range current {
			collectText(child, parts)
		}
	case string:
		if strings.TrimSpace(current) != "" {
			*parts = append(*parts, current)
		}
	}
}

func extractResponseText(response *types.ClaudeResponse) string {
	if response == nil {
		return ""
	}
	parts := make([]string, 0, len(response.Content))
	for _, block := range response.Content {
		if strings.EqualFold(block.Type, "text") && strings.TrimSpace(block.Text) != "" {
			parts = append(parts, strings.TrimSpace(block.Text))
		}
	}
	return strings.Join(parts, "\n")
}
