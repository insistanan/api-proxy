package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
	"insistanan/api-proxy/internal/dialogue/session"
	"insistanan/api-proxy/internal/infra/config"
	"insistanan/api-proxy/internal/infra/upstreamreq"
	"insistanan/api-proxy/internal/protocol/converters"
	"insistanan/api-proxy/internal/protocol/items"
	"insistanan/api-proxy/internal/protocol/types"
)

// ResponsesProvider Responses API 提供商
type ResponsesProvider struct {
	SessionManager *session.SessionManager
}

// ConvertToProviderRequest 将 Responses 请求转换为上游格式。
// 解析入站请求后交给 BuildRequest；转换产物
// （previous_response_id 移除 / 历史边界）写回 gin context 供响应出口消费。
func (p *ResponsesProvider) ConvertToProviderRequest(
	c *gin.Context,
	upstream *config.UpstreamConfig,
	apiKey string,
) (*http.Request, []byte, error) {
	// 同一请求可能在多个渠道/Key 间 failover；不能让前一个候选设置的
	// “已移除 previous_response_id”状态污染后续候选。
	c.Set(upstreamreq.ContextKeyResponsesPreviousIDDropped, false)
	c.Set(upstreamreq.ContextKeyResponsesHistoryBoundary, false)
	c.Set(responseToolContextKey, (*converters.ResponseToolContext)(nil))
	// 读取原始请求体
	bodyBytes, err := io.ReadAll(c.Request.Body)
	if err != nil {
		return nil, nil, fmt.Errorf("读取请求体失败: %w", err)
	}
	c.Request.Body = io.NopCloser(bytes.NewReader(bodyBytes))

	conversationID := ""
	if value, exists := c.Get(upstreamreq.ContextKeyConversationUserID); exists {
		conversationID, _ = value.(string)
	}

	outcome, err := p.BuildRequest(RequestInput{
		Ctx:                c.Request.Context(),
		Method:             http.MethodPost,
		Path:               "/v1/responses",
		Header:             c.Request.Header,
		Body:               bodyBytes,
		ConversationID:     conversationID,
		ResponseSession:    ResponseSessionForRequest(c),
		CodexDisguise:      c.GetBool(upstreamreq.ContextKeyCodexDisguise),
		ClaudeCodeDisguise: c.GetBool(upstreamreq.ContextKeyClaudeCodeDisguise),
	}, upstream, apiKey)
	if err != nil {
		if outcome != nil {
			c.Set(upstreamreq.ContextKeyResponsesPreviousIDDropped, outcome.PreviousIDDropped)
			c.Set(upstreamreq.ContextKeyResponsesHistoryBoundary, outcome.HistoryBoundary)
		}
		return nil, bodyBytes, err
	}
	c.Set(upstreamreq.ContextKeyResponsesPreviousIDDropped, outcome.PreviousIDDropped)
	c.Set(upstreamreq.ContextKeyResponsesHistoryBoundary, outcome.HistoryBoundary)
	c.Set(responseToolContextKey, outcome.ResponseTools)
	return outcome.Request, outcome.OriginalBody, nil
}

