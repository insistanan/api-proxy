package converters

import "github.com/insistanan/api-proxy/internal/protocol/contract"

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
