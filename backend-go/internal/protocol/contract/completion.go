package contract

import "fmt"

// Completion 保留停止原因，不能把截断或审核终止统一包装为成功。
type Completion struct {
	Status string
	Reason string
}

func Finish(protocol, reason string) (Completion, error) {
	result := Completion{Status: "completed"}
	switch protocol {
	case "claude":
		switch reason {
		case "", "end_turn", "stop_sequence", "tool_use":
		case "max_tokens", "model_context_window_exceeded":
			result = Completion{"incomplete", "max_output_tokens"}
		case "refusal":
			result = Completion{"incomplete", "content_filter"}
		default:
			return Completion{}, fmt.Errorf("Claude 停止原因无法转换: %q", reason)
		}
	case "openai":
		switch reason {
		case "", "stop", "tool_calls", "function_call":
		case "length":
			result = Completion{"incomplete", "max_output_tokens"}
		case "content_filter":
			result = Completion{"incomplete", "content_filter"}
		default:
			return Completion{}, fmt.Errorf("Chat 停止原因无法转换: %q", reason)
		}
	case "gemini":
		switch reason {
		case "", "STOP":
		case "MAX_TOKENS":
			result = Completion{"incomplete", "max_output_tokens"}
		case "SAFETY", "RECITATION", "BLOCKLIST", "PROHIBITED_CONTENT", "SPII", "IMAGE_SAFETY":
			result = Completion{"incomplete", "content_filter"}
		default:
			return Completion{}, fmt.Errorf("Gemini 停止原因无法转换: %q", reason)
		}
	default:
		return Completion{}, fmt.Errorf("不支持的结束协议: %q", protocol)
	}
	return result, nil
}

func (c Completion) Apply(response map[string]interface{}) {
	response["status"] = c.Status
	if c.Reason != "" {
		response["incomplete_details"] = map[string]interface{}{"reason": c.Reason}
	}
}
