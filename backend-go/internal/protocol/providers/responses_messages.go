package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"insistanan/api-proxy/internal/dialogue/conversation"
	"insistanan/api-proxy/internal/infra/upstreamreq"
	"insistanan/api-proxy/internal/protocol/codec"
	"insistanan/api-proxy/internal/protocol/converters"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"insistanan/api-proxy/internal/infra/config"
	"insistanan/api-proxy/internal/orchestration/upstreamcontrol"
	"insistanan/api-proxy/internal/protocol/types"
)

// MessagesResponsesProvider 将 Claude Messages 入口请求转换为 OpenAI Responses 上游协议。
// 字段仅在单次请求生命周期内使用（handler 内同一实例会串联 Convert/Stream）。
type MessagesResponsesProvider struct {
	cacheLease     *conversation.RequestLease
	conversationID string
	claudeReq      *types.ClaudeRequest
	upstream       *config.UpstreamConfig
	resolvedModel  string
	enableChain    bool
	chainIdentity  upstreamcontrol.Identity
}

type claudeResponsesRequest struct {
	Model                string        `json:"model"`
	Instructions         string        `json:"instructions,omitempty"`
	Input                []interface{} `json:"input"`
	Stream               bool          `json:"stream"`
	MaxOutputTokens      int           `json:"max_output_tokens,omitempty"`
	Temperature          *float64      `json:"temperature,omitempty"`
	TopP                 *float64      `json:"top_p,omitempty"`
	Stop                 []string      `json:"stop,omitempty"`
	Tools                []interface{} `json:"tools,omitempty"`
	ToolChoice           interface{}   `json:"tool_choice,omitempty"`
	Reasoning            interface{}   `json:"reasoning,omitempty"`
	PromptCacheKey       string        `json:"prompt_cache_key,omitempty"`
	PromptCacheRetention string        `json:"prompt_cache_retention,omitempty"`
	PreviousResponseID   string        `json:"previous_response_id,omitempty"`
}

func (p *MessagesResponsesProvider) ConvertToProviderRequest(c *gin.Context, upstream *config.UpstreamConfig, apiKey string) (*http.Request, []byte, error) {
	originalBodyBytes, err := io.ReadAll(c.Request.Body)
	if err != nil {
		return nil, nil, fmt.Errorf("读取请求体失败: %w", err)
	}
	c.Request.Body = io.NopCloser(bytes.NewReader(originalBodyBytes))

	// 主流程已经把经过 registry 解析的内部 conversation ID 写入 context。
	conversationID := ""
	if value, exists := c.Get(upstreamreq.ContextKeyConversationUserID); exists {
		conversationID, _ = value.(string)
	}

	outcome, err := p.BuildRequest(RequestInput{
		Ctx:            c.Request.Context(),
		Method:         http.MethodPost,
		Path:           "/v1/responses",
		Header:         c.Request.Header,
		Body:           originalBodyBytes,
		Query:          func(key string) string { return c.Query(key) },
		ConversationID: conversationID,
		CodexDisguise:  c.GetBool(upstreamreq.ContextKeyCodexDisguise),
	}, upstream, apiKey)
	if err != nil {
		return nil, originalBodyBytes, err
	}
	return outcome.Request, outcome.OriginalBody, nil
}

