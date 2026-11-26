// Package messages 提供 Claude Messages API 的处理器
package messages

import (
	"encoding/json"
	"errors"
	"fmt"
	"insistanan/api-proxy/internal/infra/upstreamreq"
	"insistanan/api-proxy/internal/protocol/codec"
	"insistanan/api-proxy/internal/protocol/contract"
	"insistanan/api-proxy/internal/protocol/tokenusage"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"insistanan/api-proxy/internal/api/common"
	messagestreams "insistanan/api-proxy/internal/api/streams/messages"
	"insistanan/api-proxy/internal/infra/config"
	"insistanan/api-proxy/internal/infra/middleware"
	"insistanan/api-proxy/internal/orchestration/scheduler"
	"insistanan/api-proxy/internal/protocol/providers"
	"insistanan/api-proxy/internal/protocol/types"
	"insistanan/api-proxy/internal/proxycore"
	"insistanan/api-proxy/internal/safety/pipeline"
)

const messagesProviderContextKey = "messages.provider"

// Handler Messages API 代理处理器
// 支持多渠道调度：当配置多个渠道时自动启用
// Handler Messages API 代理处理器。
// 使用通用 RunProxyRequest 骨架，通过 ProtocolSpec 注入协议特有逻辑。
func Handler(
	envCfg *config.EnvConfig,
	cfgManager *config.ConfigManager,
	channelScheduler *scheduler.ChannelScheduler,
	contentSafetyPipelines ...*pipeline.Pipeline,
) gin.HandlerFunc {
	contentSafetyPipeline := pipeline.ResolveContentSafetyPipeline(cfgManager, contentSafetyPipelines...)
	spec := proxycore.ProtocolSpec{
		Kind:         scheduler.ChannelKindMessages,
		LogName:      "Messages",
		HookPipeline: contentSafetyPipeline,
		ParseRequest: func(c *gin.Context, body []byte) (string, bool, []string, bool) {
			body, _ = proxycore.RemoveEmptySignatures(body, envCfg.Current().EnableRequestLogs, "Messages")
			var claudeReq types.ClaudeRequest
			if len(body) > 0 {
				_ = json.Unmarshal(body, &claudeReq)
			}
			prompts := proxycore.ExtractPromptsFromClaude(claudeReq.Messages)
			return claudeReq.Model, claudeReq.Stream, prompts, true
		},
		PreRoute: nil,
		BuildUpstreamRequest: func(c *gin.Context, up *config.UpstreamConfig, apiKey string, body []byte) (*http.Request, error) {
			provider := providers.GetProvider(up.ServiceType)
			if provider == nil {
				return nil, fmt.Errorf("unsupported service type: %s", up.ServiceType)
			}
			// Messages->Responses 的 Provider 在构造请求时会保存本次请求的
			// conversation、模型和 previous_response_id 链状态。成功处理阶段
			// 必须复用同一个实例；重新 GetProvider 会丢失这些字段，导致链
			// 无法登记，后续请求只能重复发送完整历史。
			c.Set(messagesProviderContextKey, provider)
			req, _, err := provider.ConvertToProviderRequest(c, up, apiKey)
			return req, err
		},
		HandleSuccess: func(c *gin.Context, resp *http.Response, up *config.UpstreamConfig, apiKey string, body []byte, startTime time.Time) (*types.Usage, error) {
			provider, exists := c.Get(messagesProviderContextKey)
			if !exists {
				provider = providers.GetProvider(up.ServiceType)
			}
			resolvedProvider, ok := provider.(providers.Provider)
			if !ok {
				return nil, fmt.Errorf("invalid Messages provider state for service type: %s", up.ServiceType)
			}
			var claudeReq types.ClaudeRequest
			_ = json.Unmarshal(body, &claudeReq)
			if claudeReq.Stream {
				return messagestreams.HandleStreamResponse(c, resp, resolvedProvider, envCfg, startTime, up, body, claudeReq.Model)
			}
			return handleNormalResponse(c, resp, resolvedProvider, envCfg, startTime, body, up, apiKey)
		},
	}
	return func(c *gin.Context) {
		proxycore.RunProxyRequest(c, envCfg, cfgManager, channelScheduler, spec)
	}
}

