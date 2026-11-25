package visionlayer

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/insistanan/api-proxy/internal/infra/config"
	"github.com/insistanan/api-proxy/internal/infra/httpclient"
	"github.com/insistanan/api-proxy/internal/orchestration/scheduler"
	"github.com/insistanan/api-proxy/internal/orchestration/upstreamcontrol"
	"github.com/insistanan/api-proxy/internal/protocol/codec"
	"github.com/insistanan/api-proxy/internal/protocol/providers"
	"github.com/insistanan/api-proxy/internal/protocol/types"
	"github.com/insistanan/api-proxy/internal/protocol/visionmedia"
	"github.com/insistanan/api-proxy/internal/storage/metrics"
)

type visionHealthOutcome uint8

const (
	visionHealthNeutral visionHealthOutcome = iota
	visionHealthSuccess
	visionHealthFailure
)

// 重试决策和健康记账彼此独立：限流可以换渠道，但不应触发渠道熔断。
type visionAttemptResult struct {
	descriptions map[string]string
	retryable    bool
	health       visionHealthOutcome
	err          error
}

// requestImageDescriptions 只发送已完成模型解析的图片批次。
// maxFailures 只限制单渠道尝试次数，不参与判断错误是否允许跨渠道重试。
func requestImageDescriptions(
	c *gin.Context,
	envCfg *config.EnvConfig,
	cfgManager *config.ConfigManager,
	channelScheduler *scheduler.ChannelScheduler,
	selection *scheduler.SelectionResult,
	visionModel string,
	profile visionAnalysisProfile,
	images []visionImage,
	maxFailures int,
) visionAttemptResult {
	if err := c.Request.Context().Err(); err != nil {
		return visionAttemptResult{err: err}
	}
	upstream := selection.Upstream
	// 图片理解渠道可能来自其他会话协议；指标、熔断、URL 健康与日志
	// 必须使用渠道自身所属的 kind，而不是入口协议。
	channelKind := selection.Kind
	provider := providers.GetProvider(upstream.ServiceType)
	if provider == nil {
		return visionAttemptResult{err: fmt.Errorf("图片理解渠道 %q 的服务类型 %q 不受支持", upstream.Name, upstream.ServiceType)}
	}
	// 缓存和发送使用同一次解析的模型，清空映射以防 Provider 二次重定向。
	visionUpstream := upstream.Clone()
	if cfgManager.ClientCompatibilityEnabled(upstream) {
		visionUpstream.ClientIdentityMode = "compatibility"
	} else {
		visionUpstream.ClientIdentityMode = "api"
	}
	visionUpstream.DefaultModel = visionModel
	visionUpstream.ModelMapping = nil

	visionBody, err := buildVisionRequest(visionModel, profile, images)
	if err != nil {
		return visionAttemptResult{err: err}
	}

	visionRequestID := fmt.Sprintf("vision-%d", atomic.AddUint64(&visionCallCounter, 1))
	urlResults := channelScheduler.GetSortedURLsForChannel(channelKind, selection.ChannelIndex, upstream.GetAllBaseURLs())
	var lastResult visionAttemptResult
	failCount := 0
	attemptCount := 0

	for _, urlResult := range urlResults {
		baseURL := urlResult.URL
		// 失败与暂停按 URL + Key 隔离，前一地址不能排除备用地址的同一个 Key。
		failedKeys := make(map[string]bool)
		for attempt := 0; attempt < len(upstream.APIKeys); attempt++ {
			if err := c.Request.Context().Err(); err != nil {
				return visionAttemptResult{err: err}
			}
			apiKey, keyErr := cfgManager.GetNextAPIKey(upstream, failedKeys, "VisionLayer")
			if keyErr != nil {
				if lastResult.err == nil {
					lastResult = visionAttemptResult{retryable: true, err: keyErr}
				}
				break
			}
			if channelScheduler.ShouldSuspendKey(baseURL, apiKey, selection.ChannelIndex, channelKind) {
				failedKeys[apiKey] = true
				continue
			}

			attemptStart := time.Now()
			retried := attemptCount > 0
			attemptCount++
			upstreamCopy := visionUpstream.Clone()
			upstreamCopy.BaseURL = baseURL
			request, requestErr := buildProviderRequest(c, provider, upstreamCopy, apiKey, visionBody, images)
			if requestErr != nil {
				recordVisionAttempt(channelScheduler, selection, upstream, visionRequestID, visionModel, baseURL, apiKey, "failed", 0, false, attemptStart, "build_request", requestErr.Error(), retried, nil)
				return visionAttemptResult{err: requestErr}
			}

			timeout := time.Duration(envCfg.Current().RequestTimeout) * time.Millisecond
			channelScheduler.RecordRequestStart(baseURL, apiKey, selection.ChannelIndex, channelKind)
			metricsRequestID := channelScheduler.RecordRequestConnected(baseURL, apiKey, visionModel, selection.ChannelIndex, channelKind)
			response, requestErr := httpclient.SendForUpstream(request, timeout, cfgManager, upstreamCopy, apiKey)
			if requestErr != nil {
				if c.Request.Context().Err() != nil || upstreamcontrol.IsLocalError(requestErr) {
					channelScheduler.MetricsManager(channelKind).RecordRequestFinalizeNeutral(baseURL, apiKey, selection.ChannelIndex, metricsRequestID)
					channelScheduler.RecordRequestEnd(baseURL, apiKey, selection.ChannelIndex, channelKind)
					recordVisionAttempt(channelScheduler, selection, upstream, visionRequestID, visionModel, baseURL, apiKey, "cancelled", 0, false, attemptStart, "local_admission", requestErr.Error(), false, nil)
					return visionAttemptResult{
						retryable: c.Request.Context().Err() == nil && !errors.Is(requestErr, upstreamcontrol.ErrBudget),
						err:       requestErr,
					}
				}
				channelScheduler.RecordRequestFinalizeFailure(baseURL, apiKey, selection.ChannelIndex, metricsRequestID, channelKind)
				channelScheduler.RecordRequestEnd(baseURL, apiKey, selection.ChannelIndex, channelKind)
				channelScheduler.MarkURLFailure(channelKind, selection.ChannelIndex, baseURL)
				cfgManager.MarkKeyAsFailed(apiKey, "VisionLayer")
				failedKeys[apiKey] = true
				lastResult = visionAttemptResult{retryable: true, health: visionHealthFailure, err: requestErr}
				failCount++
				recordVisionAttempt(channelScheduler, selection, upstream, visionRequestID, visionModel, baseURL, apiKey, "failed", 0, false, attemptStart, "network", requestErr.Error(), retried, nil)
				if failCount >= maxFailures {
					return lastResult
				}
				continue
			}

			body, readErr := io.ReadAll(response.Body)
			response.Body.Close()
			if cancelErr := c.Request.Context().Err(); cancelErr != nil {
				channelScheduler.MetricsManager(channelKind).RecordRequestFinalizeNeutral(baseURL, apiKey, selection.ChannelIndex, metricsRequestID)
				channelScheduler.RecordRequestEnd(baseURL, apiKey, selection.ChannelIndex, channelKind)
				recordVisionAttempt(channelScheduler, selection, upstream, visionRequestID, visionModel, baseURL, apiKey, "cancelled", response.StatusCode, false, attemptStart, "client_cancelled", cancelErr.Error(), false, nil)
				return visionAttemptResult{err: cancelErr}
			}
			if readErr != nil {
				channelScheduler.RecordRequestFinalizeFailure(baseURL, apiKey, selection.ChannelIndex, metricsRequestID, channelKind)
				channelScheduler.RecordRequestEnd(baseURL, apiKey, selection.ChannelIndex, channelKind)
				channelScheduler.MarkURLFailure(channelKind, selection.ChannelIndex, baseURL)
				cfgManager.MarkKeyAsFailed(apiKey, "VisionLayer")
				failedKeys[apiKey] = true
				lastResult = visionAttemptResult{retryable: true, health: visionHealthFailure, err: readErr}
				failCount++
				recordVisionAttempt(channelScheduler, selection, upstream, visionRequestID, visionModel, baseURL, apiKey, "failed", response.StatusCode, false, attemptStart, "read_response", readErr.Error(), retried, nil)
				if failCount >= maxFailures {
					return lastResult
				}
				continue
			}
			processed := processVisionResponse(provider, response, body, images)
			if processed.err != nil {
				processed.err = fmt.Errorf("图片理解渠道 %q 返回 HTTP %d（协议=%s，模型=%s，URL=%s）: %w",
					upstream.Name, response.StatusCode, upstream.ServiceType, visionModel, baseURL, processed.err)
				if processed.health == visionHealthNeutral {
					channelScheduler.MetricsManager(channelKind).RecordRequestFinalizeNeutral(baseURL, apiKey, selection.ChannelIndex, metricsRequestID)
				} else {
					channelScheduler.RecordRequestFinalizeFailure(baseURL, apiKey, selection.ChannelIndex, metricsRequestID, channelKind)
				}
				channelScheduler.RecordRequestEnd(baseURL, apiKey, selection.ChannelIndex, channelKind)
				if processed.endpointFailure {
					channelScheduler.MarkURLFailure(channelKind, selection.ChannelIndex, baseURL)
				}
				recordVisionAttempt(channelScheduler, selection, upstream, visionRequestID, visionModel, baseURL, apiKey, "failed", response.StatusCode, false, attemptStart, processed.errorType, processed.err.Error(), retried, processed.usage)
				lastResult = processed.visionAttemptResult
				if !processed.retryable || processed.rateLimited {
					// 限流交给下一渠道；不在当前组合重复尝试或把 Key 标为故障。
					return lastResult
				}
				cfgManager.MarkKeyAsFailed(apiKey, "VisionLayer")
				failedKeys[apiKey] = true
				failCount++
				if failCount >= maxFailures {
					return lastResult
				}
				continue
			}

			channelScheduler.RecordRequestFinalizeSuccess(baseURL, apiKey, selection.ChannelIndex, metricsRequestID, processed.usage, channelKind)
			channelScheduler.RecordRequestEnd(baseURL, apiKey, selection.ChannelIndex, channelKind)
			channelScheduler.MarkURLSuccess(channelKind, selection.ChannelIndex, baseURL)
			recordVisionAttempt(channelScheduler, selection, upstream, visionRequestID, visionModel, baseURL, apiKey, "completed", response.StatusCode, true, attemptStart, "", "", retried, processed.usage)
			return processed.visionAttemptResult
		}
	}

	if lastResult.err == nil {
		lastResult = visionAttemptResult{retryable: true, err: fmt.Errorf("图片理解渠道 %q 没有可尝试的 URL 与 Key 组合", upstream.Name)}
	}
	return lastResult
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
	for _, fingerprint := range visionmedia.ExtractImageFingerprints(body) {
		actual[fingerprint] = struct{}{}
	}
	for _, image := range images {
		if _, ok := actual[image.fingerprint]; !ok {
			return fmt.Errorf("图片 %s 在转换为图片理解渠道协议时丢失", image.id)
		}
	}
	return nil
}

func recordVisionAttempt(
	channelScheduler *scheduler.ChannelScheduler,
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
	// 渠道日志跟随图片理解渠道自身协议，跨协议调用才能落在正确协议的日志里。
	store := channelScheduler.GetChannelLogStore(selection.Kind)
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
		KeyMask:      codec.MaskAPIKey(apiKey),
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
