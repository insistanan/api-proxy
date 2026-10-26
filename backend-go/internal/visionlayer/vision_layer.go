// Package visionlayer 为不支持图片的渠道提供受控的图片理解层。
package visionlayer

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/BenedictKing/api-proxy/internal/config"
	"github.com/BenedictKing/api-proxy/internal/httpclient"
	"github.com/BenedictKing/api-proxy/internal/metrics"
	"github.com/BenedictKing/api-proxy/internal/providers"
	"github.com/BenedictKing/api-proxy/internal/scheduler"
	"github.com/BenedictKing/api-proxy/internal/types"
	"github.com/BenedictKing/api-proxy/internal/utils"
	"github.com/gin-gonic/gin"
)

// PrepareRequest 在 Provider 完成协议转换后处理最终上游请求。
// 原始客户端请求保持不变；纯文本上游只接收带位置标识的图片理解文本。
func PrepareRequest(
	c *gin.Context,
	envCfg *config.EnvConfig,
	cfgManager *config.ConfigManager,
	channelScheduler *scheduler.ChannelScheduler,
	kind scheduler.ChannelKind,
	targetUpstream *config.UpstreamConfig,
	requestedModel string,
	conversationID string,
	request *http.Request,
) error {
	if kind == scheduler.ChannelKindImages {
		// Images 入口里的图片是生图/图生图素材，不是对话里需要理解的内容。
		return nil
	}
	if request == nil || request.Body == nil || targetUpstream == nil || targetUpstream.VisionCapable {
		return nil
	}
	body, err := io.ReadAll(request.Body)
	if err != nil {
		return wrapRequestError(http.StatusInternalServerError, "VISION_LAYER_INTERNAL_ERROR", fmt.Errorf("读取最终上游请求失败: %w", err))
	}
	request.Body = io.NopCloser(bytes.NewReader(body))

	var payload interface{}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil {
		// multipart 等非 JSON 请求不属于对话图片理解层。
		return nil
	}
	images, err := collectImagesForUpstream(payload, targetUpstream.ServiceType)
	if err != nil {
		if !utils.ValueHasVisionContent(payload) {
			return nil
		}
		return err
	}
	if len(images) == 0 {
		return nil
	}
	visionChannelID, err := preferredVisionChannelID(targetUpstream)
	if err != nil {
		return err
	}
	visionModelInput := strings.TrimSpace(targetUpstream.VisionLayerModel)
	if visionModelInput == "" {
		visionModelInput = strings.TrimSpace(requestedModel)
	}
	if visionModelInput == "" {
		return fmt.Errorf("渠道 %q 未提供可供图片理解渠道解析的模型名", targetUpstream.Name)
	}
	profile := resolveVisionAnalysisProfile(extractUserText(payload))

	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" {
		return wrapRequestError(http.StatusInternalServerError, "VISION_LAYER_INTERNAL_ERROR", fmt.Errorf("图片理解层缺少对话标识"))
	}

	// 查询对话中已记录的图片指纹。如果某图片指纹已在对话记录中但缓存未命中，
	// 说明之前解析失败了。对这类图片采用非阻塞模式：失败时用占位文本替代，
	// 不中断用户的对话请求。
	knownFingerprints := loadKnownImageFingerprints(channelScheduler, conversationID)

	descriptions := make(map[string]string, len(images))
	pendingImages := make([]visionImage, 0, len(images))
	waitingImages := make([]visionWaiter, 0, len(images))
	pendingFingerprints := make(map[string]struct{}, len(images))
	for _, image := range images {
		fingerprint := utils.ImageFingerprintForBlock(image)
		if fingerprint == "" {
			return fmt.Errorf("无法为图片理解层中的图片生成指纹")
		}
		if _, exists := descriptions[fingerprint]; exists {
			continue
		}
		if _, exists := pendingFingerprints[fingerprint]; exists {
			continue
		}

		cacheKey := buildImageCacheKey(fingerprint, kind, visionModelInput)
		result, ok, err := loadCache(channelScheduler, conversationID, cacheKey)
		if err != nil {
			return wrapRequestError(http.StatusInternalServerError, "VISION_LAYER_CACHE_ERROR", fmt.Errorf("读取图片理解缓存失败: %w", err))
		}
		if ok {
			descriptions[fingerprint] = result
			continue
		}

		memoryKey := memoryCacheKey(conversationID, cacheKey)
		cached, cachedOK, call, owner := claimAnalysis(memoryKey)
		if cachedOK {
			descriptions[fingerprint] = cached
			continue
		}
		pendingFingerprints[fingerprint] = struct{}{}
		if !owner {
			waitingImages = append(waitingImages, visionWaiter{fingerprint: fingerprint, call: call})
			continue
		}
		pendingImages = append(pendingImages, visionImage{
			id:          fmt.Sprintf("image_%d", len(pendingImages)+1),
			fingerprint: fingerprint,
			block:       image,
			cacheKey:    cacheKey,
			memoryKey:   memoryKey,
			call:        call,
			nonBlocking: knownFingerprints[fingerprint],
		})
	}

	for start := 0; start < len(pendingImages); start += maxVisionBatchImages {
		end := start + maxVisionBatchImages
		if end > len(pendingImages) {
			end = len(pendingImages)
		}
		batch := pendingImages[start:end]
		batchResult, err := describeImages(c, envCfg, cfgManager, channelScheduler, kind, targetUpstream.PoolID, visionChannelID, visionModelInput, profile, batch)
		if err != nil {
			// 检查这批图片是否全部为非阻塞模式（已在对话中出现过但缓存未命中）。
			// 如果是，用占位文本替代而非中断请求——用户不应因之前已解析失败的图片
			// 而在后续纯文本消息中反复被阻塞。
			allNonBlocking := true
			for _, img := range batch {
				if !img.nonBlocking {
					allNonBlocking = false
					break
				}
			}
			if allNonBlocking {
				for _, img := range batch {
					placeholder := "[图片理解暂时不可用，请基于上下文继续对话]"
					descriptions[img.fingerprint] = placeholder
					finishAnalysis(img.memoryKey, img.call, placeholder, nil)
				}
				continue
			}
			err = wrapRequestError(http.StatusBadGateway, "VISION_LAYER_UPSTREAM_ERROR", err)
			failPendingAnalyses(pendingImages, err)
			return err
		}
		for _, image := range batch {
			result, ok := batchResult[image.id]
			if !ok {
				err = wrapRequestError(http.StatusBadGateway, "VISION_LAYER_INVALID_RESPONSE", fmt.Errorf("图片理解结果缺少编号 %s", image.id))
				failPendingAnalyses(pendingImages, err)
				return err
			}
			if err := storeCache(channelScheduler, conversationID, image.cacheKey, result); err != nil {
				err = wrapRequestError(http.StatusInternalServerError, "VISION_LAYER_CACHE_ERROR", fmt.Errorf("保存图片理解缓存失败: %w", err))
				failPendingAnalyses(pendingImages, err)
				return err
			}
			finishAnalysis(image.memoryKey, image.call, result, nil)
			descriptions[image.fingerprint] = result
		}
	}

	for _, waiter := range waitingImages {
		select {
		case <-waiter.call.done:
			if waiter.call.err != nil {
				return waiter.call.err
			}
			descriptions[waiter.fingerprint] = waiter.call.result
		case <-c.Request.Context().Done():
			return c.Request.Context().Err()
		}
	}

	transformed, err := transformImagesForUpstream(payload, targetUpstream.ServiceType, descriptions)
	if err != nil {
		return wrapRequestError(http.StatusInternalServerError, "VISION_LAYER_INTERNAL_ERROR", err)
	}
	transformedBody, err := utils.MarshalJSONNoEscape(transformed)
	if err != nil {
		return wrapRequestError(http.StatusInternalServerError, "VISION_LAYER_INTERNAL_ERROR", fmt.Errorf("序列化图片理解层请求失败: %w", err))
	}
	request.Body = io.NopCloser(bytes.NewReader(transformedBody))
	request.ContentLength = int64(len(transformedBody))
	request.Header.Set("Content-Type", "application/json")
	return nil
}

