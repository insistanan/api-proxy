package converters

import (
	"testing"

	"github.com/insistanan/api-proxy/internal/protocol/types"
	"github.com/stretchr/testify/assert"
)

func TestExtractReasoningFromClaude(t *testing.T) {
	// 1. thinking: enabled + budget_tokens
	claudeReq := &types.ClaudeRequest{
		Thinking: map[string]interface{}{
			"type":          "enabled",
			"budget_tokens": 8192,
		},
		MaxTokens: 16384,
	}
	cfg := ExtractReasoningFromClaude(claudeReq)
	assert.True(t, cfg.IsEnabled())
	assert.Equal(t, "high", cfg.Effort)
	assert.Equal(t, 8192, cfg.BudgetTokens)

	// 2. output_config.effort
	claudeReq2 := &types.ClaudeRequest{
		OutputConfig: map[string]interface{}{
			"effort": "low",
		},
		MaxTokens: 8192,
	}
	cfg2 := ExtractReasoningFromClaude(claudeReq2)
	assert.True(t, cfg2.IsEnabled())
	assert.Equal(t, "low", cfg2.Effort)
	assert.Equal(t, 2048, cfg2.BudgetTokens)

	// 3. thinking: adaptive
	claudeReq3 := &types.ClaudeRequest{
		Thinking: map[string]interface{}{
			"type": "adaptive",
		},
	}
	cfg3 := ExtractReasoningFromClaude(claudeReq3)
	assert.True(t, cfg3.IsEnabled())
	assert.Equal(t, "auto", cfg3.Effort)
	assert.Equal(t, 0, cfg3.BudgetTokens)
}

func TestExtractReasoningFromOpenAIChat(t *testing.T) {
	// 1. 标准 reasoning_effort
	payload := map[string]interface{}{
		"model":            "o3-mini",
		"reasoning_effort": "high",
	}
	cfg := ExtractReasoningFromOpenAIChat(payload)
	assert.True(t, cfg.IsEnabled())
	assert.Equal(t, "high", cfg.Effort)
	assert.Equal(t, 8192, cfg.BudgetTokens)

	// 2. 第三方兼容 thinking
	payload2 := map[string]interface{}{
		"model": "deepseek-reasoner",
		"thinking": map[string]interface{}{
			"type":          "enabled",
			"budget_tokens": 4096,
		},
	}
	cfg2 := ExtractReasoningFromOpenAIChat(payload2)
	assert.True(t, cfg2.IsEnabled())
	assert.Equal(t, "medium", cfg2.Effort)
	assert.Equal(t, 4096, cfg2.BudgetTokens)
}

func TestExtractReasoningFromResponses(t *testing.T) {
	reasoningRaw := map[string]interface{}{
		"effort": "medium",
	}
	cfg := ExtractReasoningFromResponses(reasoningRaw)
	assert.True(t, cfg.IsEnabled())
	assert.Equal(t, "medium", cfg.Effort)
	assert.Equal(t, 4096, cfg.BudgetTokens)
}

func TestExtractReasoningFromGemini(t *testing.T) {
	budget := int32(4096)
	geminiCfg := &types.GeminiThinkingConfig{
		ThinkingBudget: &budget,
	}
	cfg := ExtractReasoningFromGemini(geminiCfg, 8192)
	assert.True(t, cfg.IsEnabled())
	assert.Equal(t, "medium", cfg.Effort)
	assert.Equal(t, 4096, cfg.BudgetTokens)
}

func TestApplyReasoningToTargets(t *testing.T) {
	cfg := ReasoningConfig{
		Effort:       "high",
		BudgetTokens: 8192,
	}

	// 1. Apply to Claude
	claudePayload := map[string]interface{}{}
	ApplyReasoningToClaude(cfg, claudePayload, 16384)
	thinking, ok := claudePayload["thinking"].(map[string]interface{})
	assert.True(t, ok)
	assert.Equal(t, "enabled", thinking["type"])
	assert.Equal(t, 8192, thinking["budget_tokens"])

	// 2. Apply to OpenAI Chat
	chatPayload := map[string]interface{}{}
	ApplyReasoningToOpenAIChat(cfg, chatPayload)
	assert.Equal(t, "high", chatPayload["reasoning_effort"])

	// 3. Apply to Responses
	responsesPayload := map[string]interface{}{}
	ApplyReasoningToResponses(cfg, responsesPayload)
	reasoning, ok := responsesPayload["reasoning"].(map[string]interface{})
	assert.True(t, ok)
	assert.Equal(t, "high", reasoning["effort"])

	// 4. Apply to Gemini
	generationConfig := map[string]interface{}{}
	ApplyReasoningToGemini(cfg, generationConfig)
	thinkingConfig, ok := generationConfig["thinkingConfig"].(map[string]interface{})
	assert.True(t, ok)
	assert.Equal(t, 8192, thinkingConfig["thinkingBudget"])
}
