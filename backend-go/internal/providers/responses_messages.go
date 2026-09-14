package providers

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/BenedictKing/api-proxy/internal/config"
	"github.com/BenedictKing/api-proxy/internal/converters"
	"github.com/BenedictKing/api-proxy/internal/session"
	"github.com/BenedictKing/api-proxy/internal/types"
	"github.com/BenedictKing/api-proxy/internal/utils"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/tidwall/gjson"
)

// MessagesResponsesProvider 将 Claude Messages 入口请求转换为 OpenAI Responses 上游协议。
// 字段仅在单次请求生命周期内使用（handler 内同一实例会串联 Convert/Stream）。
type MessagesResponsesProvider struct {
	conversationID string
	claudeReq      *types.ClaudeRequest
	upstream       *config.UpstreamConfig
	resolvedModel  string
	enableChain    bool
}

type claudeResponsesRequest struct {
	Model                string        `json:"model"`
	Instructions         string        `json:"instructions,omitempty"`
	Input                []interface{} `json:"input"`
	Stream               bool          `json:"stream"`
	MaxOutputTokens      int           `json:"max_output_tokens,omitempty"`
	Temperature          float64       `json:"temperature,omitempty"`
	TopP                 float64       `json:"top_p,omitempty"`
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
	if value, exists := c.Get(utils.ContextKeyConversationUserID); exists {
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
	}, upstream, apiKey)
	if err != nil {
		return nil, originalBodyBytes, err
	}
	return outcome.Request, outcome.OriginalBody, nil
}

// BuildRequest 以显式输入构造 Responses 上游请求（R4-a 主实现，不依赖 gin）。
func (p *MessagesResponsesProvider) BuildRequest(input RequestInput, upstream *config.UpstreamConfig, apiKey string) (*RequestOutcome, error) {
	originalBodyBytes := input.Body

	var claudeReq types.ClaudeRequest
	if err := json.Unmarshal(originalBodyBytes, &claudeReq); err != nil {
		return nil, fmt.Errorf("解析Claude请求体失败: %w", err)
	}

	// 主流程已经把经过 registry 解析的内部 conversation ID 写入输入。
	// Responses 链必须使用这个稳定 ID：compact 清理、渠道路由和链状态才能
	// 指向同一条会话。只有直接调用 provider 的旧测试/内部调用没有该值时，
	// 才回退到请求里的外部会话标识。
	conversationID := strings.TrimSpace(input.ConversationID)
	if conversationID == "" {
		conversationID = extractMessagesConversationIDFromInput(input.Header, input.Query, &claudeReq)
	}
	p.conversationID = conversationID
	p.claudeReq = &claudeReq
	p.upstream = upstream
	p.resolvedModel = config.ResolveUpstreamModel(claudeReq.Model, upstream)
	p.enableChain = upstream != nil && upstream.EnablePreviousResponseID && conversationID != ""
	responsesReq, err := claudeRequestToResponsesRequest(&claudeReq, upstream, conversationID)
	if err != nil {
		return nil, err
	}
	reqBodyBytes, err := utils.MarshalJSONNoEscape(responsesReq)
	if err != nil {
		return nil, fmt.Errorf("序列化Responses请求体失败: %w", err)
	}

	req, err := http.NewRequestWithContext(input.Ctx, http.MethodPost, utils.BuildUpstreamURL(upstream.GetEffectiveBaseURL(), "/v1", "/responses"), bytes.NewReader(reqBodyBytes))
	if err != nil {
		return nil, fmt.Errorf("创建Responses请求失败: %w", err)
	}
	req.Header = utils.PrepareUpstreamHeadersFromHeader(input.Header, req.URL.Host)
	utils.SetAuthenticationHeader(req.Header, apiKey)
	req.Header.Set("Content-Type", "application/json")

	return &RequestOutcome{Request: req, OriginalBody: originalBodyBytes}, nil
}

func (p *MessagesResponsesProvider) ConvertToClaudeResponse(providerResp *types.ProviderResponse) (*types.ClaudeResponse, error) {
	var responsesResp types.ResponsesResponse
	if err := json.Unmarshal(providerResp.Body, &responsesResp); err != nil {
		return nil, err
	}
	if p != nil && p.enableChain && p.claudeReq != nil {
		rememberResponsesChainFromBody(p.conversationID, p.claudeReq, p.upstream, p.resolvedModel, providerResp.Body)
	}
	return responsesResponseToClaude(&responsesResp), nil
}

func (p *MessagesResponsesProvider) HandleStreamResponse(body io.ReadCloser) (<-chan string, <-chan error, error) {
	return p.HandleStreamResponseCtx(context.Background(), body)
}

func (p *MessagesResponsesProvider) HandleStreamResponseCtx(ctx context.Context, body io.ReadCloser) (<-chan string, <-chan error, error) {
	pump := newStreamPump(ctx)
	eventChan, errChan := pump.eventChan, pump.errChan

	conversationID := p.conversationID
	claudeReq := p.claudeReq
	upstream := p.upstream
	resolvedModel := p.resolvedModel
	enableChain := p.enableChain

	go func() {
		defer close(eventChan)
		defer close(errChan)
		defer body.Close()

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
			line := strings.TrimSpace(scanner.Text())
			if line == "" || line == "data: [DONE]" {
				continue
			}
			for _, event := range state.processLine(line) {
				send(event)
			}
		}
		// 流内失败（response.failed / 顶层 error）：经 errChan 上报由消费端统一
		// 写错误事件并补终止序列；不伪造 message_stop，也不登记链/缓存。
		if state.failed {
			fail(NewUpstreamStreamFailedError(state.capacityError, state.failedMessage))
			return
		}
		for _, event := range state.finish() {
			send(event)
		}
		if err := scanner.Err(); err != nil {
			if isDisconnectLikeError(err) {
				return
			}
			fail(err)
			return
		}
		// 思考内容按 Claude Messages 契约下发；下一轮转换默认跳过历史
		// thinking，因此不会把它再次送入 Responses 上游。成功完成的本轮
		// 仍登记到代理内部缓存，供故障转移到严格 Chat 渠道时补回。
		state.cacheReasoning()
		if enableChain && state.completed && claudeReq != nil && state.upstreamResponseID != "" {
			rememberResponsesChain(conversationID, claudeReq, upstream, resolvedModel,
				state.upstreamResponseID, state.outputFingerprint())
		}
	}()

	return eventChan, errChan, nil
}