func preferredVisionChannelID(targetUpstream *config.UpstreamConfig) (string, error) {
	if targetUpstream == nil || !targetUpstream.VisionLayerEnabled {
		return "", nil
	}
	channelID := strings.TrimSpace(targetUpstream.VisionLayerChannelID)
	if channelID == "" {
		return "", fmt.Errorf("渠道 %q 已启用图片理解层，但未选择图片理解渠道", targetUpstream.Name)
	}
	return channelID, nil
}

func describeImages(
	c *gin.Context,
	envCfg *config.EnvConfig,
	cfgManager *config.ConfigManager,
	channelScheduler *scheduler.ChannelScheduler,
	kind scheduler.ChannelKind,
	targetPoolID string,
	visionChannelID string,
	visionModelInput string,
	profile visionAnalysisProfile,
	images []visionImage,
) (map[string]string, error) {
	var lastErr error
	requestContext := c.Request.Context()
	if err := requestContext.Err(); err != nil {
		return nil, err
	}

	// 显式配置的图片理解渠道始终优先；默认模式直接从公共池开始。
	if visionChannelID != "" {
		selection, err := channelScheduler.SelectVisionChannel(requestContext, kind, visionChannelID, targetPoolID)
		if err == nil {
			result, failCount, attemptErr := describeImagesOnChannel(c, envCfg, cfgManager, channelScheduler, kind, selection, visionModelInput, profile, images, 2)
			if selection.Permit != nil {
				// 图片理解旁路结束：中性释放（旁路结果不计入目标渠道健康度）。
				selection.Permit.Release()
				selection.Permit = nil
			}
			if attemptErr == nil {
				return result, nil
			}
			if failCount < 2 {
				// 不可重试错误不应继续轮询其他渠道。
				return nil, attemptErr
			}
			lastErr = attemptErr
		} else {
			lastErr = err
		}
	}

	// 公共池优先；全部可重试失败后，再尝试文本渠道所在分组。
	stages := []func() []*scheduler.SelectionResult{
		func() []*scheduler.SelectionResult {
			return channelScheduler.ListPublicVisionChannels(requestContext, kind, visionChannelID)
		},
		func() []*scheduler.SelectionResult {
			return channelScheduler.ListPoolVisionChannels(requestContext, kind, visionChannelID, targetPoolID)
		},
	}
	for _, loadCandidates := range stages {
		if err := requestContext.Err(); err != nil {
			return nil, err
		}
		candidates := loadCandidates()
		for _, candidate := range candidates {
			if candidate == nil || candidate.Upstream == nil {
				continue
			}
			// 列表接口只负责筛选候选；真正开始尝试时才预留 in-flight，
			// 避免把尚未尝试的后备渠道也算入负载，并让并发请求看到当前尝试。
			candidate.Kind = kind
			channelScheduler.ReserveChannel(kind, candidate.ChannelIndex)
			candidate.Permit = channelScheduler.NewReservationPermit(kind, candidate.ChannelIndex)
			result, failCount, err := describeImagesOnChannel(c, envCfg, cfgManager, channelScheduler, kind, candidate, visionModelInput, profile, images, 2)
			if candidate.Permit != nil {
				// 图片理解旁路结束：中性释放（旁路结果不计入渠道健康度）。
				candidate.Permit.Release()
				candidate.Permit = nil
			}
			if err == nil {
				return result, nil
			}
			lastErr = err
			if failCount < 2 {
				return nil, err
			}
		}
	}

	if lastErr == nil {
		return nil, fmt.Errorf("公共图片理解池和当前分组中没有可用的图片理解渠道")
	}
	return nil, fmt.Errorf("所有可用图片理解渠道均失败: %w", lastErr)
}

