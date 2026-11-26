package converters

import (
	"github.com/tidwall/gjson"
	"insistanan/api-proxy/internal/protocol/contract"
	"insistanan/api-proxy/internal/protocol/types"
)

func validateGeminiConversion(req *types.GeminiRequest, target string) error {
	for _, field := range []string{"maxOutputTokens", "candidateCount"} {
		if value := gjson.GetBytes(req.RawJSON, "generationConfig."+field); value.Exists() && value.Int() <= 0 {
			return contract.Invalid("generationConfig.%s 必须为正整数", field)
		}
	}
	if len(req.SafetySettings) > 0 {
		return contract.Unsupported("Gemini → %s 无法等价映射 safetySettings", target)
	}
	if req.CachedContent != "" {
		return contract.Unsupported("cachedContent 只能由原生 Gemini 上游处理")
	}
	if cfg := req.GenerationConfig; cfg != nil {
		if cfg.Temperature != nil {
			if *cfg.Temperature < 0 || *cfg.Temperature > 2 {
				return contract.Invalid("temperature 必须在 0 到 2 之间")
			}
			if target == "claude" && *cfg.Temperature > 1 {
				return contract.Unsupported("Claude 无法接受 temperature > 1")
			}
		}
		if cfg.TopP != nil && (*cfg.TopP < 0 || *cfg.TopP > 1) {
			return contract.Invalid("topP 必须在 0 到 1 之间")
		}
		if (target == "openai" || target == "responses") && cfg.TopK != nil {
			return contract.Unsupported("%s 无法等价表达 Gemini topK", target)
		}
		if cfg.CandidateCount < 0 {
			return contract.Invalid("candidateCount 必须为正整数")
		}
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
	if req.SystemInstruction != nil {
		for _, part := range req.SystemInstruction.Parts {
			if part.InlineData != nil || part.FileData != nil || part.FunctionCall != nil || part.FunctionResponse != nil {
				return contract.Unsupported("Gemini 系统指令只能转换文本")
			}
		}
	}
	for _, key := range []string{"generationConfig.audioTimestamp", "generationConfig.speechConfig", "generationConfig.mediaResolution", "generationConfig.responseLogprobs", "generationConfig.logprobs", "toolConfig.retrievalConfig"} {
		if gjson.GetBytes(req.RawJSON, key).Exists() {
			return contract.Unsupported("Gemini → %s 尚不支持 %s", target, key)
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
			for _, key := range []string{"functionResponse.parts", "functionResponse.scheduling", "functionResponse.willContinue", "functionCall.partialArgs", "functionCall.willContinue"} {
				if part.Get(key).Exists() {
					return contract.Unsupported("Gemini → %s 尚不支持 %s", target, key)
				}
			}
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
		} else if target == "responses" {
			payload["tool_choice"] = map[string]interface{}{"type": "function", "name": name}
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
	names := make(map[string]bool)
	for _, tool := range req.Tools {
		for _, declaration := range tool.FunctionDeclarations {
			names[declaration.Name] = true
		}
	}
	return contract.ToolChoice(payload["tool_choice"], names)
}
