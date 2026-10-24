// 本文件是单渠道内"Key/BaseURL 层"故障转移的主循环：按调用方给定的 URLResults 顺序
// （对话粘性会把偏好 BaseURL 提到最前）遍历 BaseURL，每个 BaseURL 内轮换 Key，处理熔断跳过、
// 全 Key 熔断时的强制探测、同候选重试、请求准备（内容安全 + 视觉层）失败与成功响应写回。
// 上层的模型映射循环在 upstream_failover.go，失败该怎么办的分类在 upstream_attempt_error.go。
package proxycore

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/BenedictKing/api-proxy/internal/config"
	"github.com/BenedictKing/api-proxy/internal/handlers/hooks"
	"github.com/BenedictKing/api-proxy/internal/mediasanitizer"
	"github.com/BenedictKing/api-proxy/internal/proxy"
	"github.com/BenedictKing/api-proxy/internal/ratelimit"
	"github.com/BenedictKing/api-proxy/internal/rectifier"
	"github.com/BenedictKing/api-proxy/internal/scheduler"
	"github.com/BenedictKing/api-proxy/internal/types"
	"github.com/BenedictKing/api-proxy/internal/utils"
	"github.com/BenedictKing/api-proxy/internal/visionlayer"
	"github.com/gin-gonic/gin"
)

type upstreamAttemptPreflight struct {
	proxyURL string
}

// maxEnvelopeSniffBytes 是 2xx 错误信封检测的最大读取字节数。错误信封必然
// 很小；正常非流式响应通常也在该上限内（HandleSuccess 本就全量读）；超过
// 上限的巨型响应按读失败处理，走 failover 让其他候选接手。
const maxEnvelopeSniffBytes = 10 * 1024 * 1024

