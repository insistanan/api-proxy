// Package chat 提供 OpenAI Chat Completions API 的代理处理器
package chat

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/insistanan/api-proxy/internal/admin/modelcatalog"
	"github.com/insistanan/api-proxy/internal/handlers/hooks"
	"github.com/insistanan/api-proxy/internal/handlers/proxycore"
	"github.com/insistanan/api-proxy/internal/infra/config"
	"github.com/insistanan/api-proxy/internal/infra/utils"
	"github.com/insistanan/api-proxy/internal/orchestration/scheduler"
	"github.com/insistanan/api-proxy/internal/protocol/converters"
	"github.com/insistanan/api-proxy/internal/protocol/types"
)

const chatToolSearchProxyName = "tool_search"
const chatWebSearchProxyName = "web_search"

// Handler Chat Completions API 代理处理器。
// Chat 是独立一等公民：只走 Chat 渠道池，不默认进行 Anthropic/Gemini 协议转换。
// Handler Chat Completions API 代理处理器。
// Chat 是独立一等公民：只走 Chat 渠道池，不默认进行 Anthropic/Gemini 协议转换。
// 使用通用 RunProxyRequest 骨架，通过 ProtocolSpec 注入协议特有逻辑。
func Handler(
	envCfg *config.EnvConfig,
	cfgManager *config.ConfigManager,
	channelScheduler *scheduler.ChannelScheduler,
	contentSafetyPipelines ...*hooks.Pipeline,
) gin.HandlerFunc {
	contentSafetyPipeline := hooks.ResolveContentSafetyPipeline(cfgManager, contentSafetyPipelines...)
	spec := proxycore.ProtocolSpec{
		Kind:         scheduler.ChannelKindChat,
		LogName:      "Chat",
		HookPipeline: contentSafetyPipeline,
		ParseRequest: func(c *gin.Context, body []byte) (string, bool, []string, bool) {
			if len(body) == 0 {
				c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid Chat Completions request body"})
				return "", false, nil, false
			}
			var chatReq types.OpenAIRequest
			if err := json.Unmarshal(body, &chatReq); err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid Chat Completions request body", "detail": err.Error()})
				return "", false, nil, false
			}
			if strings.TrimSpace(chatReq.Model) == "" {
				c.JSON(http.StatusBadRequest, gin.H{"error": "model is required"})
				return "", false, nil, false
			}
			prompts := proxycore.ExtractPromptsFromOpenAI(chatReq.Messages)
			return chatReq.Model, chatReq.Stream, prompts, true
		},
		PreRoute: func(c *gin.Context, body []byte, model string, conversationID string, startTime time.Time) bool {
			route, ok := modelcatalog.ResolveChatRoute(c.Request.Context(), cfgManager, model)
			if !ok {
				return false
			}
			var chatReq types.OpenAIRequest
			_ = json.Unmarshal(body, &chatReq)
			handleRoutedChat(c, envCfg, cfgManager, channelScheduler, route, body, model, chatReq.Stream, conversationID, startTime)
			return true
		},
		BuildUpstreamRequest: func(c *gin.Context, up *config.UpstreamConfig, apiKey string, body []byte) (*http.Request, error) {
			return buildChatUpstreamRequest(c, up, apiKey, body)
		},
		HandleSuccess: func(c *gin.Context, resp *http.Response, up *config.UpstreamConfig, apiKey string, body []byte, startTime time.Time) (*types.Usage, error) {
			var chatReq types.OpenAIRequest
			_ = json.Unmarshal(body, &chatReq)
			return handleSuccess(c, resp, envCfg, startTime, chatReq.Stream, body)
		},
	}
	return func(c *gin.Context) {
		proxycore.RunProxyRequest(c, envCfg, cfgManager, channelScheduler, spec)
	}
}

