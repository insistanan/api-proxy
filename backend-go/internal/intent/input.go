// Package intent 实现意图分类、分组决策及会话切换保护，不修改主请求提示词。
package intent

import (
	"encoding/json"
	"fmt"
	"strings"
)

type Input struct {
	Text             string
	ToolContinuation bool
	Opaque           bool
	History          bool
}

// ParseInput 只提取最后一个真实用户轮次。工具结果、系统提示和签名不送给分类器。
func ParseInput(body []byte) (Input, error) {
	var root map[string]any
	if err := json.Unmarshal(body, &root); err != nil {
		return Input{}, fmt.Errorf("解析意图输入失败: %w", err)
	}
	result := Input{}
	if id, _ := root["previous_response_id"].(string); id != "" {
		result.Opaque = true
		result.History = true
	}
	if id, _ := root["conversation"].(string); id != "" {
		result.Opaque = true
		result.History = true
	}
	if _, ok := root["conversation"].(map[string]any); ok {
		result.Opaque = true
		result.History = true
	}
	var items []any
	for _, key := range []string{"messages", "input", "contents"} {
		if list, ok := root[key].([]any); ok {
			items = list
			break
		}
	}
	if text, ok := root["input"].(string); ok {
		result.Text = text
	}
	userCount := 0
	for _, item := range items {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		role, _ := m["role"].(string)
		typ, _ := m["type"].(string)
		if role == "assistant" || role == "model" || typ == "reasoning" || typ == "function_call" {
			result.History = true
		}
		if role == "tool" || typ == "function_call_output" {
			result.ToolContinuation = true
			continue
		}
		if opaqueBlocks(m) {
			result.Opaque = true
		}
		if role != "user" {
			continue
		}
		userCount++
		result.ToolContinuation = toolBlocks(m)
		if !result.ToolContinuation {
			result.Text = visibleText(m)
		}
	}
	result.History = result.History || userCount > 1
	// 有界输入；按 rune 截断，避免中文 UTF-8 半字符。
	chars := []rune(strings.TrimSpace(result.Text))
	if len(chars) > 4000 {
		chars = chars[:4000]
	}
	result.Text = string(chars)
	return result, nil
}

func opaqueBlocks(v any) bool {
	switch m := v.(type) {
	case map[string]any:
		if typ, _ := m["type"].(string); typ == "thinking" || typ == "redacted_thinking" || typ == "compaction" {
			return true
		}
		for _, key := range []string{"encrypted_content", "signature", "thoughtSignature", "thought_signature"} {
			if value, ok := m[key]; ok && value != nil && value != "" {
				return true
			}
		}
		for _, key := range []string{"content", "parts"} {
			if opaqueBlocks(m[key]) {
				return true
			}
		}
	case []any:
		for _, item := range m {
			if opaqueBlocks(item) {
				return true
			}
		}
	}
	return false
}

func toolBlocks(v any) bool {
	switch m := v.(type) {
	case map[string]any:
		if m["type"] == "tool_result" || m["type"] == "function_call_output" || m["functionResponse"] != nil {
			return true
		}
		for _, key := range []string{"content", "parts"} {
			if toolBlocks(m[key]) {
				return true
			}
		}
	case []any:
		for _, item := range m {
			if toolBlocks(item) {
				return true
			}
		}
	}
	return false
}

func visibleText(v any) string {
	switch m := v.(type) {
	case string:
		return m
	case map[string]any:
		if text, ok := m["text"].(string); ok {
			return text
		}
		if content, ok := m["content"]; ok {
			return visibleText(content)
		}
		return visibleText(m["parts"])
	case []any:
		parts := make([]string, 0, len(m))
		for _, item := range m {
			if text := visibleText(item); text != "" {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, "\n")
	}
	return ""
}
