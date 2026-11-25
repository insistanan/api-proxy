package visionlayer

import (
	"fmt"
	"net/http"

	"github.com/insistanan/api-proxy/internal/infra/upstreamreq"
	"github.com/insistanan/api-proxy/internal/protocol/codec"
	"github.com/insistanan/api-proxy/internal/protocol/providers"
	"github.com/insistanan/api-proxy/internal/protocol/types"
)

type visionResponseResult struct {
	visionAttemptResult
	usage           *types.Usage
	errorType       string
	endpointFailure bool
	rateLimited     bool
}

// processVisionResponse 只解释完整响应，不修改调度状态。
// 发送循环统一使用这里的结果记账，避免 HTTP 状态、错误正文和停止原因各走一套规则。
func processVisionResponse(provider providers.Provider, response *http.Response, body []byte, images []visionImage) visionResponseResult {
	result := visionResponseResult{visionAttemptResult: visionAttemptResult{health: visionHealthFailure}}
	decoded, _, err := codec.DecompressBodyWithLimit(codec.GetContentEncoding(response.Header), body, 64<<20)
	if err != nil {
		result.err = fmt.Errorf("解压图片理解响应失败: %w", err)
		result.errorType = "decode_response"
		result.endpointFailure = true
		result.retryable = true
		return result
	}
	body = decoded
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices || upstreamreq.IsUpstreamErrorEnvelope(body) {
		result.err = fmt.Errorf("上游返回错误: %s", truncateErrorBody(body))
		result.errorType = "upstream"
		result.endpointFailure = upstreamreq.IsEndpointFailure(response.StatusCode, body)
		result.retryable = shouldRetryVisionAttempt(response.StatusCode, body)
		errorKind := upstreamreq.ClassifyResponseError(body)
		switch {
		case errorKind == upstreamreq.ErrorRequestRejected:
			result.health = visionHealthNeutral
			result.errorType = "request_rejected"
		case response.StatusCode == http.StatusTooManyRequests || errorKind == upstreamreq.ErrorRateLimited:
			result.health = visionHealthNeutral
			result.errorType = "rate_limit"
			result.rateLimited = true
		}
		return result
	}

	claudeResponse, err := provider.ConvertToClaudeResponse(&types.ProviderResponse{
		StatusCode: response.StatusCode,
		Headers:    response.Header,
		Body:       body,
	})
	if err != nil {
		result.err = fmt.Errorf("解析图片理解响应失败: %w", err)
		result.errorType = "response_processing"
		result.endpointFailure = true
		return result
	}
	if claudeResponse != nil {
		result.usage = claudeResponse.Usage
		switch claudeResponse.StopReason {
		case "", "end_turn", "stop_sequence":
			// 保留未提供停止原因的协议；明确截断或拒绝的结果不能进入缓存。
		case "refusal":
			result.err = fmt.Errorf("图片理解请求被上游拒绝，停止原因为 %q", claudeResponse.StopReason)
			result.errorType = "request_rejected"
			result.health = visionHealthNeutral
			return result
		default:
			result.err = fmt.Errorf("图片理解响应未正常完成，停止原因为 %q", claudeResponse.StopReason)
			result.errorType = "incomplete_response"
			return result
		}
	}
	rawResult := extractResponseText(claudeResponse)
	if rawResult == "" {
		result.err = fmt.Errorf("图片理解响应未返回文字结果")
		result.errorType = "empty_response"
		return result
	}
	descriptions, err := parseVisionBatchResponse(rawResult, images)
	if err != nil {
		result.err = fmt.Errorf("图片理解响应的多图结果无效: %w", err)
		result.errorType = "invalid_batch_response"
		return result
	}
	result.descriptions = descriptions
	result.health = visionHealthSuccess
	return result
}

func shouldRetryVisionAttempt(statusCode int, body []byte) bool {
	switch upstreamreq.ClassifyResponseError(body) {
	case upstreamreq.ErrorRequestRejected:
		return false
	case upstreamreq.ErrorRateLimited:
		return true
	}
	// 2xx 错误对象不应落入成功状态码分支；业务拒绝已在上方排除。
	if statusCode >= http.StatusOK && statusCode < http.StatusMultipleChoices && upstreamreq.IsUpstreamErrorEnvelope(body) {
		return true
	}
	retryable, _ := upstreamreq.ClassifyUpstreamStatus(statusCode)
	return retryable
}