func claudeRequestToResponsesRequest(claudeReq *types.ClaudeRequest, upstream *config.UpstreamConfig, conversationID string) (*claudeResponsesRequest, error) {
	model := config.ResolveUpstreamModel(claudeReq.Model, upstream)
	req := &claudeResponsesRequest{
		Model:  model,
		Input:  claudeMessagesToResponsesInput(claudeReq.Messages, upstream != nil && upstream.IncludeHistoryThinking),
		Stream: claudeReq.Stream,
	}
	if systemText := extractSystemText(claudeReq.System); systemText != "" {
		req.Instructions = systemText
	}
	if claudeReq.MaxCompletionTokens > 0 {
		req.MaxOutputTokens = claudeReq.MaxCompletionTokens
	} else if claudeReq.MaxTokens > 0 {
		req.MaxOutputTokens = claudeReq.MaxTokens
	}
	if claudeReq.Temperature > 0 {
		req.Temperature = claudeReq.Temperature
	}
	// top_p / stop_sequences 直传（cc-switch transform_responses.rs 同映射）。
	if claudeReq.TopP > 0 {
		req.TopP = claudeReq.TopP
	}
	if len(claudeReq.StopSequences) > 0 {
		req.Stop = claudeReq.StopSequences
	}
	if len(claudeReq.Tools) > 0 {
		req.Tools = claudeToolsToResponsesTools(claudeReq.Tools)
	}
	if toolChoice := claudeToolChoiceToResponses(claudeReq.ToolChoice); toolChoice != nil {
		req.ToolChoice = toolChoice
	}
	if reasoning := claudeReasoningToResponsesReasoning(claudeReq); reasoning != nil {
		req.Reasoning = reasoning
	}
	// Claude metadata 仅用于代理内部识别会话。
	// 它在 Responses 中没有等价语义，且许多兼容网关会直接拒绝该字段，因此不向上游透传。
	// prompt_cache_key：默认发送以提升缓存亲和；渠道可 DisablePromptCacheKey 关闭。
	// retention 仅对官方 OpenAI 发送，避免第三方网关因未知字段拒请求。
	if upstream == nil || !upstream.DisablePromptCacheKey {
		req.PromptCacheKey = buildClaudeResponsesPromptCacheKey(claudeReq, upstream, model)
		if shouldSendOpenAIPromptCacheRetention(upstream) {
			req.PromptCacheRetention = "24h"
		}
	}

	// previous_response_id 链式上下文（可选）：仅发送“新增 messages 后缀”，减少重复传输。
	// 注意：官方仍对链上历史 input 计费；失败时由上层全量重试或清链。
	if upstream != nil && upstream.EnablePreviousResponseID && conversationID != "" {
		applyPreviousResponseIDChain(req, claudeReq, upstream, conversationID, model)
	}
	return req, nil
}

// shouldSendOpenAIPromptCacheRetention 仅官方 OpenAI 支持 prompt_cache_retention。
func shouldSendOpenAIPromptCacheRetention(upstream *config.UpstreamConfig) bool {
	if upstream == nil {
		return false
	}
	u, err := url.Parse(strings.TrimRight(upstream.GetEffectiveBaseURL(), "#"))
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	return host == "api.openai.com" || strings.HasSuffix(host, ".api.openai.com")
}

func buildClaudeResponsesPromptCacheKey(claudeReq *types.ClaudeRequest, upstream *config.UpstreamConfig, model string) string {
	channel := ""
	baseURL := ""
	if upstream != nil {
		channel = strings.TrimSpace(upstream.Name)
		baseURL = strings.TrimRight(upstream.GetEffectiveBaseURL(), "/#")
	}
	// 仅用稳定前缀字段做 cache key：system/tools/model/channel/baseURL。
	// system 文本与 tools schema 先做归一化，去掉 cache_control 等不影响语义的字段，避免 key 抖动。
	stableParts := map[string]interface{}{
		"protocol": "claude-messages-to-openai-responses-v2",
		"model":    model,
		"channel":  channel,
		"baseURL":  baseURL,
		"system":   extractSystemText(claudeReq.System),
		"tools":    normalizeToolsForPromptCacheKey(claudeReq.Tools),
	}
	sum := sha256.Sum256([]byte(utils.CanonicalJSON(stableParts)))
	return "claude-resp-" + hex.EncodeToString(sum[:])[:24]
}

// normalizeToolsForPromptCacheKey 生成用于 cache key 的稳定 tools 视图（忽略 cache_control 等）。
func normalizeToolsForPromptCacheKey(tools []types.ClaudeTool) []map[string]interface{} {
	if len(tools) == 0 {
		return nil
	}
	out := make([]map[string]interface{}, 0, len(tools))
	for _, tool := range tools {
		if tool.Name == "" || tool.Name == "BatchTool" {
			continue
		}
		out = append(out, map[string]interface{}{
			"name":        tool.Name,
			"description": tool.Description,
			"parameters":  cleanJsonSchema(tool.InputSchema),
		})
	}
	return out
}

func claudeMessagesToResponsesInput(messages []types.ClaudeMessage, includeHistoryThinking bool) []interface{} {
	items := make([]interface{}, 0, len(messages))
	for _, msg := range messages {
		switch msg.Role {
		case "assistant":
			items = append(items, claudeAssistantMessageToResponsesItems(msg, includeHistoryThinking)...)
		case "tool":
			items = append(items, claudeToolMessageToResponsesItems(msg)...)
		default:
			items = append(items, claudeUserMessageToResponsesItems(msg)...)
		}
	}
	return items
}

func claudeUserMessageToResponsesItems(msg types.ClaudeMessage) []interface{} {
	blocks := utils.NormalizeContentBlocks(msg.Content)
	if len(blocks) == 0 {
		if text, ok := msg.Content.(string); ok && text != "" {
			return []interface{}{map[string]interface{}{
				"type":    "message",
				"role":    "user",
				"content": []interface{}{map[string]interface{}{"type": "input_text", "text": text}},
			}}
		}
		return nil
	}

	var textBlocks []interface{}
	var items []interface{}
	for _, block := range blocks {
		blockType, _ := block["type"].(string)
		if blockType == "tool_result" {
			if len(textBlocks) > 0 {
				items = append(items, map[string]interface{}{
					"type":    "message",
					"role":    "user",
					"content": textBlocks,
				})
				textBlocks = nil
			}
			content := block["content"]
			items = append(items, map[string]interface{}{
				"type":    "function_call_output",
				"call_id": block["tool_use_id"],
				"output":  responsesToolOutput(content),
			})
			continue
		}
		if text, ok := utils.ExtractTextFromBlock(block); ok {
			textBlocks = append(textBlocks, map[string]interface{}{"type": "input_text", "text": text})
			continue
		}
		if imageBlock, ok := toResponsesImageContentBlock(block); ok {
			textBlocks = append(textBlocks, imageBlock)
		}
	}
	if len(textBlocks) > 0 {
		items = append(items, map[string]interface{}{
			"type":    "message",
			"role":    "user",
			"content": textBlocks,
		})
	}
	return items
}

func claudeAssistantMessageToResponsesItems(msg types.ClaudeMessage, includeHistoryThinking bool) []interface{} {
	blocks := utils.NormalizeContentBlocks(msg.Content)
	if len(blocks) == 0 {
		if text, ok := msg.Content.(string); ok && text != "" {
			return []interface{}{map[string]interface{}{
				"type":    "message",
				"role":    "assistant",
				"content": []interface{}{map[string]interface{}{"type": "output_text", "text": text}},
			}}
		}
		return nil
	}

	var textBlocks []interface{}
	var items []interface{}
	for _, block := range blocks {
		blockType, _ := block["type"].(string)
		switch blockType {
		case "text":
			if text, _ := block["text"].(string); text != "" {
				textBlocks = append(textBlocks, map[string]interface{}{"type": "output_text", "text": text})
			}
		case "thinking":
			if includeHistoryThinking {
				if text, _ := block["thinking"].(string); text != "" {
					items = append(items, map[string]interface{}{
						"type": "reasoning",
						"summary": []interface{}{map[string]interface{}{
							"type": "summary_text",
							"text": text,
						}},
					})
				}
			}
			// 默认不回灌：减膨胀 + 稳前缀；渠道 IncludeHistoryThinking=true 时开启
		case "redacted_thinking":
			// redacted_thinking 即使开启历史 thinking 也不回灌（无可读内容）
		case "tool_use":
			id, _ := block["id"].(string)
			name, _ := block["name"].(string)
			argsBytes, _ := json.Marshal(block["input"])
			if len(argsBytes) == 0 || string(argsBytes) == "null" {
				argsBytes = []byte("{}")
			}
			items = append(items, map[string]interface{}{
				"type":      "function_call",
				"call_id":   id,
				"name":      name,
				"arguments": string(argsBytes),
			})
		}
	}
	if len(textBlocks) > 0 {
		items = append([]interface{}{map[string]interface{}{
			"type":    "message",
			"role":    "assistant",
			"content": textBlocks,
		}}, items...)
	}
	return items
}

