// Package availability 解释真实请求结果，供管理台展示和免费模型排序使用，不执行主动探测。
package availability

import (
	"net/http"

	"github.com/insistanan/api-proxy/internal/infra/upstreamreq"
)

const (
	Success     = "success"
	Failure     = "failure"
	Unsupported = "unsupported"
	Credential  = "credential"
	RateLimited = "rate_limited"
	Ignored     = "ignored"
)

// 由调用方与分类器共用的错误阶段；ModelCapacity 同时标识明确的模型容量原因。
const (
	IntentState   = "intent_state"
	ModelCapacity = "model_capacity"
)

// Sample 不包含正文和明文凭据。CandidateID 是 URL、实际协议和 Key 的组合指纹。
type Sample struct {
	Kind        string `json:"kind"`
	ChannelID   string `json:"channelId"`
	Model       string `json:"model"`
	CandidateID string `json:"candidateId"`
	Outcome     string `json:"outcome"`
	Reason      string `json:"reason"`
	// CooldownUntil 仅记录模型容量错误的显式截止时间；共享冷却读取准入控制器。
	CooldownUntil int64 `json:"cooldownUntil,omitempty"`
}

// Classify 优先使用错误阶段和明确错误码；普通 404/403 不推断模型不支持。
func Classify(success bool, status int, errorType, message string) (string, string) {
	switch errorType {
	case "client_cancelled", "cancelled":
		return Ignored, "client_cancelled"
	case "local_admission", "build_request", "request_body", "configuration_error", "unsupported_protocol", "vision_layer", "local_response", "content_safety", "content_safety_hook", IntentState:
		return Ignored, "local_error"
	}
	if success {
		return Success, Success
	}
	rateLimitReason := RateLimited
	if errorType == ModelCapacity {
		rateLimitReason = ModelCapacity
	}
	switch upstreamreq.ClassifyResponseError([]byte(message)) {
	case upstreamreq.ErrorModelUnsupported:
		return Unsupported, "model_not_found"
	case upstreamreq.ErrorCredential:
		return Credential, "credential_or_quota"
	case upstreamreq.ErrorRequestRejected:
		return Ignored, "request_rejected"
	case upstreamreq.ErrorRateLimited:
		return RateLimited, rateLimitReason
	}
	if status == http.StatusUnauthorized || status == http.StatusPaymentRequired {
		return Credential, "credential_or_quota"
	}
	if status == http.StatusTooManyRequests || errorType == ModelCapacity {
		return RateLimited, rateLimitReason
	}
	// 这些阶段意味着响应未能完整交付，即便曾收到 HTTP 200 也不是成功。
	switch errorType {
	case "network", "read_body", "read_response", "response_processing", "empty_response", "error_envelope":
		return Failure, errorType
	case "content_policy", "content_policy_error", "invalid_request_error":
		return Ignored, "request_rejected"
	}
	if status >= 500 || status == http.StatusRequestTimeout {
		return Failure, "upstream_error"
	}
	if status >= 400 && status < 500 {
		return Ignored, "request_rejected"
	}
	return Ignored, "unclassified"
}
