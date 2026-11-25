package converters

import (
	"github.com/insistanan/api-proxy/internal/protocol/contract"
	"github.com/tidwall/gjson"
	"math"
	"strings"
)

// ValidateClaudeConversion 在降为目标消息之前拒绝不可表达的内容，避免白名单转换悄悄丢字段。
func ValidateClaudeConversion(body []byte, target string) error {
	root := gjson.ParseBytes(body)
	if !gjson.ValidBytes(body) || !root.IsObject() {
		return contract.Invalid("Messages 请求必须是 JSON 对象")
	}
	if format := root.Get("output_config.format"); format.Exists() {
		return contract.Unsupported("Messages → %s 尚不支持 output_config.format", target)
	}
	if root.Get("top_k").Exists() {
		return contract.Unsupported("Messages → %s 尚不支持 top_k", target)
	}
	if root.Get("tool_choice.disable_parallel_tool_use").Bool() {
		return contract.Unsupported("Messages → %s 无法保证 disable_parallel_tool_use", target)
	}
	for _, message := range root.Get("messages").Array() {
		role := message.Get("role").String()
		if role != "user" && role != "assistant" {
			return contract.Invalid("Messages 消息 role 无效: %q", role)
		}
		if err := validateContentBlocks(message.Get("content"), target, true); err != nil {
			return err
		}
	}
	return ValidateClaudeReasoning(body)
}

func validateContentBlocks(content gjson.Result, target string, claude bool) error {
	if !content.IsArray() {
		return nil
	}
	for _, block := range content.Array() {
		typ := block.Get("type").String()
		switch typ {
		case "text", "input_text", "output_text", "thinking", "redacted_thinking":
		case "image", "input_image", "image_url":
			if claude && target == "gemini" && block.Get("source.type").String() == "url" && !strings.HasPrefix(block.Get("source.url").String(), "data:") {
				return contract.Unsupported("Messages → Gemini 尚不支持远程 URL 图片")
			}
		case "tool_use":
			if !claude {
				return contract.Unsupported("Responses → %s 不支持内容块 %q", target, typ)
			}
		case "tool_result":
			if block.Get("is_error").Bool() {
				return contract.Unsupported("Messages → %s 尚不支持工具结果 is_error", target)
			}
			for _, result := range arrayContentBlocks(block.Get("content")) {
				if result.Get("type").String() != "text" {
					return contract.Unsupported("Messages → %s 尚不支持多模态工具结果", target)
				}
			}
		default:
			return contract.Unsupported("转换到 %s 尚不支持内容块 %q", target, typ)
		}
	}
	return nil
}

func ValidateClaudeReasoning(body []byte) error {
	root := gjson.ParseBytes(body)
	if effort := root.Get("output_config.effort"); effort.Exists() {
		if effort.Type != gjson.String {
			return contract.Invalid("output_config.effort 必须是字符串")
		}
		if _, err := NormalizeReasoningEffortForConstrainedUpstream(effort.String()); err != nil {
			return contract.Invalid("%v", err)
		}
	}
	thinking := root.Get("thinking")
	if !thinking.Exists() {
		return nil
	}
	if !thinking.IsObject() {
		return contract.Invalid("thinking 必须是对象")
	}
	switch thinking.Get("type").String() {
	case "disabled", "adaptive":
		return nil
	case "enabled":
		budget := thinking.Get("budget_tokens")
		if budget.Type != gjson.Number || budget.Float() < 1024 || budget.Float() != math.Trunc(budget.Float()) {
			return contract.Invalid("thinking.budget_tokens 必须是至少 1024 的整数")
		}
		if maximum := root.Get("max_tokens").Int(); maximum > 0 && budget.Int() >= maximum {
			return contract.Invalid("thinking.budget_tokens 必须小于 max_tokens")
		}
		return nil
	default:
		return contract.Invalid("thinking.type 无效: %q", thinking.Get("type").String())
	}
}

// 字符串工具结果本身是合法文本，不能按单个内容块解读。
func arrayContentBlocks(content gjson.Result) []gjson.Result {
	if !content.IsArray() {
		return nil
	}
	return content.Array()
}
