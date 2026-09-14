package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/BenedictKing/api-proxy/internal/config"
	"github.com/BenedictKing/api-proxy/internal/converters"
	"github.com/BenedictKing/api-proxy/internal/session"
	"github.com/BenedictKing/api-proxy/internal/types"
	"github.com/BenedictKing/api-proxy/internal/utils"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// ResponsesProvider Responses API 提供商
type ResponsesProvider struct {
	SessionManager *session.SessionManager
}

// ConvertToProviderRequest 将 Responses 请求转换为上游格式。
// R4-a：解析 gin 后委托显式实现 BuildRequest（薄壳）；转换产物
// （previous_response_id 移除 / 历史边界）写回 gin context 供响应出口消费。
func (p *ResponsesProvider) ConvertToProviderRequest(
	c *gin.Context,
	upstream *config.UpstreamConfig,
	apiKey string,
) (*http.Request, []byte, error) {
	// 同一请求可能在多个渠道/Key 间 failover；不能让前一个候选设置的
	// “已移除 previous_response_id”状态污染后续候选。
	c.Set(utils.ContextKeyResponsesPreviousIDDropped, false)
	c.Set(utils.ContextKeyResponsesHistoryBoundary, false)
	// 读取原始请求体
	bodyBytes, err := io.ReadAll(c.Request.Body)
	if err != nil {
		return nil, nil, fmt.Errorf("读取请求体失败: %w", err)
	}
	c.Request.Body = io.NopCloser(bytes.NewReader(bodyBytes))

	conversationID := ""
	if value, exists := c.Get(utils.ContextKeyConversationUserID); exists {
		conversationID, _ = value.(string)
	}

	outcome, err := p.BuildRequest(RequestInput{
		Ctx:            c.Request.Context(),
		Method:         http.MethodPost,
		Path:           "/v1/responses",
		Header:         c.Request.Header,
		Body:           bodyBytes,
		ConversationID: conversationID,
		CodexDisguise:  c.GetBool(utils.ContextKeyCodexDisguise),
	}, upstream, apiKey)
	if err != nil {
		if outcome != nil {
			c.Set(utils.ContextKeyResponsesPreviousIDDropped, outcome.PreviousIDDropped)
			c.Set(utils.ContextKeyResponsesHistoryBoundary, outcome.HistoryBoundary)
		}
		return nil, bodyBytes, err
	}
	c.Set(utils.ContextKeyResponsesPreviousIDDropped, outcome.PreviousIDDropped)
	c.Set(utils.ContextKeyResponsesHistoryBoundary, outcome.HistoryBoundary)
	return outcome.Request, outcome.OriginalBody, nil
}

