package converters

import (
	"fmt"
	"strings"

	"github.com/insistanan/api-proxy/internal/protocol/types"
)

// 思考等级档位的统一内部表示：none/auto/minimal/low/medium/high/xhigh/max。
// 各协议入口解析后都必须归一化到该词汇表，再交给具体上游转换器渲染。
//
// 预算分界点（ReasoningBudgetTokens 与 EffortFromReasoningBudget 共用）：
//
//	minimal/low  -> 1024
//	medium/auto  -> 4096
//	high         -> 8192
//	xhigh/max    -> 16384
//
// 反向阈值取相邻预算的中点（2560/6144/12288），保证
// effort -> budget -> effort 的往返不发生档位漂移。
const (
	reasoningBudgetLow       = 1024
	reasoningBudgetMedium    = 4096
	reasoningBudgetHigh      = 8192
	reasoningBudgetXhigh     = 16384
	reasoningBudgetLowUpper  = 2560  // (< 2560 -> low)
	reasoningBudgetMedUpper  = 6144  // (< 6144 -> medium)
	reasoningBudgetHighUpper = 12288 // (< 12288 -> high)，>= 12288 -> xhigh
)

// NormalizeReasoningEffortForConstrainedUpstream 将外部等级映射为内部标准档位。
// 修复：保留 "max" 作为一个合法档位，因为部分国产模型或兼容网关（如硅基流动）
// 的 OpenAPI 兼容层自己扩展了 "max" 值。强行折叠为 "xhigh" 会导致上游不识别。
func NormalizeReasoningEffortForConstrainedUpstream(effort string) (string, error) {
	switch effort {
	case "", "none", "auto", "minimal", "low", "medium", "high", "xhigh", "max":
		return effort, nil
	case "ultra":
		return "max", nil
	default:
		return "", fmt.Errorf("不支持的 reasoning.effort=%q", effort)
	}
}

// ReasoningBudgetTokens 把 effort 映射为 Claude thinking.budget_tokens。
// maxOutputTokens <= 0 时使用档位默认预算；> 0 时按比例分配，并确保 budget < maxOutputTokens。
//
// high 与 xhigh/max 使用不同比例（0.8 / 0.9），保证 Responses->Claude 路径下二者可区分。
func ReasoningBudgetTokens(effort string, maxOutputTokens int) int {
	if maxOutputTokens <= 0 {
		switch effort {
		case "minimal", "low":
			return reasoningBudgetLow
		case "medium", "auto":
			return reasoningBudgetMedium
		case "high":
			return reasoningBudgetHigh
		case "xhigh", "max":
			return reasoningBudgetXhigh
		default:
			return 0
		}
	}
	ratio := 0.5
	switch effort {
	case "minimal", "low":
		ratio = 0.25
	case "medium", "auto":
		ratio = 0.5
	case "high":
		ratio = 0.8
	case "xhigh", "max":
		// 预留约 10% 输出，其余给思考，区分 max 与 high。
		ratio = 0.9
	default:
		return 0
	}
	budget := int(float64(maxOutputTokens) * ratio)
	if budget < reasoningBudgetLow {
		budget = reasoningBudgetLow
	}
	if budget >= maxOutputTokens {
		budget = maxOutputTokens - 1
	}
	if budget <= 0 {
		return 0
	}
	return budget
}

// EffortFromReasoningBudget 把 Claude thinking.budget_tokens（或 Gemini thinkingBudget 数值）
// 反向归一化为内部 effort 档位。阈值与 ReasoningBudgetTokens 的默认预算表对齐，
// 保证 mirror 路径不降级。budget <= 0 视为自适应（auto）。
func EffortFromReasoningBudget(budget int) string {
	switch {
	case budget <= 0:
		return "auto"
	case budget < reasoningBudgetLowUpper:
		return "low"
	case budget < reasoningBudgetMedUpper:
		return "medium"
	case budget < reasoningBudgetHighUpper:
		return "high"
	default:
		return "xhigh"
	}
}

// ReasoningEffortToOpenAIChatReasoningEffort 把内部 effort 映射为 OpenAI Chat
// reasoning_effort 字段值。返回空串表示应省略该字段（auto / 无思考）。
func ReasoningEffortToOpenAIChatReasoningEffort(effort string) string {
	switch effort {
	case "", "auto":
		return ""
	case "none", "minimal", "low", "medium", "high", "xhigh", "max":
		return effort
	default:
		return ""
	}
}

// EffortFromGeminiThinkingConfig 把 Gemini 入口请求的 thinkingConfig 解析为内部 effort。
// 优先级：thinkingLevel > thinkingBudget > includeThoughts。返回空串表示未配置思考参数。
//
// thinkingLevel: none/minimal/low/medium/high（minimal 视为 low，none 表示关闭思考）。
// thinkingBudget: 0 表示关闭思考；-1 表示自适应；正值走 EffortFromReasoningBudget 反向阈值。
// includeThoughts=true 且无 level/budget：视为 auto（自适应）。
func EffortFromGeminiThinkingConfig(cfg *types.GeminiThinkingConfig) string {
	if cfg == nil {
		return ""
	}
	if level := strings.ToLower(strings.TrimSpace(cfg.ThinkingLevel)); level != "" {
		switch level {
		case "none":
			return "none"
		case "minimal":
			return "minimal"
		case "low", "medium", "high":
			return level
		}
	}
	if cfg.ThinkingBudget != nil {
		budget := int(*cfg.ThinkingBudget)
		switch {
		case budget == 0:
			return "none"
		case budget < 0:
			return "auto"
		default:
			return EffortFromReasoningBudget(budget)
		}
	}
	if cfg.IncludeThoughts {
		return "auto"
	}
	return ""
}
