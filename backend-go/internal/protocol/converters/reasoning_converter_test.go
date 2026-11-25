package converters

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"insistanan/api-proxy/internal/protocol/types"
)

func TestDisabledThinkingRemainsExplicitAcrossProtocols(t *testing.T) {
	budget := int32(0)
	input := &types.GeminiRequest{GenerationConfig: &types.GeminiGenerationConfig{MaxOutputTokens: 20000, ThinkingConfig: &types.GeminiThinkingConfig{ThinkingBudget: &budget}}}
	chat, err := GeminiToOpenAIRequest(input, "chat-model")
	assert.NoError(t, err)
	assert.Equal(t, "none", chat["reasoning_effort"])
	claude, err := GeminiToClaudeRequest(input, "claude-model")
	assert.NoError(t, err)
	assert.Equal(t, "disabled", claude["thinking"].(map[string]interface{})["type"])
	thinking, err := ResponsesReasoningToClaudeThinking(map[string]interface{}{"effort": "none"}, 4000)
	assert.NoError(t, err)
	assert.Equal(t, "disabled", thinking.(map[string]interface{})["type"])
	conflict, err := ExtractReasoningFromClaude(&types.ClaudeRequest{Thinking: map[string]interface{}{"type": "disabled"}, OutputConfig: map[string]interface{}{"effort": "max"}})
	assert.NoError(t, err)
	assert.Equal(t, "none", conflict.Effort)
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