func claudeToolMessageToResponsesItems(msg types.ClaudeMessage) []interface{} {
	blocks := utils.NormalizeContentBlocks(msg.Content)
	items := make([]interface{}, 0, len(blocks))
	for _, block := range blocks {
		if blockType, _ := block["type"].(string); blockType != "tool_result" {
			continue
		}
		content := block["content"]
		items = append(items, map[string]interface{}{
			"type":    "function_call_output",
			"call_id": block["tool_use_id"],
			"output":  responsesToolOutput(content),
		})
	}
	return items
}

func claudeToolsToResponsesTools(tools []types.ClaudeTool) []interface{} {
	out := make([]interface{}, 0, len(tools))
	for _, tool := range tools {
		if tool.Name == "" || tool.Name == "BatchTool" {
			continue
		}
		out = append(out, map[string]interface{}{
			"type":        "function",
			"name":        tool.Name,
			"description": tool.Description,
			"parameters":  cleanJsonSchema(tool.InputSchema),
		})
	}
	return out
}

func responsesToolOutput(content interface{}) string {
	if content == nil {
		return ""
	}
	if str, ok := content.(string); ok {
		return str
	}
	if blocks := utils.NormalizeContentBlocks(content); len(blocks) > 0 {
		texts := make([]string, 0, len(blocks))
		for _, block := range blocks {
			if text, ok := utils.ExtractTextFromBlock(block); ok {
				texts = append(texts, text)
			}
		}
		if len(texts) > 0 {
			return strings.Join(texts, "\n")
		}
	}
	contentJSON, err := utils.MarshalJSONNoEscape(content)
	if err != nil {
		return fmt.Sprint(content)
	}
	return string(contentJSON)
}

func toResponsesImageContentBlock(block map[string]interface{}) (map[string]interface{}, bool) {
	imageBlock, ok := utils.ToOpenAIImageContentBlock(block)
	if !ok {
		return nil, false
	}
	imageURL, ok := imageBlock["image_url"].(map[string]interface{})
	if !ok {
		return nil, false
	}
	url, _ := imageURL["url"].(string)
	if url == "" {
		return nil, false
	}
	return map[string]interface{}{
		"type":      "input_image",
		"image_url": url,
	}, true
}

func claudeToolChoiceToResponses(raw interface{}) interface{} {
	if raw == nil {
		return nil
	}
	if value, ok := raw.(string); ok {
		switch value {
		case "auto", "none":
			return value
		case "any":
			return "required"
		default:
			return nil
		}
	}
	if obj, ok := raw.(map[string]interface{}); ok {
		typ, _ := obj["type"].(string)
		switch typ {
		case "auto", "none":
			return typ
		case "any":
			return "required"
		case "tool":
			return map[string]interface{}{"type": "function", "name": obj["name"]}
		}
	}
	return nil
}

func claudeReasoningToResponsesReasoning(claudeReq *types.ClaudeRequest) interface{} {
	if effort := resolveClaudeReasoningEffort(claudeReq); effort != "" {
		return map[string]interface{}{"effort": effort}
	}
	return nil
}

func resolveClaudeReasoningEffort(claudeReq *types.ClaudeRequest) string {
	cfg := converters.ExtractReasoningFromClaude(claudeReq)
	return cfg.Effort
}

func numericBudgetTokens(raw interface{}) (float64, bool) {
	switch v := raw.(type) {
	case float64:
		return v, true
	case float32:
		return float64(v), true
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	case json.Number:
		n, err := v.Float64()
		return n, err == nil
	default:
		return 0, false
	}
}

func responsesResponseToClaude(resp *types.ResponsesResponse) *types.ClaudeResponse {
	claudeResp := &types.ClaudeResponse{
		ID:      generateID(),
		Type:    "message",
		Role:    "assistant",
		Content: []types.ClaudeContent{},
	}
	var textParts []string
	thinkingParts := make([]types.ClaudeContent, 0)
	for _, item := range resp.Output {
		switch item.Type {
		case "message":
			for _, block := range utils.NormalizeContentBlocks(item.Content) {
				if text, ok := utils.ExtractTextFromBlock(block); ok && text != "" {
					textParts = append(textParts, text)
				}
			}
		case "reasoning":
			if text := converters.ExtractResponsesReasoningText(item); text != "" {
				thinkingParts = append(thinkingParts, types.ClaudeContent{
					Type:     "thinking",
					Thinking: text,
				})
			}
		case "function_call", "custom_tool_call":
			flushClaudeText(claudeResp, &textParts)
			var input interface{} = map[string]interface{}{}
			if item.Type == "custom_tool_call" {
				input = map[string]interface{}{"input": responsesToolOutput(item.Content)}
			} else if item.Arguments != "" {
				_ = json.Unmarshal([]byte(item.Arguments), &input)
			}
			callID := item.CallID
			if callID == "" {
				callID = strings.TrimPrefix(item.ID, "fc_")
				if callID == item.ID {
					callID = strings.TrimPrefix(item.ID, "ctc_")
				}
			}
			claudeResp.Content = append(claudeResp.Content, types.ClaudeContent{
				Type:  "tool_use",
				ID:    callID,
				Name:  item.Name,
				Input: input,
			})
		}
	}
	flushClaudeText(claudeResp, &textParts)
	if len(thinkingParts) > 0 {
		// Claude thinking 必须位于正文和 tool_use 之前。Responses→Messages
		// 的历史回灌默认会跳过 thinking，因此下发它不会把思考重复塞回
		// Responses 上游上下文；内部缓存仍保留给严格 Chat 渠道补回。
		claudeResp.Content = append(append([]types.ClaudeContent{}, thinkingParts...), claudeResp.Content...)
		CacheClaudeResponseReasoning(claudeResp)
	}
	claudeResp.StopReason = "end_turn"
	for _, content := range claudeResp.Content {
		if content.Type == "tool_use" {
			claudeResp.StopReason = "tool_use"
			break
		}
	}
	if resp.Status == "incomplete" {
		claudeResp.StopReason = "max_tokens"
	}
	cacheReadTokens := resp.Usage.CacheReadInputTokens
	if cacheReadTokens == 0 && resp.Usage.InputTokensDetails != nil {
		cacheReadTokens = resp.Usage.InputTokensDetails.CachedTokens
	}
	claudeResp.Usage = &types.Usage{
		InputTokens:                resp.Usage.InputTokens,
		OutputTokens:               resp.Usage.OutputTokens,
		CacheCreationInputTokens:   resp.Usage.CacheCreationInputTokens,
		CacheCreation5mInputTokens: resp.Usage.CacheCreation5mInputTokens,
		CacheCreation1hInputTokens: resp.Usage.CacheCreation1hInputTokens,
		CacheReadInputTokens:       cacheReadTokens,
		CacheTTL:                   resp.Usage.CacheTTL,
	}
	return claudeResp
}

func flushClaudeText(resp *types.ClaudeResponse, parts *[]string) {
	if len(*parts) == 0 {
		return
	}
	resp.Content = append(resp.Content, types.ClaudeContent{
		Type: "text",
		Text: strings.Join(*parts, ""),
	})
	*parts = nil
}

type responsesToClaudeStreamState struct {
	responseID          string
	upstreamResponseID  string
	messageStarted      bool
	nextBlockIndex      int
	textBlockIndex      int
	textBlockOpen       bool
	stopReason          string
	model               string
	toolCalls           map[string]*responsesStreamToolCall
	toolCallOrder       []string
	emittedToolCalls    map[string]bool
	reasoning           strings.Builder
	reasoningDeltaSeen  bool
	reasoningBlockIndex int
	reasoningBlockOpen  bool
	reasoningEmittedLen int
	assistantText       strings.Builder
	completedOutput     []types.ResponsesItem
	completed           bool
	// Upstream Responses usage fields captured from response.completed / usage events.
	// Without these, request logs always show cacheReadTokens=0 for Claude entry.
	inputTokens              int
	outputTokens             int
	cacheReadInputTokens     int
	cacheCreationInputTokens int
	hasUsage                 bool

	// 上游失败状态（response.failed / 顶层 error 事件）：
	// failed 阻止 finish() 伪造成功终止；capacityError 标记 model at capacity
	// 类容量错误（上层同渠道重试）；failedMessage 保存错误信封供上报。
	failed        bool
	capacityError bool
	failedMessage string
}

