package converters

import "fmt"

// ConverterFactory 转换器工厂
// 根据上游服务类型返回对应的转换器实例

// NewConverterStrict 创建旧结构化转换器实例，未知 serviceType 显式返回错误。
// Responses 主链路的协议分发在 responses_protocol.go，仅 Claude 上游仍走本工厂。
func NewConverterStrict(serviceType string) (ResponsesConverter, error) {
	switch serviceType {
	case "openai":
		return &OpenAIChatConverter{}, nil
	case "claude":
		return &ClaudeConverter{}, nil
	case "responses":
		return &ResponsesPassthroughConverter{}, nil
	case "gemini":
		return nil, fmt.Errorf("Responses -> Gemini 使用 responses_protocol.go 直接分发，不支持旧结构化转换器")
	default:
		return nil, fmt.Errorf("Responses 上游 serviceType %q 不支持", serviceType)
	}
}
