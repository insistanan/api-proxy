package utils

import (
	"bytes"
	"encoding/json"
	"strings"
)

// 本文件是"上游失败是否值得重试"的唯一出处。
// 收敛前状态码表只存在于 handlers/proxycore/failover_classify.go，而 visionlayer 因为
// 依赖方向（handlers/proxycore -> visionlayer）无法复用它，于是自己维护了一份
// 只看状态码的判断，把内容审核类 403 也当成可重试，重试同时还会把好 key
// 标记为失败。谓词下沉到 utils 后两条链路共用同一份判定。

// ClassifyUpstreamStatus 仅按 HTTP 状态码判断上游失败是否值得换 Key/URL 重试。
// 返回: (retryable 是否值得重试, quotaRelated 是否额度/计费相关)
//
// quotaRelated 供调度器调整 Key 优先级使用；只关心是否重试的调用方忽略第二个值。
// 注意：状态码只是第一层判断。响应体里的错误码可能表明"换 Key 也没用"
// （内容审核、请求内容非法），调用方必须再过 IsNonRetryableUpstreamErrorBody。
func ClassifyUpstreamStatus(statusCode int) (bool, bool) {
	switch {
	// 认证/授权错误 (应 failover，非配额相关)
	case statusCode == 401:
		return true, false
	case statusCode == 403:
		return true, false

	// 配额/计费错误 (应 failover，配额相关)
	case statusCode == 402:
		return true, true
	case statusCode == 429:
		return true, true

	// 超时错误 (应 failover，非配额相关)
	case statusCode == 408:
		return true, false

	// 需要检查消息体的状态码 (交给调用方的第二层判断)
	case statusCode == 400:
		return false, false

	// 请求错误 (不应 failover，客户端问题)
	case statusCode == 404, statusCode == 405, statusCode == 406,
		statusCode == 409, statusCode == 410, statusCode == 411,
		statusCode == 412, statusCode == 413, statusCode == 414,
		statusCode == 415, statusCode == 416, statusCode == 417,
		statusCode == 422, statusCode == 423, statusCode == 424,
		statusCode == 426, statusCode == 428, statusCode == 431,
		statusCode == 451:
		return false, false

	// 服务端错误 (应 failover，非配额相关)
	case statusCode >= 500:
		return true, false

	// 其他 4xx (保守处理，不 failover)
	case statusCode >= 400 && statusCode < 500:
		return false, false

	// 成功/重定向 (不应 failover)
	default:
		return false, false
	}
}

// IsContentPolicyErrorCode 判断错误码是否为内容审核类拦截。
func IsContentPolicyErrorCode(code string) bool {
	switch strings.ToLower(strings.TrimSpace(code)) {
	case "sensitive_words_detected", "content_policy_violation", "content_filter", "content_blocked", "moderation_blocked":
		return true
	default:
		return false
	}
}

// IsNonRetryableUpstreamErrorCode 判断错误码是否不应重试。
// 这些错误与请求内容相关，换 Key 重试不会改变结果。
func IsNonRetryableUpstreamErrorCode(code string) bool {
	if IsContentPolicyErrorCode(code) {
		return true
	}
	nonRetryableCodes := []string{
		// 请求内容无效
		"invalid_request",
		"invalid_request_error",
		"bad_request",
	}
	codeLower := strings.ToLower(code)
	for _, c := range nonRetryableCodes {
		if codeLower == c {
			return true
		}
	}
	return false
}

// IsContentPolicyErrorBody 从上游响应体提取 error.code 后判断是否为内容审核拦截。
func IsContentPolicyErrorBody(bodyBytes []byte) bool {
	return IsContentPolicyErrorCode(upstreamErrorCode(bodyBytes))
}

// IsUpstreamModelCapacityMessage 判断错误消息是否为已知的明确模型容量
// 错误（"selected model is at capacity" / "model is at capacity"）。
// 唯一判据出处（R4-a 收敛）：proxycore 的响应体分类与 providers 的流内
// 失败分类共用，避免两处措辞漂移后同一错误被解释成不同结果。
// 仅匹配已知措辞，避免裸 capacity 误判业务正文。
func IsUpstreamModelCapacityMessage(message string) bool {
	lower := strings.ToLower(message)
	return strings.Contains(lower, "selected model is at capacity") ||
		strings.Contains(lower, "model is at capacity")
}

// IsUpstreamModelCapacityErrorBody 判断上游响应体是否为模型容量错误。
func IsUpstreamModelCapacityErrorBody(bodyBytes []byte) bool {
	return IsUpstreamModelCapacityMessage(string(bodyBytes))
}

// IsUpstreamErrorEnvelope 判断 body 是否为"HTTP 2xx 但 body 是错误信封"。
// 部分 OpenAI 兼容网关用 200 包错误返回，若当成功处理，错误会被原样转给客户端
// 且不触发 failover。判定条件（全部满足才算命中）：
//   - body 是合法 JSON 顶层对象；
//   - "error" 字段存在且为 JSON 对象（null 不算——Responses 正常响应就带
//     "error": null 字段；字符串形式的 error 也不算，避免误判业务正文）。
func IsUpstreamErrorEnvelope(bodyBytes []byte) bool {
	if len(bodyBytes) == 0 {
		return false
	}
	// 快速预检：合法 JSON 顶层对象以 '{' 开头（允许前导空白）。
	trimmed := bytes.TrimLeft(bodyBytes, " \t\r\n")
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return false
	}
	var envelope struct {
		Error json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(bodyBytes, &envelope); err != nil {
		return false
	}
	trimmedError := bytes.TrimSpace(envelope.Error)
	return len(trimmedError) > 0 && trimmedError[0] == '{'
}

// IsNonRetryableUpstreamErrorBody 从上游响应体提取 error.code 后判断是否不应重试。
func IsNonRetryableUpstreamErrorBody(bodyBytes []byte) bool {
	return IsNonRetryableUpstreamErrorCode(upstreamErrorCode(bodyBytes))
}

// upstreamErrorCode 提取上游错误响应的 error.code；无法解析或字段缺失时返回空串。
// 空串在两个 IsXxxCode 谓词里都判为 false，等于"没有证据说明不可重试"，
// 由调用方的状态码判断决定，不在这里替调用方兜底成"可重试"或"不可重试"。
func upstreamErrorCode(bodyBytes []byte) string {
	if len(bodyBytes) == 0 {
		return ""
	}
	var errResp struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(bodyBytes, &errResp); err != nil {
		return ""
	}
	return errResp.Error.Code
}
