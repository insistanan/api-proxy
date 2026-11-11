// Package availability 仅解释真实请求结果，不参与选渠和主动探测。
package availability

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/insistanan/api-proxy/internal/utils"
)

const (
	Success     = "success"
	Failure     = "failure"
	Unsupported = "unsupported"
	Credential  = "credential"
	RateLimited = "rate_limited"
	Ignored     = "ignored"
)

// Sample 不包含正文和明文凭据。CandidateID 是 URL、实际协议和 Key 的组合指纹。
type Sample struct {
	Kind          string `json:"kind"`
	ChannelID     string `json:"channelId"`
	Model         string `json:"model"`
	CandidateID   string `json:"candidateId"`
	Outcome       string `json:"outcome"`
	Reason        string `json:"reason"`
	CooldownUntil int64  `json:"cooldownUntil,omitempty"`
}

// Classify 优先使用错误阶段和明确错误码；普通 404/403 不推断模型不支持。
func Classify(success bool, status int, errorType, message string) (string, string) {
	switch errorType {
	case "client_cancelled", "cancelled":
		return Ignored, "client_cancelled"
	case "local_admission", "build_request", "request_body", "configuration_error", "unsupported_protocol", "vision_layer", "local_response", "content_safety", "content_safety_hook":
		return Ignored, "local_error"
	}
	if success {
		return Success, Success
	}
	type upstreamError struct {
		Code   json.RawMessage `json:"code"`
		Type   string          `json:"type"`
		Status string          `json:"status"`
		Param  string          `json:"param"`
	}
	var envelope struct {
		upstreamError
		Error    upstreamError `json:"error"`
		Response struct {
			Error upstreamError `json:"error"`
		} `json:"response"`
	}
	// 非 JSON 错误仍按 HTTP 状态和调用阶段分类，不将正文解析失败视为成功。
	if json.Unmarshal([]byte(message), &envelope) == nil {
		for _, detail := range []upstreamError{envelope.Error, envelope.Response.Error, envelope.upstreamError} {
			codes := []string{detail.Type, detail.Status}
			var code string
			if json.Unmarshal(detail.Code, &code) == nil {
				codes = append([]string{code}, codes...)
			}
			for _, code := range codes {
				switch strings.ToLower(code) {
				case "model_not_found", "model_not_supported", "unsupported_model", "model_not_exist":
					if detail.Param == "" || detail.Param == "model" {
						return Unsupported, "model_not_found"
					}
					return Ignored, "request_rejected"
				case "invalid_api_key", "authentication_error", "invalid_key", "insufficient_quota", "quota_exceeded", "account_deactivated", "unauthenticated":
					return Credential, "credential_or_quota"
				case "context_length_exceeded", "safety_violation":
					return Ignored, "request_rejected"
				case "rate_limit_exceeded", "rate_limit_error", "resource_exhausted":
					return RateLimited, "rate_limited"
				}
				if utils.IsNonRetryableUpstreamErrorCode(code) {
					return Ignored, "request_rejected"
				}
			}
		}
	}
	if status == http.StatusUnauthorized || status == http.StatusPaymentRequired {
		return Credential, "credential_or_quota"
	}
	if status == http.StatusTooManyRequests || errorType == "model_capacity" {
		return RateLimited, "rate_limited"
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

// Cooldown 只供观测展示。缺少有效 Retry-After 时显示 30 秒估计冷却，不改变准入。
func Cooldown(value string, now time.Time) int64 {
	value = strings.TrimSpace(value)
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil && seconds >= 0 && seconds <= 7*24*60*60 {
		return now.Add(time.Duration(seconds) * time.Second).UnixMilli()
	}
	if until, err := http.ParseTime(value); err == nil && until.After(now) {
		return until.UnixMilli()
	}
	return now.Add(30 * time.Second).UnixMilli()
}
