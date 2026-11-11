// 单渠道 Key/BaseURL 重试编排；候选准备、兼容重试和响应交付按阶段委托。
package proxycore

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/insistanan/api-proxy/internal/config"
	"github.com/insistanan/api-proxy/internal/handlers/hooks"
	"github.com/insistanan/api-proxy/internal/proxy"
	"github.com/insistanan/api-proxy/internal/scheduler"
	"github.com/insistanan/api-proxy/internal/types"
	"github.com/insistanan/api-proxy/internal/upstreamcontrol"
	"github.com/insistanan/api-proxy/internal/utils"
	"github.com/insistanan/api-proxy/internal/visionlayer"
)

func (a UpstreamAttempt) tryWithAllKeys() UpstreamAttemptResult {
	c := a.Context
	envCfg := a.EnvConfig
	cfgManager := a.ConfigManager
	channelScheduler := a.ChannelScheduler
	kind := a.Kind
	apiType := a.APIType
	metricsManager := a.MetricsManager
	upstream := a.Upstream
	urlResults := a.URLResults
	requestBody := a.RequestBody
	isStream := a.IsStream
	logCtx := a.LogContext

	preflight, valid, err := a.prepareAllKeys()
	if err != nil {
		// 本地配置错误（如代理解析失败）：中性结束，显式返回内部错误。
		return newUpstreamAttemptResult(false, "", 0, nil, nil, err).withHealth(channelHealthNeutral)
	}
	if !valid {
		// 没有可尝试的候选（无 Key / 缺回调等）：不产生渠道健康结论。
		return newUpstreamAttemptResult(false, "", 0, nil, nil, nil)
	}
	proxyURL := preflight.proxyURL

	urlResults = preferConversationBaseURL(channelScheduler, envCfg, apiType, logCtx, urlResults)

	failoverState := newUpstreamFailoverState()
	requestLogID := BindRequestLogID(c)
	capabilities := newUpstreamCapabilityState(upstream)

	// 强制探测模式：基于本次优先尝试的 BaseURL 判断（避免 BaseURL/BaseURLs 不一致导致误判）
	forceProbeMode := AreAllKeysSuspended(metricsManager, urlResults[0].URL, upstream.APIKeys, logCtx.ChannelIndex)
	if forceProbeMode {
		log.Printf("[%s-ForceProbe] 渠道 %s 所有 Key 都被熔断，启用强制探测模式", apiType, upstream.Name)
	}

	for urlIdx, urlResult := range urlResults {
		currentBaseURL := urlResult.URL
		originalIdx := urlResult.OriginalIdx // 原始索引用于指标记录
		retryState := newCandidateRetryState(len(upstream.APIKeys))

		for attempt := 0; attempt < retryState.MaxRetries(); attempt++ {
			if err := c.Request.Context().Err(); err != nil {
				return newUpstreamAttemptResult(true, "", 0, nil, nil, err).withHealth(channelHealthNeutral)
			}
			RestoreRequestBody(c, requestBody)
			ResetRequestLogFirstToken(c)
			attemptStart := time.Now()

			apiKey, err := a.NextAPIKey(upstream, retryState.FailedKeys())
			if err != nil {
				// 候选选取耗尽不能覆盖明确的本地容量错误。
				if !upstreamcontrol.IsLocalError(failoverState.lastError) {
					failoverState.lastError = err
				}
				break // 当前 BaseURL 没有可用 Key，尝试下一个 BaseURL
			}

			// 检查熔断状态
			if !forceProbeMode && metricsManager.ShouldSuspendKey(currentBaseURL, apiKey, logCtx.ChannelIndex) {
				retryState.MarkKeyFailed(apiKey)
				log.Printf("[%s-Circuit] 跳过熔断中的 Key: %s", apiType, utils.MaskAPIKey(apiKey))
				continue
			}

			if envCfg.ShouldLog("info") {
				log.Printf("[%s-Key] 使用API密钥: %s (BaseURL %d/%d, 尝试 %d/%d)",
					apiType, utils.MaskAPIKey(apiKey), urlIdx+1, len(urlResults), attempt+1, retryState.MaxRetries())
			}

			// 使用深拷贝避免并发修改问题
			upstreamCopy := a.prepareCandidateUpstream(currentBaseURL, capabilities)

			req, prepareStage, err := a.buildPreparedAttemptRequest(upstreamCopy, apiKey)
			if err != nil && c.Request.Context().Err() != nil {
				if req != nil && req.Body != nil {
					_ = req.Body.Close()
				}
				return newUpstreamAttemptResult(true, "", 0, nil, nil, c.Request.Context().Err()).withHealth(channelHealthNeutral)
			}
			if upstreamcontrol.IsLocalError(err) {
				if req != nil && req.Body != nil {
					_ = req.Body.Close()
				}
				recordAttemptLog(c, logCtx, upstream, apiType, requestLogID, currentBaseURL, apiKey, "failed", http.StatusServiceUnavailable, false, attemptStart, "local_admission", err.Error(), !errors.Is(err, upstreamcontrol.ErrBudget), isStream, nil)
				if skipLocalAdmissionCandidate(err, &failoverState, retryState, apiKey) {
					continue
				}
				return localAdmissionResult(c, err, false)
			}
			if err != nil && prepareStage == "build_request" {
				if types.IsUnsupportedProtocol(err) {
					body, _ := json.Marshal(gin.H{"error": gin.H{"message": err.Error(), "type": "unsupported_protocol", "code": "unsupported_protocol"}})
					recordAttemptLog(c, logCtx, upstream, apiType, requestLogID, currentBaseURL, apiKey, "failed", http.StatusBadRequest, false, attemptStart, "unsupported_protocol", err.Error(), true, isStream, nil)
					// 更换 Key 或 URL 不能补齐协议能力；交给模型层的其他目标渠道。
					return newUpstreamAttemptResult(false, "", 0, &FailoverError{Status: http.StatusBadRequest, Body: body}, nil, err).withHealth(channelHealthNeutral)
				}
				var requestErr *RequestValidationError
				var configErr *config.ConfigError
				var clientErr interface{ ClientRequestError() bool }
				if errors.As(err, &requestErr) || errors.As(err, &configErr) || (errors.As(err, &clientErr) && clientErr.ClientRequestError()) {
					if req != nil && req.Body != nil {
						_ = req.Body.Close()
					}
					status, errorType := http.StatusBadRequest, "invalid_request_error"
					if configErr != nil {
						status, errorType = http.StatusInternalServerError, "configuration_error"
					}
					recordAttemptLog(c, logCtx, upstream, apiType, requestLogID, currentBaseURL, apiKey, "failed", status, false, attemptStart, errorType, err.Error(), false, isStream, nil)
					c.JSON(status, gin.H{"error": gin.H{"message": err.Error(), "type": errorType, "code": errorType}})
					return newUpstreamAttemptResult(true, "", 0, nil, nil, err).withHealth(channelHealthNeutral)
				}
				failoverState.lastError = err
				retryState.MarkKeyFailed(apiKey)
				channelScheduler.RecordFailure(currentBaseURL, apiKey, logCtx.ChannelIndex, kind)
				recordAttemptLog(c, logCtx, upstream, apiType, requestLogID, currentBaseURL, apiKey, "failed", 0, false, attemptStart, "build_request", err.Error(), true, isStream, nil)
				continue
			}
			if err != nil {
				if req != nil && req.Body != nil {
					_ = req.Body.Close()
				}
				if c.Request.Context().Err() != nil {
					return newUpstreamAttemptResult(true, "", 0, nil, nil, c.Request.Context().Err()).withHealth(channelHealthNeutral)
				}
				if prepareStage == "content_safety" {
					status := handleContentSafetyPreparationError(c, apiType, err)
					attemptStatus := "failed"
					if status == http.StatusForbidden {
						attemptStatus = "blocked"
					}
					recordAttemptLog(c, logCtx, upstream, apiType, requestLogID, currentBaseURL, apiKey, attemptStatus, status, false, attemptStart, prepareStage, err.Error(), false, isStream, nil)
					// 内容安全准备失败：本地检查系统问题，中性结束。
					return newUpstreamAttemptResult(true, "", 0, nil, nil, err).withHealth(channelHealthNeutral)
				}
				status, code := visionlayer.ErrorResponse(err)
				payload := gin.H{
					"error": err.Error(),
					"code":  code,
				}
				recordAttemptLog(c, logCtx, upstream, apiType, requestLogID, currentBaseURL, apiKey, "failed", status, false, attemptStart, "vision_layer", err.Error(), true, isStream, nil)
				if cfgManager.GetFuzzyModeEnabled() && channelScheduler.GetActiveChannelCountForModel(kind, logCtx.Model) > 1 {
					body, _ := json.Marshal(payload)
					// 视觉层本地失败：中性结束，交给外层换渠道。
					return newUpstreamAttemptResult(false, "", 0, &FailoverError{Status: status, Body: body}, nil, err).withHealth(channelHealthNeutral)
				}
				c.JSON(status, payload)
				return newUpstreamAttemptResult(true, "", 0, nil, nil, err).withHealth(channelHealthNeutral)
			}
			// 请求准备成功后再启动请求画像和活跃度观测。
			lifecycle := a.startAttemptLifecycle(currentBaseURL, apiKey)
			candidateStep := candidateAttempt{
				attempt:          a,
				baseURL:          currentBaseURL,
				apiKey:           apiKey,
				originalURLIndex: originalIdx,
				startedAt:        attemptStart,
				requestLogID:     requestLogID,
				upstream:         upstreamCopy,
				lifecycle:        &lifecycle,
				failover:         &failoverState,
				retries:          retryState,
			}

			req = AttachRequestLogID(c, req)
			req = req.WithContext(cfgManager.UpstreamRequestContext(req.Context(), upstreamCopy, apiKey))
			req = a.observeAvailabilityRequest(req)
			resp, err := SendRequest(req, upstreamCopy, envCfg, isStream, apiType, proxyURL, a.Permit, a.RequestTimeout)
			a.observeAvailabilityResponse(resp)
			if err != nil {
				if upstreamcontrol.IsLocalError(err) {
					lifecycle.finalizeNeutral()
					recordAttemptLog(c, logCtx, upstream, apiType, requestLogID, currentBaseURL, apiKey, "failed", http.StatusServiceUnavailable, false, attemptStart, "local_admission", err.Error(), !errors.Is(err, upstreamcontrol.ErrBudget), isStream, nil)
					if skipLocalAdmissionCandidate(err, &failoverState, retryState, apiKey) {
						continue
					}
					return localAdmissionResult(c, err, false)
				}
				failoverState.lastError = err
				// 区分客户端取消和真实渠道故障（统一口径）
				if isClientSideError(err) {
					// 客户端取消：不计入失败，不触发 failover
					lifecycle.finalizeClientCancelled()
					recordAttemptLog(c, logCtx, upstream, apiType, requestLogID, currentBaseURL, apiKey, "cancelled", 0, false, attemptStart, "client_cancelled", err.Error(), false, isStream, nil)
					log.Printf("[%s-Cancel] 请求已取消（SendRequest 阶段）", apiType)
					// 客户端明确取消：中性结束，立即停止。
					return newUpstreamAttemptResult(true, "", 0, nil, nil, err).withHealth(channelHealthNeutral)
				}
				// 真实渠道故障：计入失败，继续 failover
				retryState.MarkKeyFailed(apiKey)
				cfgManager.MarkKeyAsFailed(apiKey, apiType)
				lifecycle.finalizeFailed()
				if a.MarkURLFailure != nil {
					a.MarkURLFailure(currentBaseURL)
				}
				recordAttemptLog(c, logCtx, upstream, apiType, requestLogID, currentBaseURL, apiKey, "failed", 0, false, attemptStart, "network", err.Error(), true, isStream, nil)
				log.Printf("[%s-Key] 警告: API密钥失败: %v", apiType, err)
				continue
			}

			// 收到响应头，性能画像记录首字节时间
			if pm := lifecycle.profiles; pm != nil {
				pm.RecordFirstByte(lifecycle.observation.ProfileRequestID)
			}

			if resp.StatusCode < 200 || resp.StatusCode >= 300 {
				respBodyBytes, readErr := readUpstreamErrorBody(resp)
				if cancelErr := c.Request.Context().Err(); cancelErr != nil {
					lifecycle.finalizeClientCancelled()
					recordAttemptLog(c, logCtx, upstream, apiType, requestLogID, currentBaseURL, apiKey, "cancelled", resp.StatusCode, false, attemptStart, "client_cancelled", cancelErr.Error(), false, isStream, nil)
					return newUpstreamAttemptResult(true, "", 0, nil, nil, cancelErr).withHealth(channelHealthNeutral)
				}
				if readErr != nil {
					// 响应体读取中断：连接已不可靠，按渠道网络故障处理，
					// 不把截断 body 用于错误分类或回给客户端。
					failoverState.lastError = fmt.Errorf("读取上游错误响应体失败: %w", readErr)
					retryState.MarkKeyFailed(apiKey)
					cfgManager.MarkKeyAsFailed(apiKey, apiType)
					lifecycle.finalizeFailed()
					if a.MarkURLFailure != nil {
						a.MarkURLFailure(currentBaseURL)
					}
					recordAttemptLog(c, logCtx, upstream, apiType, requestLogID, currentBaseURL, apiKey, "failed", http.StatusBadGateway, false, attemptStart, "read_body", readErr.Error(), true, isStream, nil)
					log.Printf("[%s-Key] 警告: 读取上游错误响应体失败 (状态: %d)，尝试下一个密钥: %v", apiType, resp.StatusCode, readErr)
					continue
				}
				a.rememberAvailabilityFailure(resp, respBodyBytes)
				retrySucceeded := false

				if IsUpstreamModelCapacityError(respBodyBytes) {
					lifecycle.finalizeNeutral()

					failoverState.lastError = fmt.Errorf("上游模型暂时满载")
					failoverState.lastFailoverError = &FailoverError{Status: resp.StatusCode, Body: respBodyBytes}
					// 模型容量状态：即使后续重试耗尽，渠道结束原因也是中性。
					failoverState.markNeutralTermination()
					candidate := currentBaseURL + "\x00" + apiKey
					canRetry := retryState.CanRetryCandidate(candidate, c.Request.Context())
					recordAttemptLog(c, logCtx, upstream, apiType, requestLogID, currentBaseURL, apiKey, "failed", resp.StatusCode, false, attemptStart, "model_capacity", string(respBodyBytes), canRetry, isStream, nil)
					if canRetry {
						retryCount := retryState.RecordCandidateRetry(candidate)
						log.Printf("[%s-Capacity] 模型暂时满载，使用同一 BaseURL 和 Key 重试 (%d/%d)", apiType, retryCount, proxy.DefaultMaxSameCandidateRetries)
						continue
					}
					retryState.MarkKeyFailed(apiKey)
					continue
				}

				// 429 的组合冷却已由发送出口按 Retry-After 登记；重试重新进入 FIFO。
				if resp.StatusCode == http.StatusTooManyRequests && !utils.IsNonRetryableUpstreamErrorBody(respBodyBytes) {
					recordAttemptLog(c, logCtx, upstream, apiType, requestLogID, currentBaseURL, apiKey, "failed", resp.StatusCode, false, attemptStart, "upstream", string(respBodyBytes), true, isStream, nil)
					candidate := currentBaseURL + "\x00" + apiKey
					if retryState.CanRetryCandidate(candidate, c.Request.Context()) {
						retryCount := retryState.RecordCandidateRetry(candidate)
						log.Printf("[%s-RateLimit] 渠道 %s 收到 429，释放名额后按 Retry-After 重新排队 (%d/1)", apiType, upstream.Name, retryCount)
						lifecycle.finalizeNeutral()
						RestoreRequestBody(c, requestBody)
						continue
					}
					lifecycle.finalizeNeutral()
					failoverState.lastError = fmt.Errorf("上游限流冷却中")
					failoverState.lastFailoverError = &FailoverError{Status: resp.StatusCode, Body: respBodyBytes}
					failoverState.markNeutralTermination()
					retryState.MarkKeyFailed(apiKey)
					continue
				}

				var aborted *UpstreamAttemptResult
				resp, respBodyBytes, retrySucceeded, aborted = candidateStep.retryCompatibility(resp, respBodyBytes, proxyURL, &capabilities)
				if aborted != nil {
					if skipLocalAdmissionCandidate(aborted.LastError, &failoverState, retryState, apiKey) {
						continue
					}
					return *aborted
				}

				if !retrySucceeded {
					decision := a.decideUpstreamFailure(resp.StatusCode, respBodyBytes)
					switch decision.action {
					case upstreamFailureTryNextChannel:
						// 内容审核与当前请求正文、上游策略相关。不要换 Key，也不要计入渠道故障；
						// 多渠道可用时把原始错误交给外层继续选渠。
						lifecycle.finalizeNeutral()

						failoverState.lastError = fmt.Errorf("上游内容审核拒绝请求")
						failoverState.lastFailoverError = &FailoverError{Status: resp.StatusCode, Body: respBodyBytes}
						recordAttemptLog(c, logCtx, upstream, apiType, requestLogID, currentBaseURL, apiKey, "failed", resp.StatusCode, false, attemptStart, decision.errorType, string(respBodyBytes), true, isStream, nil)
						log.Printf("[%s-ContentPolicy] 渠道 %s 拒绝请求，跳过当前渠道", apiType, upstream.Name)
						// 内容审核拒绝：中性结束；重试决定由外层按协议的审核转移规则处理。
						return newUpstreamAttemptResult(false, "", 0, failoverState.lastFailoverError, nil, failoverState.lastError).withHealth(channelHealthNeutral)

					case upstreamFailureTryNextKey:
						failoverState.lastError = fmt.Errorf("上游错误: %d", resp.StatusCode)
						retryState.MarkKeyFailed(apiKey)
						cfgManager.MarkKeyAsFailed(apiKey, apiType)
						lifecycle.finalizeFailed()
						if a.MarkURLFailure != nil {
							a.MarkURLFailure(currentBaseURL)
						}
						log.Printf("[%s-Key] 警告: API密钥失败 (状态: %d)，尝试下一个密钥", apiType, resp.StatusCode)

						failoverState.lastFailoverError = &FailoverError{
							Status: resp.StatusCode,
							Body:   respBodyBytes,
						}

						if decision.quotaRelated {
							failoverState.deprioritizeCandidates[apiKey] = true
						}
						recordAttemptLog(c, logCtx, upstream, apiType, requestLogID, currentBaseURL, apiKey, "failed", resp.StatusCode, false, attemptStart, decision.errorType, string(respBodyBytes), true, isStream, nil)
						continue

					case upstreamFailureRespond:
						// 非 failover 错误，记录失败指标后返回（请求已处理）。
						// 注意：lifecycle 记的是 Key 级指标；渠道级健康结果为中性
						// （请求参数/尺寸等错误与渠道健康无关）。
						lifecycle.finalizeFailed()
						recordAttemptLog(c, logCtx, upstream, apiType, requestLogID, currentBaseURL, apiKey, "failed", resp.StatusCode, false, attemptStart, decision.errorType, string(respBodyBytes), false, isStream, nil)
						restoredBody, restoreErr := hooks.RestoreAttachedResponseBody(c, respBodyBytes)
						if restoreErr != nil {
							c.JSON(http.StatusInternalServerError, gin.H{"error": "敏感信息还原失败", "code": "CONTENT_SAFETY_HOOK_ERROR"})
							return newUpstreamAttemptResult(true, "", 0, nil, nil, restoreErr).withHealth(channelHealthNeutral)
						}
						c.Data(resp.StatusCode, "application/json", restoredBody)
						return newUpstreamAttemptResult(true, "", 0, nil, nil, nil).withHealth(channelHealthNeutral)
					}
				}
			}

			// 成功响应：处理 quota key 降级
			if a.DeprioritizeKey != nil && len(failoverState.deprioritizeCandidates) > 0 {
				for key := range failoverState.deprioritizeCandidates {
					a.DeprioritizeKey(key)
				}
			}

			// 2xx 错误信封检测（参照 cc-switch forwarder 的 buffer 校验）：部分
			// OpenAI 兼容网关用 HTTP 200 包错误 body 返回。非流式 JSON 响应先
			// 缓冲校验，命中错误信封按 Key 失败处理继续 failover，不再把错误
			// 当成功交给 HandleSuccess。流式响应交给 HandleSuccess（流式防御
			// 在 streams 层 prime-then-commit）。全量读取与 HandleSuccess 非流式
			// 路径的 io.ReadAll 开销相同；上限外的超大响应按读失败处理。
			if !isStream && looksLikeJSONResponse(resp) {
				var sniff []byte
				var readErr error
				sniffComplete := true
				if kind == scheduler.ChannelKindImages {
					sniff, sniffComplete, readErr = readImagesErrorEnvelopePrefix(resp)
				} else {
					sniff, readErr = readBodyWithLimit(resp.Body, maxEnvelopeSniffBytes)
				}
				if readErr != nil {
					// 响应体读取中断/超上限：连接已不可靠或响应异常巨大，按网络故障处理。
					// body 不会被 HandleSuccess 接手，必须显式关闭，否则连接不归还连接池。
					resp.Body.Close()
					if cancelErr := c.Request.Context().Err(); cancelErr != nil {
						lifecycle.finalizeClientCancelled()
						recordAttemptLog(c, logCtx, upstream, apiType, requestLogID, currentBaseURL, apiKey, "cancelled", resp.StatusCode, false, attemptStart, "client_cancelled", cancelErr.Error(), false, isStream, nil)
						return newUpstreamAttemptResult(true, "", 0, nil, nil, cancelErr).withHealth(channelHealthNeutral)
					}
					failoverState.lastError = readErr
					failureBody, _ := json.Marshal(gin.H{"error": "读取上游响应失败"})
					failoverState.lastFailoverError = &FailoverError{Status: http.StatusBadGateway, Body: failureBody}
					retryState.MarkKeyFailed(apiKey)
					cfgManager.MarkKeyAsFailed(apiKey, apiType)
					lifecycle.finalizeFailed()
					if a.MarkURLFailure != nil {
						a.MarkURLFailure(currentBaseURL)
					}
					recordAttemptLog(c, logCtx, upstream, apiType, requestLogID, currentBaseURL, apiKey, "failed", resp.StatusCode, false, attemptStart, "read_body", readErr.Error(), true, isStream, nil)
					log.Printf("[%s-Key] 警告: 读取上游响应体失败 (状态: %d)，尝试下一个密钥: %v", apiType, resp.StatusCode, readErr)
					continue
				}
				if sniffComplete && utils.IsUpstreamErrorEnvelope(sniff) {
					_ = resp.Body.Close()
					failoverState.lastError = fmt.Errorf("上游 2xx 响应携带错误信封")
					failoverState.lastFailoverError = &FailoverError{Status: http.StatusBadGateway, Body: sniff}
					retryState.MarkKeyFailed(apiKey)
					cfgManager.MarkKeyAsFailed(apiKey, apiType)
					lifecycle.finalizeFailed()
					if a.MarkURLFailure != nil {
						a.MarkURLFailure(currentBaseURL)
					}
					recordAttemptLog(c, logCtx, upstream, apiType, requestLogID, currentBaseURL, apiKey, "failed", http.StatusBadGateway, false, attemptStart, "error_envelope", string(sniff), true, isStream, nil)
					log.Printf("[%s-Envelope] 渠道 %s 返回 2xx 错误信封，尝试下一个密钥", apiType, upstream.Name)
					continue
				}
				// 非命中：body 原样放回，HandleSuccess 正常消费。
				if sniffComplete {
					_ = resp.Body.Close()
					resp.Body = io.NopCloser(bytes.NewReader(sniff))
				}
			}

			result, retry := candidateStep.deliverResponse(resp)
			if retry {
				continue
			}
			return result
		}

		// 当前 BaseURL 的所有 Key 都失败，记录并尝试下一个 BaseURL
		if envCfg.ShouldLog("info") && urlIdx < len(urlResults)-1 {
			log.Printf("[%s-BaseURL] BaseURL %d/%d 所有 Key 失败，切换到下一个 BaseURL", apiType, urlIdx+1, len(urlResults))
		}
	}

	if upstreamcontrol.IsLocalError(failoverState.lastError) {
		return localAdmissionResult(c, failoverState.lastError, cfgManager.GetFuzzyModeEnabled() && channelScheduler.GetActiveChannelCountForModel(kind, logCtx.Model) > 1)
	}
	// 候选耗尽的最终健康结果（R2-a）：发生过明确中性结束原因（容量、审核
	// 兼容重试失败）时按中性记账；否则为可重试真实故障耗尽候选。
	if failoverState.neutralTermination {
		return newUpstreamAttemptResult(false, "", 0, failoverState.lastFailoverError, nil, failoverState.lastError).withHealth(channelHealthNeutral)
	}
	return newUpstreamAttemptResult(false, "", 0, failoverState.lastFailoverError, nil, failoverState.lastError).withHealth(channelHealthFailure)
}