// describeImagesOnChannel 在指定的图片理解渠道上尝试解析图片。
// maxFailures 限制单个渠道上的最大失败次数（URL×Key 组合维度），达到阈值后不再继续。
// 返回 (结果, 失败次数, 错误)。
func describeImagesOnChannel(
	c *gin.Context,
	envCfg *config.EnvConfig,
	cfgManager *config.ConfigManager,
	channelScheduler *scheduler.ChannelScheduler,
	kind scheduler.ChannelKind,
	selection *scheduler.SelectionResult,
	visionModelInput string,
	profile visionAnalysisProfile,
	images []visionImage,
	maxFailures int,
) (map[string]string, int, error) {
	upstream := selection.Upstream
	provider := providers.GetProvider(upstream.ServiceType)
	if provider == nil {
		return nil, 0, fmt.Errorf("图片理解渠道 %q 的服务类型 %q 不受支持", upstream.Name, upstream.ServiceType)
	}
	// 图片理解模型必须按图片理解渠道自身的配置解析。文字渠道的协议、模型映射
	// 和默认模型不应影响这里；解析后清空映射，防止 Provider 再次重定向模型。
	visionModel := config.ResolveUpstreamModel(visionModelInput, upstream)
	if visionModel == "" {
		return nil, 0, fmt.Errorf("图片理解渠道 %q 未能解析可用模型", upstream.Name)
	}
	visionUpstream := upstream.Clone()
	visionUpstream.DefaultModel = visionModel
	visionUpstream.ModelMapping = nil

	visionBody, err := buildVisionRequest(visionModel, profile, images)
	if err != nil {
		return nil, 0, err
	}

	visionRequestID := fmt.Sprintf("vision-%d", atomic.AddUint64(&visionCallCounter, 1))
	failedKeys := make(map[string]bool)
	urlResults := channelScheduler.GetSortedURLsForChannel(kind, selection.ChannelIndex, upstream.GetAllBaseURLs())
	var lastErr error
	failCount := 0

	for _, urlResult := range urlResults {
		baseURL := urlResult.URL
		for attempt := 0; attempt < len(upstream.APIKeys); attempt++ {
			apiKey, keyErr := cfgManager.GetNextAPIKey(upstream, failedKeys, "VisionLayer")
			if keyErr != nil {
				lastErr = keyErr
				break
			}
			if channelScheduler.ShouldSuspendKey(baseURL, apiKey, selection.ChannelIndex, kind) {
				failedKeys[apiKey] = true
				continue
			}

			attemptStart := time.Now()
			upstreamCopy := visionUpstream.Clone()
			upstreamCopy.BaseURL = baseURL
			request, requestErr := buildProviderRequest(c, provider, upstreamCopy, apiKey, visionBody, images)
			if requestErr != nil {
				lastErr = requestErr
				failedKeys[apiKey] = true
				failCount++
				recordVisionAttempt(channelScheduler, kind, selection, upstream, visionRequestID, visionModel, baseURL, apiKey, "failed", 0, false, attemptStart, "build_request", requestErr.Error(), attempt > 0, nil)
				if failCount >= maxFailures {
					return nil, failCount, lastErr
				}
				continue
			}

			timeout := time.Duration(envCfg.RequestTimeout) * time.Millisecond
			client, clientErr := httpclient.GetManager().GetStandardClientForUpstream(timeout, cfgManager, upstream)
			if clientErr != nil {
				_ = request.Body.Close()
				return nil, failCount, clientErr
			}
			channelScheduler.RecordRequestStart(baseURL, apiKey, selection.ChannelIndex, kind)
			metricsRequestID := channelScheduler.RecordRequestConnected(baseURL, apiKey, visionModel, selection.ChannelIndex, kind)
			response, requestErr := client.Do(request)
			if requestErr != nil {
				channelScheduler.RecordRequestFinalizeFailure(baseURL, apiKey, selection.ChannelIndex, metricsRequestID, kind)
				channelScheduler.RecordRequestEnd(baseURL, apiKey, selection.ChannelIndex, kind)
				channelScheduler.MarkURLFailure(kind, selection.ChannelIndex, baseURL)
				cfgManager.MarkKeyAsFailed(apiKey, "VisionLayer")
				failedKeys[apiKey] = true
				lastErr = requestErr
				failCount++
				recordVisionAttempt(channelScheduler, kind, selection, upstream, visionRequestID, visionModel, baseURL, apiKey, "failed", 0, false, attemptStart, "network", requestErr.Error(), attempt > 0, nil)
				if failCount >= maxFailures {
					return nil, failCount, lastErr
				}
				continue
			}

			body, readErr := io.ReadAll(response.Body)
			response.Body.Close()
			if readErr != nil {
				channelScheduler.RecordRequestFinalizeFailure(baseURL, apiKey, selection.ChannelIndex, metricsRequestID, kind)
				channelScheduler.RecordRequestEnd(baseURL, apiKey, selection.ChannelIndex, kind)
				channelScheduler.MarkURLFailure(kind, selection.ChannelIndex, baseURL)
				cfgManager.MarkKeyAsFailed(apiKey, "VisionLayer")
				failedKeys[apiKey] = true
				lastErr = readErr
				failCount++
				recordVisionAttempt(channelScheduler, kind, selection, upstream, visionRequestID, visionModel, baseURL, apiKey, "failed", response.StatusCode, false, attemptStart, "read_response", readErr.Error(), attempt > 0, nil)
				if failCount >= maxFailures {
					return nil, failCount, lastErr
				}
				continue
			}
			body = utils.DecompressGzipIfNeeded(response, body)
			if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
				channelScheduler.RecordRequestFinalizeFailure(baseURL, apiKey, selection.ChannelIndex, metricsRequestID, kind)
				channelScheduler.RecordRequestEnd(baseURL, apiKey, selection.ChannelIndex, kind)
				channelScheduler.MarkURLFailure(kind, selection.ChannelIndex, baseURL)
				lastErr = fmt.Errorf("图片理解渠道 %q 返回 HTTP %d（协议=%s，模型=%s，URL=%s）: %s",
					upstream.Name, response.StatusCode, upstream.ServiceType, visionModel, baseURL, truncateErrorBody(body))
				retry := shouldRetryVisionAttempt(response.StatusCode, body)
				if retry {
					cfgManager.MarkKeyAsFailed(apiKey, "VisionLayer")
					failedKeys[apiKey] = true
				}
				recordVisionAttempt(channelScheduler, kind, selection, upstream, visionRequestID, visionModel, baseURL, apiKey, "failed", response.StatusCode, false, attemptStart, "upstream", lastErr.Error(), attempt > 0, nil)
				if retry {
					failCount++
					if failCount >= maxFailures {
						return nil, failCount, lastErr
					}
					continue
				}
				return nil, failCount, lastErr
			}

			claudeResponse, convertErr := provider.ConvertToClaudeResponse(&types.ProviderResponse{
				StatusCode: response.StatusCode,
				Headers:    response.Header,
				Body:       body,
			})
			if convertErr != nil {
				channelScheduler.RecordRequestFinalizeFailure(baseURL, apiKey, selection.ChannelIndex, metricsRequestID, kind)
				channelScheduler.RecordRequestEnd(baseURL, apiKey, selection.ChannelIndex, kind)
				channelScheduler.MarkURLFailure(kind, selection.ChannelIndex, baseURL)
				lastErr = fmt.Errorf("解析图片理解响应失败: %w", convertErr)
				recordVisionAttempt(channelScheduler, kind, selection, upstream, visionRequestID, visionModel, baseURL, apiKey, "failed", response.StatusCode, false, attemptStart, "response_processing", lastErr.Error(), attempt > 0, nil)
				return nil, failCount, lastErr
			}
			rawResult := extractResponseText(claudeResponse)
			if rawResult == "" {
				channelScheduler.RecordRequestFinalizeFailure(baseURL, apiKey, selection.ChannelIndex, metricsRequestID, kind)
				channelScheduler.RecordRequestEnd(baseURL, apiKey, selection.ChannelIndex, kind)
				lastErr = fmt.Errorf("图片理解渠道 %q 未返回文字结果", upstream.Name)
				recordVisionAttempt(channelScheduler, kind, selection, upstream, visionRequestID, visionModel, baseURL, apiKey, "failed", response.StatusCode, false, attemptStart, "empty_response", lastErr.Error(), attempt > 0, nil)
				return nil, failCount, lastErr
			}
			result, parseErr := parseVisionBatchResponse(rawResult, images)
			if parseErr != nil {
				channelScheduler.RecordRequestFinalizeFailure(baseURL, apiKey, selection.ChannelIndex, metricsRequestID, kind)
				channelScheduler.RecordRequestEnd(baseURL, apiKey, selection.ChannelIndex, kind)
				lastErr = fmt.Errorf("图片理解渠道 %q 返回的多图结果无效: %w", upstream.Name, parseErr)
				recordVisionAttempt(channelScheduler, kind, selection, upstream, visionRequestID, visionModel, baseURL, apiKey, "failed", response.StatusCode, false, attemptStart, "invalid_batch_response", lastErr.Error(), attempt > 0, nil)
				return nil, failCount, lastErr
			}

			channelScheduler.RecordRequestFinalizeSuccess(baseURL, apiKey, selection.ChannelIndex, metricsRequestID, claudeResponse.Usage, kind)
			channelScheduler.RecordRequestEnd(baseURL, apiKey, selection.ChannelIndex, kind)
			channelScheduler.MarkURLSuccess(kind, selection.ChannelIndex, baseURL)
			recordVisionAttempt(channelScheduler, kind, selection, upstream, visionRequestID, visionModel, baseURL, apiKey, "completed", response.StatusCode, true, attemptStart, "", "", attempt > 0, claudeResponse.Usage)
			return result, failCount, nil
		}
	}

	if lastErr == nil {
		lastErr = fmt.Errorf("图片理解渠道 %q 没有可用的 BaseURL", upstream.Name)
	}
	return nil, failCount, lastErr
}