func handleNormalResponse(
	c *gin.Context,
	resp *http.Response,
	provider providers.Provider,
	envCfg *config.EnvConfig,
	startTime time.Time,
	requestBody []byte,
	upstream *config.UpstreamConfig,
	apiKey string,
) (*types.Usage, error) {
	envCfg = proxycore.RequestLogEnv(c, envCfg)
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		c.JSON(500, gin.H{"error": "Failed to read response"})
		return nil, err
	}

	decompressedBytes, wasDecoded, decompressErr := codec.DecompressResponseBodyIfNeeded(resp, bodyBytes, envCfg.Current().MaxRequestBodySize)
	if decompressErr != nil && errors.Is(decompressErr, codec.ErrDecompressedBodyTooLarge) {
		log.Printf("[Messages-Response] 错误: 解压响应体超过大小限制: %v", decompressErr)
		c.JSON(http.StatusBadGateway, gin.H{"error": "Decompressed response body too large"})
		return nil, decompressErr
	}
	if decompressErr != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "Failed to decode upstream response"})
		return nil, decompressErr
	}
	if wasDecoded {
		bodyBytes = decompressedBytes
		upstreamreq.StripEntityHeadersForRebuiltBody(resp.Header)
		resp.Header.Del("Content-Encoding")
	}

	if envCfg.Current().EnableResponseLogs {
		responseTime := time.Since(startTime).Milliseconds()
		log.Printf("[Messages-Timing] 响应完成: %dms, 状态: %d", responseTime, resp.StatusCode)
		proxycore.LogResponseDetails(resp, bodyBytes, envCfg, "Messages")
	}

	providerResp := &types.ProviderResponse{
		StatusCode: resp.StatusCode,
		Headers:    resp.Header,
		Body:       bodyBytes,
		Stream:     false,
	}
	if contract.IsClaudeOnDemandCompaction(requestBody) {
		if err := contract.ValidateClaudeCompactionResponse(bodyBytes); err != nil {
			return tokenusage.ExtractJSONUsage(bodyBytes), err
		}
	}

	claudeResp, err := provider.ConvertToClaudeResponse(providerResp)
	if err != nil {
		return nil, fmt.Errorf("转换上游响应失败: %w", err)
	}
	// 思考缓存限定同一会话、渠道和模型；跨渠道续接依赖客户端回传原文。
	var cacheRequest types.ClaudeRequest
	if err := json.Unmarshal(requestBody, &cacheRequest); err != nil {
		return nil, fmt.Errorf("解析思考缓存来源失败: %w", err)
	}
	if err := providers.CacheClaudeResponseReasoning(c.Request.Context(), claudeResp, providers.ReasoningCacheScope(
		c.GetString(upstreamreq.ContextKeyConversationUserID), upstream, cacheRequest.Model,
	)); err != nil {
		return claudeResp.Usage, err
	}

	// Token 补全逻辑
	// 先保存上游 usage 快照；客户端副本后续会做错报校正，不能反向
	// 污染计费、熔断与性能画像。
	var metricsUsage *types.Usage
	if claudeResp.Usage != nil {
		metricsUsage = tokenusage.BillingUsage(claudeResp.Usage)
	}
	preserveCompactionUsage := tokenusage.PreserveCompactionUsage(requestBody) ||
		claudeResp.Usage != nil && len(claudeResp.Usage.Iterations) > 0
	var usageRewrite claudeUsageRewrite
	if claudeResp.Usage == nil && !preserveCompactionUsage {
		estimatedInput := tokenusage.SafeEstimatedInputTokens(tokenusage.EstimateRequestTokens(requestBody))
		estimatedOutput := tokenusage.EstimateResponseTokens(claudeResp.Content)
		claudeResp.Usage = &types.Usage{
			InputTokens:  estimatedInput,
			OutputTokens: estimatedOutput,
		}
		usageRewrite.injectUsage = true
		if envCfg.Current().EnableResponseLogs {
			log.Printf("[Messages-Token] 上游无Usage, 本地估算: input=%d, output=%d", estimatedInput, estimatedOutput)
		}
	} else if !preserveCompactionUsage {
		originalInput := claudeResp.Usage.InputTokens
		originalOutput := claudeResp.Usage.OutputTokens
		patched := false

		hasCacheTokens := tokenusage.AnthropicCachedInputTokens(
			claudeResp.Usage.CacheReadInputTokens,
			claudeResp.Usage.CacheCreationInputTokens,
			claudeResp.Usage.CacheCreation5mInputTokens,
			claudeResp.Usage.CacheCreation1hInputTokens,
		) > 0

		if claudeResp.Usage.InputTokens <= 1 && !hasCacheTokens {
			claudeResp.Usage.InputTokens = tokenusage.SafeEstimatedInputTokens(tokenusage.EstimateRequestTokens(requestBody))
			patched = true
			usageRewrite.patchInput = true
		}
		if claudeResp.Usage.OutputTokens <= 1 {
			claudeResp.Usage.OutputTokens = tokenusage.EstimateResponseTokens(claudeResp.Content)
			patched = true
			usageRewrite.patchOutput = true
		}
		if envCfg.Current().EnableResponseLogs {
			if patched {
				log.Printf("[Messages-Token] 虚假值补全: InputTokens=%d->%d, OutputTokens=%d->%d",
					originalInput, claudeResp.Usage.InputTokens, originalOutput, claudeResp.Usage.OutputTokens)
			}
			log.Printf("[Messages-Token] InputTokens=%d, OutputTokens=%d, CacheCreationInputTokens=%d, CacheReadInputTokens=%d, CacheCreation5m=%d, CacheCreation1h=%d, CacheTTL=%s",
				claudeResp.Usage.InputTokens, claudeResp.Usage.OutputTokens,
				claudeResp.Usage.CacheCreationInputTokens, claudeResp.Usage.CacheReadInputTokens,
				claudeResp.Usage.CacheCreation5mInputTokens, claudeResp.Usage.CacheCreation1hInputTokens,
				claudeResp.Usage.CacheTTL)
		}
	}

	// 监听客户端断开连接。
	// 通过 channel 传递"响应已写出"信号，goroutine 内不再访问 c.Writer：
	// request context 在 handler 返回后随即取消，而 gin.Context 会被归还
	// sync.Pool 回收复用，届时读取 c.Writer.Written() 是 use-after-recycle 竞态。
	responded := make(chan struct{})
	ctx := c.Request.Context()
	statusCode := resp.StatusCode
	go func() {
		<-ctx.Done()
		select {
		case <-responded:
			// 响应已写出，正常结束
		default:
			if envCfg.Current().EnableResponseLogs {
				responseTime := time.Since(startTime).Milliseconds()
				log.Printf("[Messages-Timing] 响应中断: %dms, 状态: %d", responseTime, statusCode)
			}
		}
	}()

	// 客户端与内部指标使用不同副本。正常 Anthropic usage 必须完整保留缓存创建和读取量，
	// 客户端会按 input + cache_read + cache_creation 计算上下文规模；TTL 字段只是创建量明细。
	clientResp := *claudeResp
	if claudeResp.Usage != nil {
		clientUsage := *claudeResp.Usage
		// 上游 usage 合理性校验（同流式出口 sanityCheckMessageStreamInputTokens）：
		// messages+claude 上游是全代理唯一完全不校验、原样透传的路径，实测中转渠道会
		// 双向错报（448KB 报 26032 / 压缩后 24KB 报 209736），客户端据此误判压缩时机。
		// 只改客户端副本；上方 claudeResp.Usage 原件继续供指标与请求日志使用。
		if correction, need := tokenusage.SanityCheckedAnthropicUsageForRequest(
			requestBody,
			tokenusage.EstimateRequestTokens(requestBody),
			claudeResp.Usage.InputTokens,
			claudeResp.Usage.CacheReadInputTokens,
			claudeResp.Usage.CacheCreationInputTokens,
			claudeResp.Usage.CacheCreation5mInputTokens,
			claudeResp.Usage.CacheCreation1hInputTokens,
		); need && !preserveCompactionUsage {
			if envCfg.Current().EnableResponseLogs {
				log.Printf("[Messages-Token] 上游 usage 错报校正: input_tokens=%d->%d, clear_cache=%v（本地估算，仅改下发值）",
					claudeResp.Usage.InputTokens, correction.InputTokens, correction.ClearCache)
			}
			clientUsage.InputTokens = correction.InputTokens
			if correction.ClearCache {
				usageRewrite.clearCache = true
				clientUsage.CacheReadInputTokens = 0
				clientUsage.CacheCreationInputTokens = 0
				clientUsage.CacheCreation5mInputTokens = 0
				clientUsage.CacheCreation1hInputTokens = 0
				clientUsage.CacheTTL = ""
			}
			usageRewrite.patchInput = true
		}
		clientResp.Usage = &clientUsage
	}
	var clientBody []byte
	if upstream != nil && upstream.ServiceType == "claude" {
		// 只有 Claude→Claude 能做服务端 auto mode。未知字段（含 safeguard_results）
		// 和 tool_use.id 必须留下；usage 只在估算、虚假值补全或错报校正时改对应字段。
		// openai/gemini/responses 上游仍走结构体转换，原始 JSON 不是 Messages 响应。
		clientBody, err = claudePassthroughBody(bodyBytes, clientResp.Usage, usageRewrite)
		if err != nil {
			return nil, err
		}
	} else {
		clientBody, err = codec.MarshalJSONNoEscape(&clientResp)
		if err != nil {
			return nil, fmt.Errorf("序列化 Messages 响应失败: %w", err)
		}
	}
	clientBody, err = pipeline.RunAttachedPostResponseHooks(c.Request.Context(), c, clientBody, resp)
	if err != nil {
		return nil, err
	}

	upstreamreq.ForwardResponseHeaders(resp.Header, c.Writer)
	proxycore.MarkRequestLogFirstToken(c)
	writeErr := common.WriteJSONResponse(c, http.StatusOK, clientBody)
	close(responded) // 响应已写出，解除断连监听 goroutine 的等待条件
	if writeErr != nil {
		return metricsUsage, writeErr
	}

	if envCfg.Current().EnableResponseLogs {
		responseTime := time.Since(startTime).Milliseconds()
		log.Printf("[Messages-Timing] 响应发送完成: %dms, 状态: %d", responseTime, resp.StatusCode)
	}

	return metricsUsage, nil
}