// BuildRequest 以显式输入构造 Responses 上游请求（不依赖 Gin）。
// 转换产物经 RequestOutcome 返回（错误路径同样返回部分产物供调用方消费）。
func (p *ResponsesProvider) buildRequest(input RequestInput, upstream *config.UpstreamConfig, apiKey string) (*RequestOutcome, error) {
	bodyBytes := input.Body
	outcome := &RequestOutcome{OriginalBody: bodyBytes}

	var reqBody []byte
	targetModel := ""
	isStream := false

	// 根据 Responses 上游协议转换请求体
	if upstream.ServiceType == converters.ResponsesUpstreamResponses {
		var responsesReq types.ResponsesRequest
		if err := json.Unmarshal(bodyBytes, &responsesReq); err != nil {
			return outcome, &types.InvalidRequestError{Err: fmt.Errorf("解析 Responses 请求失败: %w", err)}
		}
		model := config.ResolveUpstreamModel(responsesReq.Model, upstream)
		targetModel = model
		isStream = converters.ResponsesRequestStream(bodyBytes)
		historyBoundary := responsesRequestStartsNewHistoryBoundary(&responsesReq)
		effectiveRequest := responsesReq
		effectiveBody := bodyBytes
		if historyBoundary {
			outcome.HistoryBoundary = true
			outcome.PreviousIDDropped = true
			effectiveRequest.PreviousResponseID = ""
			var err error
			effectiveBody, err = removeResponsesPreviousResponseID(bodyBytes)
			if err != nil {
				return outcome, err
			}
		}
		conversationID := input.ConversationID
		sess := input.ResponseSession
		var err error
		if sess == nil && p.SessionManager != nil && strings.TrimSpace(effectiveRequest.PreviousResponseID) != "" {
			sess, err = p.SessionManager.GetOrCreateSessionForConversation(input.Ctx, effectiveRequest.PreviousResponseID, conversationID)
			if err != nil {
				return outcome, fmt.Errorf("获取 Responses 透传会话失败: %w", err)
			}
		}
		owner := upstream.RequestIdentity(apiKey).Fingerprint()
		restoredBody, restored, restoreErr := restoreNativeResponseHistory(input.Ctx, p.SessionManager, sess, &effectiveRequest, effectiveBody, owner)
		if restoreErr != nil {
			return outcome, restoreErr
		}
		if restored {
			effectiveBody = restoredBody
			outcome.PreviousIDDropped = true
			// 仅切换服务端链，本地历史继续合并；不能用客户端后缀替换完整会话。
			sess = nil
		}
		reqBody, err = converters.ConvertResponsesRequestToUpstream(upstream.ServiceType, model, effectiveBody, isStream, sess, &effectiveRequest, upstream)
		if err != nil {
			return outcome, err
		}
		if upstream.ServiceType == converters.ResponsesUpstreamResponses &&
			!restored &&
			strings.TrimSpace(responsesReq.PreviousResponseID) != "" &&
			gjson.GetBytes(reqBody, "previous_response_id").String() == "" {
			outcome.PreviousIDDropped = true
			outcome.HistoryBoundary = true
		}
	} else {
		var responsesReq types.ResponsesRequest
		if err := json.Unmarshal(bodyBytes, &responsesReq); err != nil {
			return outcome, &types.InvalidRequestError{Err: fmt.Errorf("解析 Responses 请求失败: %w", err)}
		}

		isStream = responsesReq.Stream

		conversationID := input.ConversationID
		historyBoundary := responsesRequestStartsNewHistoryBoundary(&responsesReq)
		effectiveRequest := responsesReq
		effectiveBody := bodyBytes
		if historyBoundary {
			outcome.HistoryBoundary = true
			outcome.PreviousIDDropped = true
			effectiveRequest.PreviousResponseID = ""
			var err error
			effectiveBody, err = removeResponsesPreviousResponseID(bodyBytes)
			if err != nil {
				return outcome, err
			}
		}
		// 获取或创建会话
		sess := input.ResponseSession
		if p.SessionManager == nil {
			return outcome, fmt.Errorf("Responses 会话管理器未初始化")
		}
		var err error
		if sess == nil {
			sess, err = p.SessionManager.GetOrCreateSessionForConversation(input.Ctx, effectiveRequest.PreviousResponseID, conversationID)
			if err != nil {
				return outcome, fmt.Errorf("获取会话失败: %w", err)
			}
		}
		if historyBoundary {
			// 当前 input 已经是客户端提供的新历史根；旧 session 只用于
			// 响应完成后替换本地状态，不能参与本次上游请求转换。
			sess = nil
		}

		if err := validateResponseHistoryBoundary(sess, effectiveRequest.PreviousResponseID); err != nil {
			return outcome, err
		}

		// 模型重定向
		effectiveRequest.Model = config.ResolveUpstreamModel(effectiveRequest.Model, upstream)
		targetModel = effectiveRequest.Model

		reqBody, err = converters.ConvertResponsesRequestToUpstream(upstream.ServiceType, effectiveRequest.Model, effectiveBody, effectiveRequest.Stream, sess, &effectiveRequest, upstream)
		if err != nil {
			return outcome, err
		}
		outcome.ResponseTools, err = converters.NewResponseToolContext(&effectiveRequest, sess)
		if err != nil {
			return outcome, err
		}
	}

	// 构建 HTTP 请求
	targetURL := p.buildTargetURLWithModel(upstream, targetModel, isStream)
	req, err := http.NewRequestWithContext(input.Ctx, http.MethodPost, targetURL, nil)
	if err != nil {
		return outcome, err
	}

	// 设置请求头（透明代理）
	// 使用统一的头部处理逻辑，保留客户端的大部分 headers
	req.Header = upstreamreq.PrepareUpstreamHeadersFromHeader(input.Header, req.URL.Host)

	inherited := input.CodexDisguise
	if upstream.ServiceType == "claude" {
		inherited = input.ClaudeCodeDisguise
	}
	compatibility := upstream.UseClientCompatibility(inherited) && (upstream.ServiceType == "responses" || upstream.ServiceType == "claude")
	upstreamreq.PrepareTargetIdentity(req.Header, upstream.ServiceType, compatibility)
	if compatibility {
		upstreamreq.SeedDisguiseSession(req.Header, input.ConversationID, upstream.ServiceType)
		if upstream.ServiceType == "claude" {
			upstreamreq.ApplyClaudeCodeDisguise(req.Header, isStream)
			reqBody = upstreamreq.ApplyClaudeCodeBodyDisguise(reqBody, req.Header.Get("X-Claude-Code-Session-Id"))
		} else {
			upstreamreq.ApplyCodexDisguise(req.Header, isStream)
		}
	}
	// 伪装会改写正文；首次发送、Content-Length 和失败重试必须使用同一份最终载荷。
	req.Body = io.NopCloser(bytes.NewReader(reqBody))
	req.ContentLength = int64(len(reqBody))
	req.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(reqBody)), nil }
	req.Header.Del("Content-Length")

	// 根据 ServiceType 设置对应的认证头（会覆盖客户端的认证头）
	switch upstream.ServiceType {
	case "gemini":
		// 只有 Gemini 使用特殊的认证头
		upstreamreq.SetGeminiAuthenticationHeader(req.Header, apiKey)
	default:
		// claude, responses, openai 等都使用 Authorization: Bearer
		upstreamreq.SetAuthenticationHeader(req.Header, apiKey)
	}

	// 确保 Content-Type 正确
	req.Header.Set("Content-Type", "application/json")

	outcome.Request = req
	return outcome, nil
}

