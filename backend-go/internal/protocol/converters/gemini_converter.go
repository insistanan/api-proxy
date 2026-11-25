package converters

import (
	"fmt"
	"strings"

	"github.com/insistanan/api-proxy/internal/protocol/contract"
	"github.com/insistanan/api-proxy/internal/protocol/types"
)

// ============== Gemini -> Claude/OpenAI 转换器 ==============

// GeminiToClaudeRequest 将 Gemini 请求转换为 Claude Messages API 格式
func GeminiToClaudeRequest(geminiReq *types.GeminiRequest, model string) (map[string]interface{}, error) {
	if err := validateGeminiConversion(geminiReq, "claude"); err != nil {
		return nil, err
	}
	claudeReq := map[string]interface{}{
		"model": model,
	}

	budget := 0
	if geminiReq.GenerationConfig != nil {
		budget = geminiReq.GenerationConfig.MaxOutputTokens
	}
	budget, err := claudeOutputBudget(model, budget)
	if err != nil {
		return nil, err
	}
	claudeReq["max_tokens"] = budget
	// 1. 转换 systemInstruction -> system
	if geminiReq.SystemInstruction != nil && len(geminiReq.SystemInstruction.Parts) > 0 {
		systemText := extractTextFromGeminiParts(geminiReq.SystemInstruction.Parts)
		if systemText != "" {
			claudeReq["system"] = systemText
		}
	}

	// 2. 转换 contents -> messages
	messages, err := convertGeminiHistory(geminiReq.Contents, "claude")
	if err != nil {
		return nil, err
	}
	claudeReq["messages"] = messages

	// 3. 转换 generationConfig
	if geminiReq.GenerationConfig != nil {
		cfg := geminiReq.GenerationConfig
		if cfg.MaxOutputTokens > 0 {
			claudeReq["max_tokens"] = cfg.MaxOutputTokens
		}
		if cfg.Temperature != nil {
			claudeReq["temperature"] = *cfg.Temperature
		}
		if cfg.TopP != nil {
			claudeReq["top_p"] = *cfg.TopP
		}
		if cfg.TopK != nil {
			claudeReq["top_k"] = *cfg.TopK
		}
		if len(cfg.StopSequences) > 0 {
			claudeReq["stop_sequences"] = cfg.StopSequences
		}
		// 转换 thinkingConfig -> Claude thinking（type=enabled + budget_tokens）。
		// 之前完全漏读 ThinkingConfig，导致 Gemini 入口转 Claude 上游时思考配置被静默丢弃。
		if cfg.ThinkingConfig != nil {
			reasoningCfg := ExtractReasoningFromGemini(cfg.ThinkingConfig, cfg.MaxOutputTokens)
			ApplyReasoningToClaude(reasoningCfg, claudeReq, cfg.MaxOutputTokens)
		}
	}

	// 4. 转换 tools -> tools
	if len(geminiReq.Tools) > 0 {
		claudeTools := []map[string]interface{}{}
		for _, tool := range geminiReq.Tools {
			for _, fn := range tool.FunctionDeclarations {
				claudeTool := map[string]interface{}{
					"name": fn.Name,
				}
				if fn.Description != "" {
					claudeTool["description"] = fn.Description
				}
				if fn.JSONSchema() != nil {
					claudeTool["input_schema"] = fn.JSONSchema()
				} else {
					// Claude 需要 input_schema，提供空 schema
					claudeTool["input_schema"] = map[string]interface{}{
						"type":       "object",
						"properties": map[string]interface{}{},
					}
				}
				claudeTools = append(claudeTools, claudeTool)
			}
		}
		if len(claudeTools) > 0 {
			claudeReq["tools"] = claudeTools
		}
	}

	if err := applyGeminiToolChoice(geminiReq, "claude", claudeReq); err != nil {
		return nil, err
	}
	return claudeReq, nil
}