type responsesStreamToolCall struct {
	CallID    string
	Name      string
	Arguments string
}

func newResponsesToClaudeStreamState() *responsesToClaudeStreamState {
	return &responsesToClaudeStreamState{
		responseID:       fmt.Sprintf("msg_%s", uuid.New().String()),
		toolCalls:        make(map[string]*responsesStreamToolCall),
		emittedToolCalls: make(map[string]bool),
	}
}

func (s *responsesToClaudeStreamState) processLine(line string) []string {
	if strings.HasPrefix(line, "event:") {
		return nil
	}
	data, isData := utils.SSEDataJSON(line)
	if !isData {
		return nil
	}
	root := gjson.Parse(data)
	if root.Get("error").Exists() {
		s.failed = true
		return []string{buildClaudeSSE("error", map[string]interface{}{
			"type":  "error",
			"error": root.Get("error").Value(),
		})}
	}

	eventType := root.Get("type").String()

	// response.failed 的错误信封在 response.error 下（与顶层 error 事件不同形态）。
	// 不处理会让流尾 finish() 伪造 message_stop，客户端把失败当成功终止。
	// model at capacity 类容量错误转 RetrySameCandidateError 由上层同渠道重试。
	if eventType == "response.failed" {
		s.failed = true
		failedPayload := root.Get("response.error").Raw
		if failedPayload == "" {
			failedPayload = data
		}
		if proxycoreIsModelCapacity(failedPayload) {
			s.capacityError = true
		} else {
			s.failedMessage = failedPayload
		}
		return nil
	}

	s.captureUpstreamResponseID(root)
	s.captureResponsesUsage(root)
	out := s.ensureMessageStart(root)
	switch {
	case strings.Contains(eventType, "output_text.delta"):
		out = append(out, s.emitTextDelta(root.Get("delta").String())...)
	case strings.Contains(eventType, "reasoning") && strings.Contains(eventType, "delta"):
		if delta := root.Get("delta").String(); delta != "" {
			s.reasoningDeltaSeen = true
			s.reasoning.WriteString(delta)
			out = append(out, s.emitReasoningDelta(delta)...)
		}
	case strings.Contains(eventType, "reasoning") && strings.Contains(eventType, "done"):
		// reasoning_summary_text.done 常带完整 summary；已有 delta 时不能再拼一次。
		if !s.reasoningDeltaSeen && s.reasoning.Len() == 0 {
			if text := root.Get("text").String(); text != "" {
				s.reasoning.WriteString(text)
				out = append(out, s.emitReasoningDelta(text)...)
			}
		}
		out = append(out, s.closeReasoningBlock()...)
	case strings.Contains(eventType, "function_call_arguments.delta") || strings.Contains(eventType, "custom_tool_call_input.delta"):
		s.captureToolArgumentDelta(root)
	case strings.Contains(eventType, "response.output_item.added"):
		s.captureToolCall(root)
	case strings.Contains(eventType, "response.output_item.done"):
		s.captureToolCall(root)
		itemType := root.Get("item.type").String()
		if itemType == "function_call" || itemType == "custom_tool_call" {
			out = append(out, s.emitToolUse(root)...)
		}
	case strings.Contains(eventType, "function_call_arguments.done"):
		s.captureToolCall(root)
		out = append(out, s.emitToolUse(root)...)
	case strings.Contains(eventType, "response.completed"):
		s.completed = true
		out = append(out, s.emitCompletedOutput(root)...)
		if s.stopReason == "" {
			s.stopReason = "end_turn"
		}
		if status := root.Get("response.status").String(); status == "incomplete" {
			s.stopReason = "max_tokens"
		}
		// Final usage is usually on response.completed; re-capture then emit message_delta with real tokens.
		s.captureResponsesUsage(root)
		out = append(out, s.emitMessageDelta()...)
	default:
		if text := root.Get("delta").String(); text != "" && strings.Contains(eventType, "text") {
			out = append(out, s.emitTextDelta(text)...)
		}
	}
	return out
}

// emitCompletedOutput 兼容只在 response.completed.response.output 返回完整结果的
// 上游。标准增量流已经交付的正文不再重复发送，但未交付的工具调用仍会补发。
func (s *responsesToClaudeStreamState) emitCompletedOutput(root gjson.Result) []string {
	output := root.Get("response.output")
	if !output.Exists() || !output.IsArray() {
		return nil
	}

	var out []string
	completedItems := make([]types.ResponsesItem, 0, len(output.Array()))
	output.ForEach(func(_, item gjson.Result) bool {
		itemType := item.Get("type").String()
		if parsed, ok := responsesStreamOutputItem(item); ok {
			completedItems = append(completedItems, parsed)
		}
		switch itemType {
		case "message":
			if s.assistantText.Len() > 0 {
				return true
			}
			content := item.Get("content")
			if content.Type == gjson.String {
				out = append(out, s.emitTextDelta(content.String())...)
				return true
			}
			if content.Exists() && content.IsArray() {
				content.ForEach(func(_, block gjson.Result) bool {
					blockType := block.Get("type").String()
					if blockType == "text" || blockType == "input_text" || blockType == "output_text" {
						out = append(out, s.emitTextDelta(block.Get("text").String())...)
					}
					return true
				})
			}
		case "reasoning":
			if s.reasoning.Len() == 0 {
				item.Get("summary").ForEach(func(_, summary gjson.Result) bool {
					s.reasoning.WriteString(summary.Get("text").String())
					return true
				})
			}
			if s.reasoningEmittedLen < s.reasoning.Len() {
				pending := s.reasoning.String()[s.reasoningEmittedLen:]
				out = append(out, s.emitReasoningDelta(pending)...)
			}
		case "function_call", "custom_tool_call":
			payload := map[string]interface{}{"item": item.Value()}
			payloadJSON, err := json.Marshal(payload)
			if err != nil {
				return true
			}
			itemRoot := gjson.ParseBytes(payloadJSON)
			s.captureToolCall(itemRoot)
			out = append(out, s.emitToolUse(itemRoot)...)
		}
		return true
	})
	if len(completedItems) > 0 {
		s.completedOutput = completedItems
	}
	return out
}

func (s *responsesToClaudeStreamState) finish() []string {
	if s.failed {
		// 错误流：不伪造成功终止（message_delta/message_stop 由错误路径统一处理）。
		return nil
	}
	out := []string{}
	out = append(out, s.closeReasoningBlock()...)
	out = append(out, s.closeTextBlock()...)
	if s.messageStarted {
		if s.stopReason == "" {
			s.stopReason = "end_turn"
			out = append(out, s.emitMessageDelta()...)
		}
		out = append(out, buildClaudeSSE("message_stop", map[string]interface{}{"type": "message_stop"}))
	}
	return out
}

// proxycoreIsModelCapacity 委托 utils.IsUpstreamModelCapacityMessage：
// 容量错误判据的唯一出处（R4-a 收敛），消除 providers 与 proxycore 的
// 重复实现（providers 不能 import proxycore：proxycore→visionlayer→providers
// 会成环；utils 是双方共同依赖）。
func proxycoreIsModelCapacity(payload string) bool {
	return utils.IsUpstreamModelCapacityMessage(payload)
}

