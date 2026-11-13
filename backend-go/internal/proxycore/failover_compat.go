package proxycore

import (
	"encoding/json"
	"strings"
)

// IsPromptCacheKeyUnsupported 判断上游是否明确拒绝 prompt_cache_key。
// 只接受 4xx 的“参数不支持”语义，避免将“该字段必填”等不同错误误判为可删除字段。
func IsPromptCacheKeyUnsupported(statusCode int, bodyBytes []byte) bool {
	if statusCode < 400 || statusCode >= 500 || len(bodyBytes) == 0 {
		return false
	}

	message := string(bodyBytes)
	var envelope struct {
		Error struct {
			Message string `json:"message"`
			Param   string `json:"param"`
		} `json:"error"`
	}
	if json.Unmarshal(bodyBytes, &envelope) == nil {
		if envelope.Error.Message != "" {
			message = envelope.Error.Message
		}
		if strings.EqualFold(strings.TrimSpace(envelope.Error.Param), "prompt_cache_key") {
			message += " prompt_cache_key"
		}
	}

	normalized := strings.ToLower(message)
	if !strings.Contains(normalized, "prompt_cache_key") {
		return false
	}
	unsupportedMarkers := []string{
		"unsupported parameter",
		"unsupported field",
		"unknown parameter",
		"unknown field",
		"unrecognized parameter",
		"not supported",
		"does not support",
		"不支持",
	}
	for _, marker := range unsupportedMarkers {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return false
}

// IsReasoningContentRequired 判断上游是否因 thinking 历史缺少 reasoning_content
// 拒绝了请求。仅识别明确的 4xx 字段校验，避免把普通推理模型错误误判为兼容能力。
func IsReasoningContentRequired(statusCode int, bodyBytes []byte) bool {
	if statusCode < 400 || statusCode >= 500 || len(bodyBytes) == 0 {
		return false
	}

	message := strings.ToLower(string(bodyBytes))
	if !strings.Contains(message, "reasoning_content") {
		return false
	}
	return strings.Contains(message, "thinking mode") &&
		(strings.Contains(message, "must be passed back") || strings.Contains(message, "passed back to the api"))
}