// BuildRequest 以显式输入构造 Responses 上游请求（R4-a 主实现，不依赖 gin）。
// 转换产物经 RequestOutcome 返回（错误路径同样返回部分产物供调用方消费）。
func (p *ResponsesProvider) BuildRequest(input RequestInput, upstream *config.UpstreamConfig, apiKey string) (*RequestOutcome, error) {
	bodyBytes := input.Body
	outcome := &RequestOutcome{OriginalBody: bodyBytes}

	var reqBody []byte
	targetModel := ""
	isStream := false

	// 根据 Responses 上游协议转换请求体
	if upstream.ServiceType == converters.ResponsesUpstreamResponses {
		var responsesReq types.ResponsesRequest
		if err := json.Unmarshal(bodyBytes, &responsesReq); err != nil {
			return outcome, fmt.Errorf("解析 Responses 请求失败: %w", err)
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
		var sess *session.Session
		var err error
		if p.SessionManager != nil && strings.TrimSpace(effectiveRequest.PreviousResponseID) != "" {
			sess, err = p.SessionManager.GetOrCreateSessionForConversation(effectiveRequest.PreviousResponseID, conversationID)
			if err != nil {
				return outcome, fmt.Errorf("获取 Responses 透传会话失败: %w", err)
			}
		}
		reqBody, err = converters.ConvertResponsesRequestToUpstream(upstream.ServiceType, model, effectiveBody, isStream, sess, &effectiveRequest, upstream)
		if err != nil {
			return outcome, err
		}
		if upstream.ServiceType == converters.ResponsesUpstreamResponses &&
			strings.TrimSpace(responsesReq.PreviousResponseID) != "" &&
			gjson.GetBytes(reqBody, "previous_response_id").String() == "" {
			outcome.PreviousIDDropped = true
			outcome.HistoryBoundary = true
		}
	} else {
		var responsesReq types.ResponsesRequest
		if err := json.Unmarshal(bodyBytes, &responsesReq); err != nil {
			return outcome, fmt.Errorf("解析 Responses 请求失败: %w", err)
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
		var sess *session.Session
		if p.SessionManager == nil {
			return outcome, fmt.Errorf("Responses 会话管理器未初始化")
		}
		var err error
		sess, err = p.SessionManager.GetOrCreateSessionForConversation(effectiveRequest.PreviousResponseID, conversationID)
		if err != nil {
			return outcome, fmt.Errorf("获取会话失败: %w", err)
		}
		if historyBoundary {
			// 当前 input 已经是客户端提供的新历史根；旧 session 只用于
			// 响应完成后替换本地状态，不能参与本次上游请求转换。
			sess = nil
		} else if converters.ResponsesInputRerootsSession(sess, effectiveRequest.Input) {
			// input 形态判断不了客户端自己压缩上下文后的形态（摘要成新根 + 重发最近
			// 几轮），只有与本地会话的重叠能认出来，而这个判断必须等 session 载入后
			// 才能做。漏掉这里会让转换器把压缩前的整段历史重新前置到新根之前。
			outcome.HistoryBoundary = true
			if strings.TrimSpace(effectiveRequest.PreviousResponseID) != "" {
				outcome.PreviousIDDropped = true
			}
			effectiveRequest.PreviousResponseID = ""
			effectiveBody, err = removeResponsesPreviousResponseID(effectiveBody)
			if err != nil {
				return outcome, err
			}
			sess = nil
		}

		// 模型重定向
		effectiveRequest.Model = config.ResolveUpstreamModel(effectiveRequest.Model, upstream)
		targetModel = effectiveRequest.Model

		reqBody, err = converters.ConvertResponsesRequestToUpstream(upstream.ServiceType, effectiveRequest.Model, effectiveBody, effectiveRequest.Stream, sess, &effectiveRequest, upstream)
		if err != nil {
			return outcome, err
		}
	}

	// 构建 HTTP 请求
	targetURL := p.buildTargetURLWithModel(upstream, targetModel, isStream)
	req, err := http.NewRequestWithContext(input.Ctx, http.MethodPost, targetURL, bytes.NewReader(reqBody))
	if err != nil {
		return outcome, err
	}

	// 设置请求头（透明代理）
	// 使用统一的头部处理逻辑，保留客户端的大部分 headers
	req.Header = utils.PrepareUpstreamHeadersFromHeader(input.Header, req.URL.Host)

	if input.CodexDisguise {
		utils.ApplyCodexDisguise(req.Header, isStream)
	}

	// 根据 ServiceType 设置对应的认证头（会覆盖客户端的认证头）
	switch upstream.ServiceType {
	case "gemini":
		// 只有 Gemini 使用特殊的认证头
		utils.SetGeminiAuthenticationHeader(req.Header, apiKey)
	default:
		// claude, responses, openai 等都使用 Authorization: Bearer
		utils.SetAuthenticationHeader(req.Header, apiKey)
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
	return utils.ResponsesItemsContainCompactionFromInput(req.Input) ||
		converters.ResponsesInputCarriesOwnHistory(req.Input)
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

	// "#"后缀与版本前缀约定见 utils.BuildUpstreamURL
	return utils.BuildUpstreamURL(upstream.BaseURL, "/v1", endpoint)
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

// HandleStreamResponse 处理流式响应（暂不实现）
func (p *ResponsesProvider) HandleStreamResponse(body io.ReadCloser) (<-chan string, <-chan error, error) {
	return nil, nil, fmt.Errorf("Responses Provider 暂不支持流式响应")
}

// HandleStreamResponseCtx 处理流式响应（暂不实现）
func (p *ResponsesProvider) HandleStreamResponseCtx(ctx context.Context, body io.ReadCloser) (<-chan string, <-chan error, error) {
	return nil, nil, fmt.Errorf("Responses Provider 暂不支持流式响应")
}