// UpstreamStreamFailedError 表示 Responses 上游在流内报告了失败
// （response.failed / 顶层 error 事件）。Capacity=true 时是 model at capacity
// 类容量错误，调用方（proxycore）可按同候选重试处理；否则是真实上游失败。
type UpstreamStreamFailedError struct {
	Capacity bool
	Payload  string
}

func (e *UpstreamStreamFailedError) Error() string {
	if e.Capacity {
		return "upstream stream failed: model at capacity"
	}
	if e.Payload != "" {
		return "upstream stream failed: " + e.Payload
	}
	return "upstream stream failed"
}

// NewUpstreamStreamFailedError 构造流内失败错误。
func NewUpstreamStreamFailedError(capacity bool, payload string) error {
	return &UpstreamStreamFailedError{Capacity: capacity, Payload: payload}
}

// IsUpstreamStreamFailed 判断 err 是否为流内失败错误（供上层分类）。
func IsUpstreamStreamFailed(err error) (*UpstreamStreamFailedError, bool) {
	var target *UpstreamStreamFailedError
	if errors.As(err, &target) {
		return target, true
	}
	return nil, false
}

func (s *responsesToClaudeStreamState) captureUpstreamResponseID(root gjson.Result) {
	if s.upstreamResponseID != "" {
		return
	}
	if responseID := root.Get("response.id").String(); responseID != "" {
		s.upstreamResponseID = responseID
		return
	}
	// response.created 等事件可能把 id 放在顶层
	if eventType := root.Get("type").String(); strings.HasPrefix(eventType, "response.") {
		if responseID := root.Get("id").String(); responseID != "" && strings.HasPrefix(responseID, "resp_") {
			s.upstreamResponseID = responseID
		}
	}
}

func (s *responsesToClaudeStreamState) ensureMessageStart(root gjson.Result) []string {
	if s.messageStarted {
		return nil
	}
	if model := root.Get("response.model").String(); model != "" {
		s.model = model
	} else if model := root.Get("model").String(); model != "" {
		s.model = model
	}
	s.messageStarted = true
	// Prefer any early usage if present; otherwise leave zeros (stream handler may estimate).
	// captureResponsesUsage 已把 s.inputTokens 收敛成 uncached 余量，这里直接用。
	startInputTokens := s.inputTokens
	// cache_* 同时供管理端采集与 Claude 客户端计算完整上下文占用，客户端出口保持原样。
	startUsage := map[string]interface{}{
		"input_tokens":  startInputTokens,
		"output_tokens": 0,
	}
	if s.cacheReadInputTokens > 0 {
		startUsage["cache_read_input_tokens"] = s.cacheReadInputTokens
		startUsage["input_tokens_details"] = map[string]interface{}{
			"cached_tokens": s.cacheReadInputTokens,
		}
	}
	if s.cacheCreationInputTokens > 0 {
		startUsage["cache_creation_input_tokens"] = s.cacheCreationInputTokens
	}
	return []string{buildClaudeSSE("message_start", map[string]interface{}{
		"type": "message_start",
		"message": map[string]interface{}{
			"id":            s.responseID,
			"type":          "message",
			"role":          "assistant",
			"content":       []interface{}{},
			"model":         s.model,
			"stop_reason":   nil,
			"stop_sequence": nil,
			"usage":         startUsage,
		},
	})}
}

func (s *responsesToClaudeStreamState) emitTextDelta(text string) []string {
	if text == "" {
		return nil
	}
	s.assistantText.WriteString(text)
	out := s.closeReasoningBlock()
	if !s.textBlockOpen {
		s.textBlockIndex = s.nextBlockIndex
		s.nextBlockIndex++
		s.textBlockOpen = true
		out = append(out, buildClaudeSSE("content_block_start", map[string]interface{}{
			"type":  "content_block_start",
			"index": s.textBlockIndex,
			"content_block": map[string]interface{}{
				"type": "text",
				"text": "",
			},
		}))
	}
	out = append(out, buildClaudeSSE("content_block_delta", map[string]interface{}{
		"type":  "content_block_delta",
		"index": s.textBlockIndex,
		"delta": map[string]interface{}{"type": "text_delta", "text": text},
	}))
	return out
}

func (s *responsesToClaudeStreamState) emitToolUse(root gjson.Result) []string {
	key := responsesToolCallKey(root)
	if key != "" && s.emittedToolCalls[key] {
		return nil
	}
	out := s.closeReasoningBlock()
	out = append(out, s.closeTextBlock()...)
	stored := s.toolCalls[key]
	callID := root.Get("item.call_id").String()
	if callID == "" {
		callID = root.Get("call_id").String()
	}
	if callID == "" && stored != nil {
		callID = stored.CallID
	}
	name := root.Get("item.name").String()
	if name == "" {
		name = root.Get("name").String()
	}
	if name == "" && stored != nil {
		name = stored.Name
	}
	arguments := root.Get("arguments").String()
	if arguments == "" {
		arguments = root.Get("item.arguments").String()
	}
	if arguments == "" {
		input := root.Get("item.input")
		if input.Exists() {
			if input.Type == gjson.String {
				inputJSON, _ := json.Marshal(map[string]string{"input": input.String()})
				arguments = string(inputJSON)
			} else if strings.TrimSpace(input.Raw) != "" {
				arguments = input.Raw
			}
		}
	}
	if arguments == "" && stored != nil {
		arguments = stored.Arguments
	}
	if callID == "" {
		callID = fmt.Sprintf("call_%d", s.nextBlockIndex)
	}
	var input interface{} = map[string]interface{}{}
	if arguments != "" {
		if err := json.Unmarshal([]byte(arguments), &input); err != nil && root.Get("item.type").String() == "custom_tool_call" {
			input = map[string]interface{}{"input": arguments}
		}
	}
	if stored != nil {
		stored.CallID = callID
		stored.Name = name
		stored.Arguments = arguments
	}
	index := s.nextBlockIndex
	s.nextBlockIndex++
	s.stopReason = "tool_use"
	if key != "" {
		s.emittedToolCalls[key] = true
	}
	return append(out, processToolUsePart(callID, name, input, index)...)
}

func (s *responsesToClaudeStreamState) captureToolCall(root gjson.Result) {
	key := responsesToolCallKey(root)
	if key == "" {
		return
	}
	itemType := root.Get("item.type").String()
	if itemType != "" && itemType != "function_call" && itemType != "custom_tool_call" {
		return
	}
	call := s.toolCalls[key]
	if call == nil {
		call = &responsesStreamToolCall{}
		s.toolCalls[key] = call
		s.toolCallOrder = append(s.toolCallOrder, key)
	}
	if callID := root.Get("item.call_id").String(); callID != "" {
		call.CallID = callID
	} else if callID := root.Get("call_id").String(); callID != "" {
		call.CallID = callID
	}
	if name := root.Get("item.name").String(); name != "" {
		call.Name = name
	} else if name := root.Get("name").String(); name != "" {
		call.Name = name
	}
	if arguments := root.Get("item.arguments").String(); arguments != "" {
		call.Arguments = arguments
	} else if arguments := root.Get("arguments").String(); arguments != "" {
		call.Arguments = arguments
	} else if input := root.Get("item.input"); input.Exists() {
		if input.Type == gjson.String {
			inputJSON, _ := json.Marshal(map[string]string{"input": input.String()})
			call.Arguments = string(inputJSON)
		} else if strings.TrimSpace(input.Raw) != "" {
			call.Arguments = input.Raw
		}
	}
}

func (s *responsesToClaudeStreamState) captureToolArgumentDelta(root gjson.Result) {
	key := responsesToolCallKey(root)
	if key == "" {
		return
	}
	call := s.toolCalls[key]
	if call == nil {
		call = &responsesStreamToolCall{}
		s.toolCalls[key] = call
		s.toolCallOrder = append(s.toolCallOrder, key)
	}
	if callID := root.Get("call_id").String(); callID != "" {
		call.CallID = callID
	}
	if name := root.Get("name").String(); name != "" {
		call.Name = name
	}
	if delta := root.Get("delta").String(); delta != "" {
		call.Arguments += delta
	}
}