func handleRoutedChat(
	c *gin.Context,
	envCfg *config.EnvConfig,
	cfgManager *config.ConfigManager,
	channelScheduler *scheduler.ChannelScheduler,
	route modelcatalog.ChatRoute,
	bodyBytes []byte,
	model string,
	stream bool,
	conversationID string,
	startTime time.Time,
) {
	upstream, err := chatRouteUpstream(cfgManager, route)
	if err != nil {
		proxycore.MarkConversationFailure(channelScheduler, conversationID, scheduler.ChannelKindChat, err)
		c.JSON(http.StatusNotFound, gin.H{
			"error": gin.H{
				"message": err.Error(),
				"type":    "invalid_request_error",
			},
		})
		return
	}

	if err := channelScheduler.ValidateFixedChannel(conversationID, scheduler.ChannelKindChat, route.ChannelIndex); err != nil {
		proxycore.MarkConversationFailure(channelScheduler, conversationID, scheduler.ChannelKindChat, err)
		c.JSON(http.StatusConflict, gin.H{"error": err.Error(), "code": "CONVERSATION_ROUTE_OVERRIDE"})
		return
	}

	routedBody, err := replaceChatModel(bodyBytes, route.UpstreamModel)
	if err != nil {
		proxycore.MarkConversationFailure(channelScheduler, conversationID, scheduler.ChannelKindChat, err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	urlResults := proxycore.BuildDefaultURLResults([]string{route.BaseURL})
	result := proxycore.NewAttemptBuilder(
		c, envCfg, cfgManager, channelScheduler,
		scheduler.ChannelKindChat, "Chat", upstream, model, routedBody, stream,
		route.ChannelIndex, conversationID,
	).
		WithURLResults(urlResults).
		WithNextAPIKey(func(upstream *config.UpstreamConfig, failedKeys map[string]bool) (string, error) {
			if failedKeys[route.APIKey] {
				return "", fmt.Errorf("路由模型 %s 指定的 API Key 已失败", route.Alias)
			}
			return route.APIKey, nil
		}).
		WithBuildRequest(func(c *gin.Context, upstreamCopy *config.UpstreamConfig, apiKey string) (*http.Request, error) {
			return buildChatDirectRequest(c, upstreamCopy, apiKey, routedBody)
		}).
		WithMarkURL(func(url string) {
			channelScheduler.MarkURLFailure(scheduler.ChannelKindChat, route.ChannelIndex, url)
		}, func(url string) {
			channelScheduler.MarkURLSuccess(scheduler.ChannelKindChat, route.ChannelIndex, url)
		}).
		WithHandleSuccess(func(c *gin.Context, resp *http.Response, upstreamCopy *config.UpstreamConfig, apiKey string) (*types.Usage, error) {
			return handleSuccess(c, resp, envCfg, startTime, stream, routedBody)
		}).
		Build().TryWithModelMappingFailover()
	if result.Handled {
		if result.SuccessKey != "" {
			proxycore.MarkConversationSuccess(channelScheduler, conversationID, scheduler.ChannelKindChat, route.ChannelIndex, route.ChannelName)
			channelScheduler.ConsumePromotionCount(route.ChannelIndex, scheduler.ChannelKindChat)
		} else if result.LastError != nil && !errors.Is(result.LastError, context.Canceled) {
			proxycore.MarkConversationFailure(channelScheduler, conversationID, scheduler.ChannelKindChat, result.LastError)
		}
		return
	}

	log.Printf("[Chat-Route] 路由模型失败: alias=%s channel=%d key=%s", route.Alias, route.ChannelIndex, route.KeyID)
	proxycore.MarkConversationFailure(channelScheduler, conversationID, scheduler.ChannelKindChat, result.LastError)
	proxycore.HandleAllKeysFailed(c, cfgManager.GetFuzzyModeEnabledForProtocol("chat"), result.FailoverError, result.LastError, "Chat")
}

func chatRouteUpstream(cfgManager *config.ConfigManager, route modelcatalog.ChatRoute) (*config.UpstreamConfig, error) {
	cfg := cfgManager.GetConfig()
	if route.ChannelIndex < 0 || route.ChannelIndex >= len(cfg.ChatUpstream) {
		return nil, fmt.Errorf("路由模型 %s 指向的 Chat 渠道不存在", route.Alias)
	}

	upstream := cfg.ChatUpstream[route.ChannelIndex].Clone()
	if config.GetChannelStatus(upstream) != config.ChannelStatusActive || !config.IsChannelSchedulable(upstream) {
		return nil, fmt.Errorf("路由模型 %s 指向的 Chat 渠道不可用", route.Alias)
	}

	keyExists := false
	for _, apiKey := range upstream.APIKeys {
		if apiKey == route.APIKey {
			keyExists = true
			break
		}
	}
	if !keyExists {
		return nil, fmt.Errorf("路由模型 %s 指向的 API Key 已不存在", route.Alias)
	}

	upstream.BaseURL = route.BaseURL
	upstream.BaseURLs = nil
	upstream.APIKeys = []string{route.APIKey}
	upstream.ModelMapping = nil
	upstream.DefaultModel = ""
	return upstream, nil
}

func buildChatUpstreamRequest(c *gin.Context, upstream *config.UpstreamConfig, apiKey string, originalBody []byte) (*http.Request, error) {
	bodyBytes, err := applyChatModelMapping(originalBody, upstream)
	if err != nil {
		return nil, err
	}

	url := buildChatCompletionsURL(upstream.GetEffectiveBaseURL(), upstream.UseFullURL)
	req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("创建 Chat 请求失败: %w", err)
	}

	req.Header = utils.PrepareUpstreamHeaders(c, req.URL.Host)
	utils.SetAuthenticationHeader(req.Header, apiKey)

	return req, nil
}

func applyChatModelMapping(bodyBytes []byte, upstream *config.UpstreamConfig) ([]byte, error) {
	var payload map[string]interface{}
	decoder := json.NewDecoder(bytes.NewReader(bodyBytes))
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil {
		return nil, fmt.Errorf("解析 Chat 请求体失败: %w", err)
	}

	model, ok := payload["model"].(string)
	if !ok || strings.TrimSpace(model) == "" {
		return nil, fmt.Errorf("model is required")
	}
	if mappedModel := config.ResolveUpstreamModel(model, upstream); strings.TrimSpace(mappedModel) != "" {
		payload["model"] = mappedModel
	}
	// 归一化 reasoning_effort：保留兼容网关扩展的 max，并将 ultra 归一化为 max。
	// 未知值显式报错，避免静默删除后让调用方误以为思考档位已经生效。
	if err := normalizeChatReasoningEffort(payload); err != nil {
		return nil, err
	}
	if err := sanitizeOpenAIChatPayloadForUpstream(payload); err != nil {
		if !types.IsUnsupportedProtocol(err) {
			return nil, &types.InvalidRequestError{Err: err}
		}
		return nil, err
	}
	if upstream != nil && upstream.DisablePromptCacheKey {
		delete(payload, "prompt_cache_key")
		delete(payload, "prompt_cache_retention")
	}
	stripChatRoutingMetadata(payload)
	ensureChatStreamUsageOptions(payload)

	return utils.MarshalJSONNoEscape(payload)
}

// normalizeChatReasoningEffort 就地归一化 Chat 请求顶层 reasoning_effort 字段。
// auto 与空字符串同样省略字段，等价于交由上游默认策略。
func normalizeChatReasoningEffort(payload map[string]interface{}) error {
	raw, exists := payload["reasoning_effort"]
	if !exists {
		return nil
	}
	effort, ok := raw.(string)
	if !ok {
		return fmt.Errorf("reasoning_effort 必须是字符串")
	}
	normalized, err := converters.NormalizeReasoningEffortForConstrainedUpstream(strings.TrimSpace(effort))
	if err != nil {
		return err
	}
	switch normalized {
	case "", "auto":
		delete(payload, "reasoning_effort")
	default:
		payload["reasoning_effort"] = normalized
	}
	return nil
}

// stripChatRoutingMetadata removes proxy-only routing fields before forwarding
// the request. Some OpenAI-compatible upstreams reject unknown top-level fields.
func stripChatRoutingMetadata(payload map[string]interface{}) {
	metadata, ok := payload["metadata"].(map[string]interface{})
	if !ok {
		return
	}
	delete(metadata, "channel_index")
	delete(metadata, "purpose")
	if len(metadata) == 0 {
		delete(payload, "metadata")
	}
}

func ensureChatStreamUsageOptions(payload map[string]interface{}) {
	stream, _ := payload["stream"].(bool)
	if !stream {
		return
	}
	options, ok := payload["stream_options"].(map[string]interface{})
	if !ok || options == nil {
		payload["stream_options"] = map[string]interface{}{"include_usage": true}
		return
	}
	if _, exists := options["include_usage"]; !exists {
		options["include_usage"] = true
	}
}

func buildChatCompletionsURL(baseURL string, useFullURL ...bool) string {
	return utils.BuildUpstreamURL(baseURL, "/v1", "/chat/completions", useFullURL...)
}

func handleSuccess(c *gin.Context, resp *http.Response, envCfg *config.EnvConfig, startTime time.Time, isStream bool, originalBody []byte) (*types.Usage, error) {
	defer resp.Body.Close()

	if isStream {
		return handleStreamSuccess(c, resp, envCfg, startTime)
	}

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to read response"})
		return nil, err
	}
	decompressedBytes, wasDecoded, decompressErr := utils.DecompressResponseBodyIfNeeded(resp, bodyBytes, envCfg.Current().MaxRequestBodySize)
	if decompressErr != nil && errors.Is(decompressErr, utils.ErrDecompressedBodyTooLarge) {
		log.Printf("[Chat-Response] 错误: 解压响应体超过大小限制: %v", decompressErr)
		c.JSON(http.StatusBadGateway, gin.H{"error": "Decompressed response body too large"})
		return nil, decompressErr
	}
	if wasDecoded {
		bodyBytes = decompressedBytes
		utils.StripEntityHeadersForRebuiltBody(resp.Header)
		resp.Header.Del("Content-Encoding")
	}

	if envCfg.Current().EnableResponseLogs {
		responseTime := time.Since(startTime).Milliseconds()
		log.Printf("[Chat-Timing] Chat 响应完成: %dms, 状态: %d", responseTime, resp.StatusCode)
		proxycore.LogResponseDetails(resp, bodyBytes, envCfg, "Chat")
	}

	usage := extractChatUsage(bodyBytes)
	if usage != nil && usage.InputTokens == 0 && usage.PromptTokens > 0 {
		usage.InputTokens = usage.PromptTokens
	}
	if usage != nil && usage.OutputTokens == 0 && usage.CompletionTokens > 0 {
		usage.OutputTokens = usage.CompletionTokens
	}
	if usage == nil {
		usage = &types.Usage{InputTokens: utils.EstimateTokens(string(originalBody))}
	}
	bodyBytes, err = hooks.RunAttachedPostResponseHooks(c.Request.Context(), c, bodyBytes, resp)
	if err != nil {
		return nil, err
	}

	utils.ForwardResponseHeaders(resp.Header, c.Writer)
	proxycore.MarkRequestLogFirstToken(c)
	c.Data(resp.StatusCode, "application/json", bodyBytes)
	return usage, nil
}

