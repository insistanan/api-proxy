package providers

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/BenedictKing/api-proxy/internal/config"
	"github.com/BenedictKing/api-proxy/internal/types"
	"github.com/BenedictKing/api-proxy/internal/utils"
	"github.com/gin-gonic/gin"
)

// OpenAIProvider OpenAI 提供商
type OpenAIProvider struct{}

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
		Ctx:    c.Request.Context(),
		Method: http.MethodPost,
		Path:   "/v1/chat/completions",
		Header: c.Request.Header,
		Body:   originalBodyBytes,
	}, upstream, apiKey)
	if err != nil {
		return nil, originalBodyBytes, err
	}
	return outcome.Request, outcome.OriginalBody, nil
}

// BuildRequest 以显式输入构造 OpenAI Chat 请求（R4-a 主实现，不依赖 gin）。
func (p *OpenAIProvider) BuildRequest(input RequestInput, upstream *config.UpstreamConfig, apiKey string) (*RequestOutcome, error) {
	originalBodyBytes := input.Body

	var claudeReq types.ClaudeRequest
	if err := json.Unmarshal(originalBodyBytes, &claudeReq); err != nil {
		return nil, fmt.Errorf("解析Claude请求体失败: %w", err)
	}

	// Chat 推理模型要求将 reasoning_content 原样随 assistant 历史消息回传。
	// 因此所有客户端统一保留历史 thinking，避免严格上游在下一轮拒绝请求。

	openaiReq := &types.OpenAIRequest{
		Model:       config.ResolveUpstreamModel(claudeReq.Model, upstream),
		Messages:    p.convertMessages(&claudeReq, upstream != nil && upstream.RequireReasoningContent),
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
	url := utils.BuildUpstreamURL(upstream.GetEffectiveBaseURL(), "/v1", "/chat/completions")

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

	return &RequestOutcome{Request: req, OriginalBody: originalBodyBytes}, nil
}

// convertMessages 转换消息
func (p *OpenAIProvider) convertMessages(claudeReq *types.ClaudeRequest, forceReasoningContent bool) []types.OpenAIMessage {
	messages := []types.OpenAIMessage{}

	// 添加系统消息
	if claudeReq.System != nil {
		systemText := extractSystemText(claudeReq.System)
		if systemText != "" {
			messages = append(messages, types.OpenAIMessage{
				Role:    "system",
				Content: systemText,
			})
		}
	}

	for _, msg := range claudeReq.Messages {
		openaiMsg := p.convertMessage(msg, forceReasoningContent)
		messages = append(messages, openaiMsg...)
	}

	return messages
}

// convertMessage 转换单个消息
func (p *OpenAIProvider) convertMessage(msg types.ClaudeMessage, forceReasoningContent bool) []types.OpenAIMessage {
	messages := []types.OpenAIMessage{}

	// 如果是字符串内容
	if str, ok := msg.Content.(string); ok {
		if msg.Role != "tool" {
			openaiMsg := types.OpenAIMessage{
				Role:    normalizeRole(msg.Role),
				Content: str,
			}
			if openaiMsg.Role == "assistant" && forceReasoningContent {
				openaiMsg.ReasoningContent = reasoningContentForAssistantMessage(openaiMsg, true)
			}
			messages = append(messages, openaiMsg)
		}
		return messages
	}

	contents := utils.NormalizeContentBlocks(msg.Content)
	if len(contents) == 0 {
		return messages
	}

	textContents := []string{}
	reasoningContents := []string{}
	toolCalls := []types.OpenAIToolCall{}
	multimodalContents := []map[string]interface{}{}
	hasVisionContent := false
	hasRedactedThinking := false
	flushAssistantOrUserMessage := func() {
		if len(textContents) == 0 && len(reasoningContents) == 0 && len(toolCalls) == 0 && len(multimodalContents) == 0 {
			return
		}
		role := normalizeRole(msg.Role)
		if role == "tool" {
			textContents = nil
			toolCalls = nil
			multimodalContents = nil
			hasVisionContent = false
			return
		}

		openaiMsg := types.OpenAIMessage{
			Role: role,
		}
		if hasVisionContent {
			openaiMsg.Content = multimodalContents
		} else if len(textContents) > 0 {
			openaiMsg.Content = strings.Join(textContents, "")
		} else {
			openaiMsg.Content = nil
		}
		if len(toolCalls) > 0 {
			openaiMsg.ToolCalls = toolCalls
		}
		// 客户端回传历史时可能把明文 thinking 转成 redacted_thinking 或直接丢弃，
		// 导致本消息没有任何明文 reasoning。用完整 assistant 消息（文本 + 工具调用）
		// 从缓存补回，满足 Chat 推理模型（如 DeepSeek）对原样回传的校验。若旧会话
		// 重启后缓存已不存在，则工具调用历史至少携带兼容续接值，避免严格渠道因
		// reasoning_content 缺失直接拒绝请求。
		if role == "assistant" && len(reasoningContents) == 0 {
			if reasoning := reasoningContentForAssistantMessage(openaiMsg, forceReasoningContent || hasRedactedThinking); reasoning != "" {
				reasoningContents = append(reasoningContents, reasoning)
			}
		}
		if role == "assistant" && len(reasoningContents) > 0 {
			openaiMsg.ReasoningContent = strings.Join(reasoningContents, "")
		}

		messages = append(messages, openaiMsg)
		textContents = nil
		reasoningContents = nil
		toolCalls = nil
		multimodalContents = nil
		hasVisionContent = false
		hasRedactedThinking = false
	}

	for _, content := range contents {
		contentType, _ := content["type"].(string)

		switch contentType {
		case "thinking":
			if normalizeRole(msg.Role) == "assistant" {
				if thinking, _ := content["thinking"].(string); thinking != "" {
					reasoningContents = append(reasoningContents, thinking)
				}
			}
		case "redacted_thinking":
			// 记录该历史回合确实产生过推理；缓存失效时仍需补齐严格 Chat
			// 渠道所要求的 reasoning_content 字段。
			hasRedactedThinking = normalizeRole(msg.Role) == "assistant"
		case "text":
			if text, ok := content["text"].(string); ok {
				textContents = append(textContents, text)
				multimodalContents = append(multimodalContents, map[string]interface{}{
					"type": "text",
					"text": text,
				})
			}
		case "image", "image_url", "input_image":
			if imageBlock, ok := utils.ToOpenAIImageContentBlock(content); ok {
				hasVisionContent = true
				multimodalContents = append(multimodalContents, imageBlock)
			}

		case "tool_use":
			id, _ := content["id"].(string)
			name, _ := content["name"].(string)
			input := content["input"]

			inputJSON, _ := json.Marshal(input)
			toolCalls = append(toolCalls, types.OpenAIToolCall{
				ID:   id,
				Type: "function",
				Function: types.OpenAIToolCallFunction{
					Name:      name,
					Arguments: string(inputJSON),
				},
			})

		case "tool_result":
			if normalizeRole(msg.Role) != "user" {
				flushAssistantOrUserMessage()
			}

			toolUseID, _ := content["tool_use_id"].(string)
			resultContent := content["content"]

			var contentStr string
			if str, ok := resultContent.(string); ok {
				contentStr = str
			} else {
				contentJSON, _ := json.Marshal(resultContent)
				contentStr = string(contentJSON)
			}

			messages = append(messages, types.OpenAIMessage{
				Role:       "tool",
				ToolCallID: toolUseID,
				Content:    contentStr,
			})
		}
	}

	flushAssistantOrUserMessage()

	return messages
}

// convertTools 转换工具
func (p *OpenAIProvider) convertTools(claudeTools []types.ClaudeTool) []types.OpenAITool {
	tools := []types.OpenAITool{}

	for _, tool := range claudeTools {
		if tool.Name == "" || tool.Name == "BatchTool" {
			continue
		}
		tools = append(tools, types.OpenAITool{
			Type: "function",
			Function: types.OpenAIToolFunction{
				Name:        tool.Name,
				Description: tool.Description,
				Parameters:  cleanJsonSchema(tool.InputSchema),
			},
		})
	}

	return tools
}

// cleanJsonSchema 清理 JSON Schema，移除某些上游不支持的字段
func cleanJsonSchema(schema interface{}) interface{} {
	cleaned := cleanJsonSchemaFields(schema)
	// 根级缺 type/properties 时补最小 object schema（cc-switch clean_schema 同语义）：
	// 部分严格 OpenAI 兼容网关对缺根 type 的 function parameters 直接 400。
	if schemaMap, ok := cleaned.(map[string]interface{}); ok {
		if _, hasType := schemaMap["type"]; !hasType {
			schemaMap["type"] = "object"
			if _, hasProps := schemaMap["properties"]; !hasProps {
				schemaMap["properties"] = map[string]interface{}{}
			}
		}
	}
	return cleaned
}

func cleanJsonSchemaFields(schema interface{}) interface{} {
	if schema == nil {
		return schema
	}

	// 如果是 map，递归清理
	if schemaMap, ok := schema.(map[string]interface{}); ok {
		cleaned := make(map[string]interface{})

		for key, value := range schemaMap {
			// 移除不需要的字段
			if key == "$schema" || key == "title" || key == "examples" || key == "additionalProperties" {
				continue
			}
			// 移除 format 字段（当类型为 string 时）
			if key == "format" {
				if schemaType, hasType := schemaMap["type"]; hasType && schemaType == "string" {
					continue
				}
			}
			// 递归处理嵌套对象
			if key == "properties" || key == "items" {
				cleaned[key] = cleanJsonSchemaFields(value)
			} else if valueMap, isMap := value.(map[string]interface{}); isMap {
				cleaned[key] = cleanJsonSchemaFields(valueMap)
			} else if valueSlice, isSlice := value.([]interface{}); isSlice {
				cleanedSlice := make([]interface{}, len(valueSlice))
				for i, item := range valueSlice {
					cleanedSlice[i] = cleanJsonSchemaFields(item)
				}
				cleaned[key] = cleanedSlice
			} else {
				cleaned[key] = value
			}
		}

		return cleaned
	}

	// 如果是数组，递归清理每个元素
	if schemaSlice, ok := schema.([]interface{}); ok {
		cleaned := make([]interface{}, len(schemaSlice))
		for i, item := range schemaSlice {
			cleaned[i] = cleanJsonSchemaFields(item)
		}
		return cleaned
	}

	// 其他类型直接返回
	return schema
}

// ConvertToClaudeResponse 转换为 Claude 响应
func (p *OpenAIProvider) ConvertToClaudeResponse(providerResp *types.ProviderResponse) (*types.ClaudeResponse, error) {
	// 少数 OpenAI 兼容网关会接受 Chat Completions 请求却返回 Responses
	// 结构。先识别这类成功响应，避免图片理解层因没有 choices 而丢失结果。
	var responseShape struct {
		Choices json.RawMessage `json:"choices"`
		Output  json.RawMessage `json:"output"`
	}
	if err := json.Unmarshal(providerResp.Body, &responseShape); err != nil {
		return nil, err
	}
	if len(responseShape.Output) > 0 && string(responseShape.Output) != "null" &&
		(len(responseShape.Choices) == 0 || string(responseShape.Choices) == "null" || string(responseShape.Choices) == "[]") {
		var responsesResp types.ResponsesResponse
		if err := json.Unmarshal(providerResp.Body, &responsesResp); err != nil {
			return nil, err
		}
		return responsesResponseToClaude(&responsesResp), nil
	}

	var openaiResp types.OpenAIResponse
	if err := json.Unmarshal(providerResp.Body, &openaiResp); err != nil {
		return nil, err
	}
	populateOpenAIReasoningContent(&openaiResp, providerResp.Body)

	var usageEnvelope openAIUsageEnvelope
	if err := json.Unmarshal(providerResp.Body, &usageEnvelope); err != nil {
		return nil, err
	}

	claudeResp := &types.ClaudeResponse{
		ID:      generateID(),
		Type:    "message",
		Role:    "assistant",
		Content: []types.ClaudeContent{},
	}

	if len(openaiResp.Choices) > 0 {
		choice := openaiResp.Choices[0]
		msg := choice.Message
		visibleContent := ""
		thinkingContent := ""
		if content, ok := msg.Content.(string); ok {
			var embeddedReasoning string
			visibleContent, embeddedReasoning = splitEmbeddedReasoning(content)
			thinkingContent = msg.ReasoningContent + embeddedReasoning
		} else {
			thinkingContent = msg.ReasoningContent
		}

		// 剥离 content 中与 reasoning_content 重复的前缀：部分上游在 content
		// 中原样重复了 reasoning_content 的内容，需要去除以避免正文出现两份。
		if thinkingContent != "" && visibleContent != "" {
			visibleContent = stripReasoningPrefixFromContent(visibleContent, thinkingContent)
		}

		// 缓存 reasoning_content，供客户端回传历史时补回（Chat 推理模型要求）。
		// Chat 推理模型会要求下一轮回传 reasoning_content。只在代理内部缓存，
		// 不向 Messages 客户端发送 thinking：Cursor 会把它写入本地历史并在每轮重发，
		// 既泄漏思考过程，也会快速占满上下文。
		if thinkingContent != "" {
			cacheMessage := msg
			cacheMessage.Content = visibleContent
			storeReasoningForAssistantMessage(cacheMessage, thinkingContent)
		}

		// 添加文本内容
		if visibleContent != "" {
			claudeResp.Content = append(claudeResp.Content, types.ClaudeContent{
				Type: "text",
				Text: visibleContent,
			})
		}

		// 添加工具调用
		for _, toolCall := range msg.ToolCalls {
			var input interface{}
			json.Unmarshal([]byte(toolCall.Function.Arguments), &input)

			claudeResp.Content = append(claudeResp.Content, types.ClaudeContent{
				Type:  "tool_use",
				ID:    toolCall.ID,
				Name:  toolCall.Function.Name,
				Input: input,
			})
		}

		// 设置停止原因
		if len(msg.ToolCalls) > 0 {
			claudeResp.StopReason = "tool_use"
		} else if choice.FinishReason == "length" {
			claudeResp.StopReason = "max_tokens"
		} else {
			claudeResp.StopReason = "end_turn"
		}
	}

	// 添加使用统计
	if openaiResp.Usage != nil {
		normalizedUsage := normalizeOpenAIUsage(openaiResp.Usage, usageEnvelope.Usage)

		claudeResp.Usage = &types.Usage{
			InputTokens:                normalizedUsage.InputTokens,
			OutputTokens:               normalizedUsage.OutputTokens,
			CacheCreationInputTokens:   normalizedUsage.CacheCreationInputTokens,
			CacheReadInputTokens:       normalizedUsage.CacheReadInputTokens,
			CacheCreation5mInputTokens: normalizedUsage.CacheCreation5mInputTokens,
			CacheCreation1hInputTokens: normalizedUsage.CacheCreation1hInputTokens,
			CacheTTL:                   normalizedUsage.CacheTTL,
		}
	}

	return claudeResp, nil
}

// HandleStreamResponse 处理流式响应
// 兼容旧签名：内部委托带 ctx 的版本，使用 context.Background()。
func (p *OpenAIProvider) HandleStreamResponse(body io.ReadCloser) (<-chan string, <-chan error, error) {
	return p.HandleStreamResponseCtx(context.Background(), body)
}

// HandleStreamResponseCtx 处理流式响应（支持客户端断连中止）。
// 协议转换逻辑在 chat_stream.go 的 chatToClaudeStreamState；本函数只负责
// streamPump 脚手架（事件/错误双通道、断连中止、SSE scanner）与状态机驱动。
func (p *OpenAIProvider) HandleStreamResponseCtx(ctx context.Context, body io.ReadCloser) (<-chan string, <-chan error, error) {
	pump := newStreamPump(ctx)
	eventChan, errChan := pump.eventChan, pump.errChan

	go func() {
		defer close(eventChan)
		defer close(errChan)
		defer body.Close()

		send := pump.send
		fail := pump.fail

		state := newChatToClaudeStreamState()
		scanner := pump.newScanner(body)

		for scanner.Scan() {
			for _, event := range state.ProcessLine(scanner.Text()) {
				if !send(event) {
					return
				}
			}
		}
		if err := scanner.Err(); err != nil {
			if isDisconnectLikeError(err) {
				// 客户端主动断开：不再伪造成功收尾。
				return
			}
			fail(err)
			return
		}
		if state.Failed() {
			// 上游错误事件：经 errChan 交给消费端统一写 error 事件并补协议终止序列，
			// 生产端不再伪造 message_stop。
			fail(fmt.Errorf("upstream returned an error event"))
			return
		}
		for _, event := range state.Finish() {
			if !send(event) {
				return
			}
		}
	}()

	return eventChan, errChan, nil
}

// ToolCallAccumulator 工具调用累加器（Chat / Responses 上游流共用）。
type ToolCallAccumulator struct {
	ID                 string
	Name               string
	Arguments          string
	BlockIndex         int
	Started            bool
	StartEventEmitted  bool
	EmittedArgumentLen int
}

type openAIUsageEnvelope struct {
	Usage *openAIUsageDetails `json:"usage"`
}

type openAIUsageDetails struct {
	InputTokens                int                `json:"input_tokens"`
	OutputTokens               int                `json:"output_tokens"`
	PromptTokens               int                `json:"prompt_tokens"`
	CompletionTokens           int                `json:"completion_tokens"`
	CacheCreationInputTokens   int                `json:"cache_creation_input_tokens"`
	CacheReadInputTokens       int                `json:"cache_read_input_tokens"`
	CacheCreation5mInputTokens int                `json:"cache_creation_5m_input_tokens"`
	CacheCreation1hInputTokens int                `json:"cache_creation_1h_input_tokens"`
	CacheTTL                   string             `json:"cache_ttl"`
	CachedContentTokenCount    int                `json:"cachedContentTokenCount"`
	PromptTokenDetails         openAITokenDetails `json:"prompt_tokens_details"`
	InputTokenDetails          openAITokenDetails `json:"input_tokens_details"`
}

type openAITokenDetails struct {
	CachedTokens int `json:"cached_tokens"`
}

func normalizeOpenAIUsage(base *types.Usage, details *openAIUsageDetails) types.Usage {
	var usage types.Usage
	if base != nil {
		usage = *base
	}
	if details != nil {
		usage.InputTokens = firstPositiveInt(details.InputTokens, details.PromptTokens, usage.InputTokens, usage.PromptTokens)
		usage.OutputTokens = firstPositiveInt(details.OutputTokens, details.CompletionTokens, usage.OutputTokens, usage.CompletionTokens)
		usage.CacheCreationInputTokens = firstPositiveInt(details.CacheCreationInputTokens, usage.CacheCreationInputTokens)
		cacheReadFromDetails := firstPositiveInt(
			details.InputTokenDetails.CachedTokens,
			details.PromptTokenDetails.CachedTokens,
			details.CachedContentTokenCount,
		)
		usage.CacheReadInputTokens = firstPositiveInt(
			details.CacheReadInputTokens,
			cacheReadFromDetails,
			usage.CacheReadInputTokens,
		)
		usage.CacheCreation5mInputTokens = firstPositiveInt(details.CacheCreation5mInputTokens, usage.CacheCreation5mInputTokens)
		usage.CacheCreation1hInputTokens = firstPositiveInt(details.CacheCreation1hInputTokens, usage.CacheCreation1hInputTokens)
		if details.CacheTTL != "" {
			usage.CacheTTL = details.CacheTTL
		}
		// input_tokens 收敛到 Anthropic uncached 口径。
		// Anthropic 契约由客户端自行求和：
		//   total_input = input_tokens + cache_read_input_tokens + cache_creation_input_tokens
		// 所以在下发 cache_read 的同时把总量塞进 input_tokens，规范客户端会把缓存前缀算两遍，
		// 上下文满度虚高，表现为 compact 之后立刻又触发 compact。
		//
		// 减不减由 cache 量的**字段名来源**决定，不看数值：
		//   - cached_tokens / prompt_tokens_details.cached_tokens / cachedContentTokenCount
		//     是 subset 式命名（OpenAI、Gemini 语义下 cached ⊆ prompt/input），此时上游报的
		//     input 是总量，必须减。
		//   - cache_read_input_tokens 是 Anthropic 式命名，上游本就按 uncached 报 input，
		//     再减一次就是双减（对照 converters.TestExtractUsageMetrics_ClaudeCacheReadDoesNotDoubleSubtract）。
		// 与 converters.ExtractUsageMetrics 同一判据，两条路径不允许分叉。
		//
		// 减法绑定在"本次 details 报出的 input 量"而非最终 usage.InputTokens，因此对流式
		// mergeOpenAIUsageFromChunk 的反复调用幂等：本 chunk 报了 input 就重算一次完整减法，
		// 没报就保留上一轮已减的值，不会二次相减。
		reportedInput := firstPositiveInt(details.InputTokens, details.PromptTokens)
		if reportedInput > 0 && cacheReadFromDetails > 0 && details.CacheReadInputTokens == 0 {
			usage.InputTokens = reportedInput - cacheReadFromDetails
			if usage.InputTokens < 0 {
				// 上游异常回包（cached > prompt）。钳制到 0 而不是放行负数：
				// 负数会流进客户端的上下文估算与 sqlite 指标日志。
				usage.InputTokens = 0
			}
		}
	}
	return usage
}

func mergeOpenAIUsageFromChunk(chunk map[string]interface{}, dst *types.Usage) bool {
	if chunk == nil || dst == nil {
		return false
	}
	usageRaw, ok := chunk["usage"]
	if !ok || usageRaw == nil {
		return false
	}

	payload, err := json.Marshal(map[string]interface{}{"usage": usageRaw})
	if err != nil {
		return false
	}
	var envelope openAIUsageEnvelope
	if err := json.Unmarshal(payload, &envelope); err != nil || envelope.Usage == nil {
		return false
	}
	normalized := normalizeOpenAIUsage(dst, envelope.Usage)
	*dst = normalized
	return openAIUsageHasData(normalized)
}

func buildOpenAIMessageDeltaEvent(stopReason string, usage types.Usage, hasUsage bool) string {
	// Cache fields are emitted for both the admin stream collector and the Anthropic client.
	// The client adds cache_read/cache_creation back when sizing the context window. That
	// addition is only correct because
	// normalizeOpenAIUsage has already reduced input_tokens to the uncached remainder --
	// the two must stay in lockstep: re-introducing total-style input_tokens here would
	// restore the double-count. Which client is on the other end is not knowable here, so do
	// not re-introduce a vendor name in this comment.
	usageMap := map[string]interface{}{
		"output_tokens": usage.OutputTokens,
	}
	if hasUsage {
		if usage.InputTokens > 0 {
			usageMap["input_tokens"] = usage.InputTokens
		}
		if usage.CacheCreationInputTokens > 0 {
			usageMap["cache_creation_input_tokens"] = usage.CacheCreationInputTokens
		}
		if usage.CacheReadInputTokens > 0 {
			usageMap["cache_read_input_tokens"] = usage.CacheReadInputTokens
			usageMap["input_tokens_details"] = map[string]interface{}{
				"cached_tokens": usage.CacheReadInputTokens,
			}
		}
		if usage.CacheCreation5mInputTokens > 0 {
			usageMap["cache_creation_5m_input_tokens"] = usage.CacheCreation5mInputTokens
		}
		if usage.CacheCreation1hInputTokens > 0 {
			usageMap["cache_creation_1h_input_tokens"] = usage.CacheCreation1hInputTokens
		}
		if usage.CacheTTL != "" {
			usageMap["cache_ttl"] = usage.CacheTTL
		}
	}
	deltaEvent := map[string]interface{}{
		"type": "message_delta",
		"delta": map[string]interface{}{
			"stop_reason":   stopReason,
			"stop_sequence": nil,
		},
		"usage": usageMap,
	}
	deltaJSON, _ := json.Marshal(deltaEvent)
	return fmt.Sprintf("event: message_delta\ndata: %s\n\n", deltaJSON)
}

func openAIUsageHasData(usage types.Usage) bool {
	return usage.InputTokens > 0 ||
		usage.OutputTokens > 0 ||
		usage.CacheCreationInputTokens > 0 ||
		usage.CacheReadInputTokens > 0 ||
		usage.CacheCreation5mInputTokens > 0 ||
		usage.CacheCreation1hInputTokens > 0
}

func firstPositiveInt(values ...int) int {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}

func shouldFlushOpenAITextDelta(buffer string, latest string) bool {
	if len(buffer) >= 96 {
		return true
	}
	if strings.Contains(latest, "\n") {
		return true
	}
	trimmed := strings.TrimSpace(buffer)
	if trimmed == "" {
		return false
	}
	for _, r := range []rune(trimmed) {
		switch r {
		case '。', '！', '？', '；', '：', '.', '!', '?', ';', ':':
			return true
		}
	}
	return false
}

// processToolUsePart 处理工具使用部分
func processToolUsePart(id, name string, input interface{}, index int) []string {
	events := []string{}

	// content_block_start
	startEvent := map[string]interface{}{
		"type":  "content_block_start",
		"index": index,
		"content_block": map[string]interface{}{
			"type":  "tool_use",
			"id":    id,
			"name":  name,
			"input": map[string]interface{}{},
		},
	}
	startJSON, _ := json.Marshal(startEvent)
	events = append(events, fmt.Sprintf("event: content_block_start\ndata: %s\n\n", startJSON))

	// content_block_delta
	inputJSON, _ := json.Marshal(input)
	deltaEvent := map[string]interface{}{
		"type":  "content_block_delta",
		"index": index,
		"delta": map[string]string{
			"type":         "input_json_delta",
			"partial_json": string(inputJSON),
		},
	}
	deltaJSON, _ := json.Marshal(deltaEvent)
	events = append(events, fmt.Sprintf("event: content_block_delta\ndata: %s\n\n", deltaJSON))

	// content_block_stop
	stopEvent := map[string]interface{}{
		"type":  "content_block_stop",
		"index": index,
	}
	stopJSON, _ := json.Marshal(stopEvent)
	events = append(events, fmt.Sprintf("event: content_block_stop\ndata: %s\n\n", stopJSON))

	return events
}

// 辅助函数

func (p *OpenAIProvider) applyTokenLimit(openaiReq *types.OpenAIRequest, claudeReq *types.ClaudeRequest, upstream *config.UpstreamConfig) {
	tokenValue := 65535
	if claudeReq.MaxCompletionTokens > 0 {
		tokenValue = claudeReq.MaxCompletionTokens
	} else if claudeReq.MaxTokens > 0 {
		tokenValue = claudeReq.MaxTokens
	}

	if shouldUseMaxCompletionTokens(openaiReq.Model, claudeReq, upstream) {
		openaiReq.MaxCompletionTokens = tokenValue
		return
	}

	openaiReq.MaxTokens = tokenValue
}

func shouldUseMaxCompletionTokens(targetModel string, claudeReq *types.ClaudeRequest, upstream *config.UpstreamConfig) bool {
	if claudeReq.MaxCompletionTokens > 0 {
		return true
	}

	// Kimi/Moonshot 兼容网关更容易接受 max_completion_tokens。
	if looksLikeKimiOrMoonshot(targetModel) {
		return true
	}

	// OpenAI o-series / gpt-5 推理模型只接受 max_completion_tokens
	//（cc-switch transform.rs 同判据；对官方与兼容网关都发 max_tokens 会被 400 拒绝）。
	if isOpenAIReasoningModelFamily(targetModel) {
		return true
	}

	if upstream == nil {
		return false
	}

	if looksLikeKimiOrMoonshot(upstream.Name) || looksLikeKimiOrMoonshot(upstream.BaseURL) {
		return true
	}

	for sourceModel, mappedModels := range upstream.ModelMapping {
		if sourceModel == claudeReq.Model && len(mappedModels) > 0 && looksLikeKimiOrMoonshot(mappedModels[0]) {
			return true
		}
	}

	return false
}

// isOpenAIReasoningModelFamily 判断目标模型是否属于只接受 max_completion_tokens 的
// OpenAI 推理模型家族（o1/o3/o4 系列与 gpt-5 系列）。子串匹配与 cc-switch 的
// is_openai_o_series 判据一致。
func isOpenAIReasoningModelFamily(model string) bool {
	value := strings.ToLower(model)
	return strings.Contains(value, "o1") || strings.Contains(value, "o3") ||
		strings.Contains(value, "o4") || strings.Contains(value, "gpt-5")
}

func looksLikeKimiOrMoonshot(value string) bool {
	value = strings.ToLower(value)
	return strings.Contains(value, "kimi") || strings.Contains(value, "moonshot")
}

// convertToolChoice 转换 tool_choice
func (p *OpenAIProvider) convertToolChoice(toolChoice interface{}) interface{} {
	if toolChoice == nil {
		return nil
	}

	// 字符串格式：Claude "auto"/"any" → OpenAI "auto"/"required"
	if str, ok := toolChoice.(string); ok {
		switch str {
		case "any":
			return "required"
		case "auto":
			return "auto"
		case "none":
			return "none"
		default:
			return "auto"
		}
	}

	// 对象格式：Claude {type: "auto"/"any"/"tool", name: "..."} → OpenAI 对应格式
	if obj, ok := toolChoice.(map[string]interface{}); ok {
		tcType, _ := obj["type"].(string)
		switch tcType {
		case "auto":
			return "auto"
		case "any":
			return "required"
		case "none":
			return "none"
		case "tool":
			name, _ := obj["name"].(string)
			if name != "" {
				return map[string]interface{}{
					"type": "function",
					"function": map[string]string{
						"name": name,
					},
				}
			}
			return "auto"
		default:
			return "auto"
		}
	}

	return nil
}

// convertThinkingToReasoningEffort 保留给测试/兼容调用；实现统一走 resolveClaudeReasoningEffort。
func (p *OpenAIProvider) convertThinkingToReasoningEffort(thinking interface{}) string {
	return resolveClaudeReasoningEffort(&types.ClaudeRequest{Thinking: thinking})
}

// buildClaudeChatPromptCacheKey builds a stable prompt_cache_key for Messages->Chat.
func buildClaudeChatPromptCacheKey(claudeReq *types.ClaudeRequest, upstream *config.UpstreamConfig, model string) string {
	if claudeReq == nil {
		return ""
	}
	channel := ""
	baseURL := ""
	if upstream != nil {
		channel = strings.TrimSpace(upstream.Name)
		baseURL = strings.TrimRight(upstream.GetEffectiveBaseURL(), "/#")
	}
	stableParts := map[string]interface{}{
		"protocol": "claude-messages-to-openai-chat-v1",
		"model":    model,
		"channel":  channel,
		"baseURL":  baseURL,
		"system":   extractSystemText(claudeReq.System),
		"tools":    normalizeToolsForPromptCacheKey(claudeReq.Tools),
	}
	sum := sha256.Sum256([]byte(utils.CanonicalJSON(stableParts)))
	return "claude-chat-" + hex.EncodeToString(sum[:])[:24]
}

func extractSystemText(system interface{}) string {
	if str, ok := system.(string); ok {
		return str
	}

	// 可能是数组
	arr, ok := system.([]interface{})
	if !ok {
		return ""
	}

	parts := []string{}
	for _, item := range arr {
		obj, ok := item.(map[string]interface{})
		if !ok {
			continue
		}

		if obj["type"] == "text" {
			if text, ok := obj["text"].(string); ok {
				parts = append(parts, text)
			}
		}
	}

	return strings.Join(parts, "\n")
}

func normalizeRole(role string) string {
	role = strings.ToLower(role)
	switch role {
	case "user", "assistant", "system", "tool":
		return role
	default:
		return "user"
	}
}

func generateID() string {
	return fmt.Sprintf("msg_%d", time.Now().UnixNano())
}

// extractOpenAIMessageText 提取 OpenAI 响应消息中的纯文本（用于 reasoning 缓存键）。
// Content 可能是 string，也可能是部分 OpenAI 兼容网关返回的内容块数组。
func extractOpenAIMessageText(msg types.OpenAIMessage) string {
	switch v := msg.Content.(type) {
	case string:
		return v
	case []interface{}:
		var b strings.Builder
		for _, item := range v {
			obj, ok := item.(map[string]interface{})
			if !ok {
				continue
			}
			if t, _ := obj["type"].(string); t == "text" {
				if text, ok := obj["text"].(string); ok {
					b.WriteString(text)
				}
			}
		}
		return b.String()
	}
	return ""
}

// extractOpenAIReasoningContent 兼容部分 Chat 网关使用的非标准推理字段。
// 对外仍统一发送 reasoning_content，满足严格渠道的历史消息校验。
func extractOpenAIReasoningContent(fields map[string]interface{}) string {
	for _, key := range []string{"reasoning_content", "reasoning", "thinking"} {
		if value, ok := fields[key].(string); ok && value != "" {
			return value
		}
	}
	return ""
}

// splitEmbeddedReasoning 隐藏部分 Chat 网关把推理直接塞进 content 的常见标签格式。
// 没有标签时原样返回，避免误伤普通回答文本。
func splitEmbeddedReasoning(content string) (visible, reasoning string) {
	visibleBuilder := strings.Builder{}
	reasoningBuilder := strings.Builder{}
	reasoningMode := false
	visible, reasoning = splitEmbeddedReasoningDelta(content, &reasoningMode)
	visibleBuilder.WriteString(visible)
	reasoningBuilder.WriteString(reasoning)
	return visibleBuilder.String(), reasoningBuilder.String()
}

// stripReasoningPrefixFromContent 从 content 中剥离与 reasoning 重复的前缀。
// 部分上游在 content 中原样重复了 reasoning_content 的内容，导致正文出现两份
// 完全相同的思考文本。此函数检查 content 是否以 reasoning 的内容开头，
// 若是则去除重复前缀，返回剩余正文。
func stripReasoningPrefixFromContent(content string, reasoning string) string {
	if reasoning == "" || content == "" {
		return content
	}
	// 完全匹配：content 就是 reasoning 的原样副本
	if content == reasoning {
		return ""
	}
	// 前缀匹配：content 以 reasoning 开头，后面是正文
	if strings.HasPrefix(content, reasoning) {
		remaining := strings.TrimPrefix(content, reasoning)
		// 去除可能的前导空白/换行
		return strings.TrimLeft(remaining, "\r\n")
	}
	// 反向前缀匹配：reasoning 以 content 开头（reasoning 更长），
	// 说明 content 只是 reasoning 的一部分，无独立正文
	if strings.HasPrefix(reasoning, content) {
		return ""
	}
	return content
}

func splitEmbeddedReasoningDelta(content string, reasoningMode *bool) (visible, reasoning string) {
	if content == "" {
		return "", ""
	}
	if reasoningMode == nil {
		return content, ""
	}

	lower := strings.ToLower(content)
	visibleBuilder := strings.Builder{}
	reasoningBuilder := strings.Builder{}
	for len(content) > 0 {
		if *reasoningMode {
			closeIndex, closeLength := findEmbeddedReasoningClose(lower)
			if closeIndex < 0 {
				reasoningBuilder.WriteString(content)
				break
			}
			reasoningBuilder.WriteString(content[:closeIndex])
			content = content[closeIndex+closeLength:]
			lower = lower[closeIndex+closeLength:]
			*reasoningMode = false
			continue
		}

		openIndex, openLength := findEmbeddedReasoningOpen(lower)
		if openIndex < 0 {
			visibleBuilder.WriteString(content)
			break
		}
		visibleBuilder.WriteString(content[:openIndex])
		content = content[openIndex+openLength:]
		lower = lower[openIndex+openLength:]
		*reasoningMode = true
	}
	return visibleBuilder.String(), reasoningBuilder.String()
}

func findEmbeddedReasoningOpen(value string) (int, int) {
	thinkIndex := strings.Index(value, "<think>")
	thinkingIndex := strings.Index(value, "<thinking>")
	if thinkIndex < 0 {
		return thinkingIndex, len("<thinking>")
	}
	if thinkingIndex < 0 || thinkIndex < thinkingIndex {
		return thinkIndex, len("<think>")
	}
	return thinkingIndex, len("<thinking>")
}

func findEmbeddedReasoningClose(value string) (int, int) {
	thinkIndex := strings.Index(value, "</think>")
	thinkingIndex := strings.Index(value, "</thinking>")
	if thinkIndex < 0 {
		return thinkingIndex, len("</thinking>")
	}
	if thinkingIndex < 0 || thinkIndex < thinkingIndex {
		return thinkIndex, len("</think>")
	}
	return thinkingIndex, len("</thinking>")
}

func populateOpenAIReasoningContent(response *types.OpenAIResponse, body []byte) {
	if response == nil || len(response.Choices) == 0 {
		return
	}
	var envelope struct {
		Choices []struct {
			Message map[string]interface{} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return
	}
	for index := range response.Choices {
		if response.Choices[index].Message.ReasoningContent != "" || index >= len(envelope.Choices) {
			continue
		}
		response.Choices[index].Message.ReasoningContent = extractOpenAIReasoningContent(envelope.Choices[index].Message)
	}
}