// cacheReasoning 保存成功完成的 Responses 流式推理，供后续切换到严格 Chat
// 渠道时补回 reasoning_content。下一轮 Messages→Responses 转换默认跳过历史
// thinking，因此它不会进入上游上下文。
func (s *responsesToClaudeStreamState) cacheReasoning() {
	if s == nil || !s.completed || s.reasoning.Len() == 0 {
		return
	}

	content := make([]types.ClaudeContent, 0, len(s.toolCallOrder)+1)
	if s.assistantText.Len() > 0 {
		content = append(content, types.ClaudeContent{
			Type: "text",
			Text: s.assistantText.String(),
		})
	}
	for _, key := range s.toolCallOrder {
		call := s.toolCalls[key]
		if call == nil {
			continue
		}
		var input interface{} = map[string]interface{}{}
		if call.Arguments != "" {
			_ = json.Unmarshal([]byte(call.Arguments), &input)
		}
		content = append(content, types.ClaudeContent{
			Type:  "tool_use",
			ID:    call.CallID,
			Name:  call.Name,
			Input: input,
		})
	}
	CacheClaudeResponseReasoning(&types.ClaudeResponse{
		Role:    "assistant",
		Content: append([]types.ClaudeContent{{Type: "thinking", Thinking: s.reasoning.String()}}, content...),
	})
}

func (s *responsesToClaudeStreamState) outputFingerprint() string {
	if s == nil || !s.completed {
		return ""
	}
	if len(s.completedOutput) > 0 {
		return responsesAssistantOutputFingerprint(s.completedOutput)
	}
	items := make([]types.ResponsesItem, 0, len(s.toolCallOrder)+1)
	if s.assistantText.Len() > 0 {
		items = append(items, types.ResponsesItem{
			Type: "message", Role: "assistant",
			Content: []interface{}{map[string]interface{}{
				"type": "output_text", "text": s.assistantText.String(),
			}},
		})
	}
	for _, key := range s.toolCallOrder {
		call := s.toolCalls[key]
		if call == nil {
			continue
		}
		items = append(items, types.ResponsesItem{
			Type: "function_call", CallID: call.CallID, Name: call.Name, Arguments: call.Arguments,
		})
	}
	return responsesAssistantOutputFingerprint(items)
}

func responsesToolCallKey(root gjson.Result) string {
	if itemID := root.Get("item.id").String(); itemID != "" {
		return itemID
	}
	if itemID := root.Get("item_id").String(); itemID != "" {
		return itemID
	}
	if outputIndex := root.Get("output_index"); outputIndex.Exists() {
		return fmt.Sprintf("output_%d", outputIndex.Int())
	}
	if callID := root.Get("item.call_id").String(); callID != "" {
		return callID
	}
	if callID := root.Get("call_id").String(); callID != "" {
		return callID
	}
	return ""
}

func (s *responsesToClaudeStreamState) closeTextBlock() []string {
	if !s.textBlockOpen {
		return nil
	}
	s.textBlockOpen = false
	return []string{buildClaudeSSE("content_block_stop", map[string]interface{}{
		"type":  "content_block_stop",
		"index": s.textBlockIndex,
	})}
}

func (s *responsesToClaudeStreamState) emitReasoningDelta(text string) []string {
	if text == "" {
		return nil
	}
	out := []string{}
	if !s.reasoningBlockOpen {
		s.reasoningBlockIndex = s.nextBlockIndex
		s.nextBlockIndex++
		s.reasoningBlockOpen = true
		out = append(out, buildClaudeSSE("content_block_start", map[string]interface{}{
			"type":  "content_block_start",
			"index": s.reasoningBlockIndex,
			"content_block": map[string]interface{}{
				"type":     "thinking",
				"thinking": "",
			},
		}))
	}
	s.reasoningEmittedLen += len(text)
	return append(out, buildClaudeSSE("content_block_delta", map[string]interface{}{
		"type":  "content_block_delta",
		"index": s.reasoningBlockIndex,
		"delta": map[string]interface{}{"type": "thinking_delta", "thinking": text},
	}))
}

func (s *responsesToClaudeStreamState) closeReasoningBlock() []string {
	if !s.reasoningBlockOpen {
		return nil
	}
	s.reasoningBlockOpen = false
	return []string{buildClaudeSSE("content_block_stop", map[string]interface{}{
		"type":  "content_block_stop",
		"index": s.reasoningBlockIndex,
	})}
}

func responsesStreamOutputItem(item gjson.Result) (types.ResponsesItem, bool) {
	if !item.Exists() || !item.IsObject() {
		return types.ResponsesItem{}, false
	}
	payload, err := json.Marshal(item.Value())
	if err != nil {
		return types.ResponsesItem{}, false
	}
	var parsed types.ResponsesItem
	if err := json.Unmarshal(payload, &parsed); err != nil || parsed.Type == "" {
		return types.ResponsesItem{}, false
	}
	if parsed.Content == nil {
		if parsed.Type == "custom_tool_call" {
			parsed.Content = item.Get("input").Value()
		} else {
			parsed.Content = item.Get("output").Value()
		}
	}
	return parsed, true
}

func (s *responsesToClaudeStreamState) emitMessageDelta() []string {
	if s.stopReason == "" {
		s.stopReason = "end_turn"
	}
	// s.inputTokens 已由 captureResponsesUsage 收敛成 uncached 余量。
	// cache_read_input_tokens 和 cache_creation_input_tokens 既给管理端采集，也会到达
	// Claude 客户端，让客户端按 Anthropic 契约求和 input_tokens + cache_read +
	// cache_creation 得到真实上下文占用。三者必须同步：把 input_tokens 改回总量会双计。
	clientInputTokens := s.inputTokens
	usageMap := map[string]interface{}{
		"output_tokens": s.outputTokens,
	}
	if clientInputTokens > 0 {
		usageMap["input_tokens"] = clientInputTokens
	}
	if s.cacheReadInputTokens > 0 {
		usageMap["cache_read_input_tokens"] = s.cacheReadInputTokens
		usageMap["input_tokens_details"] = map[string]interface{}{
			"cached_tokens": s.cacheReadInputTokens,
		}
	}
	if s.cacheCreationInputTokens > 0 {
		usageMap["cache_creation_input_tokens"] = s.cacheCreationInputTokens
	}
	return []string{buildClaudeSSE("message_delta", map[string]interface{}{
		"type": "message_delta",
		"delta": map[string]interface{}{
			"stop_reason":   s.stopReason,
			"stop_sequence": nil,
		},
		"usage": usageMap,
	})}
}

