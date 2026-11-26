package providers

import (
	"encoding/json"
	"fmt"
	"strings"

	"insistanan/api-proxy/internal/protocol/contract"
	"insistanan/api-proxy/internal/protocol/types"
)

// ConvertToClaudeResponse 转换为 Claude 响应
func (p *GeminiProvider) ConvertToClaudeResponse(providerResp *types.ProviderResponse) (*types.ClaudeResponse, error) {
	if err := contract.ValidateResponse("gemini", providerResp.Body); err != nil {
		return nil, err
	}
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

	// 完整响应已通过公共契约校验，候选结构不再用空成功响应兜底。
	candidates := geminiResp["candidates"].([]interface{})
	candidate := candidates[0].(map[string]interface{})
	finishReason, _ := candidate["finishReason"].(string)
	stopReason, err := geminiStopReasonFromFinish(finishReason, false)
	if err != nil {
		return nil, err
	}
	claudeResp.StopReason = stopReason

	// 安全拒绝可不带正文，仍要处理终态与用量。
	content, _ := candidate["content"].(map[string]interface{})
	parts, _ := content["parts"].([]interface{})

	rectifyGeminiFunctionCallIDs(parts)
	shadowTurn := buildGeminiShadowTurnFromParts(parts)
	if geminiShadowTurnHasParts(shadowTurn) && p.persistShadow != nil {
		if err := p.persistShadow(shadowTurn); err != nil {
			return nil, err
		}
	}

	textParts := make([]string, 0)
	toolUseParts := make([]types.ClaudeContent, 0)

	// 处理各个部分
	for _, p := range parts {
		part, ok := p.(map[string]interface{})
		if !ok {
			continue
		}

		if isGeminiThoughtPart(part) {
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

	// 只有正常结束才能改为工具交付；工具内容不能掩盖截断或审核拒绝。
	if claudeResp.StopReason == "end_turn" {
		for _, block := range claudeResp.Content {
			if block.Type == "tool_use" {
				claudeResp.StopReason = "tool_use"
				break
			}
		}
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

// Gemini 的 MAX_TOKENS 必须在工具交付前判断，流式与非流式共用同一映射。
func geminiStopReasonFromFinish(finishReason string, hasToolCall bool) (string, error) {
	switch strings.ToUpper(strings.TrimSpace(finishReason)) {
	case "":
		return "", nil
	case "STOP":
		if hasToolCall {
			return "tool_use", nil
		}
		return "end_turn", nil
	case "MAX_TOKENS", "LENGTH":
		return "max_tokens", nil
	case "SAFETY", "RECITATION", "BLOCKLIST", "PROHIBITED_CONTENT", "SPII", "IMAGE_SAFETY", "IMAGE_PROHIBITED_CONTENT", "MODEL_ARMOR":
		return "refusal", nil
	default:
		return "", fmt.Errorf("Gemini 响应未正常完成，finishReason=%q", finishReason)
	}
}
