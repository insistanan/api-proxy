// 本文件收拢"上游失败/响应处理失败该怎么办"的错误类型与分类决策：
// decideUpstreamFailure 把状态码与响应体交给同包 failover_classify.go / content_policy_retry.go 的谓词
// （ShouldRetryWithNextKey / shouldFailoverToNextChannel，底层为 utils.ClassifyUpstreamStatus），
// classifyResponseProcessingError 把写回阶段的 error 归类；本文件只做"谓词结果 -> 本层动作"的映射。
package proxycore

import (
	"github.com/BenedictKing/api-proxy/internal/handlers/hooks"
	"github.com/BenedictKing/api-proxy/internal/providers"
	"github.com/BenedictKing/api-proxy/internal/proxy"

	"errors"
	"net/http"
)

// isClientSideError 判断错误是否由客户端明确取消（不应计入渠道失败）。
// 实现委托 internal/proxy（R4-b：与 HTTP 框架无关的分类谓词唯一出处）。
func isClientSideError(err error) bool {
	return proxy.IsClientSideCancel(err)
}

// RetrySameCandidateError 表示响应已成功到达代理，但模型暂时无法产出内容
// （模型容量状态，不应污染失败指标）。实现委托 internal/proxy（R4-b）。
type RetrySameCandidateError = proxy.RetrySameCandidateError

func NewRetrySameCandidateError(err error) error {
	return proxy.NewRetrySameCandidateError(err)
}

func isRetrySameCandidateError(err error) bool {
	return proxy.IsRetrySameCandidateError(err)
}

// IsUpstreamModelCapacityError 判断上游响应体是否为已知模型容量错误。
// 判据唯一出处 utils（R4-a 收敛），委托 internal/proxy 转发（R4-b）。
func IsUpstreamModelCapacityError(body []byte) bool {
	return proxy.IsUpstreamModelCapacityErrorBody(body)
}

type upstreamFailureAction uint8

const (
	upstreamFailureRespond upstreamFailureAction = iota
	upstreamFailureTryNextKey
	upstreamFailureTryNextChannel
)

type upstreamFailureDecision struct {
	action       upstreamFailureAction
	quotaRelated bool
	errorType    string
}

type responseProcessingAction uint8

const (
	responseProcessingClientCancelled responseProcessingAction = iota
	responseProcessingContentSafety
	responseProcessingContentSafetyHook
	responseProcessingRetryCandidate
	responseProcessingChannelFailure
)

type responseProcessingDecision struct {
	action    responseProcessingAction
	safetyErr *hooks.ContentSafetyError
	hookErr   *hooks.ContentSafetyHookError
}

func (a UpstreamAttempt) decideUpstreamFailure(statusCode int, body []byte) upstreamFailureDecision {
	if a.LogContext.AllowContentPolicyChannelFailover && shouldFailoverToNextChannel(
		body,
		a.ChannelScheduler.GetActiveChannelCountForModel(a.Kind, a.LogContext.Model),
	) {
		return upstreamFailureDecision{
			action:    upstreamFailureTryNextChannel,
			errorType: "content_policy",
		}
	}

	shouldFailover, quotaRelated := ShouldRetryWithNextKey(
		statusCode,
		body,
		a.ConfigManager.GetFuzzyModeEnabled(),
		a.APIType,
	)
	if shouldFailover {
		return upstreamFailureDecision{
			action:       upstreamFailureTryNextKey,
			quotaRelated: quotaRelated,
			errorType:    classifyUpstreamError(statusCode, quotaRelated),
		}
	}

	return upstreamFailureDecision{
		action:    upstreamFailureRespond,
		errorType: classifyUpstreamError(statusCode, false),
	}
}

func classifyResponseProcessingError(err error, responseWritten bool) responseProcessingDecision {
	if isClientSideError(err) {
		return responseProcessingDecision{action: responseProcessingClientCancelled}
	}
	if safetyErr := hooks.ContentSafetyErrorFrom(err); safetyErr != nil {
		return responseProcessingDecision{
			action:    responseProcessingContentSafety,
			safetyErr: safetyErr,
		}
	}
	if hookErr := contentSafetyHookError(err); hookErr != nil {
		return responseProcessingDecision{
			action:  responseProcessingContentSafetyHook,
			hookErr: hookErr,
		}
	}
	// 上游流内失败（response.failed / error 事件）：容量错误按同候选重试处理
	// （与 RetrySameCandidateError 同语义）；其余为真实渠道失败。
	if streamFailed, ok := providers.IsUpstreamStreamFailed(err); ok {
		if streamFailed.Capacity && !responseWritten {
			return responseProcessingDecision{action: responseProcessingRetryCandidate}
		}
		return responseProcessingDecision{action: responseProcessingChannelFailure}
	}
	if isRetrySameCandidateError(err) && !responseWritten {
		return responseProcessingDecision{action: responseProcessingRetryCandidate}
	}
	return responseProcessingDecision{action: responseProcessingChannelFailure}
}

func contentSafetyHookError(err error) *hooks.ContentSafetyHookError {
	var target *hooks.ContentSafetyHookError
	if errors.As(err, &target) {
		return target
	}
	return nil
}

func classifyUpstreamError(statusCode int, quotaRelated bool) string {
	if quotaRelated {
		return "quota"
	}
	switch {
	case statusCode == http.StatusTooManyRequests:
		return "rate_limit"
	case statusCode == http.StatusUnauthorized || statusCode == http.StatusForbidden:
		return "auth"
	case statusCode >= 500:
		return "upstream_5xx"
	case statusCode >= 400:
		return "upstream_4xx"
	default:
		return "upstream"
	}
}
