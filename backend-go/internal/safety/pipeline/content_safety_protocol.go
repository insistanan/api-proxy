package pipeline

import "strings"

func PayloadProtocolForServiceType(serviceType, fallback string) string {
	fallback = strings.ToLower(strings.TrimSpace(fallback))
	// Chat / Images 入口始终按原样透传自己的载荷，不按 ServiceType 转换：
	// 二者都没有协议转换环节，而 Images 渠道通常配 ServiceType=openai，
	// 若走下面的映射会被当成 Chat Completions 载荷解析，prompt 检查将全部落空。
	if fallback == "chat" || fallback == "images" {
		return fallback
	}
	switch strings.ToLower(strings.TrimSpace(serviceType)) {
	case "claude":
		return "messages"
	case "openai":
		return "chat"
	case "responses":
		return "responses"
	case "gemini":
		return "gemini"
	default:
		return fallback
	}
}
