package converters

import (
	"fmt"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
	"insistanan/api-proxy/internal/infra/config"
	"insistanan/api-proxy/internal/protocol/types"
)

func HasChannelReasoningOverride(upstream *config.UpstreamConfig) bool {
	return upstream != nil && upstream.ReasoningEffort != "" && upstream.ReasoningEffort != "inherit"
}

// RemoveRequestReasoningParameters 只清除请求级思考配置，不触碰历史内容、签名或会话链。
// 在协议转换之前执行，让被渠道覆盖的非法/冲突等级不再参与转换校验。
func RemoveRequestReasoningParameters(body []byte) ([]byte, error) {
	if !gjson.ValidBytes(body) || !gjson.ParseBytes(body).IsObject() {
		return nil, fmt.Errorf("思考参数覆盖需要有效的 JSON 请求对象")
	}
	result := body
	for _, path := range []string{"thinking", "reasoning_effort", "reasoning.effort", "output_config.effort", "generationConfig.thinkingConfig"} {
		var err error
		result, err = sjson.DeleteBytes(result, path)
		if err != nil {
			return nil, fmt.Errorf("移除请求思考参数 %s 失败: %w", path, err)
		}
	}
	for _, path := range []string{"reasoning", "output_config"} {
		value := gjson.GetBytes(result, path)
		if value.Exists() && (!value.IsObject() || len(value.Map()) == 0) {
			var err error
			result, err = sjson.DeleteBytes(result, path)
			if err != nil {
				return nil, fmt.Errorf("清理请求思考配置 %s 失败: %w", path, err)
			}
		}
	}
	return result, nil
}

// ApplyChannelReasoningOverride 在模型映射与协议转换完成后渲染实际协议的参数。
// 不支持的模型参数由上游明确拒绝，不删除参数重试或偷偷降档。
func ApplyChannelReasoningOverride(body []byte, upstream *config.UpstreamConfig, model string) ([]byte, error) {
	if !HasChannelReasoningOverride(upstream) {
		return body, nil
	}
	if err := config.ValidateReasoningOptions(upstream); err != nil {
		return nil, err
	}
	result, err := RemoveRequestReasoningParameters(body)
	if err != nil {
		return nil, err
	}
	if resolved := gjson.GetBytes(result, "model").String(); resolved != "" {
		model = resolved
	}
	// 只保留目标协议的配置容器，避免兼容扩展通过原生透传混入另一种协议。
	for field, owner := range map[string]string{"reasoning": "responses", "output_config": "claude", "generationConfig": "gemini"} {
		if upstream.ServiceType != owner {
			result, err = sjson.DeleteBytes(result, field)
			if err != nil {
				return nil, fmt.Errorf("清理跨协议思考字段 %s 失败: %w", field, err)
			}
		}
	}
	effort := upstream.ReasoningEffort
	if effort == "auto" {
		// 渠道自动与跟随请求不同：不发送请求级思考控制，使用模型默认。
		// Responses 的 summary 等选项也属于 reasoning 控制；历史 item 不受影响。
		result, err = sjson.DeleteBytes(result, "reasoning")
		if err != nil {
			return nil, fmt.Errorf("清理自动思考配置失败: %w", err)
		}
		return result, nil
	}
	var path string
	var value interface{}
	switch upstream.ServiceType {
	case "openai", "chat":
		path, value = "reasoning_effort", effort
	case "responses":
		path, value = "reasoning.effort", effort
	case "claude":
		path = "thinking"
		if effort == "none" {
			value = map[string]interface{}{"type": "disabled"}
		} else {
			if err := validateClaudeThinkingToolChoice(gjson.GetBytes(result, "tool_choice").Value()); err != nil {
				return nil, err
			}
			value = map[string]interface{}{"type": "adaptive"}
			result, err = sjson.SetBytes(result, "output_config.effort", effort)
			if err != nil {
				return nil, fmt.Errorf("覆盖 Claude effort 失败: %w", err)
			}
		}
	case "gemini":
		path = "generationConfig.thinkingConfig"
		value, err = channelGeminiThinking(model, effort)
		if err != nil {
			return nil, err
		}
	default:
		return nil, &types.UnsupportedProtocolError{Err: fmt.Errorf("上游协议 %q 不支持渠道思考控制", upstream.ServiceType)}
	}
	result, err = sjson.SetBytes(result, path, value)
	if err != nil {
		return nil, fmt.Errorf("覆盖渠道思考参数失败: %w", err)
	}
	return result, nil
}

func channelGeminiThinking(model, effort string) (interface{}, error) {
	model = strings.ToLower(model)
	if strings.Contains(model, "gemini-2.5") {
		return responsesReasoningToGemini25Thinking(model, effort)
	}
	if effort == "none" {
		return nil, &types.UnsupportedProtocolError{Err: fmt.Errorf("模型 %q 无法通过 thinkingLevel 保证关闭思考；请选择支持关闭的模型", model)}
	}
	// level 模型直接发送选择的等级；不把 xhigh/max 静默折叠为 high。
	return &types.GeminiThinkingConfig{ThinkingLevel: effort, IncludeThoughts: true}, nil
}