// CountTokensHandler 处理 /v1/messages/count_tokens 请求
func CountTokensHandler(envCfg *config.EnvConfig, cfgManager *config.ConfigManager, channelScheduler *scheduler.ChannelScheduler) gin.HandlerFunc {
	return func(c *gin.Context) {
		middleware.ProxyAuthMiddleware(envCfg)(c)
		if c.IsAborted() {
			return
		}

		// 使用统一的请求体读取函数，应用大小限制
		bodyBytes, err := proxycore.ReadRequestBody(c, envCfg.Current().MaxRequestBodySize)
		if err != nil {
			// ReadRequestBody 已经返回了错误响应
			return
		}

		var req struct {
			Model    string      `json:"model"`
			System   interface{} `json:"system"`
			Messages interface{} `json:"messages"`
			Tools    interface{} `json:"tools"`
		}
		if err := json.Unmarshal(bodyBytes, &req); err != nil {
			c.JSON(400, gin.H{"error": "Invalid JSON"})
			return
		}

		inputTokens := tokenusage.EstimateRequestTokens(bodyBytes)

		c.JSON(200, gin.H{
			"input_tokens": inputTokens,
		})

		if envCfg.Current().EnableResponseLogs {
			log.Printf("[Messages-Token] CountTokens本地估算: model=%s, input_tokens=%d", req.Model, inputTokens)
		}
	}
}
