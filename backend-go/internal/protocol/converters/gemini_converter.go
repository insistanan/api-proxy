package converters

import (
	"strings"

	"insistanan/api-proxy/internal/protocol/contract"
	"insistanan/api-proxy/internal/protocol/types"
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
			reasoningCfg, err := ExtractReasoningFromGemini(cfg.ThinkingConfig, cfg.MaxOutputTokens)
			if err != nil {
				return nil, err
			}
			if reasoningCfg.BudgetTokens > 0 && (reasoningCfg.BudgetTokens < 1024 || reasoningCfg.BudgetTokens >= budget) {
				return nil, contract.Unsupported("Claude 思考预算必须至少 1024 且小于 max_tokens")
			}
			ApplyReasoningToClaude(reasoningCfg, claudeReq, budget)
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
			reasoningCfg, err := ExtractReasoningFromGemini(cfg.ThinkingConfig, cfg.MaxOutputTokens)
			if err != nil {
				return nil, err
			}
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