func buildChatDirectRequest(c *gin.Context, upstream *config.UpstreamConfig, apiKey string, bodyBytes []byte) (*http.Request, error) {
	var err error
	bodyBytes, err = stripChatPromptCacheFields(bodyBytes, upstream)
	if err != nil {
		return nil, err
	}
	url := buildChatCompletionsURL(upstream.GetEffectiveBaseURL(), upstream.UseFullURL)
	req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("创建 Chat 请求失败: %w", err)
	}

	req.Header = utils.PrepareUpstreamHeaders(c, req.URL.Host)
	utils.SetAuthenticationHeader(req.Header, apiKey)
	return req, nil
}

func stripChatPromptCacheFields(bodyBytes []byte, upstream *config.UpstreamConfig) ([]byte, error) {
	if upstream == nil || !upstream.DisablePromptCacheKey {
		return bodyBytes, nil
	}
	var payload map[string]interface{}
	decoder := json.NewDecoder(bytes.NewReader(bodyBytes))
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil {
		return nil, fmt.Errorf("解析 Chat 请求体失败: %w", err)
	}
	delete(payload, "prompt_cache_key")
	delete(payload, "prompt_cache_retention")
	return utils.MarshalJSONNoEscape(payload)
}

func replaceChatModel(bodyBytes []byte, model string) ([]byte, error) {
	var payload map[string]interface{}
	decoder := json.NewDecoder(bytes.NewReader(bodyBytes))
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil {
		return nil, fmt.Errorf("解析 Chat 请求体失败: %w", err)
	}
	if strings.TrimSpace(model) == "" {
		return nil, fmt.Errorf("路由模型缺少上游模型名")
	}
	payload["model"] = model
	if err := sanitizeOpenAIChatPayloadForUpstream(payload); err != nil {
		return nil, err
	}
	ensureChatStreamUsageOptions(payload)
	return utils.MarshalJSONNoEscape(payload)
}
