// 同候选协议兼容重试：能力探测、等价转义、思考整流与图片降级。
package proxycore

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/insistanan/api-proxy/internal/mediasanitizer"
	"github.com/insistanan/api-proxy/internal/rectifier"
	"github.com/insistanan/api-proxy/internal/scheduler"
	"github.com/insistanan/api-proxy/internal/types"
	"github.com/insistanan/api-proxy/internal/upstreamcontrol"
	"github.com/insistanan/api-proxy/internal/utils"
)

// retryCompatibility 按既有顺序尝试协议自愈；中止时把错误交回主循环决定候选转移。
func (step candidateAttempt) retryCompatibility(
	resp *http.Response,
	respBodyBytes []byte,
	proxyURL string,
	capabilities *upstreamCapabilityState,
) (*http.Response, []byte, bool, *UpstreamAttemptResult) {
	a := step.attempt
	c := a.Context
	cfgManager := a.ConfigManager
	kind := a.Kind
	apiType := a.APIType
	upstream := a.Upstream
	requestBody := a.RequestBody
	apiKey := step.apiKey
	upstreamCopy := step.upstream

	retrySucceeded := false
	finishRetryContentSafety := step.finishRetryPreparation

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
			return resp, respBodyBytes, retrySucceeded, aborted
		}
	}

	// 部分 DeepSeek 兼容渠道会在 thinking 模式下要求每条 assistant
	// 历史都带 reasoning_content。Cursor 等客户端可能已将该字段
	// 隐藏；首次收到明确 400 后，记录严格能力并重建同渠道请求。
	// 只有原文可恢复时才重试；缺失原文时返回请求错误。
	if !retrySucceeded && (kind == scheduler.ChannelKindMessages || kind == scheduler.ChannelKindResponses) && upstreamCopy.ServiceType == "openai" && !upstreamCopy.RequireReasoningContent &&
		IsReasoningContentRequired(resp.StatusCode, respBodyBytes) {
		log.Printf("[%s-ChannelCapability] 渠道 %s 要求完整 reasoning_content，校验原文后准备同 key 重试", apiType, upstream.Name)
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
			return resp, respBodyBytes, retrySucceeded, aborted
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
				return resp, respBodyBytes, retrySucceeded, aborted
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
				return resp, respBodyBytes, retrySucceeded, aborted
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
				return resp, respBodyBytes, retrySucceeded, aborted
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
				return resp, respBodyBytes, retrySucceeded, aborted
			}
			RestoreRequestBody(c, requestBody)
		}
	}

	return resp, respBodyBytes, retrySucceeded, nil
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
// abort 非 nil 时终止当前尝试；局部准入错误仍可尝试同渠道其他组合。
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
		if types.IsUnsupportedProtocol(retryResult.Err) {
			finishRetryContentSafety(retryResult.Err)
			body, _ := json.Marshal(gin.H{"error": gin.H{"message": retryResult.Err.Error(), "type": "unsupported_protocol"}})
			abort := newUpstreamAttemptResult(false, "", 0, &FailoverError{Status: http.StatusBadRequest, Body: body}, nil, retryResult.Err).withHealth(channelHealthNeutral)
			return resp, respBodyBytes, retrySucceeded, &abort
		}
		var clientErr interface{ ClientRequestError() bool }
		if errors.As(retryResult.Err, &clientErr) && clientErr.ClientRequestError() {
			finishRetryContentSafety(retryResult.Err)
			abort := newUpstreamAttemptResult(true, "", 0, nil, nil, retryResult.Err).withHealth(channelHealthNeutral)
			return resp, respBodyBytes, retrySucceeded, &abort
		}
		if upstreamcontrol.IsLocalError(retryResult.Err) {
			finishRetryContentSafety(retryResult.Err)
			abort := newUpstreamAttemptResult(false, "", 0, nil, nil, retryResult.Err).withHealth(channelHealthNeutral)
			if errors.Is(retryResult.Err, upstreamcontrol.ErrBudget) {
				abort = abort.withAbortFailover()
			}
			return resp, respBodyBytes, retrySucceeded, &abort
		}
		if isClientSideError(retryResult.Err) {
			finishRetryContentSafety(retryResult.Err)
			abort := newUpstreamAttemptResult(true, "", 0, nil, nil, retryResult.Err).withHealth(channelHealthNeutral)
			return resp, respBodyBytes, retrySucceeded, &abort
		}
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
