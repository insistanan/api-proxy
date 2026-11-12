package converters

import (
	"strings"

	"github.com/insistanan/api-proxy/internal/protocol/types"
)

// ReasoningConfig 统一内部思考配置模型
type ReasoningConfig struct {
	Effort       string // none, auto, minimal, low, medium, high, xhigh, max
	BudgetTokens int    // >= 1024，或 0 表示未指定/自适应
}

// IsEnabled 判断是否启用了思考
func (rc ReasoningConfig) IsEnabled() bool {
	return rc.Effort != "" && rc.Effort != "none"
}

// ExtractReasoningFromClaude 从 Claude Messages 请求提取思考配置
func ExtractReasoningFromClaude(claudeReq *types.ClaudeRequest) ReasoningConfig {
	if claudeReq == nil {
		return ReasoningConfig{}
	}

	// 优先检查 output_config.effort
	if claudeReq.OutputConfig != nil {
		if rawEffort, ok := claudeReq.OutputConfig["effort"].(string); ok && rawEffort != "" {
			normEffort, err := NormalizeReasoningEffortForConstrainedUpstream(strings.ToLower(strings.TrimSpace(rawEffort)))
			if err == nil && normEffort != "" {
				return ReasoningConfig{
					Effort:       normEffort,
					BudgetTokens: ReasoningBudgetTokens(normEffort, claudeReq.MaxTokens),
				}
			}
		}
	}

	// 检查 thinking 字段
	if claudeReq.Thinking == nil {
		return ReasoningConfig{}
	}

	obj, ok := claudeReq.Thinking.(map[string]interface{})
	if !ok {
		return ReasoningConfig{}
	}

	typ, _ := obj["type"].(string)
	switch strings.ToLower(typ) {
	case "", "disabled":
		return ReasoningConfig{Effort: "none", BudgetTokens: 0}
	case "adaptive":
		return ReasoningConfig{Effort: "auto", BudgetTokens: 0}
	case "enabled":
		budget, ok := numericBudgetTokens(obj["budget_tokens"])
		if !ok || budget <= 0 {
			return ReasoningConfig{Effort: "high", BudgetTokens: reasoningBudgetHigh}
		}
		effort := EffortFromReasoningBudget(budget)
		return ReasoningConfig{Effort: effort, BudgetTokens: budget}
	default:
		return ReasoningConfig{}
	}
}

// ExtractReasoningFromOpenAIChat 从 OpenAI Chat Completions 请求提取思考配置
func ExtractReasoningFromOpenAIChat(payload map[string]interface{}) ReasoningConfig {
	if payload == nil {
		return ReasoningConfig{}
	}

	// 检查 reasoning_effort
	if raw, exists := payload["reasoning_effort"]; exists {
		if str, ok := raw.(string); ok && str != "" {
			norm, err := NormalizeReasoningEffortForConstrainedUpstream(strings.ToLower(strings.TrimSpace(str)))
			if err == nil && norm != "" {
				return ReasoningConfig{
					Effort:       norm,
					BudgetTokens: ReasoningBudgetTokens(norm, 0),
				}
			}
		}
	}

	// 检查兼容性 thinking 字段（某些第三方客户端或兼容网关可能传入 thinking）
	if rawThinking, exists := payload["thinking"]; exists {
		if obj, ok := rawThinking.(map[string]interface{}); ok {
			typ, _ := obj["type"].(string)
			if typ == "enabled" {
				budget, _ := numericBudgetTokens(obj["budget_tokens"])
				effort := "high"
				if budget > 0 {
					effort = EffortFromReasoningBudget(budget)
				}
				return ReasoningConfig{Effort: effort, BudgetTokens: budget}
			} else if typ == "adaptive" {
				return ReasoningConfig{Effort: "auto", BudgetTokens: 0}
			} else if typ == "disabled" {
				return ReasoningConfig{Effort: "none", BudgetTokens: 0}
			}
		}
	}

	return ReasoningConfig{}
}

