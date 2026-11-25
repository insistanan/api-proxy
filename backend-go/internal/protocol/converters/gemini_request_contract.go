package converters

import (
	"github.com/insistanan/api-proxy/internal/protocol/contract"
	"github.com/insistanan/api-proxy/internal/protocol/types"
	"github.com/tidwall/gjson"
)

func validateGeminiConversion(req *types.GeminiRequest, target string) error {
	if len(req.SafetySettings) > 0 {
		return contract.Unsupported("Gemini → %s 无法等价映射 safetySettings", target)
	}
	if req.CachedContent != "" {
		return contract.Unsupported("cachedContent 只能由原生 Gemini 上游处理")
	}
	if cfg := req.GenerationConfig; cfg != nil {
		if cfg.CandidateCount > 1 {
			return contract.Unsupported("Gemini → %s 当前只支持一个候选", target)
		}
		if cfg.ResponseSchema != nil || cfg.ResponseJSONSchema != nil || cfg.ResponseMimeType != "" || len(cfg.ResponseModalities) > 0 {
			return contract.Unsupported("Gemini → %s 尚不支持指定输出 schema 或模态", target)
		}
		if cfg.MaxOutputTokens < 0 {
			return contract.Invalid("maxOutputTokens 必须为正数")
		}
		if cfg.ThinkingConfig != nil {
			thinking := cfg.ThinkingConfig
			if thinking.ThinkingBudget != nil && *thinking.ThinkingBudget < -1 {
				return contract.Invalid("thinkingBudget 只能为 -1、0 或正整数")
			}
			switch thinking.ThinkingLevel {
			case "", "none", "minimal", "low", "medium", "high":
			default:
				return contract.Invalid("thinkingLevel 无效: %q", thinking.ThinkingLevel)
			}
		}
	}
	for _, tool := range gjson.GetBytes(req.RawJSON, "tools").Array() {
		for field := range tool.Map() {
			if field != "functionDeclarations" {
				return contract.Unsupported("Gemini → %s 不支持工具 %q", target, field)
			}
		}
	}
	for _, content := range gjson.GetBytes(req.RawJSON, "contents").Array() {
		for _, part := range content.Get("parts").Array() {
			for field := range part.Map() {
				switch field {
				case "text", "thought", "thoughtSignature", "thought_signature", "inlineData", "fileData", "functionCall", "functionResponse":
				default:
					return contract.Unsupported("Gemini → %s 不支持 part 字段 %q", target, field)
				}
			}
		}
	}
	return nil
}

func applyGeminiToolChoice(req *types.GeminiRequest, target string, payload map[string]interface{}) error {
	if req.ToolConfig == nil || req.ToolConfig.FunctionCallingConfig == nil {
		return nil
	}
	cfg := req.ToolConfig.FunctionCallingConfig
	choice := "auto"
	switch cfg.Mode {
	case "", "AUTO":
	case "NONE":
		choice = "none"
	case "ANY":
		choice = "required"
	default:
		return contract.Unsupported("Gemini → %s 不支持工具模式 %q", target, cfg.Mode)
	}
	if len(cfg.AllowedFunctionNames) > 1 || (len(cfg.AllowedFunctionNames) > 0 && cfg.Mode != "ANY") {
		return contract.Unsupported("Gemini → %s 无法等价表达 allowedFunctionNames", target)
	}
	if len(cfg.AllowedFunctionNames) == 1 {
		name := cfg.AllowedFunctionNames[0]
		if target == "claude" {
			payload["tool_choice"] = map[string]interface{}{"type": "tool", "name": name}
		} else {
			payload["tool_choice"] = map[string]interface{}{"type": "function", "function": map[string]interface{}{"name": name}}
		}
	} else if target == "claude" {
		if choice == "required" {
			choice = "any"
		}
		payload["tool_choice"] = map[string]interface{}{"type": choice}
	} else {
		payload["tool_choice"] = choice
	}
	return nil
}
