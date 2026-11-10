package providers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/insistanan/api-proxy/internal/config"
	"github.com/insistanan/api-proxy/internal/types"
	"github.com/insistanan/api-proxy/internal/utils"
)

// OpenAIProvider OpenAI 提供商
type OpenAIProvider struct {
	hideReasoning          bool
	parseEmbeddedReasoning bool
	reasoningScope         string
}

// ConvertToProviderRequest 转换为 OpenAI 请求。
// R4-a：解析 gin 后委托显式实现 BuildRequest（薄壳）。
func (p *OpenAIProvider) ConvertToProviderRequest(c *gin.Context, upstream *config.UpstreamConfig, apiKey string) (*http.Request, []byte, error) {
	originalBodyBytes, err := io.ReadAll(c.Request.Body)
	if err != nil {
		return nil, nil, fmt.Errorf("读取请求体失败: %w", err)
	}
	// 恢复请求体，以便gin context可以被其他地方再次读取（尽管这里我们已经完全处理了）
	c.Request.Body = io.NopCloser(bytes.NewReader(originalBodyBytes))

	outcome, err := p.BuildRequest(RequestInput{
		Ctx:            c.Request.Context(),
		Method:         http.MethodPost,
		Path:           "/v1/chat/completions",
		Header:         c.Request.Header,
		Body:           originalBodyBytes,
		ConversationID: c.GetString(utils.ContextKeyConversationUserID),
	}, upstream, apiKey)
	if err != nil {
		return nil, originalBodyBytes, err
	}
	return outcome.Request, outcome.OriginalBody, nil
}

// BuildRequest 以显式输入构造 OpenAI Chat 请求（R4-a 主实现，不依赖 gin）。
func (p *OpenAIProvider) BuildRequest(input RequestInput, upstream *config.UpstreamConfig, apiKey string) (*RequestOutcome, error) {
	originalBodyBytes := input.Body
	if err := config.ValidateReasoningOptions(upstream); err != nil {
		return nil, err
	}
	p.hideReasoning = upstream != nil && upstream.ReasoningOutputMode == "hide"
	p.parseEmbeddedReasoning = upstream != nil && upstream.ParseEmbeddedReasoning

	var claudeReq types.ClaudeRequest
	if err := json.Unmarshal(originalBodyBytes, &claudeReq); err != nil {
		return nil, fmt.Errorf("解析Claude请求体失败: %w", err)
	}
	p.reasoningScope = ReasoningCacheScope(input.ConversationID, upstream, claudeReq.Model)
	messages, err := p.convertMessages(&claudeReq, upstream != nil && upstream.RequireReasoningContent)
	if err != nil {
		return nil, err
	}

	// Chat 推理模型要求将 reasoning_content 原样随 assistant 历史消息回传。
	// 因此所有客户端统一保留历史 thinking，避免严格上游在下一轮拒绝请求。

	openaiReq := &types.OpenAIRequest{
		Model:       config.ResolveUpstreamModel(claudeReq.Model, upstream),
		Messages:    messages,
		Stream:      claudeReq.Stream,
		Temperature: claudeReq.Temperature,
	}
	// top_p / stop_sequences：与 temperature 同级的采样参数直传
	//（cc-switch transform.rs 同映射；0 值/空数组省略避免触发部分网关的严格校验）。
	if claudeReq.TopP > 0 {
		openaiReq.TopP = claudeReq.TopP
	}
	if len(claudeReq.StopSequences) > 0 {
		openaiReq.Stop = claudeReq.StopSequences
	}
	if claudeReq.Stream {
		openaiReq.StreamOptions = map[string]interface{}{"include_usage": true}
	}

	// 只发送一个 token 限制字段，避免部分 OpenAI 兼容网关因同时收到
	// max_tokens 和 max_completion_tokens 而返回 invalid_request。
	p.applyTokenLimit(openaiReq, &claudeReq, upstream)

	// 转换工具
	if len(claudeReq.Tools) > 0 {
		openaiReq.Tools = p.convertTools(claudeReq.Tools)
	}

	// 转换 tool_choice
	openaiReq.ToolChoice = p.convertToolChoice(claudeReq.ToolChoice)
	if len(openaiReq.Tools) == 0 {
		openaiReq.ToolChoice = nil
	}

	// 转换 thinking / output_config.effort → reasoning_effort
	// "auto" 在 OpenAI Chat Completions 无等价字段：省略即交由上游默认策略，避免硬塞无效值触发 400。
	if effort := resolveClaudeReasoningEffort(&claudeReq); effort != "" && effort != "auto" {
		openaiReq.ReasoningEffort = effort
	}
	// 稳定 prompt_cache_key：提升 OpenAI/兼容网关的缓存亲和；不支持端通常忽略。
	if upstream == nil || !upstream.DisablePromptCacheKey {
		openaiReq.PromptCacheKey = buildClaudeChatPromptCacheKey(&claudeReq, upstream, openaiReq.Model)
	}
	// --- 转换逻辑结束 ---

	reqBodyBytes, err := json.Marshal(openaiReq)
	if err != nil {
		return nil, fmt.Errorf("序列化OpenAI请求体失败: %w", err)
	}

	// 构建URL（"#"后缀与版本前缀约定见 utils.BuildUpstreamURL）
	url := utils.BuildUpstreamURL(upstream.GetEffectiveBaseURL(), "/v1", "/chat/completions", upstream.UseFullURL)

	req, err := http.NewRequestWithContext(input.Ctx, http.MethodPost, url, bytes.NewReader(reqBodyBytes))
	if err != nil {
		return nil, fmt.Errorf("创建OpenAI请求失败: %w", err)
	}

	// 使用统一的头部处理逻辑（透明代理）
	// 保留客户端的大部分 headers，只移除/替换必要的认证和代理相关 headers
	req.Header = utils.PrepareUpstreamHeadersFromHeader(input.Header, req.URL.Host)

	// 删除开发测试注入的客户端请求头，避免上游严格验证报错。
	req.Header.Del("X-Codex-Window-Id")
	req.Header.Del("X-Codex-Installation-Id")
	req.Header.Del("X-Request-Id")
	req.Header.Del("X-Codex-Turn-Metadata")
	req.Header.Del("X-Claude-Code-Session-Id")
	req.Header.Del("X-Stainless-Lang")
	req.Header.Del("X-Stainless-Runtime")
	req.Header.Del("X-Stainless-Runtime-Version")
	req.Header.Del("X-Stainless-Os")
	req.Header.Del("X-Stainless-Arch")
	req.Header.Del("X-Stainless-Package-Version")
	req.Header.Del("X-Stainless-Retry-Count")
	req.Header.Del("X-Stainless-Timeout")
	req.Header.Del("X-App")

	utils.SetAuthenticationHeader(req.Header, apiKey)
	utils.PrepareTargetIdentity(req.Header, "openai", false)

	return &RequestOutcome{Request: req, OriginalBody: originalBodyBytes}, nil
}