// ExtractReasoningFromResponses 从 Responses API 请求提取思考配置
func ExtractReasoningFromResponses(reasoningRaw interface{}) ReasoningConfig {
	if reasoningRaw == nil {
		return ReasoningConfig{}
	}
	obj, ok := reasoningRaw.(map[string]interface{})
	if !ok {
		return ReasoningConfig{}
	}
	effort, _ := obj["effort"].(string)
	if effort == "" {
		return ReasoningConfig{}
	}
	norm, err := NormalizeReasoningEffortForConstrainedUpstream(strings.ToLower(strings.TrimSpace(effort)))
	if err != nil || norm == "" {
		return ReasoningConfig{}
	}
	return ReasoningConfig{
		Effort:       norm,
		BudgetTokens: ReasoningBudgetTokens(norm, 0),
	}
}

// ExtractReasoningFromGemini 从 Gemini GenerationConfig.ThinkingConfig 提取思考配置
func ExtractReasoningFromGemini(cfg *types.GeminiThinkingConfig, maxOutputTokens int) ReasoningConfig {
	if cfg == nil {
		return ReasoningConfig{}
	}
	effort := EffortFromGeminiThinkingConfig(cfg)
	if effort == "" {
		return ReasoningConfig{}
	}
	budget := 0
	if cfg.ThinkingBudget != nil && *cfg.ThinkingBudget > 0 {
		budget = int(*cfg.ThinkingBudget)
	} else {
		budget = ReasoningBudgetTokens(effort, maxOutputTokens)
	}
	return ReasoningConfig{
		Effort:       effort,
		BudgetTokens: budget,
	}
}

// ApplyReasoningToClaude 将统一思考配置应用到 Claude 请求字典
func ApplyReasoningToClaude(config ReasoningConfig, claudePayload map[string]interface{}, maxOutputTokens int) {
	if claudePayload == nil || !config.IsEnabled() {
		return
	}
	if config.Effort == "auto" {
		claudePayload["thinking"] = map[string]interface{}{
			"type": "adaptive",
		}
		return
	}
	budget := config.BudgetTokens
	if budget <= 0 {
		limit := maxOutputTokens
		if limit <= 0 {
			limit = 65536
		}
		budget = ReasoningBudgetTokens(config.Effort, limit)
	}
	if budget > 0 {
		claudePayload["thinking"] = map[string]interface{}{
			"type":          "enabled",
			"budget_tokens": budget,
		}
	}
}

// ApplyReasoningToOpenAIChat 将统一思考配置应用到 OpenAI Chat Completions 请求字典
func ApplyReasoningToOpenAIChat(config ReasoningConfig, chatPayload map[string]interface{}) {
	if chatPayload == nil || !config.IsEnabled() {
		return
	}
	reasoningEffort := ReasoningEffortToOpenAIChatReasoningEffort(config.Effort)
	if reasoningEffort != "" {
		chatPayload["reasoning_effort"] = reasoningEffort
	}
}

// ApplyReasoningToResponses 将统一思考配置应用到 Responses 请求字典
func ApplyReasoningToResponses(config ReasoningConfig, responsesPayload map[string]interface{}) {
	if responsesPayload == nil || !config.IsEnabled() {
		return
	}
	responsesPayload["reasoning"] = map[string]interface{}{
		"effort": config.Effort,
	}
}

// ApplyReasoningToGemini 将统一思考配置应用到 Gemini generationConfig 字典
func ApplyReasoningToGemini(config ReasoningConfig, generationConfig map[string]interface{}) {
	if generationConfig == nil || !config.IsEnabled() {
		return
	}
	thinkingConfig := map[string]interface{}{}
	switch config.Effort {
	case "none":
		thinkingConfig["thinkingBudget"] = 0
	case "auto":
		thinkingConfig["thinkingBudget"] = -1
	default:
		if config.BudgetTokens > 0 {
			thinkingConfig["thinkingBudget"] = config.BudgetTokens
		} else {
			thinkingConfig["thinkingLevel"] = config.Effort
		}
	}
	generationConfig["thinkingConfig"] = thinkingConfig
}

func numericBudgetTokens(v interface{}) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int32:
		return int(n), true
	case int64:
		return int(n), true
	case float64:
		return int(n), true
	default:
		return 0, false
	}
}