// GeminiToOpenAIRequest 将 Gemini 请求转换为 OpenAI Chat Completions 格式
func GeminiToOpenAIRequest(geminiReq *types.GeminiRequest, model string) (map[string]interface{}, error) {
	if err := validateGeminiConversion(geminiReq, "openai"); err != nil {
		return nil, err
	}
	openaiReq := map[string]interface{}{
		"model": model,
	}

	messages := []map[string]interface{}{}

	// 1. 转换 systemInstruction -> system message
	if geminiReq.SystemInstruction != nil && len(geminiReq.SystemInstruction.Parts) > 0 {
		systemText := extractTextFromGeminiParts(geminiReq.SystemInstruction.Parts)
		if systemText != "" {
			messages = append(messages, map[string]interface{}{
				"role":    "system",
				"content": systemText,
			})
		}
	}

	// 2. 转换 contents -> messages
	history, err := convertGeminiHistory(geminiReq.Contents, "openai")
	if err != nil {
		return nil, err
	}
	messages = append(messages, history...)
	openaiReq["messages"] = messages

	// 3. 转换 generationConfig
	if geminiReq.GenerationConfig != nil {
		cfg := geminiReq.GenerationConfig
		if cfg.MaxOutputTokens > 0 {
			openaiReq["max_tokens"] = cfg.MaxOutputTokens
		}
		if cfg.Temperature != nil {
			openaiReq["temperature"] = *cfg.Temperature
		}
		if cfg.TopP != nil {
			openaiReq["top_p"] = *cfg.TopP
		}
		if len(cfg.StopSequences) > 0 {
			openaiReq["stop"] = cfg.StopSequences
		}
		// 转换 thinkingConfig -> reasoning_effort。之前完全漏读 ThinkingConfig，
		// 导致 Gemini 入口转 OpenAI Chat 上游时思考配置被静默丢弃。
		if cfg.ThinkingConfig != nil {
			reasoningCfg := ExtractReasoningFromGemini(cfg.ThinkingConfig, cfg.MaxOutputTokens)
			ApplyReasoningToOpenAIChat(reasoningCfg, openaiReq)
		}
	}

	// 4. 转换 tools -> tools
	if len(geminiReq.Tools) > 0 {
		openaiTools := []map[string]interface{}{}
		for _, tool := range geminiReq.Tools {
			for _, fn := range tool.FunctionDeclarations {
				openaiTool := map[string]interface{}{
					"type": "function",
					"function": map[string]interface{}{
						"name":        fn.Name,
						"description": fn.Description,
						"parameters":  fn.JSONSchema(),
					},
				}
				openaiTools = append(openaiTools, openaiTool)
			}
		}
		if len(openaiTools) > 0 {
			openaiReq["tools"] = openaiTools
		}
	}

	if err := applyGeminiToolChoice(geminiReq, "openai", openaiReq); err != nil {
		return nil, err
	}
	return openaiReq, nil
}

// ============== Claude/OpenAI -> Gemini 响应转换 ==============

// ClaudeResponseToGemini 将 Claude 响应转换为 Gemini 格式
func ClaudeResponseToGemini(claudeResp map[string]interface{}) (*types.GeminiResponse, error) {
	geminiResp := &types.GeminiResponse{
		Candidates: []types.GeminiCandidate{},
	}

	// 1. 转换 content -> candidates[0].content.parts
	content, ok := claudeResp["content"].([]interface{})
	if !ok {
		return nil, fmt.Errorf("上游响应缺少有效内容对象")
	}

	parts := []types.GeminiPart{}
	for _, c := range content {
		contentBlock, ok := c.(map[string]interface{})
		if !ok {
			continue
		}

		blockType, _ := contentBlock["type"].(string)
		switch blockType {
		case "text":
			text, _ := contentBlock["text"].(string)
			parts = append(parts, types.GeminiPart{
				Text: text,
			})
		case "tool_use":
			name, _ := contentBlock["name"].(string)
			args, ok := contentBlock["input"].(map[string]interface{})
			if !ok || args == nil {
				return nil, fmt.Errorf("Claude 工具 %q 的 input 必须是对象", name)
			}

			functionCall := &types.GeminiFunctionCall{
				ID: stringMapValue(contentBlock, "id"), Name: name,
				Args: args,
			}

			// Claude 签名不能用于 Gemini 回放，不跨协议复制或生成占位签名。

			parts = append(parts, types.GeminiPart{
				FunctionCall: functionCall,
			})
		}
	}

	// 2. 转换 stop_reason -> finishReason
	finishReason, err := GeminiFinishReason("claude", stringMapValue(claudeResp, "stop_reason"))
	if err != nil {
		return nil, err
	}

	candidate := types.GeminiCandidate{
		Content: &types.GeminiContent{
			Parts: parts,
			Role:  "model",
		},
		FinishReason: finishReason,
		Index:        0,
	}
	geminiResp.Candidates = append(geminiResp.Candidates, candidate)

	// 3. 转换 usage -> usageMetadata
	if usageRaw, ok := claudeResp["usage"].(map[string]interface{}); ok {
		inputTokens, _ := getIntFromMap(usageRaw, "input_tokens")
		outputTokens, _ := getIntFromMap(usageRaw, "output_tokens")
		cacheRead, _ := getIntFromMap(usageRaw, "cache_read_input_tokens")

		geminiResp.UsageMetadata = &types.GeminiUsageMetadata{
			PromptTokenCount:        inputTokens + cacheRead, // Gemini 格式包含缓存
			CandidatesTokenCount:    outputTokens,
			TotalTokenCount:         inputTokens + cacheRead + outputTokens,
			CachedContentTokenCount: cacheRead,
		}
	}

	return geminiResp, nil
}

