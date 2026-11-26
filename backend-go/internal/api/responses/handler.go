// Package responses 提供 Responses API 的处理器
package responses

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"insistanan/api-proxy/internal/api/common"
	"insistanan/api-proxy/internal/dialogue/session"
	"insistanan/api-proxy/internal/infra/config"
	"insistanan/api-proxy/internal/infra/upstreamreq"
	"insistanan/api-proxy/internal/orchestration/scheduler"
	"insistanan/api-proxy/internal/protocol/codec"
	"insistanan/api-proxy/internal/protocol/converters"
	"insistanan/api-proxy/internal/protocol/items"
	"insistanan/api-proxy/internal/protocol/providers"
	"insistanan/api-proxy/internal/protocol/tokenusage"
	"insistanan/api-proxy/internal/protocol/types"
	"insistanan/api-proxy/internal/proxycore"
	"insistanan/api-proxy/internal/safety/pipeline"
)

// ErrEmptyStreamResponse 表示上游返回 HTTP 200，但没有产出任何 output。
var ErrEmptyStreamResponse = errors.New("upstream returned empty stream response (model at capacity)")

// Handler Responses API 代理处理器
// 支持多渠道调度：当配置多个渠道时自动启用。
// 请求骨架（认证/读体/会话观测/渠道分派/failover）复用 proxycore.RunProxyRequest，
// 协议差异经 ProtocolSpec 表达。
func Handler(
	envCfg *config.EnvConfig,
	cfgManager *config.ConfigManager,
	sessionManager *session.SessionManager,
	channelScheduler *scheduler.ChannelScheduler,
	contentSafetyPipelines ...*pipeline.Pipeline,
) gin.HandlerFunc {
	contentSafetyPipeline := pipeline.ResolveContentSafetyPipeline(cfgManager, contentSafetyPipelines...)
	provider := &providers.ResponsesProvider{SessionManager: sessionManager}
	if sessionManager != nil && channelScheduler != nil {
		if err := sessionManager.BindRegistry(channelScheduler.GetConversationRegistry()); err != nil {
			return func(c *gin.Context) { c.JSON(http.StatusServiceUnavailable, gin.H{"error": err.Error()}) }
		}
	}

	spec := proxycore.ProtocolSpec{
		Kind:         scheduler.ChannelKindResponses,
		LogName:      "Responses",
		HookPipeline: contentSafetyPipeline,
		// 多渠道模式下内容审核错误跨渠道转移（与历史行为一致；单渠道不生效）。
		AllowContentPolicyChannelFailover: cfgManager.GetFuzzyModeEnabledForProtocol("responses"),
		ParseRequest: func(c *gin.Context, body []byte) (string, bool, []string, bool) {
			var responsesReq types.ResponsesRequest
			if len(body) > 0 {
				if err := json.Unmarshal(body, &responsesReq); err != nil {
					c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("解析 Responses 请求失败: %v", err)})
					return "", false, nil, false
				}
				if _, err := parseInputToItems(responsesReq.Input); err != nil {
					c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("解析 Responses input 失败: %v", err)})
					return responsesReq.Model, false, nil, false
				}
			} else {
				c.JSON(http.StatusBadRequest, gin.H{"error": "Responses 请求体不能为空"})
				return "", false, nil, false
			}
			return responsesReq.Model, responsesReq.Stream, proxycore.ExtractPromptsFromResponsesInput(responsesReq.Input), true
		},
		BuildUpstreamRequest: func(c *gin.Context, up *config.UpstreamConfig, apiKey string, body []byte) (*http.Request, error) {
			if c.GetBool(upstreamreq.ContextKeyAuxiliaryTask) {
				providers.BindResponseSession(c, &types.Session{})
			}
			if err := beginResponsesTurn(c, sessionManager, body, c.GetString(upstreamreq.ContextKeyConversationUserID)); err != nil {
				return nil, err
			}
			req, _, err := provider.ConvertToProviderRequest(c, up, apiKey)
			return req, err
		},
		RequestTimeoutOverride: func(body []byte) time.Duration {
			if isResponsesImageGenerationRequest(body) {
				return 10 * time.Minute
			}
			return 0
		},
		HandleSuccess: func(c *gin.Context, resp *http.Response, up *config.UpstreamConfig, apiKey string, body []byte, startTime time.Time) (*types.Usage, error) {
			var responsesReq types.ResponsesRequest
			if len(body) > 0 {
				_ = json.Unmarshal(body, &responsesReq)
			}
			conversationValue, _ := c.Get(upstreamreq.ContextKeyConversationUserID)
			conversationID, _ := conversationValue.(string)
			return handleSuccess(c, resp, provider, up.ServiceType, envCfg, sessionManager, startTime, &responsesReq, body, channelScheduler, conversationID)
		},
	}
	return gin.HandlerFunc(func(c *gin.Context) {
		defer closeResponsesTurn(c)
		proxycore.RunProxyRequest(c, envCfg, cfgManager, channelScheduler, spec)
	})
}

