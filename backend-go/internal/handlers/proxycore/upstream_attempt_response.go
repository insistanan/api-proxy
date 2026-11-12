// 单次候选响应：兼容准备失败的收尾与成功响应交付。
package proxycore

import (
	"errors"
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/insistanan/api-proxy/internal/dialogue/intent"
	"github.com/insistanan/api-proxy/internal/handlers/hooks"
	"github.com/insistanan/api-proxy/internal/infra/config"
	"github.com/insistanan/api-proxy/internal/infra/utils"
	"github.com/insistanan/api-proxy/internal/orchestration/proxy"
	"github.com/insistanan/api-proxy/internal/orchestration/upstreamcontrol"
	"github.com/insistanan/api-proxy/internal/protocol/providers"
	"github.com/insistanan/api-proxy/internal/protocol/types"
)

// candidateAttempt 绑定已准备好的单次候选：兼容重试和响应交付共享同一记账身份。
// 重试与跨 URL 状态通过指针共享，只在当前请求内使用。
type candidateAttempt struct {
	attempt          UpstreamAttempt
	baseURL          string
	apiKey           string
	originalURLIndex int
	startedAt        time.Time
	requestLogID     string
	upstream         *config.UpstreamConfig
	lifecycle        *attemptLifecycle
	failover         *upstreamFailoverState
	retries          *candidateRetryState
}

// finishRetryPreparation 处理兼容重试准备失败，保持观测收尾与协议错误写出顺序。
func (step candidateAttempt) finishRetryPreparation(preparationErr error) {
	a := step.attempt
	c := a.Context
	apiType := a.APIType
	upstream := a.Upstream
	isStream := a.IsStream
	logCtx := a.LogContext
	requestLogID := step.requestLogID
	currentBaseURL := step.baseURL
	apiKey := step.apiKey
	attemptStart := step.startedAt
	lifecycle := step.lifecycle

	if types.IsUnsupportedProtocol(preparationErr) {
		lifecycle.finalizeNeutral()
		return
	}
	var clientErr interface{ ClientRequestError() bool }
	if errors.As(preparationErr, &clientErr) && clientErr.ClientRequestError() {
		lifecycle.finalizeNeutral()
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": preparationErr.Error(), "type": "invalid_request_error"}})
		recordAttemptLog(c, logCtx, upstream, apiType, requestLogID, currentBaseURL, apiKey, "failed", http.StatusBadRequest, false, attemptStart, "invalid_request_error", preparationErr.Error(), false, isStream, nil)
		return
	}
	if upstreamcontrol.IsLocalError(preparationErr) {
		lifecycle.finalizeNeutral()
		recordAttemptLog(c, logCtx, upstream, apiType, requestLogID, currentBaseURL, apiKey, "failed", http.StatusServiceUnavailable, false, attemptStart, "local_admission", preparationErr.Error(), !errors.Is(preparationErr, upstreamcontrol.ErrBudget), isStream, nil)
		return
	}
	if isClientSideError(preparationErr) {
		lifecycle.finalizeClientCancelled()
		recordAttemptLog(c, logCtx, upstream, apiType, requestLogID, currentBaseURL, apiKey, "cancelled", 0, false, attemptStart, "client_cancelled", preparationErr.Error(), false, isStream, nil)
		return
	}
	status := handleContentSafetyPreparationError(c, apiType, preparationErr)
	lifecycle.finalizeNeutral()
	attemptStatus := "failed"
	if status == http.StatusForbidden {
		attemptStatus = "blocked"
	}
	recordAttemptLog(c, logCtx, upstream, apiType, requestLogID, currentBaseURL, apiKey, attemptStatus, status, false, attemptStart, "content_safety", preparationErr.Error(), false, isStream, nil)
}

