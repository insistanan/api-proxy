// 上游请求准备：身份、内容安全、视觉层与同候选请求发送。
package proxycore

import (
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/insistanan/api-proxy/internal/config"
	"github.com/insistanan/api-proxy/internal/handlers/hooks"
	"github.com/insistanan/api-proxy/internal/logger"
	"github.com/insistanan/api-proxy/internal/scheduler"
	"github.com/insistanan/api-proxy/internal/upstreamcontrol"
	"github.com/insistanan/api-proxy/internal/visionlayer"
)

type upstreamAttemptPreflight struct {
	proxyURL string
}

func (a UpstreamAttempt) prepareAllKeys() (upstreamAttemptPreflight, bool, error) {
	if a.Upstream == nil || len(a.Upstream.APIKeys) == 0 {
		return upstreamAttemptPreflight{}, false, nil
	}
	if a.MetricsManager == nil {
		return upstreamAttemptPreflight{}, false, nil
	}
	if a.NextAPIKey == nil || a.BuildRequest == nil || a.HandleSuccess == nil {
		return upstreamAttemptPreflight{}, false, nil
	}
	if len(a.URLResults) == 0 {
		return upstreamAttemptPreflight{}, false, nil
	}

	proxyURL, err := a.ConfigManager.ResolveUpstreamProxyURL(a.Upstream)
	if err != nil {
		return upstreamAttemptPreflight{}, false, err
	}
	return upstreamAttemptPreflight{proxyURL: proxyURL}, true, nil
}

func (a UpstreamAttempt) buildPreparedAttemptRequest(
	upstreamCopy *config.UpstreamConfig,
	apiKey string,
) (*http.Request, string, error) {
	if err := a.checkIntentCandidate(upstreamCopy, apiKey); err != nil {
		return nil, "build_request", err
	}
	a.beginAvailabilityObservation(upstreamCopy, apiKey)
	if err := a.Context.Request.Context().Err(); err != nil {
		return nil, "client_cancelled", err
	}
	// 身份在构建前分配，正文、响应和结果使用同一个尝试 ID。
	a.Context.Request = a.Context.Request.WithContext(logger.ContextWithAttemptID(a.Context.Request.Context(), nextAttemptLogID("attempt")))
	req, err := a.BuildRequest(a.Context, upstreamCopy, apiKey)
	if err != nil {
		if req != nil && req.Body != nil {
			_ = req.Body.Close()
		}
		return nil, "build_request", err
	}
	// 最终上游请求必须继承入站取消和共享预算，避免构建器意外使用 Background。
	req = req.WithContext(a.Context.Request.Context())
	if a.SkipVisionLayer {
		if err := hooks.ResetAttachedStreamRestoration(a.Context); err != nil {
			return req, "content_safety", err
		}
		if err := hooks.RunAttachedPreRequestHooks(req.Context(), a.Context, req, upstreamCopy.Name,
			hooks.PayloadProtocolForServiceType(upstreamCopy.ServiceType, a.APIType)); err != nil {
			return req, "content_safety", err
		}
		return req, "", nil
	}
	prepareStage, err := prepareRequestForUpstream(
		a.Context,
		a.EnvConfig,
		a.ConfigManager,
		a.ChannelScheduler,
		a.Kind,
		upstreamCopy,
		a.LogContext.Model,
		a.LogContext.ConversationID,
		req,
		a.APIType,
	)
	if err != nil {
		return req, prepareStage, err
	}
	return req, "", nil
}

func (a UpstreamAttempt) prepareCandidateUpstream(
	baseURL string,
	capabilities upstreamCapabilityState,
) *config.UpstreamConfig {
	upstreamCopy := a.Upstream.Clone()
	upstreamCopy.BaseURL = baseURL
	// 在构建正文前固定本候选的身份模式，避免全局热更新使正文与发送头不一致。
	if a.ConfigManager.ClientCompatibilityEnabled(a.Upstream) {
		upstreamCopy.ClientIdentityMode = "compatibility"
	} else {
		upstreamCopy.ClientIdentityMode = "api"
	}
	upstreamCopy.DisablePromptCacheKey = capabilities.disablePromptCacheKey
	upstreamCopy.RequireReasoningContent = capabilities.requireReasoningContent
	recordConversationAttempt(a.ChannelScheduler, a.Kind, a.Upstream, a.LogContext, a.IsStream)
	return upstreamCopy
}

