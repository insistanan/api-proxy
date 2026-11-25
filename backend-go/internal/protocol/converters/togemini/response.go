package togemini

import (
	"encoding/json"
	"fmt"

	"github.com/insistanan/api-proxy/internal/protocol/contract"
	"github.com/insistanan/api-proxy/internal/protocol/tokenusage"
	"github.com/insistanan/api-proxy/internal/protocol/types"
)

// ============== Claude/OpenAI -> Gemini 响应转换 ==============

// ClaudeResponse 将 Claude 响应转换为 Gemini 格式
func ClaudeResponse(claudeResp map[string]interface{}) (*types.GeminiResponse, error) {
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
		case "thinking":
			text, _ := contentBlock["thinking"].(string)
			parts = append(parts, types.GeminiPart{Text: text, Thought: true})
		case "redacted_thinking":
			return nil, contract.Unsupported("Gemini 无法表示 Claude redacted_thinking")
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
		default:
			return nil, contract.Unsupported("Gemini 无法表示 Claude 内容块 %q", blockType)
		}
	}

	// 2. 转换 stop_reason -> finishReason
	finishReason, err := responseFinishReason("claude", claudeResp, "stop_reason")
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
	geminiResp.UsageMetadata, err = responseUsage(claudeResp["usage"])
	if err != nil {
		return nil, err
	}

	return geminiResp, nil
}

// ChatResponse 将 OpenAI 响应转换为 Gemini 格式
func ChatResponse(openaiResp map[string]interface{}) (*types.GeminiResponse, error) {
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
	finishReason, err := responseFinishReason("openai", choice, "finish_reason")
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
	geminiResp.UsageMetadata, err = responseUsage(openaiResp["usage"])
	if err != nil {
		return nil, err
	}

	return geminiResp, nil
}

// ============== 辅助函数 ==============

func stringMapValue(value map[string]interface{}, key string) string {
	result, _ := value[key].(string)
	return result
}

// FinishReason 与 Responses 终态共用源协议判定，未知原因显式报错。
func FinishReason(protocol, reason string) (string, error) {
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

// 用量解析使用公共协议规则，转换层只负责 Gemini 的字段表示。
func responseUsage(raw interface{}) (*types.GeminiUsageMetadata, error) {
	if raw == nil {
		return nil, nil
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return nil, err
	}
	usage, err := tokenusage.ParseJSONUsage(encoded)
	if err != nil {
		return nil, err
	}
	if usage == nil {
		return nil, fmt.Errorf("响应 usage 必须是对象")
	}
	return geminiUsage(usage), nil
}

func geminiUsage(usage *types.Usage) *types.GeminiUsageMetadata {
	input := usage.InputTokens + usage.CacheReadInputTokens + usage.CacheCreationInputTokens
	if usage.UnattributedCacheInput != nil {
		input = usage.InputTokens
	}
	return &types.GeminiUsageMetadata{PromptTokenCount: input, CandidatesTokenCount: usage.OutputTokens, TotalTokenCount: input + usage.OutputTokens, CachedContentTokenCount: usage.CacheReadInputTokens}
}

func responseFinishReason(protocol string, object map[string]interface{}, key string) (string, error) {
	reason, ok := object[key].(string)
	if !ok || reason == "" {
		return "", fmt.Errorf("上游 %s 普通响应缺少有效 %s", protocol, key)
	}
	return FinishReason(protocol, reason)
}