// OpenAIResponseToGemini 将 OpenAI 响应转换为 Gemini 格式
func OpenAIResponseToGemini(openaiResp map[string]interface{}) (*types.GeminiResponse, error) {
	geminiResp := &types.GeminiResponse{
		Candidates: []types.GeminiCandidate{},
	}

	// 1. 转换 choices[0].message -> candidates[0].content
	choices, ok := openaiResp["choices"].([]interface{})
	if !ok || len(choices) != 1 {
		return nil, fmt.Errorf("Chat → Gemini 需要恰好一个 choice")
	}

	choice, ok := choices[0].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("上游响应缺少有效内容对象")
	}

	parts := []types.GeminiPart{}
	finishReason, err := GeminiFinishReason("openai", stringMapValue(choice, "finish_reason"))
	if err != nil {
		return nil, err
	}

	// 处理 message
	if _, ok := choice["message"].(map[string]interface{}); !ok {
		return nil, fmt.Errorf("Chat choice 缺少 message 对象")
	}
	if message, ok := choice["message"].(map[string]interface{}); ok {
		// 文本内容
		if content, ok := message["content"].(string); ok && content != "" {
			parts = append(parts, types.GeminiPart{
				Text: content,
			})
		}

		// 工具调用
		if toolCalls, ok := message["tool_calls"].([]interface{}); ok {
			for _, tc := range toolCalls {
				toolCall, ok := tc.(map[string]interface{})
				if !ok {
					continue
				}
				function, ok := toolCall["function"].(map[string]interface{})
				if !ok {
					continue
				}
				name, _ := function["name"].(string)
				argsStr, _ := function["arguments"].(string)
				args, err := contract.Arguments(argsStr, name)
				if err != nil {
					return nil, err
				}

				// Chat 工具调用没有 Gemini 签名，转换时只保留工具内容。
				parts = append(parts, types.GeminiPart{
					FunctionCall: &types.GeminiFunctionCall{
						ID: stringMapValue(toolCall, "id"), Name: name,
						Args: args,
					},
				})
			}
		}
	}

	candidate := types.GeminiCandidate{
		Content: &types.GeminiContent{
			Parts: parts,
			Role:  "model",
		},
		FinishReason: finishReason,
		Index:        0,
	}
	geminiResp.Candidates = append(geminiResp.Candidates, candidate)

	// 2. 转换 usage -> usageMetadata
	if usageRaw, ok := openaiResp["usage"].(map[string]interface{}); ok {
		promptTokens, _ := getIntFromMap(usageRaw, "prompt_tokens")
		completionTokens, _ := getIntFromMap(usageRaw, "completion_tokens")

		// 提取缓存读取（OpenAI 格式）
		var cachedTokens int
		if details, ok := usageRaw["prompt_token_details"].(map[string]interface{}); ok {
			cachedTokens, _ = getIntFromMap(details, "cached_tokens")
		}
		if cachedTokens == 0 {
			if details, ok := usageRaw["input_tokens_details"].(map[string]interface{}); ok {
				cachedTokens, _ = getIntFromMap(details, "cached_tokens")
			}
		}

		// OpenAI 的 prompt_tokens 包含缓存，Gemini 格式也需要包含
		geminiResp.UsageMetadata = &types.GeminiUsageMetadata{
			PromptTokenCount:        promptTokens, // OpenAI 的 prompt_tokens 已包含缓存
			CandidatesTokenCount:    completionTokens,
			TotalTokenCount:         promptTokens + completionTokens,
			CachedContentTokenCount: cachedTokens,
		}
	}

	return geminiResp, nil
}

// ============== 辅助函数 ==============

// extractTextFromGeminiParts 从 Gemini Parts 中提取文本
func extractTextFromGeminiParts(parts []types.GeminiPart) string {
	texts := []string{}
	for _, part := range parts {
		if !part.Thought && part.Text != "" {
			texts = append(texts, part.Text)
		}
	}
	return strings.Join(texts, "\n")
}

func stringMapValue(value map[string]interface{}, key string) string {
	result, _ := value[key].(string)
	return result
}

// GeminiFinishReason 与 Responses 终态共用源协议判定，未知原因显式报错。
func GeminiFinishReason(protocol, reason string) (string, error) {
	completion, err := contract.Finish(protocol, reason)
	if err != nil {
		return "", err
	}
	switch completion.Reason {
	case "max_output_tokens":
		return "MAX_TOKENS", nil
	case "content_filter":
		return "SAFETY", nil
	}
	return "STOP", nil
}