// readBodyWithLimit 读取响应体，超过 limit 返回错误（与静默截断相比，
// 显式失败能触发 failover 而不是把半个 JSON 交给下游解析）。
func readBodyWithLimit(body io.Reader, limit int64) ([]byte, error) {
	limited, err := io.ReadAll(io.LimitReader(body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("读取上游响应体失败: %w", err)
	}
	if int64(len(limited)) > limit {
		return nil, fmt.Errorf("上游响应体超过 %d MB 上限", limit/1024/1024)
	}
	return limited, nil
}

// looksLikeJSONResponse 判断上游响应是否声称 JSON 载荷。错误信封只可能是
// JSON；SSE / 图片 / 其他二进制响应直接跳过检测。
func looksLikeJSONResponse(resp *http.Response) bool {
	if resp == nil {
		return false
	}
	contentType := resp.Header.Get("Content-Type")
	if contentType == "" {
		return true // 无 Content-Type 时保守检测（body 谓词本身会做 JSON 校验）
	}
	mediaType := contentType
	if idx := strings.Index(contentType, ";"); idx >= 0 {
		mediaType = contentType[:idx]
	}
	mediaType = strings.TrimSpace(strings.ToLower(mediaType))
	return mediaType == "application/json" || strings.HasSuffix(mediaType, "+json")
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

	var usage *types.Usage

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
			RestoreRequestBody(c, requestBody)
			ResetRequestLogFirstToken(c)
			attemptStart := time.Now()

			apiKey, err := a.NextAPIKey(upstream, retryState.FailedKeys())
			if err != nil {
				failoverState.lastError = err
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
			if err != nil && prepareStage == "build_request" {
				failoverState.lastError = err
				retryState.MarkKeyFailed(apiKey)
				channelScheduler.RecordFailure(currentBaseURL, apiKey, logCtx.ChannelIndex, kind)
				recordAttemptLog(c, logCtx, upstream, apiType, requestLogID, currentBaseURL, apiKey, "failed", 0, false, attemptStart, "build_request", err.Error(), true, isStream, nil)
				continue
			}
			if err != nil {
				_ = req.Body.Close()
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
				if channelScheduler.GetActiveChannelCountForModel(kind, logCtx.Model) > 1 {
					body, _ := json.Marshal(payload)
					// 视觉层本地失败：中性结束，交给外层换渠道。
					return newUpstreamAttemptResult(false, "", 0, &FailoverError{Status: status, Body: body}, nil, err).withHealth(channelHealthNeutral)
				}
				c.JSON(status, payload)
				return newUpstreamAttemptResult(true, "", 0, nil, nil, err).withHealth(channelHealthNeutral)
			}
			// 请求准备成功后再启动请求画像和活跃度观测。
			lifecycle := a.startAttemptLifecycle(currentBaseURL, apiKey)
			finishRetryContentSafety := func(preparationErr error) {
				status := handleContentSafetyPreparationError(c, apiType, preparationErr)
				lifecycle.finalizeNeutral()
				attemptStatus := "failed"
				if status == http.StatusForbidden {
					attemptStatus = "blocked"
				}
				recordAttemptLog(c, logCtx, upstream, apiType, requestLogID, currentBaseURL, apiKey, attemptStatus, status, false, attemptStart, "content_safety", preparationErr.Error(), false, isStream, nil)
			}

			// 429 平滑排队：若当前 BaseURL 处于瞬时冷却期，在 context 允许范围内轻量排队
			ratelimit.GetDefaultLimiter().WaitOrAcquire(c.Request.Context(), currentBaseURL, 0, 0, 0, 2*time.Second)

			req = AttachRequestLogID(c, req)
			resp, err := SendRequest(req, upstream, envCfg, isStream, apiType, proxyURL, a.Permit, a.RequestTimeout)
			if err != nil {
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
			if pm := channelScheduler.GetProfileManager(); pm != nil {
				pm.RecordFirstByte(lifecycle.observation.ProfileRequestID)
			}

			if resp.StatusCode < 200 || resp.StatusCode >= 300 {
				respBodyBytes, readErr := io.ReadAll(resp.Body)
				resp.Body.Close()
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
					recordAttemptLog(c, logCtx, upstream, apiType, requestLogID, currentBaseURL, apiKey, "failed", resp.StatusCode, false, attemptStart, "read_body", readErr.Error(), true, isStream, nil)
					log.Printf("[%s-Key] 警告: 读取上游错误响应体失败 (状态: %d)，尝试下一个密钥: %v", apiType, resp.StatusCode, readErr)
					continue
				}
				respBodyBytes = utils.DecompressGzipIfNeeded(resp, respBodyBytes)
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

				// 处理 429 速率限制（Rate Limit / TPM / RPM 瞬时冲顶）：
				// 记录短暂冷却，并在同一候选上进行轻量毫秒级退避排队重试 1 次，避免瞬时并发将所有 key 击穿。
				if resp.StatusCode == http.StatusTooManyRequests && !utils.IsNonRetryableUpstreamErrorBody(respBodyBytes) {
					ratelimit.GetDefaultLimiter().Record429(currentBaseURL, 600*time.Millisecond)
					candidate := currentBaseURL + "\x00" + apiKey
					if retryState.CanRetryCandidate(candidate, c.Request.Context()) {
						retryCount := retryState.RecordCandidateRetry(candidate)
						log.Printf("[%s-RateLimit] 渠道 %s 遭遇速率限制 (429)，排队退避 500ms 后原位重试 (%d/1)", apiType, upstream.Name, retryCount)
						select {
						case <-c.Request.Context().Done():
							lifecycle.finalizeClientCancelled()
							recordAttemptLog(c, logCtx, upstream, apiType, requestLogID, currentBaseURL, apiKey, "cancelled", 0, false, attemptStart, "client_cancelled", c.Request.Context().Err().Error(), false, isStream, nil)
							// 退避排队中客户端取消：中性结束，立即停止。
							return newUpstreamAttemptResult(true, "", 0, nil, nil, c.Request.Context().Err()).withHealth(channelHealthNeutral)
						case <-time.After(500 * time.Millisecond):
						}
						RestoreRequestBody(c, requestBody)
						continue
					}
				}

				// 兼容部分严格的 OpenAI 协议网关：首次明确拒绝 prompt_cache_key 时，
				// 使用同一渠道、BaseURL 和 API key 移除该字段后重试一次，并记住能力。
				if !upstreamCopy.DisablePromptCacheKey && IsPromptCacheKeyUnsupported(resp.StatusCode, respBodyBytes) {
					log.Printf("[%s-ChannelCapability] 渠道 %s 不支持 prompt_cache_key，移除后使用同一 key 重试一次", apiType, upstream.Name)
					capabilities.disablePromptCacheKey = true
					upstreamCopy.DisablePromptCacheKey = true
					if err := cfgManager.MarkPromptCacheKeyUnsupported(string(kind), upstream.ID); err != nil {
						log.Printf("[%s-ChannelCapability] 持久化 prompt_cache_key 能力失败: %v", apiType, err)
					}

					RestoreRequestBody(c, requestBody)
					retryResult := a.retrySameCandidateRequest(upstreamCopy, apiKey, proxyURL)
					var aborted *UpstreamAttemptResult
					resp, respBodyBytes, retrySucceeded, aborted = applyCompatibilityRetryResult(retryResult, resp, respBodyBytes, retrySucceeded, apiType, compatRetryLogs{
						component:     "ChannelCapability",
						buildAction:   "重建无 prompt_cache_key 请求失败",
						sendAction:    "无 prompt_cache_key 重试失败",
						prepareAction: "准备无 prompt_cache_key 请求失败",
					}, finishRetryContentSafety)
					if aborted != nil {
						return *aborted
					}
				}

				// 部分 DeepSeek 兼容渠道会在 thinking 模式下要求每条 assistant
				// 历史都带 reasoning_content。Cursor 等客户端可能已将该字段
				// 隐藏；首次收到明确 400 后，用同一渠道立即强制补齐并重试，
				// 同时持久化能力，避免之后的每轮请求都先失败一次。
				if !retrySucceeded && kind == scheduler.ChannelKindMessages && upstreamCopy.ServiceType == "openai" && !upstreamCopy.RequireReasoningContent &&
					IsReasoningContentRequired(resp.StatusCode, respBodyBytes) {
					log.Printf("[%s-ChannelCapability] 渠道 %s 要求完整 reasoning_content，补齐后使用同一 key 重试一次", apiType, upstream.Name)
					capabilities.requireReasoningContent = true
					upstreamCopy.RequireReasoningContent = true
					if err := cfgManager.MarkReasoningContentRequired(string(kind), upstream.ID); err != nil {
						log.Printf("[%s-ChannelCapability] 持久化 reasoning_content 能力失败: %v", apiType, err)
					}

					RestoreRequestBody(c, requestBody)
					retryResult := a.retrySameCandidateRequest(upstreamCopy, apiKey, proxyURL)
					var aborted *UpstreamAttemptResult
					resp, respBodyBytes, retrySucceeded, aborted = applyCompatibilityRetryResult(retryResult, resp, respBodyBytes, retrySucceeded, apiType, compatRetryLogs{
						component:     "ChannelCapability",
						buildAction:   "重建 reasoning_content 兼容请求失败",
						sendAction:    "reasoning_content 兼容重试失败",
						prepareAction: "准备 reasoning_content 兼容请求失败",
					}, finishRetryContentSafety)
					if aborted != nil {
						return *aborted
					}
				}

				// 某些 Responses 中转会扫描原始 JSON 字节，中文业务文本或历史中的审核错误码
				// 可能被稳定误判。保持 JSON 解码值不变，改用 Unicode 转义后重试一次。
				if !retrySucceeded && kind == scheduler.ChannelKindResponses && upstreamCopy.ServiceType == "responses" && utils.IsContentPolicyErrorBody(respBodyBytes) {
					escapedBody, changed, escapeErr := buildContentPolicyCompatibilityBody(requestBody)
					if escapeErr != nil {
						log.Printf("[%s-ContentPolicy] 构建等价 JSON 转义请求失败: %v", apiType, escapeErr)
					} else if changed {
						log.Printf("[%s-ContentPolicy] 使用等价 JSON Unicode 转义在同一渠道重试一次", apiType)
						RestoreRequestBody(c, escapedBody)
						retryResult := a.retrySameCandidateRequest(upstreamCopy, apiKey, proxyURL)
						var aborted *UpstreamAttemptResult
						resp, respBodyBytes, retrySucceeded, aborted = applyCompatibilityRetryResult(retryResult, resp, respBodyBytes, retrySucceeded, apiType, compatRetryLogs{
							component:     "ContentPolicy",
							buildAction:   "重建转义请求失败",
							sendAction:    "转义请求重试失败",
							prepareAction: "准备转义请求失败",
							successAction: "等价 JSON 转义重试成功",
						}, finishRetryContentSafety)
						if aborted != nil {
							return *aborted
						}
						RestoreRequestBody(c, requestBody)
					}
				}

				// Thinking Signature 整流自愈：当上游报错提示 signature 校验失败或块结构不合规时，
				// 清理历史消息中的非法 signature 与 thinking 块，并在同一渠道就地重试一次。
				if !retrySucceeded && rectifier.ShouldRectifyThinkingSignature(respBodyBytes) {
					rectifiedBody, applied, stats := rectifier.RectifyThinkingSignature(requestBody)
					if applied {
						log.Printf("[%s-Rectifier] 触发 Thinking 签名整流重试 (移除了 %d 个 thinking 块, %d 个 redacted_thinking 块, %d 个 signature 字段, topLevelThinkingRemoved=%v)",
							apiType, stats.RemovedThinkingBlocks, stats.RemovedRedactedThinkingBlocks, stats.RemovedSignatureFields, stats.RemovedTopLevelThinking)
						RestoreRequestBody(c, rectifiedBody)
						retryResult := a.retrySameCandidateRequest(upstreamCopy, apiKey, proxyURL)
						var aborted *UpstreamAttemptResult
						resp, respBodyBytes, retrySucceeded, aborted = applyCompatibilityRetryResult(retryResult, resp, respBodyBytes, retrySucceeded, apiType, compatRetryLogs{
							component:     "Rectifier",
							buildAction:   "重建签名整流请求失败",
							sendAction:    "签名整流重试失败",
							prepareAction: "准备签名整流请求失败",
							successAction: "Thinking 签名整流重试成功",
						}, finishRetryContentSafety)
						if aborted != nil {
							return *aborted
						}
						RestoreRequestBody(c, requestBody)
					}
				}

				// Thinking Budget 参数整流自愈：当上游报错提示 budget_tokens 约束失败（如 < 1024 或 max_tokens <= budget_tokens）时，
				// 自动整流 budget_tokens=32000, max_tokens=64000，并在同一渠道就地重试一次。
				if !retrySucceeded && rectifier.ShouldRectifyThinkingBudget(respBodyBytes) {
					rectifiedBody, applied, stats := rectifier.RectifyThinkingBudget(requestBody)
					if applied {
						log.Printf("[%s-Rectifier] 触发 Thinking Budget 整流重试 (budget: %d -> %d, max_tokens: %d -> %d)",
							apiType, stats.PreviousBudgetTokens, stats.NewBudgetTokens, stats.PreviousMaxTokens, stats.NewMaxTokens)
						RestoreRequestBody(c, rectifiedBody)
						retryResult := a.retrySameCandidateRequest(upstreamCopy, apiKey, proxyURL)
						var aborted *UpstreamAttemptResult
						resp, respBodyBytes, retrySucceeded, aborted = applyCompatibilityRetryResult(retryResult, resp, respBodyBytes, retrySucceeded, apiType, compatRetryLogs{
							component:     "Rectifier",
							buildAction:   "重建 Budget 整流请求失败",
							sendAction:    "Budget 整流重试失败",
							prepareAction: "准备 Budget 整流请求失败",
							successAction: "Thinking Budget 整流重试成功",
						}, finishRetryContentSafety)
						if aborted != nil {
							return *aborted
						}
						RestoreRequestBody(c, requestBody)
					}
				}

				// 模态拒绝与纯文本降级自愈（参照 cc-switch media_sanitizer）：
				// 当上游返回 400/415/422/501 且明确表示不支持图片/多模态输入时，
				// 将请求体内的图片块替换为 "[Unsupported Image]" 纯文本占位符，并在同一渠道就地重试一次。
				if !retrySucceeded && mediasanitizer.IsUnsupportedImageError(resp.StatusCode, respBodyBytes) {
					sanitizedBody, modified, count := mediasanitizer.SanitizeImagesWithMarker(requestBody)
					if modified {
						log.Printf("[%s-MediaSanitizer] 捕获模型模态拒绝错误，已将 %d 处图片降级为纯文本占位符并在同一渠道重试", apiType, count)
						RestoreRequestBody(c, sanitizedBody)
						retryResult := a.retrySameCandidateRequest(upstreamCopy, apiKey, proxyURL)
						var aborted *UpstreamAttemptResult
						resp, respBodyBytes, retrySucceeded, aborted = applyCompatibilityRetryResult(retryResult, resp, respBodyBytes, retrySucceeded, apiType, compatRetryLogs{
							component:     "MediaSanitizer",
							buildAction:   "重建图片降级请求失败",
							sendAction:    "图片降级重试失败",
							prepareAction: "准备图片降级请求失败",
							successAction: "图片降级重试成功",
						}, finishRetryContentSafety)
						if aborted != nil {
							return *aborted
						}
						RestoreRequestBody(c, requestBody)
					}
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
				sniff, readErr := readBodyWithLimit(resp.Body, maxEnvelopeSniffBytes)
				if readErr != nil {
					// 响应体读取中断/超上限：连接已不可靠或响应异常巨大，按网络故障处理。
					// body 不会被 HandleSuccess 接手，必须显式关闭，否则连接不归还连接池。
					resp.Body.Close()
					failoverState.lastError = readErr
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
				if utils.IsUpstreamErrorEnvelope(sniff) {
					failoverState.lastError = fmt.Errorf("上游 2xx 响应携带错误信封")
					failoverState.lastFailoverError = &FailoverError{Status: resp.StatusCode, Body: sniff}
					retryState.MarkKeyFailed(apiKey)
					cfgManager.MarkKeyAsFailed(apiKey, apiType)
					lifecycle.finalizeFailed()
					if a.MarkURLFailure != nil {
						a.MarkURLFailure(currentBaseURL)
					}
					recordAttemptLog(c, logCtx, upstream, apiType, requestLogID, currentBaseURL, apiKey, "failed", resp.StatusCode, false, attemptStart, "error_envelope", string(sniff), true, isStream, nil)
					log.Printf("[%s-Envelope] 渠道 %s 返回 2xx 错误信封，尝试下一个密钥", apiType, upstream.Name)
					continue
				}
				// 非命中：body 原样放回，HandleSuccess 正常消费。
				resp.Body = io.NopCloser(bytes.NewReader(sniff))
			}

			usage, err = a.HandleSuccess(c, resp, upstreamCopy, apiKey)
			if err != nil {
				failoverState.lastError = err
				decision := classifyResponseProcessingError(err, c.Writer.Written())
				// 本渠道最终健康结果：除"响应已提交后的真实上游中断"按失败
				// 记账外，取消/内容安全/本地检查错误均为中性（R2-a 表）。
				health := channelHealthNeutral
				switch decision.action {
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
						continue
					}

					// 仅在本次请求内排除该 Key，继续其他候选；不写入 Key 熔断和失败率。
					retryState.MarkKeyFailed(apiKey)
					failoverState.lastFailoverError = &FailoverError{Status: http.StatusServiceUnavailable, Body: []byte(err.Error())}
					continue

				case responseProcessingChannelFailure:
					// 真实渠道故障：计入失败指标
					cfgManager.MarkKeyAsFailed(apiKey, apiType)
					lifecycle.finalizeFailed()
					shouldRetryResponseProcessing := !c.Writer.Written()
					recordAttemptLog(c, logCtx, upstream, apiType, requestLogID, currentBaseURL, apiKey, "failed", resp.StatusCode, false, attemptStart, "response_processing", err.Error(), shouldRetryResponseProcessing, isStream, usage)
					log.Printf("[%s-Key] 警告: 响应处理失败: %s", apiType, utils.RedactSensitivePlaceholdersForLog(err.Error()))
					if shouldRetryResponseProcessing {
						retryState.MarkKeyFailed(apiKey)
						if a.MarkURLFailure != nil {
							a.MarkURLFailure(currentBaseURL)
						}
						failoverState.lastFailoverError = &FailoverError{
							Status: resp.StatusCode,
							Body:   []byte(err.Error()),
						}
						continue
					}
					// 响应已提交后的真实上游中断：渠道按失败记账，但不再故障转移。
					health = channelHealthFailure
				}
				return newUpstreamAttemptResult(true, "", 0, nil, usage, err).withHealth(health)
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
			return newUpstreamAttemptResult(true, apiKey, originalIdx, nil, usage, nil).withHealth(channelHealthSuccess)
		}

		// 当前 BaseURL 的所有 Key 都失败，记录并尝试下一个 BaseURL
		if envCfg.ShouldLog("info") && urlIdx < len(urlResults)-1 {
			log.Printf("[%s-BaseURL] BaseURL %d/%d 所有 Key 失败，切换到下一个 BaseURL", apiType, urlIdx+1, len(urlResults))
		}
	}

	// 候选耗尽的最终健康结果（R2-a）：发生过明确中性结束原因（容量、审核
	// 兼容重试失败）时按中性记账；否则为可重试真实故障耗尽候选。
	if failoverState.neutralTermination {
		return newUpstreamAttemptResult(false, "", 0, failoverState.lastFailoverError, nil, failoverState.lastError).withHealth(channelHealthNeutral)
	}
	return newUpstreamAttemptResult(false, "", 0, failoverState.lastFailoverError, nil, failoverState.lastError).withHealth(channelHealthFailure)
}

func (a UpstreamAttempt) buildPreparedAttemptRequest(
	upstreamCopy *config.UpstreamConfig,
	apiKey string,
) (*http.Request, string, error) {
	req, err := a.BuildRequest(a.Context, upstreamCopy, apiKey)
	if err != nil {
		return nil, "build_request", err
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
	resp, err := SendRequest(req, upstreamCopy, a.EnvConfig, a.IsStream, a.APIType, proxyURL, a.Permit, a.RequestTimeout)
	if err != nil {
		return compatibilityRetryResult{PreparationStage: "send_request", Err: err}
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return compatibilityRetryResult{Response: resp, Succeeded: true}
	}

	body, readErr := io.ReadAll(resp.Body)
	resp.Body.Close()
	if readErr != nil {
		// 读取中断：响应不完整，按网络故障处理，不返回截断 body。
		return compatibilityRetryResult{PreparationStage: "read_body", Err: fmt.Errorf("读取重试响应体失败: %w", readErr)}
	}
	body = utils.DecompressGzipIfNeeded(resp, body)
	return compatibilityRetryResult{Response: resp, Body: body}
}

// compatRetryLogs 描述同候选兼容性重试各失败阶段的日志文案，
// 保证收敛后日志逐字与原实现一致。
type compatRetryLogs struct {
	component     string // 日志组件名，如 "ChannelCapability"
	buildAction   string // build_request 阶段失败文案
	sendAction    string // send_request 阶段失败文案
	prepareAction string // 其余准备阶段失败文案
	successAction string // 重试成功文案；为空则不打成功日志
}

// applyCompatibilityRetryResult 归一化三种同候选兼容性重试
// （prompt_cache_key / reasoning_content / 内容审核转义）的结果处理：
//   - content_safety 阶段失败：写协议错误响应并要求主循环终止本次尝试；
//   - 其余阶段失败：仅记日志，沿用原响应继续走失败分类；
//   - 重试成功：回传新响应并标记 retrySucceeded。
//
// abort 非 nil 时主循环必须立即返回该结果。
func applyCompatibilityRetryResult(
	retryResult compatibilityRetryResult,
	resp *http.Response,
	respBodyBytes []byte,
	retrySucceeded bool,
	apiType string,
	logs compatRetryLogs,
	finishRetryContentSafety func(error),
) (*http.Response, []byte, bool, *UpstreamAttemptResult) {
	if retryResult.Err != nil {
		if retryResult.PreparationStage == "content_safety" {
			finishRetryContentSafety(retryResult.Err)
			abort := newUpstreamAttemptResult(true, "", 0, nil, nil, retryResult.Err)
			return resp, respBodyBytes, retrySucceeded, &abort
		}
		switch retryResult.PreparationStage {
		case "build_request":
			log.Printf("[%s-%s] %s: %v", apiType, logs.component, logs.buildAction, retryResult.Err)
		case "send_request":
			log.Printf("[%s-%s] %s: %v", apiType, logs.component, logs.sendAction, retryResult.Err)
		default:
			log.Printf("[%s-%s] %s: %v", apiType, logs.component, logs.prepareAction, retryResult.Err)
		}
		return resp, respBodyBytes, retrySucceeded, nil
	}

	resp = retryResult.Response
	if retryResult.Succeeded {
		retrySucceeded = true
		if logs.successAction != "" {
			log.Printf("[%s-%s] %s", apiType, logs.component, logs.successAction)
		}
	} else {
		respBodyBytes = retryResult.Body
	}
	return resp, respBodyBytes, retrySucceeded, nil
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
