package converters

// ============== FinishReason 映射 ==============

// OpenAIFinishReasonToAnthropic 将 OpenAI finish_reason 转换为 Anthropic stop_reason
// 未知原因保持原值透传，避免隐藏上游状态
func OpenAIFinishReasonToAnthropic(reason string) string {
	switch reason {
	case "stop":
		return "end_turn"
	case "length":
		return "max_tokens"
	case "tool_calls", "function_call":
		return "tool_use"
	case "content_filter":
		return "refusal"
	case "", "empty":
		return "end_turn"
	default:
		return reason // 未知原因透传
	}
}

// AnthropicStopReasonToOpenAI 将 Anthropic stop_reason 转换为 OpenAI finish_reason
// 未知原因保持原值透传，避免隐藏上游状态
func AnthropicStopReasonToOpenAI(reason string) string {
	switch reason {
	case "end_turn":
		return "stop"
	case "max_tokens":
		return "length"
	case "stop_sequence", "pause_turn":
		return "stop"
	case "tool_use":
		return "tool_calls"
	case "refusal":
		return "content_filter"
	case "", "empty":
		return "stop"
	default:
		return reason // 未知原因透传
	}
}

// OpenAIFinishReasonToResponses 将 OpenAI finish_reason 转换为 Responses API status
// 未知原因映射为 incomplete，避免将潜在错误误报为成功
func OpenAIFinishReasonToResponses(reason string) string {
	switch reason {
	case "stop", "tool_calls", "function_call":
		return "completed"
	case "length":
		return "incomplete"
	case "content_filter":
		return "failed"
	case "", "empty":
		return "completed"
	default:
		return "incomplete" // 未知原因视为未完成，避免误报成功
	}
}