// captureResponsesUsage extracts usage/cache fields from Responses SSE payloads.
// OpenAI Responses commonly places usage under response.usage on response.completed:
//
//	input_tokens, output_tokens, input_tokens_details.cached_tokens
//
// Some gateways also emit top-level usage or cache_read_input_tokens.
func (s *responsesToClaudeStreamState) captureResponsesUsage(root gjson.Result) {
	usageNode := root.Get("response.usage")
	if !usageNode.Exists() {
		usageNode = root.Get("usage")
	}
	if !usageNode.Exists() {
		return
	}

	// reportedInput 保留"本次上游报出的原始 input"，减法绑定它而不是 s.inputTokens：
	// 一条流里 captureResponsesUsage 会被多次调用，绑定累积字段会重复相减。
	reportedInput := 0
	if v := usageNode.Get("input_tokens"); v.Exists() && v.Int() > 0 {
		reportedInput = int(v.Int())
	} else if v := usageNode.Get("prompt_tokens"); v.Exists() && v.Int() > 0 {
		reportedInput = int(v.Int())
	}
	if reportedInput > 0 {
		s.inputTokens = reportedInput
		s.hasUsage = true
	}
	if v := usageNode.Get("output_tokens"); v.Exists() && v.Int() > 0 {
		s.outputTokens = int(v.Int())
		s.hasUsage = true
	} else if v := usageNode.Get("completion_tokens"); v.Exists() && v.Int() > 0 {
		s.outputTokens = int(v.Int())
		s.hasUsage = true
	}

	// 缓存量按字段名分开保存 provenance：折叠成一个值就判不出 input_tokens 是否已含缓存。
	//   cacheReadAnthropic —— cache_read_input_tokens，上游本就按 uncached 报 input。
	//   cacheReadSubset    —— *_tokens_details.cached_tokens，OpenAI 语义 cached ⊆ input。
	cacheReadAnthropic := int64(0)
	cacheReadSubset := int64(0)
	if v := usageNode.Get("cache_read_input_tokens"); v.Exists() && v.Int() > 0 {
		cacheReadAnthropic = v.Int()
	}
	if v := usageNode.Get("input_tokens_details.cached_tokens"); v.Exists() && v.Int() > 0 {
		cacheReadSubset = v.Int()
	} else if v := usageNode.Get("prompt_tokens_details.cached_tokens"); v.Exists() && v.Int() > 0 {
		cacheReadSubset = v.Int()
	}
	cacheRead := cacheReadAnthropic
	if cacheRead == 0 {
		cacheRead = cacheReadSubset
	}
	if cacheRead > 0 {
		s.cacheReadInputTokens = int(cacheRead)
		s.hasUsage = true
	}

	if v := usageNode.Get("cache_creation_input_tokens"); v.Exists() && v.Int() > 0 {
		s.cacheCreationInputTokens = int(v.Int())
		s.hasUsage = true
	}

	// input_tokens 收敛到 Anthropic uncached 口径，判据与 normalizeOpenAIUsage 完全一致：
	// 只有当缓存量来自 subset 式字段名、且上游没给 Anthropic 式 cache_read_input_tokens 时，
	// input_tokens 才是含缓存的总量，必须减；上游给了 Anthropic 名就说明它已经拆好了，
	// 再减一次是双减。客户端按契约求和 input_tokens + cache_read 才回到真实占用。
	if reportedInput > 0 && cacheReadSubset > 0 && cacheReadAnthropic == 0 {
		s.inputTokens = reportedInput - int(cacheReadSubset)
		if s.inputTokens < 0 {
			// 上游异常回包（cached > input）。钳制到 0，不让负数流进客户端与指标。
			s.inputTokens = 0
		}
	}
}

func buildClaudeSSE(event string, data map[string]interface{}) string {
	dataJSON, _ := json.Marshal(data)
	return fmt.Sprintf("event: %s\ndata: %s\n\n", event, dataJSON)
}

// extractMessagesConversationID 从请求中提取会话 ID，用于 BaseURL 粘滞与 previous_response_id 链。
func extractMessagesConversationID(c *gin.Context, claudeReq *types.ClaudeRequest) string {
	if c != nil {
		return extractMessagesConversationIDFromInput(c.Request.Header, func(key string) string { return c.Query(key) }, claudeReq)
	}
	return extractMessagesConversationIDFromInput(nil, nil, claudeReq)
}

// extractMessagesConversationIDFromInput 以显式输入提取会话 ID（R4-a，无 gin）。
func extractMessagesConversationIDFromInput(header http.Header, query func(string) string, claudeReq *types.ClaudeRequest) string {
	if header != nil {
		for _, headerName := range []string{
			"X-Conversation-Id",
			"Conversation-Id",
			"X-Session-Id",
			"Session-Id",
			"X-Claude-Code-Session-Id",
		} {
			if value := strings.TrimSpace(header.Get(headerName)); value != "" {
				return value
			}
		}
	}
	if query != nil {
		if value := strings.TrimSpace(query("conversation_id")); value != "" {
			return value
		}
	}
	if claudeReq != nil && claudeReq.Metadata != nil {
		for _, key := range []string{"conversation_id", "session_id", "user_id"} {
			if value, ok := claudeReq.Metadata[key].(string); ok {
				if trimmed := strings.TrimSpace(value); trimmed != "" {
					return trimmed
				}
			}
		}
	}
	return ""
}

// applyPreviousResponseIDChain 在渠道开启 EnablePreviousResponseID 时，
// 若会话链状态可用且前缀指纹匹配，则仅发送 messages 后缀并带 previous_response_id。
//
// 重要：OpenAI 文档写明链上历史 input 仍会计费；此优化主要减传输体积、提升缓存亲和。
func applyPreviousResponseIDChain(
	req *claudeResponsesRequest,
	claudeReq *types.ClaudeRequest,
	upstream *config.UpstreamConfig,
	conversationID string,
	model string,
) {
	if req == nil || claudeReq == nil || conversationID == "" {
		return
	}
	state, ok := session.DefaultResponseChainManager().Get(conversationID)
	if !ok || state.ResponseID == "" {
		return
	}

	// Full-history clients (Cursor) re-send the entire transcript every turn.
	// previous_response_id freezes old uncompacted server history forever while
	// cache_read climbs after client compact. NEVER attach chain for full-history
	// replays — only allow truly tiny incremental suffixes.
	if state.MessageCount > 0 {
		suffixLen := len(claudeReq.Messages) - state.MessageCount
		if suffixLen < 0 {
			// Client compact/summarize shortened history: clear chain so we never
			// re-attach pre-compact server-side context via previous_response_id.
			session.DefaultResponseChainManager().Clear(conversationID)
			return
		}
		if state.MessageFingerprint != "" {
			currentPrefix := claudeReq.Messages[:state.MessageCount]
			if utils.CanonicalJSON(currentPrefix) != state.MessageFingerprint {
				// 消息正文发生变化（包括客户端压缩/分支重写），旧的
				// Responses 链已经不能代表当前前缀，必须回退全量请求。
				session.DefaultResponseChainManager().Clear(conversationID)
				return
			}
		}
		// Cursor always resends full body; any non-trivial growth should full-send.
		if suffixLen > 2 || len(claudeReq.Messages) > 8 {
			session.DefaultResponseChainManager().Clear(conversationID)
			return
		}
	}

	// Model change: clear chain and resend the complete client history.
	if state.Model != "" && model != "" && state.Model != model {
		session.DefaultResponseChainManager().Clear(conversationID)
		return
	}
	systemFingerprint := extractSystemText(claudeReq.System)
	toolsFingerprint := utils.CanonicalJSON(normalizeToolsForPromptCacheKey(claudeReq.Tools))
	if state.SystemFingerprint != systemFingerprint || state.ToolsFingerprint != toolsFingerprint {
		session.DefaultResponseChainManager().Clear(conversationID)
		return
	}
	if state.BaseURL != "" && responseChainBaseURL(upstream) != "" &&
		state.BaseURL != responseChainBaseURL(upstream) {
		// response ID 只在生成它的 Responses 上游及其兼容链路中有效。
		// 渠道切换后继续携带旧 ID 会把请求送进错误的服务端会话，或触发
		// 上游用旧链补回压缩前上下文。
		session.DefaultResponseChainManager().Clear(conversationID)
		return
	}

	// Chain stores server-side history that was never re-compacted. Cap hard.
	const maxChainMessagesBeforeFullResend = 4
	if state.MessageCount >= maxChainMessagesBeforeFullResend {
		session.DefaultResponseChainManager().Clear(conversationID)
		return
	}
	if state.MessageCount <= 0 {
		return
	}
	if state.OutputFingerprint == "" {
		// 旧版本链状态没有记录上一条 server output，无法证明客户端
		// 重放的 assistant 消息已经存在于 previous_response_id 链中。
		// 不冒险叠加，清链并发送当前完整历史。
		session.DefaultResponseChainManager().Clear(conversationID)
		return
	}
	if len(claudeReq.Messages) <= state.MessageCount {
		// No new messages or history shortened: send the complete current payload without chain.
		if len(claudeReq.Messages) < state.MessageCount {
			session.DefaultResponseChainManager().Clear(conversationID)
		}
		return
	}
	suffix := claudeReq.Messages[state.MessageCount:]
	if len(suffix) == 0 {
		return
	}
	includeThinking := upstream != nil && upstream.IncludeHistoryThinking
	if suffix[0].Role == "assistant" {
		// Claude Messages 客户端会把上一轮 assistant response 放回完整
		// history。previous_response_id 已经包含同一 output，必须只跳过
		// 这一条 assistant 消息；否则每轮都会把上一轮正文/工具调用再注入
		// 服务端链，造成上下文和费用线性额外增长。
		if claudeAssistantOutputFingerprint(suffix[0]) != state.OutputFingerprint {
			session.DefaultResponseChainManager().Clear(conversationID)
			return
		}
		suffix = suffix[1:]
	}
	if len(suffix) == 0 {
		return
	}
	req.Input = claudeMessagesToResponsesInput(suffix, includeThinking)
	if len(req.Input) == 0 {
		return
	}
	req.PreviousResponseID = state.ResponseID
}

