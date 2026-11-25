package upstreamreq

import (
	"encoding/json"
	"net/http"
	"strings"
)

// ResponseErrorKind 表示上游明确声明的业务拒绝原因，供观测与地址健康共用。
type ResponseErrorKind uint8

const (
	ErrorUnknown ResponseErrorKind = iota
	ErrorModelUnsupported
	ErrorCredential
	ErrorRequestRejected
	ErrorRateLimited
)

type responseErrorDetail struct {
	Code   json.RawMessage `json:"code"`
	Type   string          `json:"type"`
	Status string          `json:"status"`
	Param  string          `json:"param"`
}

// ClassifyResponseError 只识别明确错误码；非 JSON 或未知错误仍交给调用方按 HTTP 状态处理。
func ClassifyResponseError(body []byte) ResponseErrorKind {
	var envelope struct {
		responseErrorDetail
		Error    responseErrorDetail `json:"error"`
		Response struct {
			Error responseErrorDetail `json:"error"`
		} `json:"response"`
	}
	if json.Unmarshal(body, &envelope) != nil {
		return ErrorUnknown
	}
	for _, detail := range []responseErrorDetail{envelope.Error, envelope.Response.Error, envelope.responseErrorDetail} {
		codes := []string{detail.Type, detail.Status}
		var code string
		if json.Unmarshal(detail.Code, &code) == nil {
			codes = append([]string{code}, codes...)
		}
		for _, code := range codes {
			switch strings.ToLower(code) {
			case "model_not_found", "model_not_supported", "unsupported_model", "model_not_exist":
				if detail.Param == "" || detail.Param == "model" {
					return ErrorModelUnsupported
				}
				return ErrorRequestRejected
			case "invalid_api_key", "authentication_error", "invalid_key", "insufficient_quota", "quota_exceeded", "account_deactivated", "unauthenticated":
				return ErrorCredential
			case "context_length_exceeded", "safety_violation":
				return ErrorRequestRejected
			case "rate_limit_exceeded", "rate_limit_error", "resource_exhausted":
				return ErrorRateLimited
			}
			if IsNonRetryableUpstreamErrorCode(code) {
				return ErrorRequestRejected
			}
		}
	}
	return ErrorUnknown
}

// IsEndpointFailure 判断完整 HTTP 错误响应是否说明地址故障，与是否换 Key 重试分开。
// 凭据、配额、模型和请求拒绝不影响同地址下其他 Key；网络及读体错误由调用方另行记录。
func IsEndpointFailure(statusCode int, body []byte) bool {
	if ClassifyResponseError(body) != ErrorUnknown || IsUpstreamModelCapacityErrorBody(body) {
		return false
	}
	return statusCode == http.StatusRequestTimeout ||
		statusCode >= http.StatusInternalServerError && statusCode < 600 ||
		statusCode >= http.StatusOK && statusCode < http.StatusMultipleChoices && IsUpstreamErrorEnvelope(body)
}