// deliverResponse 交付上游响应；retry=true 时已完成本候选收尾，由主循环选择后续候选。
func (step candidateAttempt) deliverResponse(resp *http.Response) (UpstreamAttemptResult, bool) {
	a := step.attempt
	c := a.Context
	cfgManager := a.ConfigManager
	channelScheduler := a.ChannelScheduler
	apiType := a.APIType
	upstream := a.Upstream
	isStream := a.IsStream
	logCtx := a.LogContext
	requestLogID := step.requestLogID
	currentBaseURL := step.baseURL
	originalIdx := step.originalURLIndex
	apiKey := step.apiKey
	attemptStart := step.startedAt
	upstreamCopy := step.upstream
	lifecycle := step.lifecycle
	failoverState := step.failover
	retryState := step.retries

	turn := intent.TurnFrom(c.Request.Context())
	if turn != nil {
		if err := turn.CommitTarget(upstreamCopy, apiKey, config.ResolveUpstreamModel(a.RequestedModel, upstreamCopy)); err != nil {
			_ = resp.Body.Close()
			lifecycle.finalizeNeutral()
			intentRouteError(c, err)
			return newUpstreamAttemptResult(true, "", 0, nil, nil, err).withHealth(channelHealthNeutral), false
		}
	}
	usage, err := a.HandleSuccess(c, resp, upstreamCopy, apiKey)
	if turn != nil {
		if err != nil && !c.Writer.Written() {
			if saveErr := turn.AbortUndelivered(); saveErr != nil {
				_ = resp.Body.Close()
				lifecycle.finalizeNeutral()
				intentRouteError(c, saveErr)
				recordAttemptLog(c, logCtx, upstream, apiType, requestLogID, currentBaseURL, apiKey, "failed", http.StatusInternalServerError, false, attemptStart, "intent_state", saveErr.Error(), false, isStream, usage)
				return newUpstreamAttemptResult(true, "", 0, nil, usage, saveErr).withHealth(channelHealthNeutral), false
			}
		} else if saveErr := turn.Complete(err == nil); saveErr != nil {
			log.Printf("[Intent-Record] requestId=%s 记录交付结果失败: %v", requestLogID, saveErr)
		}
	}
	if streamFailure, ok := providers.IsUpstreamStreamFailed(err); ok {
		if observation := currentAvailabilityObservation(c); observation != nil {
			observation.evidence = streamFailure.Payload
		}
	}
	// 回调结束即交还响应所有权；失败重试前也必须释放共享名额。
	_ = resp.Body.Close()
	// HandleSuccess 成功返回代表协议响应已交付。客户端随后结束 SSE 或
	// 关闭连接，不应覆盖本轮成功；交付前的取消仍由响应处理器返回错误。
	if cancelErr := c.Request.Context().Err(); err != nil && cancelErr != nil {
		lifecycle.finalizeClientCancelled()
		recordAttemptLog(c, logCtx, upstream, apiType, requestLogID, currentBaseURL, apiKey, "cancelled", resp.StatusCode, false, attemptStart, "client_cancelled", cancelErr.Error(), false, isStream, usage)
		return newUpstreamAttemptResult(true, "", 0, nil, usage, cancelErr).withHealth(channelHealthNeutral), false
	}
	if err != nil {
		failoverState.lastError = err
		decision := classifyResponseProcessingError(err, c.Writer.Written())
		// 本渠道最终健康结果：除"响应已提交后的真实上游中断"按失败
		// 记账外，取消/内容安全/本地检查错误均为中性（R2-a 表）。
		health := channelHealthNeutral
		switch decision.action {
		case responseProcessingLocalFailure:
			lifecycle.finalizeNeutral()
			recordAttemptLog(c, logCtx, upstream, apiType, requestLogID, currentBaseURL, apiKey, "failed", http.StatusInternalServerError, false, attemptStart, "local_response", err.Error(), false, isStream, usage)
			if !c.Writer.Written() {
				c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": err.Error(), "type": "local_response_error"}})
			}
		case responseProcessingClientCancelled:
			// 客户端取消/断开：计入总请求数但不计入失败
			lifecycle.finalizeClientCancelled()
			recordAttemptLog(c, logCtx, upstream, apiType, requestLogID, currentBaseURL, apiKey, "cancelled", resp.StatusCode, false, attemptStart, "client_cancelled", err.Error(), false, isStream, usage)
			log.Printf("[%s-Cancel] 请求已取消，停止渠道 failover", apiType)

		case responseProcessingContentSafety:
			lifecycle.finalizeNeutral()
			recordAttemptLog(c, logCtx, upstream, apiType, requestLogID, currentBaseURL, apiKey, "blocked", http.StatusForbidden, false, attemptStart, "content_safety", decision.safetyErr.Error(), false, isStream, usage)
			if c.Writer.Written() {
				if writeErr := hooks.WriteAttachedStreamError(c, decision.safetyErr); writeErr != nil {
					log.Printf("[%s-ContentSafety] 流错误写出失败: %v", apiType, writeErr)
				}
			} else {
				if writeErr := hooks.WriteAttachedContentSafetyError(c, decision.safetyErr); writeErr != nil {
					log.Printf("[%s-ContentSafety] 协议错误写出失败: %v", apiType, writeErr)
					c.JSON(http.StatusInternalServerError, gin.H{"error": writeErr.Error(), "code": "CONTENT_SAFETY_RESPONSE_ERROR"})
				}
			}

		case responseProcessingContentSafetyHook:
			lifecycle.finalizeNeutral()
			recordAttemptLog(c, logCtx, upstream, apiType, requestLogID, currentBaseURL, apiKey, "failed", http.StatusInternalServerError, false, attemptStart, "content_safety_hook", decision.hookErr.Error(), false, isStream, usage)
			log.Printf("[%s-ContentSafety] 本地响应检查失败: %v", apiType, decision.hookErr)
			if c.Writer.Written() {
				if writeErr := hooks.WriteAttachedStreamHookError(c, decision.hookErr); writeErr != nil {
					log.Printf("[%s-ContentSafety] 流内部错误写出失败: %v", apiType, writeErr)
				}
			} else {
				c.JSON(http.StatusInternalServerError, gin.H{
					"error": "内容安全检查失败",
					"code":  "CONTENT_SAFETY_HOOK_ERROR",
				})
			}

		case responseProcessingRetryCandidate:
			lifecycle.finalizeNeutral()
			// 容量类错误（上游未产出内容）：中性结束候选。
			failoverState.markNeutralTermination()

			candidate := currentBaseURL + "\x00" + apiKey
			canRetry := retryState.CanRetryCandidate(candidate, c.Request.Context())
			recordAttemptLog(c, logCtx, upstream, apiType, requestLogID, currentBaseURL, apiKey, "failed", resp.StatusCode, false, attemptStart, "model_capacity", err.Error(), canRetry, isStream, usage)
			if canRetry {
				retryCount := retryState.RecordCandidateRetry(candidate)
				log.Printf("[%s-Capacity] 上游未产出内容，使用同一 BaseURL 和 Key 重试 (%d/%d)", apiType, retryCount, proxy.DefaultMaxSameCandidateRetries)
				return UpstreamAttemptResult{}, true
			}

			// 仅在本次请求内排除该 Key，继续其他候选；不写入 Key 熔断和失败率。
			retryState.MarkKeyFailed(apiKey)
			failoverState.lastFailoverError = &FailoverError{Status: http.StatusServiceUnavailable, Body: []byte(err.Error())}
			return UpstreamAttemptResult{}, true

		case responseProcessingChannelFailure:
			// 真实渠道故障：计入失败指标
			failureStatus := resp.StatusCode
			if failureStatus >= http.StatusOK && failureStatus < http.StatusMultipleChoices {
				failureStatus = http.StatusBadGateway
			}
			cfgManager.MarkKeyAsFailed(apiKey, apiType)
			lifecycle.finalizeFailed()
			shouldRetryResponseProcessing := !c.Writer.Written()
			recordAttemptLog(c, logCtx, upstream, apiType, requestLogID, currentBaseURL, apiKey, "failed", failureStatus, false, attemptStart, "response_processing", err.Error(), shouldRetryResponseProcessing, isStream, usage)
			log.Printf("[%s-Key] 警告: 响应处理失败: %s", apiType, utils.RedactSensitivePlaceholdersForLog(err.Error()))
			if shouldRetryResponseProcessing {
				retryState.MarkKeyFailed(apiKey)
				if a.MarkURLFailure != nil {
					a.MarkURLFailure(currentBaseURL)
				}
				failoverState.lastFailoverError = &FailoverError{
					Status: failureStatus,
					Body:   []byte(err.Error()),
				}
				return UpstreamAttemptResult{}, true
			}
			// 响应已提交后的真实上游中断：渠道按失败记账，但不再故障转移。
			health = channelHealthFailure
		}
		return newUpstreamAttemptResult(true, "", 0, nil, usage, err).withHealth(health), false
	}

	if a.MarkURLSuccess != nil {
		a.MarkURLSuccess(currentBaseURL)
	}

	lifecycle.finalizeSuccessful(usage)
	recordAttemptLog(c, logCtx, upstream, apiType, requestLogID, currentBaseURL, apiKey, "completed", resp.StatusCode, true, attemptStart, "", "", false, isStream, usage)
	// 记录会话 BaseURL 粘滞，后续请求优先同 URL 以利 prompt cache
	if channelScheduler != nil && logCtx.ConversationID != "" {
		channelScheduler.SetPreferredBaseURL(logCtx.ConversationID, currentBaseURL)
	}
	return newUpstreamAttemptResult(true, apiKey, originalIdx, nil, usage, nil).withHealth(channelHealthSuccess), false
}