// handleSuccess 处理成功的 Responses 响应
func handleSuccess(
	c *gin.Context,
	resp *http.Response,
	provider *providers.ResponsesProvider,
	upstreamType string,
	envCfg *config.EnvConfig,
	sessionManager *session.SessionManager,
	startTime time.Time,
	originalReq *types.ResponsesRequest,
	originalRequestJSON []byte,
	channelScheduler *scheduler.ChannelScheduler,
	conversationID string,
) (*types.Usage, error) {
	envCfg = proxycore.RequestLogEnv(c, envCfg)
	defer resp.Body.Close()

	isStream := originalReq != nil && originalReq.Stream

	usageRequestJSON, err := prepareResponsesUsageRequestBody(
		originalRequestJSON,
		c.GetBool(upstreamreq.ContextKeyResponsesHistoryBoundary),
	)
	if err != nil {
		return nil, err
	}

	if isStream {
		return handleStreamSuccess(c, resp, upstreamType, envCfg, sessionManager, startTime, originalReq, originalRequestJSON, usageRequestJSON, channelScheduler, conversationID)
	}

	// 非流式响应处理
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		c.JSON(500, gin.H{"error": "Failed to read response"})
		return nil, err
	}

	decompressedBytes, wasDecoded, decompressErr := codec.DecompressResponseBodyIfNeeded(resp, bodyBytes, envCfg.Current().MaxRequestBodySize)
	if decompressErr != nil && errors.Is(decompressErr, codec.ErrDecompressedBodyTooLarge) {
		log.Printf("[Responses-Response] 错误: 解压响应体超过大小限制: %v", decompressErr)
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
		log.Printf("[Responses-Timing] Responses 响应完成: %dms, 状态: %d", responseTime, resp.StatusCode)
		proxycore.LogResponseDetails(resp, bodyBytes, envCfg, "Responses")
	}

	responsesResp, err := converters.ConvertUpstreamResponseToResponses(upstreamType, originalRequestJSON, bodyBytes, "", providers.ResponseToolsForRequest(c))
	if err != nil {
		c.JSON(500, gin.H{"error": "Failed to convert response"})
		return nil, err
	}

	// 先把用于指标的 usage 固化成上游原值快照——后续客户端补全/剥离/校正不应影响
	// 计费、熔断和性能画像使用的上游统计。
	metricsUsage := types.Usage{
		InputTokens:                responsesResp.Usage.InputTokens,
		OutputTokens:               responsesResp.Usage.OutputTokens,
		CacheCreationInputTokens:   responsesResp.Usage.CacheCreationInputTokens,
		CacheReadInputTokens:       responsesResp.Usage.CacheReadInputTokens,
		CacheCreation5mInputTokens: responsesResp.Usage.CacheCreation5mInputTokens,
		CacheCreation1hInputTokens: responsesResp.Usage.CacheCreation1hInputTokens,
		CacheTTL:                   responsesResp.Usage.CacheTTL,
	}
	if tokenusage.RecorderFrom(c.Request.Context()) != nil &&
		(upstreamType == converters.ResponsesUpstreamResponses || upstreamType == converters.ResponsesUpstreamOpenAI) {
		if reported := tokenusage.ExtractJSONUsage(bodyBytes); reported != nil {
			metricsUsage.UnattributedCacheInput = reported.UnattributedCacheInput
		}
	}
	// 容量错误有时会以 HTTP 200 的空结果返回。重试时保留上游已经报告的用量。
	if (len(responsesResp.Output) == 0 && responsesResp.Usage.OutputTokens == 0) ||
		(proxycore.IsUpstreamModelCapacityError(bodyBytes) && responsesResp.Usage.OutputTokens == 0) {
		log.Printf("[Responses] 检测到空响应 (非流式, output 为空), 尝试 failover")
		return &metricsUsage, proxycore.NewRetrySameCandidateError(ErrEmptyStreamResponse)
	}
	// 会话清理只使用上游原始 total_tokens；如果上游没有提供 total_tokens，
	// 在客户端副本补全后再采用 input+output 的有限回退。严重错报的校正值只
	// 服务于客户端压缩判断，不写入本地会话累计，避免启发式估算污染持久化状态。
	sessionTokens := responsesResp.Usage.TotalTokens
	// Token 补全逻辑只作用于客户端响应副本。
	patchResponsesUsage(responsesResp, usageRequestJSON, envCfg)
	if sessionTokens <= 0 {
		sessionTokens = responsesResp.Usage.TotalTokens
		if sessionTokens <= 0 {
			sessionTokens = responsesResp.Usage.InputTokens + responsesResp.Usage.OutputTokens
		}
	}
	// 透传分支剥离累积式缓存统计（同 stream.go 的 stripAccumulatedCacheFromCompletedEvent）：
	// grok-4.6 等 OpenAI 兼容上游的 cached_tokens 是跨请求累积命中量，原样下发会让
	// Cursor 误判上下文一直满、反复触发压缩。Claude 原生缓存与携带 previous_response_id 的
	// 链式增量请求不受影响（函数内按字段语义判断保留）
	if upstreamType == converters.ResponsesUpstreamResponses {
		stripAccumulatedCacheFromResponse(responsesResp, usageRequestJSON)
		// 剥离缓存字段后 input_tokens 成了客户端判断上下文占用的唯一依据，
		// 校正上游对长上下文的错报值，否则 Cursor 会误判上下文为空、永不触发压缩。
		// 携带 previous_response_id 的链式请求跳过校正，避免增量估算篡改服务端历史上下文。
		clientUsage := responsesResp.Usage
		correctUnderreportedInputTokensInResponse(responsesResp, &clientUsage, usageRequestJSON, envCfg)
		responsesResp.Usage = clientUsage
		normalizeContextDetailsInputTokensInResponse(responsesResp, &responsesResp.Usage, envCfg.Current().EnableResponseLogs)
	}
	responseBody, err := codec.MarshalJSONNoEscape(responsesResp)
	if err != nil {
		return &metricsUsage, fmt.Errorf("序列化 Responses 响应失败: %w", err)
	}
	responseBody, err = pipeline.RunAttachedPostResponseHooks(c.Request.Context(), c, responseBody, resp)
	if err != nil {
		return &metricsUsage, err
	}
	if err := json.Unmarshal(responseBody, responsesResp); err != nil {
		return &metricsUsage, fmt.Errorf("解析已还原 Responses 响应失败: %w", err)
	}
	if originalReq != nil {
		historyOutput, err := respitems.ResponsesHistoryOutput(responsesResp.Output, responsesResp.Status == "incomplete")
		if err != nil {
			return &metricsUsage, err
		}
		if err := commitResponsesTurn(c, sessionManager, originalReq, originalRequestJSON, historyOutput, sessionTokens, responsesResp.ID, responseUpstreamOwner(resp), false, false); err != nil {
			return &metricsUsage, err
		}
		if snapshot := providers.ResponseSessionForRequest(c); snapshot != nil && snapshot.LastResponseID != "" && !c.GetBool(upstreamreq.ContextKeyResponsesPreviousIDDropped) {
			responsesResp.PreviousID = snapshot.LastResponseID
			responsesResp.PreviousResponseID = snapshot.LastResponseID
		}
	}
	if originalReq != nil && respitems.ResponsesItemsContainCompactionFromInput(originalReq.Input) {
		clearResponsesPreviousIDs(responsesResp)
	}
	if c.GetBool(upstreamreq.ContextKeyResponsesPreviousIDDropped) {
		// 上游请求已被切换到新边界；不能把它原样返回的旧 previous ID
		// 继续交给 Cursor，否则客户端下一轮会重新走旧服务端会话。
		clearResponsesPreviousIDs(responsesResp)
	}

	upstreamreq.ForwardResponseHeaders(resp.Header, c.Writer)
	proxycore.MarkRequestLogFirstToken(c)
	body, err := json.Marshal(responsesResp)
	if err != nil {
		return &metricsUsage, fmt.Errorf("编码 Responses 响应失败: %w", err)
	}
	if err := common.WriteJSONResponse(c, http.StatusOK, body); err != nil {
		return &metricsUsage, err
	}

	// 返回 usage 数据用于指标记录（上游原值快照，不受下发侧校正影响）
	return &metricsUsage, nil
}