func responsesRequestStartsNewHistoryBoundary(req *types.ResponsesRequest) bool {
	if req == nil {
		return false
	}
	return respitems.ResponsesItemsContainCompactionFromInput(req.Input)
}

func removeResponsesPreviousResponseID(bodyBytes []byte) ([]byte, error) {
	if !gjson.GetBytes(bodyBytes, "previous_response_id").Exists() {
		return bodyBytes, nil
	}
	trimmed, err := sjson.DeleteBytes(bodyBytes, "previous_response_id")
	if err != nil {
		return nil, fmt.Errorf("删除 Responses previous_response_id 失败: %w", err)
	}
	return trimmed, nil
}

// buildTargetURL 根据上游类型构建目标 URL
// 智能拼接逻辑：
// 完整地址模式直接使用配置值；自动补全模式遵循以下规则：
// 1. 如果 baseURL 以 # 结尾，跳过自动添加 /v1
// 2. 如果 baseURL 已包含版本号后缀（如 /v1, /v2, /v8, /v1beta），直接拼接端点路径
// 3. 如果 baseURL 不包含版本号后缀，自动添加 /v1 再拼接端点路径
func (p *ResponsesProvider) buildTargetURL(upstream *config.UpstreamConfig) string {
	return p.buildTargetURLWithModel(upstream, "", false)
}

func (p *ResponsesProvider) buildTargetURLWithModel(upstream *config.UpstreamConfig, model string, stream bool) string {
	// 根据 ServiceType 确定端点路径
	var endpoint string
	switch upstream.ServiceType {
	case "responses":
		endpoint = "/responses"
	case "claude":
		endpoint = "/messages"
	case "gemini":
		if model == "" {
			model = "gemini-pro"
		}
		action := "generateContent"
		if stream {
			action = "streamGenerateContent?alt=sse"
		}
		endpoint = fmt.Sprintf("/models/%s:%s", model, action)
	default:
		endpoint = "/chat/completions"
	}

	// "#"后缀与版本前缀约定见 upstreamreq.BuildUpstreamURL
	return upstreamreq.BuildUpstreamURL(upstream.GetEffectiveBaseURL(), "/v1", endpoint, upstream.UseFullURL)
}

// ConvertToClaudeResponse 将上游响应转换为 Responses 格式（实际上不再需要 Claude 格式）
func (p *ResponsesProvider) ConvertToClaudeResponse(providerResp *types.ProviderResponse) (*types.ClaudeResponse, error) {
	// 这个方法在 ResponsesHandler 中不会被调用，这里提供兼容性实现
	return nil, fmt.Errorf("ResponsesProvider 不支持 ConvertToClaudeResponse")
}

// ConvertToResponsesResponse 将上游响应转换为 Responses 格式
func (p *ResponsesProvider) ConvertToResponsesResponse(
	providerResp *types.ProviderResponse,
	upstreamType string,
	sessionID string,
) (*types.ResponsesResponse, error) {
	return converters.ConvertUpstreamResponseToResponses(upstreamType, nil, providerResp.Body, sessionID)
}

// HandleStreamResponseCtx 处理流式响应（暂不实现）
func (p *ResponsesProvider) HandleStreamResponseCtx(ctx context.Context, body io.ReadCloser) (<-chan string, <-chan error, error) {
	return nil, nil, fmt.Errorf("Responses Provider 暂不支持流式响应")
}