// BuildRequest 以显式输入构造 Responses 上游请求（R4-a 主实现，不依赖 gin）。
func (p *MessagesResponsesProvider) buildRequest(input RequestInput, upstream *config.UpstreamConfig, apiKey string) (*RequestOutcome, error) {
	originalBodyBytes := input.Body
	if err := converters.ValidateClaudeConversion(input.Body, "responses"); err != nil {
		return nil, err
	}

	var claudeReq types.ClaudeRequest
	if err := json.Unmarshal(originalBodyBytes, &claudeReq); err != nil {
		return nil, fmt.Errorf("解析Claude请求体失败: %w", err)
	}

	p.cacheLease = conversation.RequestLeaseFrom(input.Ctx)
	// 链缓存与删除共用内部会话 ID；不自行从客户端字段猜测第二套身份。
	conversationID := strings.TrimSpace(input.ConversationID)
	p.conversationID = conversationID
	p.claudeReq = &claudeReq
	p.upstream = upstream
	p.resolvedModel = config.ResolveUpstreamModel(claudeReq.Model, upstream)
	p.enableChain = upstream != nil && upstream.EnablePreviousResponseID && conversationID != ""
	p.chainIdentity = upstream.RequestIdentity(apiKey)
	responsesReq, err := claudeRequestToResponsesRequest(&claudeReq, upstream, conversationID, p.chainIdentity)
	if err != nil {
		return nil, err
	}
	reqBodyBytes, err := codec.MarshalJSONNoEscape(responsesReq)
	if err != nil {
		return nil, fmt.Errorf("序列化Responses请求体失败: %w", err)
	}

	req, err := http.NewRequestWithContext(input.Ctx, http.MethodPost, upstreamreq.BuildUpstreamURL(upstream.GetEffectiveBaseURL(), "/v1", "/responses", upstream.UseFullURL), bytes.NewReader(reqBodyBytes))
	if err != nil {
		return nil, fmt.Errorf("创建Responses请求失败: %w", err)
	}
	req.Header = upstreamreq.PrepareUpstreamHeadersFromHeader(input.Header, req.URL.Host)
	compatibility := upstream.UseClientCompatibility(input.CodexDisguise)
	upstreamreq.PrepareTargetIdentity(req.Header, "responses", compatibility)
	if compatibility {
		upstreamreq.SeedDisguiseSession(req.Header, conversationID, "responses")
		upstreamreq.ApplyCodexDisguise(req.Header, claudeReq.Stream)
	}
	upstreamreq.SetAuthenticationHeader(req.Header, apiKey)
	req.Header.Set("Content-Type", "application/json")

	return &RequestOutcome{Request: req, OriginalBody: originalBodyBytes}, nil
}

func (p *MessagesResponsesProvider) ConvertToClaudeResponse(providerResp *types.ProviderResponse) (*types.ClaudeResponse, error) {
	var responsesResp types.ResponsesResponse
	if err := json.Unmarshal(providerResp.Body, &responsesResp); err != nil {
		return nil, err
	}
	if p != nil && p.enableChain && p.claudeReq != nil {
		p.cacheLease.Store(func() {
			rememberResponsesChainFromBody(p.conversationID, p.claudeReq, p.upstream, p.resolvedModel, providerResp.Body, p.chainIdentity)
		})
	}
	return responsesResponseToClaude(&responsesResp)
}

func (p *MessagesResponsesProvider) HandleStreamResponseCtx(ctx context.Context, body io.ReadCloser) (<-chan string, <-chan error, error) {
	pump := newStreamPump(ctx)
	eventChan, errChan := pump.eventChan, pump.errChan

	conversationID := p.conversationID
	claudeReq := p.claudeReq
	upstream := p.upstream
	resolvedModel := p.resolvedModel
	enableChain := p.enableChain
	chainIdentity := p.chainIdentity

	go func() {
		defer close(eventChan)
		defer close(errChan)
		defer codec.BindStreamBody(ctx, body)()

		send := pump.send
		fail := pump.fail

		state := newResponsesToClaudeStreamState()
		scanner := pump.newScanner(body)

		for scanner.Scan() {
			select {
			case <-ctx.Done():
				return
			default:
			}
			line, err := scanner.DataLine()
			if err != nil {
				fail(err)
				return
			}
			if payload, isData := codec.ParseSSEDataLine(line); isData && payload == codec.SSEDoneMarker {
				break
			}
			if line == "" {
				continue
			}
			for _, event := range state.processLine(line) {
				if !send(event) {
					return
				}
			}
			if state.completed || state.failed {
				break
			}
		}
		// 流内失败（response.failed / 顶层 error）：经 errChan 上报由消费端统一
		// 写错误事件并补终止序列；不伪造 message_stop，也不登记链/缓存。
		if state.failed {
			fail(NewUpstreamStreamFailedError(state.capacityError, state.failedMessage))
			return
		}
		if !pump.finishRead(state.completed, scanner.Err()) {
			return
		}
		for _, event := range state.finish() {
			if !send(event) {
				return
			}
		}

		if enableChain && state.completed && claudeReq != nil && state.upstreamResponseID != "" {
			p.cacheLease.Store(func() {
				rememberResponsesChain(conversationID, claudeReq, upstream, resolvedModel,
					state.upstreamResponseID, state.outputFingerprint(), chainIdentity)
			})
		}
	}()

	return eventChan, errChan, nil
}