func clearResponsesPreviousIDs(resp *types.ResponsesResponse) {
	if resp == nil {
		return
	}
	resp.PreviousID = ""
	resp.PreviousResponseID = ""
	if resp.Extra != nil {
		delete(resp.Extra, "previous_id")
		delete(resp.Extra, "previous_response_id")
	}
}

func isResponsesImageGenerationRequest(bodyBytes []byte) bool {
	var payload struct {
		Tools []json.RawMessage `json:"tools"`
	}
	if err := json.Unmarshal(bodyBytes, &payload); err != nil {
		return false
	}
	for _, rawTool := range payload.Tools {
		var tool struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(rawTool, &tool); err == nil && tool.Type == "image_generation" {
			return true
		}
	}
	return false
}

// parseInputToItems 解析 input 为 ResponsesItem 数组
func parseInputToItems(input interface{}) ([]types.ResponsesItem, error) {
	switch v := input.(type) {
	case string:
		return []types.ResponsesItem{{Type: "text", Content: v}}, nil
	case []interface{}:
		items := make([]types.ResponsesItem, 0, len(v))
		for index, item := range v {
			itemMap, ok := item.(map[string]interface{})
			if !ok {
				return nil, fmt.Errorf("input 第 %d 项必须是对象", index)
			}
			parsed, err := types.ParseResponsesItem(itemMap)
			if err != nil {
				return nil, fmt.Errorf("input 第 %d 项: %w", index, err)
			}
			if parsed.Type == "" {
				return nil, fmt.Errorf("input 第 %d 项缺少 type", index)
			}
			items = append(items, parsed)
		}
		return items, nil
	default:
		return nil, fmt.Errorf("unsupported input type")
	}
}