func (a UpstreamAttempt) retrySameCandidateRequest(
	upstreamCopy *config.UpstreamConfig,
	apiKey string,
	proxyURL string,
) compatibilityRetryResult {
	req, preparationStage, err := a.buildPreparedAttemptRequest(upstreamCopy, apiKey)
	if err != nil {
		if preparationStage != "build_request" && req != nil && req.Body != nil {
			_ = req.Body.Close()
		}
		return compatibilityRetryResult{
			PreparationStage: preparationStage,
			Err:              err,
		}
	}

	req = AttachRequestLogID(a.Context, req)
	req = req.WithContext(a.ConfigManager.UpstreamRequestContext(req.Context(), upstreamCopy, apiKey))
	req = a.observeAvailabilityRequest(req)
	resp, err := SendRequest(req, upstreamCopy, a.EnvConfig, a.IsStream, a.APIType, proxyURL, a.Permit, a.RequestTimeout)
	a.observeAvailabilityResponse(resp)
	if err != nil {
		errorType := "network"
		if upstreamcontrol.IsLocalError(err) {
			errorType = "local_admission"
		} else if a.Context.Request.Context().Err() != nil {
			errorType = "client_cancelled"
		}
		recordAttemptLog(a.Context, a.LogContext, upstreamCopy, a.APIType, BindRequestLogID(a.Context), upstreamCopy.BaseURL, apiKey,
			"failed", 0, false, time.Now(), errorType, err.Error(), true, a.IsStream, nil)
		return compatibilityRetryResult{PreparationStage: "send_request", Err: err}
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return compatibilityRetryResult{Response: resp, Succeeded: true}
	}

	body, readErr := readUpstreamErrorBody(resp)
	if readErr != nil {
		errorType := "read_body"
		if a.Context.Request.Context().Err() != nil {
			errorType = "client_cancelled"
		}
		recordAttemptLog(a.Context, a.LogContext, upstreamCopy, a.APIType, BindRequestLogID(a.Context), upstreamCopy.BaseURL, apiKey,
			"failed", resp.StatusCode, false, time.Now(), errorType, readErr.Error(), true, a.IsStream, nil)
		// 读取中断：响应不完整，按网络故障处理，不返回截断 body。
		return compatibilityRetryResult{PreparationStage: "read_body", Err: fmt.Errorf("读取重试响应体失败: %w", readErr)}
	}
	a.rememberAvailabilityFailure(resp, body)
	return compatibilityRetryResult{Response: resp, Body: body}
}

func prepareRequestForUpstream(
	c *gin.Context,
	envCfg *config.EnvConfig,
	cfgManager *config.ConfigManager,
	channelScheduler *scheduler.ChannelScheduler,
	kind scheduler.ChannelKind,
	upstream *config.UpstreamConfig,
	model string,
	conversationID string,
	req *http.Request,
	apiType string,
) (string, error) {
	if err := hooks.ResetAttachedStreamRestoration(c); err != nil {
		return "content_safety", err
	}
	payloadProtocol := hooks.PayloadProtocolForServiceType(upstream.ServiceType, apiType)
	if err := hooks.RunAttachedPreRequestHooks(c.Request.Context(), c, req, upstream.Name, payloadProtocol); err != nil {
		return "content_safety", err
	}
	if err := visionlayer.PrepareRequest(
		c,
		envCfg,
		cfgManager,
		channelScheduler,
		kind,
		upstream,
		model,
		conversationID,
		req,
	); err != nil {
		return "vision_layer", err
	}
	if err := hooks.RunAttachedPreRequestHooks(c.Request.Context(), c, req, upstream.Name, payloadProtocol); err != nil {
		return "content_safety", err
	}
	return "", nil
}

func handleContentSafetyPreparationError(c *gin.Context, apiType string, err error) int {
	if safetyErr := hooks.ContentSafetyErrorFrom(err); safetyErr != nil {
		if writeErr := hooks.WriteAttachedContentSafetyError(c, safetyErr); writeErr != nil {
			log.Printf("[%s-ContentSafety] 协议错误写出失败: %v", apiType, writeErr)
			c.JSON(http.StatusInternalServerError, gin.H{"error": writeErr.Error(), "code": "CONTENT_SAFETY_RESPONSE_ERROR"})
			return http.StatusInternalServerError
		}
		return http.StatusForbidden
	}
	c.JSON(http.StatusInternalServerError, gin.H{
		"error": err.Error(),
		"code":  "CONTENT_SAFETY_HOOK_ERROR",
	})
	return http.StatusInternalServerError
}
