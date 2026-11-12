package providers

import (
	"encoding/json"
	"strings"

	"github.com/insistanan/api-proxy/internal/protocol/types"
)

// ConvertToClaudeResponse 转换为 Claude 响应
func (p *GeminiProvider) ConvertToClaudeResponse(providerResp *types.ProviderResponse) (*types.ClaudeResponse, error) {
	var geminiResp map[string]interface{}
	if err := json.Unmarshal(providerResp.Body, &geminiResp); err != nil {
		return nil, err
	}

	claudeResp := &types.ClaudeResponse{
		ID:      generateID(),
		Type:    "message",
		Role:    "assistant",
		Content: []types.ClaudeContent{},
	}

	candidates, ok := geminiResp["candidates"].([]interface{})
	if !ok || len(candidates) == 0 {
		return claudeResp, nil
	}

	candidate, ok := candidates[0].(map[string]interface{})
	if !ok {
		return claudeResp, nil
	}

	content, ok := candidate["content"].(map[string]interface{})
	if !ok {
		return claudeResp, nil
	}

	parts, ok := content["parts"].([]interface{})
	if !ok {
		return claudeResp, nil
	}

	rectifyGeminiFunctionCallIDs(parts)
	shadowTurn := buildGeminiShadowTurnFromParts(parts)
	if geminiShadowTurnHasParts(shadowTurn) {
		defaultGeminiShadowStore.Record(p.shadowProviderID, p.shadowSessionID, shadowTurn)
	}

	thinkingParts := make([]types.ClaudeContent, 0)
	textParts := make([]string, 0)
	toolUseParts := make([]types.ClaudeContent, 0)

	// 处理各个部分
	for _, p := range parts {
		part, ok := p.(map[string]interface{})
		if !ok {
			continue
		}

		if isGeminiThoughtPart(part) {
			if thought, _ := part["text"].(string); thought != "" {
				thinkingParts = append(thinkingParts, types.ClaudeContent{
					Type:     "thinking",
					Thinking: thought,
				})
			}
			continue
		}

		// 文本内容
		if text, ok := part["text"].(string); ok {
			textParts = append(textParts, text)
		}

		// 函数调用
		if fc, ok := part["functionCall"].(map[string]interface{}); ok {
			name, _ := fc["name"].(string)
			args := fc["args"]
			id, _ := fc["id"].(string)

			toolUseParts = append(toolUseParts, types.ClaudeContent{
				Type:  "tool_use",
				ID:    id,
				Name:  name,
				Input: args,
			})
		}
	}
	if len(textParts) > 0 {
		claudeResp.Content = append(claudeResp.Content, types.ClaudeContent{
			Type: "text",
			Text: strings.Join(textParts, ""),
		})
	}
	claudeResp.Content = append(claudeResp.Content, toolUseParts...)
	// Gemini 的 thought 是上游内部推理，不应转换成 Claude thinking 下发。
	// Cursor 会把客户端收到的 thinking 原样写回下一轮 input；下发它会让
	// reasoning 变成可重放的普通上下文。只在代理内部登记，供切换到严格
	// reasoning_content 的 Chat 渠道时恢复。
	if len(thinkingParts) > 0 {
		cacheResp := *claudeResp
		cacheResp.Content = append(append([]types.ClaudeContent{}, thinkingParts...), claudeResp.Content...)
		CacheClaudeResponseReasoning(&cacheResp)
	}

	// 设置停止原因
	finishReason, _ := candidate["finishReason"].(string)
	if strings.Contains(strings.ToLower(finishReason), "stop") {
		// 检查是否有工具调用
		hasToolCall := false
		for _, c := range claudeResp.Content {
			if c.Type == "tool_use" {
				hasToolCall = true
				break
			}
		}

		if hasToolCall {
			claudeResp.StopReason = "tool_use"
		} else {
			claudeResp.StopReason = "end_turn"
		}
	} else if strings.Contains(strings.ToLower(finishReason), "length") {
		claudeResp.StopReason = "max_tokens"
	}

	// 使用统计
	if usageMetadata, ok := geminiResp["usageMetadata"].(map[string]interface{}); ok {
		usage := &types.Usage{}
		cachedTokens := 0

		if cachedContentTokens, ok := usageMetadata["cachedContentTokenCount"].(float64); ok {
			cachedTokens = int(cachedContentTokens)
			usage.CacheReadInputTokens = cachedTokens
		}

		if promptTokens, ok := usageMetadata["promptTokenCount"].(float64); ok {
			usage.InputTokens = int(promptTokens) - cachedTokens
			if usage.InputTokens < 0 {
				usage.InputTokens = 0
			}
		}
		thoughtsTokens := 0
		if v, ok := usageMetadata["thoughtsTokenCount"].(float64); ok {
			thoughtsTokens = int(v)
		}
		// candidatesTokenCount 不含 thoughtsTokenCount，而 Anthropic 的 output_tokens
		// 含 thinking，必须相加（三家语义对照与官方证据见 types.ClaudeOutputTokensDetails）。
		if candidatesTokens, ok := usageMetadata["candidatesTokenCount"].(float64); ok {
			usage.OutputTokens = int(candidatesTokens) + thoughtsTokens
		} else if thoughtsTokens > 0 {
			// 上游只报了 thoughts、没报 candidates。输出确实已经产生，不能记 0。
			usage.OutputTokens = thoughtsTokens
		}
		if thoughtsTokens > 0 {
			usage.OutputTokensDetails = &types.ClaudeOutputTokensDetails{ThinkingTokens: thoughtsTokens}
		}
		claudeResp.Usage = usage
	}

	return claudeResp, nil
}
