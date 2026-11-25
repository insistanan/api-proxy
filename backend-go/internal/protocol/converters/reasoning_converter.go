package converters

import (
	"math"

	"insistanan/api-proxy/internal/protocol/contract"
	"insistanan/api-proxy/internal/protocol/types"
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

// ExtractReasoningFromClaude 区分缺省、关闭、自适应和非法设置，不修补错误输入。
func ExtractReasoningFromClaude(req *types.ClaudeRequest) (ReasoningConfig, error) {
	if req == nil {
		return ReasoningConfig{}, nil
	}
	var effort string
	if raw, exists := req.OutputConfig["effort"]; exists {
		value, ok := raw.(string)
		if !ok {
			return ReasoningConfig{}, contract.Invalid("output_config.effort 必须为字符串")
		}
		var err error
		effort, err = NormalizeReasoningEffortForConstrainedUpstream(value)
		if err != nil {
			return ReasoningConfig{}, contract.Invalid("%v", err)
		}
	}
	cfg := ReasoningConfig{Effort: effort, BudgetTokens: ReasoningBudgetTokens(effort, req.MaxTokens)}
	if req.Thinking == nil {
		return cfg, nil
	}
	thinking, ok := req.Thinking.(map[string]interface{})
	if !ok {
		return ReasoningConfig{}, contract.Invalid("thinking 必须是对象")
	}
	switch thinking["type"] {
	case "disabled":
		return ReasoningConfig{Effort: "none"}, nil
	case "adaptive":
		if effort != "" {
			return cfg, nil
		}
		return ReasoningConfig{Effort: "auto"}, nil
	case "enabled":
		budget, ok := numericBudgetTokens(thinking["budget_tokens"])
		if !ok || budget < 1024 || (req.MaxTokens > 0 && budget >= req.MaxTokens) {
			return ReasoningConfig{}, contract.Invalid("thinking.budget_tokens 必须是至少 1024 且小于 max_tokens 的整数")
		}
		if effort != "" {
			return cfg, nil
		}
		return ReasoningConfig{Effort: EffortFromReasoningBudget(budget), BudgetTokens: budget}, nil
	default:
		return ReasoningConfig{}, contract.Invalid("thinking.type 无效: %v", thinking["type"])
	}
}

// ExtractReasoningFromGemini 从 Gemini GenerationConfig.ThinkingConfig 提取思考配置
func ExtractReasoningFromGemini(cfg *types.GeminiThinkingConfig, maxOutputTokens int) (ReasoningConfig, error) {
	if cfg == nil {
		return ReasoningConfig{}, nil
	}
	effort, err := EffortFromGeminiThinkingConfig(cfg)
	if err != nil {
		return ReasoningConfig{}, err
	}
	if effort == "" {
		return ReasoningConfig{}, nil
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
	}, nil
}

// ApplyReasoningToClaude 将统一思考配置应用到 Claude 请求字典
func ApplyReasoningToClaude(config ReasoningConfig, claudePayload map[string]interface{}, maxOutputTokens int) {
	if claudePayload == nil || config.Effort == "" {
		return
	}
	if config.Effort == "none" {
		claudePayload["thinking"] = map[string]interface{}{"type": "disabled"}
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
	if chatPayload == nil || config.Effort == "" {
		return
	}
	reasoningEffort := ReasoningEffortToOpenAIChatReasoningEffort(config.Effort)
	if reasoningEffort != "" {
		chatPayload["reasoning_effort"] = reasoningEffort
	}
}

// ApplyReasoningToResponses 将统一思考配置应用到 Responses 请求字典
func ApplyReasoningToResponses(config ReasoningConfig, responsesPayload map[string]interface{}) {
	if responsesPayload == nil || config.Effort == "" {
		return
	}
	responsesPayload["reasoning"] = map[string]interface{}{
		"effort": config.Effort,
	}
}

// ApplyReasoningToGemini 将统一思考配置应用到 Gemini generationConfig 字典
func ApplyReasoningToGemini(config ReasoningConfig, generationConfig map[string]interface{}) {
	if generationConfig == nil || config.Effort == "" {
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
		if math.IsNaN(n) || math.IsInf(n, 0) || n != math.Trunc(n) || n >= float64(math.MaxInt) || n < float64(math.MinInt) {
			return 0, false
		}
		return int(n), true
	default:
		return 0, false
	}
}