func buildProviderRequest(
	c *gin.Context,
	provider providers.Provider,
	upstream *config.UpstreamConfig,
	apiKey string,
	body []byte,
	images []visionImage,
) (*http.Request, error) {
	// R4-a：以显式输入构造图片理解请求，不再复制伪造 gin 请求形态。
	// 保留客户端 header（透明代理语义），但目标固定为 Messages 协议。
	outcome, err := providers.BuildUpstreamRequest(provider, providers.RequestInput{
		Ctx:    c.Request.Context(),
		Method: http.MethodPost,
		Path:   "/v1/messages",
		Header: c.Request.Header.Clone(),
		Body:   body,
	}, upstream, apiKey)
	if err != nil {
		return nil, err
	}
	request := outcome.Request
	if err := validateConvertedVisionRequest(request, images); err != nil {
		if request != nil && request.Body != nil {
			_ = request.Body.Close()
		}
		return nil, err
	}
	return request, nil
}

func validateConvertedVisionRequest(request *http.Request, images []visionImage) error {
	if request == nil || request.Body == nil {
		return fmt.Errorf("图片理解渠道转换后缺少请求体")
	}
	body, err := io.ReadAll(request.Body)
	if err != nil {
		return fmt.Errorf("读取图片理解渠道转换结果失败: %w", err)
	}
	request.Body = io.NopCloser(bytes.NewReader(body))

	actual := make(map[string]struct{})
	for _, fingerprint := range utils.ExtractImageFingerprints(body) {
		actual[fingerprint] = struct{}{}
	}
	for _, image := range images {
		if _, ok := actual[image.fingerprint]; !ok {
			return fmt.Errorf("图片 %s 在转换为图片理解渠道协议时丢失", image.id)
		}
	}
	return nil
}

