package converters

import (
	"math"

	"github.com/insistanan/api-proxy/internal/protocol/contract"
	"github.com/tidwall/gjson"
)

func validateSamplingAndBudget(body []byte, target string) error {
	for _, field := range []string{"max_output_tokens", "max_tokens"} {
		value := gjson.GetBytes(body, field)
		if value.Exists() && (value.Type != gjson.Number || value.Float() <= 0 || value.Float() != math.Trunc(value.Float())) {
			return contract.Invalid("%s 必须是正整数", field)
		}
	}
	for field, maximum := range map[string]float64{"temperature": 2, "top_p": 1} {
		value := gjson.GetBytes(body, field)
		if !value.Exists() {
			continue
		}
		if value.Type != gjson.Number || value.Float() < 0 || value.Float() > maximum {
			return contract.Invalid("%s 必须在 0 到 %v 之间", field, maximum)
		}
		if field == "temperature" && target == "claude" && value.Float() > 1 {
			return contract.Unsupported("Claude 无法接受 temperature > 1")
		}
	}
	return nil
}

// claudeOutputBudget 统一源协议缺省预算的处理；未知模型不猜测一个过大的数字。
func claudeOutputBudget(model string, explicit int) (int, error) {
	if explicit > 0 {
		return explicit, nil
	}
	if explicit < 0 {
		return 0, contract.Invalid("输出 token 上限必须为正整数")
	}
	return 0, contract.Unsupported("目标 Claude 模型 %q 需要明确输出预算，请设置 max_output_tokens 或 maxOutputTokens", model)
}