func rememberResponsesChainFromBody(conversationID string, claudeReq *types.ClaudeRequest, upstream *config.UpstreamConfig, model string, body []byte) {
	if conversationID == "" || len(body) == 0 || claudeReq == nil {
		return
	}
	if upstream == nil || !upstream.EnablePreviousResponseID {
		return
	}
	responseID := gjson.GetBytes(body, "id").String()
	if responseID == "" {
		responseID = gjson.GetBytes(body, "response.id").String()
	}
	if responseID == "" {
		return
	}
	var response struct {
		Output []types.ResponsesItem `json:"output"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return
	}
	rememberResponsesChain(conversationID, claudeReq, upstream, model, responseID,
		responsesAssistantOutputFingerprint(response.Output))
}

// rememberResponsesChain 将上游 response id 与当前 messages 指纹写入会话链。
func rememberResponsesChain(conversationID string, claudeReq *types.ClaudeRequest, upstream *config.UpstreamConfig, model string, responseID, outputFingerprint string) {
	if conversationID == "" || responseID == "" || claudeReq == nil {
		return
	}
	if upstream == nil || !upstream.EnablePreviousResponseID || outputFingerprint == "" {
		if outputFingerprint == "" {
			session.DefaultResponseChainManager().Clear(conversationID)
		}
		return
	}
	// Full-history clients grow MessageCount without bound; remembering long histories
	// makes the next applyPreviousResponseIDChain attach a huge uncompacted server chain.
	// Only remember short sessions suitable for true incremental previous_response_id.
	if len(claudeReq.Messages) > 4 {
		session.DefaultResponseChainManager().Clear(conversationID)
		return
	}
	session.DefaultResponseChainManager().Set(conversationID, session.ResponseChainState{
		ResponseID:         responseID,
		MessageCount:       len(claudeReq.Messages),
		OutputFingerprint:  outputFingerprint,
		MessageFingerprint: utils.CanonicalJSON(claudeReq.Messages),
		SystemFingerprint:  extractSystemText(claudeReq.System),
		ToolsFingerprint:   utils.CanonicalJSON(normalizeToolsForPromptCacheKey(claudeReq.Tools)),
		BaseURL:            responseChainBaseURL(upstream),
		Model:              model,
	})
}

func claudeAssistantOutputFingerprint(message types.ClaudeMessage) string {
	rawItems := claudeAssistantMessageToResponsesItems(message, false)
	if len(rawItems) == 0 {
		return ""
	}
	data, err := json.Marshal(rawItems)
	if err != nil {
		return ""
	}
	var items []types.ResponsesItem
	if err := json.Unmarshal(data, &items); err != nil {
		return ""
	}
	return responsesAssistantOutputFingerprint(items)
}

// responsesAssistantOutputFingerprint 只比较会被 Claude 客户端重放的正文和
// 工具调用，忽略 Responses 服务端专有的 reasoning、item id 和 status。
func responsesAssistantOutputFingerprint(items []types.ResponsesItem) string {
	visibleItems := make([]types.ResponsesItem, 0, len(items))
	var textParts []string
	flushText := func() {
		if len(textParts) == 0 {
			return
		}
		visibleItems = append(visibleItems, types.ResponsesItem{
			Type: "message", Role: "assistant",
			Content: []interface{}{map[string]interface{}{
				"type": "output_text", "text": strings.Join(textParts, ""),
			}},
		})
		textParts = nil
	}
	for _, item := range items {
		switch item.Type {
		case "message", "text":
			if text := responsesAssistantText(item.Content); text != "" {
				textParts = append(textParts, text)
			}
		case "function_call":
			flushText()
			callID := item.CallID
			if callID == "" {
				callID = strings.TrimPrefix(item.ID, "fc_")
				if callID == item.ID {
					callID = strings.TrimPrefix(item.ID, "ctc_")
				}
			}
			visibleItems = append(visibleItems, types.ResponsesItem{
				Type: "function_call", CallID: callID,
				Name: item.Name, Arguments: item.Arguments,
			})
		case "custom_tool_call":
			flushText()
			callID := item.CallID
			if callID == "" {
				callID = strings.TrimPrefix(item.ID, "ctc_")
			}
			args, _ := json.Marshal(map[string]interface{}{"input": responsesToolOutput(item.Content)})
			visibleItems = append(visibleItems, types.ResponsesItem{
				Type: "function_call", CallID: callID,
				Name: item.Name, Arguments: string(args),
			})
		}
	}
	flushText()
	if len(visibleItems) == 0 {
		return ""
	}
	normalized := make([]interface{}, 0, len(visibleItems))
	for _, item := range visibleItems {
		normalized = append(normalized, map[string]interface{}{
			"type": item.Type, "call_id": item.CallID,
			"name": item.Name, "arguments": normalizeResponseArguments(item.Arguments),
			"content": normalizeResponsesAssistantContent(item.Content),
		})
	}
	sum := sha256.Sum256([]byte(utils.CanonicalJSON(normalized)))
	return hex.EncodeToString(sum[:])
}

func responsesAssistantText(content interface{}) string {
	if text, ok := content.(string); ok {
		return text
	}
	var parts []string
	for _, block := range utils.NormalizeContentBlocks(content) {
		if text, ok := utils.ExtractTextFromBlock(block); ok {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "")
}

func normalizeResponseArguments(arguments string) interface{} {
	if arguments == "" {
		return ""
	}
	var value interface{}
	if err := json.Unmarshal([]byte(arguments), &value); err == nil {
		return value
	}
	return arguments
}

func normalizeResponsesAssistantContent(content interface{}) interface{} {
	if text, ok := content.(string); ok {
		return []interface{}{map[string]interface{}{"type": "output_text", "text": text}}
	}
	blocks := utils.NormalizeContentBlocks(content)
	if len(blocks) == 0 {
		return content
	}
	normalized := make([]interface{}, 0, len(blocks))
	for _, block := range blocks {
		if text, ok := utils.ExtractTextFromBlock(block); ok {
			normalized = append(normalized, map[string]interface{}{
				"type": "output_text", "text": text,
			})
			continue
		}
		normalized = append(normalized, block)
	}
	return normalized
}

func responseChainBaseURL(upstream *config.UpstreamConfig) string {
	if upstream == nil {
		return ""
	}
	return strings.TrimRight(strings.TrimSpace(upstream.GetEffectiveBaseURL()), "/#")
}