// shouldRetryVisionAttempt 判断图片理解渠道的失败响应是否值得换 Key/URL 重试。
// 状态码分类与主链路 failover 共用 utils.ClassifyUpstreamStatus（唯一出处）；
// 在此之上必须再过响应体谓词：内容审核拦截、请求内容非法这类错误换 Key 不会
// 改变结果，重试只是空转，还会把无辜的 Key 标记为失败（调用方在 retry==true
// 时会 MarkKeyAsFailed）。收敛前这里只看状态码，审核类 403 会被误判为可重试。
func shouldRetryVisionAttempt(statusCode int, bodyBytes []byte) bool {
	if utils.IsNonRetryableUpstreamErrorBody(bodyBytes) {
		return false
	}
	retryable, _ := utils.ClassifyUpstreamStatus(statusCode)
	return retryable
}

func recordVisionAttempt(
	channelScheduler *scheduler.ChannelScheduler,
	kind scheduler.ChannelKind,
	selection *scheduler.SelectionResult,
	upstream *config.UpstreamConfig,
	requestID string,
	model string,
	baseURL string,
	apiKey string,
	status string,
	statusCode int,
	success bool,
	startedAt time.Time,
	errorType string,
	errorMessage string,
	retried bool,
	usage *types.Usage,
) {
	if channelScheduler == nil || selection == nil || upstream == nil {
		return
	}
	store := channelScheduler.GetChannelLogStore(kind)
	if store == nil {
		return
	}
	store.Record(&metrics.ChannelLog{
		RequestID:    requestID,
		AttemptID:    fmt.Sprintf("%s-%d", requestID, time.Now().UnixNano()),
		Timestamp:    time.Now().Format(time.RFC3339Nano),
		Status:       status,
		StatusCode:   statusCode,
		Success:      success,
		DurationMs:   time.Since(startedAt).Milliseconds(),
		APIType:      "VisionLayer",
		Model:        model,
		InputTokens:  usageInputTokens(usage),
		OutputTokens: usageOutputTokens(usage),
		ChannelIndex: selection.ChannelIndex,
		ChannelName:  upstream.Name,
		BaseURL:      baseURL,
		KeyMask:      utils.MaskAPIKey(apiKey),
		ErrorType:    errorType,
		ErrorMessage: truncateErrorBody([]byte(errorMessage)),
		Retried:      retried,
		Stream:       false,
	})
}

func usageInputTokens(usage *types.Usage) int {
	if usage == nil {
		return 0
	}
	if usage.InputTokens > 0 {
		return usage.InputTokens
	}
	return usage.PromptTokens
}

func usageOutputTokens(usage *types.Usage) int {
	if usage == nil {
		return 0
	}
	if usage.OutputTokens > 0 {
		return usage.OutputTokens
	}
	return usage.CompletionTokens
}

func truncateErrorBody(body []byte) string {
	text := strings.TrimSpace(string(body))
	if len(text) <= 500 {
		return text
	}
	return text[:500] + "..."
}
