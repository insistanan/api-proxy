package converters

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/insistanan/api-proxy/internal/dialogue/prompt/cacheinject"
	"github.com/insistanan/api-proxy/internal/infra/config"
	"github.com/insistanan/api-proxy/internal/protocol/codec"
	"github.com/insistanan/api-proxy/internal/protocol/contract"
	"github.com/insistanan/api-proxy/internal/protocol/items"
	"github.com/insistanan/api-proxy/internal/protocol/types"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const (
	ResponsesUpstreamOpenAI    = "openai"
	ResponsesUpstreamClaude    = "claude"
	ResponsesUpstreamResponses = "responses"
	ResponsesUpstreamGemini    = "gemini"
)

// ConvertResponsesRequestToUpstream converts a Responses entry request to the target upstream protocol.
// upstream may be nil (tests); when set it drives history-thinking and prompt_cache_key options.
func ConvertResponsesRequestToUpstream(serviceType string, model string, bodyBytes []byte, stream bool, sess *types.Session, req *types.ResponsesRequest, upstream *config.UpstreamConfig) ([]byte, error) {
	if serviceType != ResponsesUpstreamResponses && req != nil &&
		respitems.ResponsesItemsContainCompactionFromInput(req.Input) {
		// 非原生 Responses 上游没有 previous_response_id 语义。当前 input
		// 已经是客户端提供的新历史根时，禁止任何协议转换器再把旧 session
		// 追加到它前面；provider 会同步删除请求中的旧链字段。
		sess = nil
	}
	if serviceType != ResponsesUpstreamResponses && req != nil {
		if err := validateResponsesConversion(req, sess, bodyBytes, serviceType); err != nil {
			return nil, err
		}
	}
	switch serviceType {
	case ResponsesUpstreamResponses:
		return convertResponsesPassthroughRequestWithSession(model, bodyBytes, upstream, sess, req)
	case ResponsesUpstreamOpenAI:
		return convertResponsesRequestToOpenAIChat(model, bodyBytes, stream, sess, req, upstream)
	case ResponsesUpstreamClaude:
		return convertResponsesRequestToClaude(sess, req, upstream)
	case ResponsesUpstreamGemini:
		if req == nil {
			return nil, fmt.Errorf("Responses -> Gemini 请求转换缺少解析后的请求")
		}
		return ConvertResponsesToGeminiRequest(model, sess, req)
	default:
		return nil, fmt.Errorf("Responses 上游 serviceType %q 不支持", serviceType)
	}
}

// ConvertUpstreamResponseToResponses 将上游非流式响应转换为 Responses 响应。
func ConvertUpstreamResponseToResponses(serviceType string, originalRequestJSON []byte, upstreamResponseJSON []byte, sessionID string, toolContexts ...*ResponseToolContext) (*types.ResponsesResponse, error) {
	if err := contract.ValidateResponse(serviceType, upstreamResponseJSON); err != nil {
		return nil, err
	}
	switch serviceType {
	case ResponsesUpstreamResponses:
		var response types.ResponsesResponse
		if err := json.Unmarshal(upstreamResponseJSON, &response); err != nil {
			return nil, fmt.Errorf("解析 Responses 响应失败: %w", err)
		}
		// 只把用量映射到统一口径，未知输出字段保留原始 JSON 数字精度。
		var usage struct {
			Usage map[string]interface{} `json:"usage"`
		}
		if err := json.Unmarshal(upstreamResponseJSON, &usage); err != nil {
			return nil, err
		}
		response.Usage = ExtractUsageMetrics(usage.Usage)
		return &response, nil
	case ResponsesUpstreamOpenAI:
		converted, err := ConvertOpenAIChatToResponsesNonStream(context.Background(), "", originalRequestJSON, nil, upstreamResponseJSON, nil, toolContexts...)
		if err != nil {
			return nil, err
		}
		var resp types.ResponsesResponse
		if err := json.Unmarshal([]byte(converted), &resp); err != nil {
			return nil, fmt.Errorf("转换 OpenAI Chat 响应失败: %w", err)
		}
		completion, err := contract.Finish("openai", gjson.GetBytes(upstreamResponseJSON, "choices.0.finish_reason").String())
		if err != nil {
			return nil, err
		}
		for _, item := range resp.Output {
			if item.Type == "function_call" {
				if _, err := contract.Arguments(item.Arguments, item.CallID); err != nil {
					return nil, err
				}
			}
		}
		applyResponseCompletion(&resp, completion)
		return &resp, nil
	case ResponsesUpstreamClaude:
		respMap, err := JSONToMap(upstreamResponseJSON)
		if err != nil {
			return nil, fmt.Errorf("解析 Claude 响应失败: %w", err)
		}
		resp, err := ClaudeResponseToResponses(respMap, sessionID)
		if err != nil {
			return nil, err
		}
		context := responseTools(originalRequestJSON, toolContexts...)
		for i, item := range resp.Output {
			restored, err := restoreResponseToolItem(item, context)
			if err != nil {
				return nil, err
			}
			resp.Output[i] = restored
		}
		completion, err := contract.Finish("claude", gjson.GetBytes(upstreamResponseJSON, "stop_reason").String())
		if err != nil {
			return nil, err
		}
		applyResponseCompletion(resp, completion)
		return resp, nil
	case ResponsesUpstreamGemini:
		return ConvertGeminiResponseToResponses(originalRequestJSON, upstreamResponseJSON, toolContexts...)
	default:
		return nil, fmt.Errorf("Responses 上游 serviceType %q 不支持", serviceType)
	}
}

// ConvertUpstreamStreamLineToResponses 将单行上游 SSE 转换为 Responses SSE。
func ConvertUpstreamStreamLineToResponses(ctx context.Context, serviceType string, model string, originalRequestJSON []byte, line []byte, state *any, toolContexts ...*ResponseToolContext) ([]string, error) {
	if payload, isData := codec.ParseSSEDataLineBytes(line); isData && len(payload) > 0 && string(payload) != codec.SSEDoneMarker {
		root := gjson.ParseBytes(payload)
		if !gjson.ValidBytes(payload) || !root.IsObject() {
			return nil, fmt.Errorf("上游 %s SSE 数据不是有效 JSON 对象", serviceType)
		}
		if serviceType != ResponsesUpstreamResponses {
			if err := contract.ResponseContent(serviceType, payload); err != nil {
				return nil, err
			}
		}
		if serviceType != ResponsesUpstreamResponses && (root.Get("type").String() == "error" ||
			(root.Get("error").Exists() && root.Get("error").Type != gjson.Null)) {
			return []string{emitMapEvent("response.failed", map[string]interface{}{
				"type": "response.failed", "response": map[string]interface{}{"error": root.Value()},
			})}, nil
		}
	}
	switch serviceType {
	case ResponsesUpstreamResponses:
		return []string{string(line) + "\n"}, nil
	case ResponsesUpstreamOpenAI:
		out := ConvertOpenAIChatToResponses(ctx, model, originalRequestJSON, nil, line, state, toolContexts...)
		if st, ok := (*state).(*chatToResponsesState); ok && st.Err != nil {
			return nil, st.Err
		}
		return out, nil
	case ResponsesUpstreamClaude:
		return ConvertClaudeStreamToResponses(ctx, model, originalRequestJSON, line, state, toolContexts...)
	case ResponsesUpstreamGemini:
		return ConvertGeminiStreamToResponses(ctx, model, originalRequestJSON, line, state, toolContexts...)
	default:
		return nil, fmt.Errorf("Responses 上游 serviceType %q 不支持", serviceType)
	}
}

// FinishUpstreamStreamToResponses 在读取结束时定稿，保留 finish_reason 之后的 usage。
// 没有协议定稿信号时返回空事件，由 handler 根据读取结果判定截断或空响应。
func FinishUpstreamStreamToResponses(serviceType string, originalRequestJSON []byte, state *any) ([]string, error) {
	switch serviceType {
	case ResponsesUpstreamOpenAI:
		if st, ok := (*state).(*chatToResponsesState); ok && st.FinishPending {
			out := st.generateCompletedEvents(originalRequestJSON)
			return out, st.Err
		}
	case ResponsesUpstreamGemini:
		if st, ok := (*state).(*responsesStreamState); ok && st.FinishPending {
			out := st.closeAllBlocks()
			out = append(out, st.completed(originalRequestJSON)...)
			return out, st.Err
		}
	case ResponsesUpstreamClaude, ResponsesUpstreamResponses:
		// 这两种协议已经在显式终止事件处定稿。
	default:
		return nil, fmt.Errorf("Responses 上游 serviceType %q 不支持", serviceType)
	}
	return nil, nil
}

func convertResponsesPassthroughRequest(model string, bodyBytes []byte, upstream *config.UpstreamConfig) ([]byte, error) {
	return convertResponsesPassthroughRequestWithSession(model, bodyBytes, upstream, nil, nil)
}

// convertResponsesPassthroughRequestWithSession 保留 Responses 原始 item 的全部扩展
// 字段，只在 previous_response_id 对应的本地 session 能确认客户端重放了完整历史时，
// 从 input 数组裁掉已经存在于服务端链上的前缀。这样不会把客户端的工具结果、图片
// 或 encrypted_content 重新编码成代理内部结构，也不会误删只发送新增后缀的正常请求。
func convertResponsesPassthroughRequestWithSession(
	model string,
	bodyBytes []byte,
	upstream *config.UpstreamConfig,
	sess *types.Session,
	req *types.ResponsesRequest,
) ([]byte, error) {
	if !gjson.ValidBytes(bodyBytes) {
		return nil, fmt.Errorf("透传模式下解析请求失败: 请求体不是有效 JSON")
	}

	result := bodyBytes
	var err error

	// 只原位修改顶层字段，避免为模型映射重排整份 Responses 请求。
	if model != "" && gjson.GetBytes(result, "model").String() != model {
		result, err = sjson.SetBytes(result, "model", model)
		if err != nil {
			return nil, fmt.Errorf("透传模式下修改 model 失败: %w", err)
		}
	}

	if upstream != nil && upstream.DisablePromptCacheKey {
		if gjson.GetBytes(result, "prompt_cache_key").Exists() {
			result, err = sjson.DeleteBytes(result, "prompt_cache_key")
			if err != nil {
				return nil, fmt.Errorf("透传模式下删除 prompt_cache_key 失败: %w", err)
			}
		}
		if gjson.GetBytes(result, "prompt_cache_retention").Exists() {
			result, err = sjson.DeleteBytes(result, "prompt_cache_retention")
			if err != nil {
				return nil, fmt.Errorf("透传模式下删除 prompt_cache_retention 失败: %w", err)
			}
		}
	}

	if req != nil && strings.TrimSpace(req.PreviousResponseID) != "" {
		result, err = trimResponsesPassthroughInput(result, req, sess)
		if err != nil {
			return nil, err
		}
	}

	return result, nil
}

func trimResponsesPassthroughInput(bodyBytes []byte, req *types.ResponsesRequest, sess *types.Session) ([]byte, error) {
	if req == nil || req.Input == nil {
		return bodyBytes, nil
	}

	input := gjson.GetBytes(bodyBytes, "input")
	if !input.Exists() || !input.IsArray() {
		return bodyBytes, nil
	}

	var rawItems []json.RawMessage
	if err := json.Unmarshal([]byte(input.Raw), &rawItems); err != nil {
		return nil, fmt.Errorf("透传模式下解析 input 数组失败: %w", err)
	}
	if len(rawItems) == 0 {
		return bodyBytes, nil
	}
	if respitems.ResponsesRawItemsContainCompaction(rawItems) {
		// compaction item 已经是新的上下文根。继续携带旧的
		// previous_response_id 会把压缩前的服务端链和压缩 item 叠加发送，
		// 这是压缩后上下文迅速恢复到旧长度的直接原因。
		trimmed, err := sjson.DeleteBytes(bodyBytes, "previous_response_id")
		if err != nil {
			return nil, fmt.Errorf("透传模式下删除压缩后的 previous_response_id 失败: %w", err)
		}
		return trimmed, nil
	}
	if sess == nil || len(sess.Messages) == 0 {
		return bodyBytes, nil
	}
	prefixEnd, ok := respitems.ResponsesItemsPrefixMatchRaw(sess.Messages, rawItems)
	if !ok {
		return bodyBytes, nil
	}

	suffix := rawItems[prefixEnd:]
	if suffix == nil {
		suffix = []json.RawMessage{}
	}
	suffixJSON, err := json.Marshal(suffix)
	if err != nil {
		return nil, fmt.Errorf("透传模式下序列化 input 后缀失败: %w", err)
	}
	trimmed, err := sjson.SetRawBytes(bodyBytes, "input", suffixJSON)
	if err != nil {
		return nil, fmt.Errorf("透传模式下裁剪重复 input 失败: %w", err)
	}
	return trimmed, nil
}

func convertResponsesRequestToOpenAIChat(model string, bodyBytes []byte, stream bool, sess *types.Session, req *types.ResponsesRequest, upstream *config.UpstreamConfig) ([]byte, error) {
	if err := ValidateResponsesToOpenAIChatRequest(bodyBytes); err != nil {
		return nil, err
	}
	if req != nil && respitems.ResponsesItemsContainCompactionFromInput(req.Input) {
		return nil, &types.UnsupportedProtocolError{Err: fmt.Errorf("Responses compaction item 只能由 Responses 原生上游处理，无法转换为 OpenAI Chat")}
	}

	// 原始 reasoning_text 会关联到 assistant.reasoning_content；摘要不冒充原文。
	includeHistoryThinking := upstream != nil && upstream.IncludeHistoryThinking
	conversionBody := bodyBytes
	var mergedItems []types.ResponsesItem
	if sess != nil && req != nil && len(sess.Messages) > 0 {
		currentItems, err := parseResponsesInput(req.Input)
		if err != nil {
			return nil, fmt.Errorf("解析 Responses input 失败: %w", err)
		}
		mergedItems = respitems.MergeResponsesItemsDedup(sess.Messages, currentItems)
		if respitems.ResponsesItemsContainCompaction(mergedItems) {
			return nil, &types.UnsupportedProtocolError{Err: fmt.Errorf("Responses compaction item 只能由 Responses 原生上游处理，无法转换为 OpenAI Chat")}
		}
		conversionBody, err = sjson.SetBytes(bodyBytes, "input", mergedItems)
		if err != nil {
			return nil, fmt.Errorf("构建 OpenAI Chat 合并 input 失败: %w", err)
		}
	}
	base := ConvertResponsesToOpenAIChatRequestWithOptions(model, conversionBody, stream, includeHistoryThinking)

	var chatReq map[string]interface{}
	if err := json.Unmarshal(base, &chatReq); err != nil {
		return nil, fmt.Errorf("解析 OpenAI Chat 转换结果失败: %w", err)
	}

	if _, ok := chatReq["messages"].([]interface{}); !ok {
		return nil, fmt.Errorf("OpenAI Chat 转换结果缺少 messages")
	}
	if upstream != nil && upstream.RequireReasoningContent {
		for index, raw := range chatReq["messages"].([]interface{}) {
			message, ok := raw.(map[string]interface{})
			if !ok {
				return nil, fmt.Errorf("Chat 历史消息 %d 不是对象", index+1)
			}
			if message["role"] == "assistant" {
				reasoning, _ := message["reasoning_content"].(string)
				if reasoning == "" {
					return nil, &types.ReasoningHistoryError{Message: fmt.Sprintf("assistant 历史消息 %d 缺少原始 reasoning_content，Responses 摘要或加密推理不能替代原文；请回传原始推理或开启新会话", index+1)}
				}
			}
		}
	}

	// session input 已在 Responses item 层合并；这里仅补齐从历史 tool_search_output
	// 恢复出来的工具定义，避免工具转换因协议字段缺失而静默退化。
	if len(mergedItems) > 0 && req != nil {
		if toolDefinitions, err := collectResponsesToolDefinitions(req.Tools, mergedItems); err != nil {
			return nil, fmt.Errorf("构建 OpenAI Chat tools 失败: %w", err)
		} else if chatTools, err := responsesToolDefinitionsToOpenAIChatTools(toolDefinitions); err != nil {
			return nil, fmt.Errorf("转换 OpenAI Chat tools 失败: %w", err)
		} else if len(chatTools) > 0 {
			chatReq["tools"] = chatTools
			if toolChoice, err := responsesToolChoiceToOpenAIChat(req.ToolChoice); err != nil {
				return nil, fmt.Errorf("转换 OpenAI Chat tool_choice 失败: %w", err)
			} else if toolChoice != nil {
				chatReq["tool_choice"] = toolChoice
			}
			if req.ParallelToolCalls != nil {
				chatReq["parallel_tool_calls"] = *req.ParallelToolCalls
			}
		}
	}

	if _, hasTools := chatReq["tools"]; !hasTools {
		delete(chatReq, "tool_choice")
		delete(chatReq, "parallel_tool_calls")
	}

	// prompt_cache_key: stable model+instructions+tools fingerprint; respect DisablePromptCacheKey.
	if upstream == nil || !upstream.DisablePromptCacheKey {
		if existing, _ := chatReq["prompt_cache_key"].(string); strings.TrimSpace(existing) == "" {
			chatReq["prompt_cache_key"] = buildResponsesChatPromptCacheKey(model, chatReq, upstream)
		}
	} else {
		delete(chatReq, "prompt_cache_key")
	}

	out, err := codec.MarshalJSONNoEscape(chatReq)
	if err != nil {
		return nil, fmt.Errorf("序列化 OpenAI Chat 请求失败: %w", err)
	}
	return out, nil
}

func buildResponsesChatPromptCacheKey(model string, chatReq map[string]interface{}, upstream *config.UpstreamConfig) string {
	channel := ""
	baseURL := ""
	if upstream != nil {
		channel = strings.TrimSpace(upstream.Name)
		baseURL = strings.TrimRight(upstream.GetEffectiveBaseURL(), "/#")
	}
	instructions := ""
	if messages, ok := chatReq["messages"].([]interface{}); ok {
		for _, raw := range messages {
			msg, ok := raw.(map[string]interface{})
			if !ok {
				continue
			}
			if msg["role"] == "system" {
				if text, ok := msg["content"].(string); ok {
					instructions = text
					break
				}
			}
		}
	}
	stableParts := map[string]interface{}{
		"protocol":     "responses-to-openai-chat-v1",
		"model":        model,
		"channel":      channel,
		"baseURL":      baseURL,
		"instructions": instructions,
		"tools":        chatReq["tools"],
	}
	sum := sha256.Sum256([]byte(codec.CanonicalJSON(stableParts)))
	return "resp-chat-" + hex.EncodeToString(sum[:])[:24]
}

func convertResponsesRequestToClaude(sess *types.Session, req *types.ResponsesRequest, upstream *config.UpstreamConfig) ([]byte, error) {
	converter := &ClaudeConverter{Upstream: upstream}
	convertedReq, err := converter.ToProviderRequest(sess, req)
	if err != nil {
		return nil, fmt.Errorf("转换请求失败: %w", err)
	}
	convertedBytes, err := codec.MarshalJSONNoEscape(convertedReq)
	if err != nil {
		return nil, fmt.Errorf("序列化Claude请求失败: %w", err)
	}
	if upstream == nil || !upstream.DisablePromptCacheInjection {
		injectedBytes, _, _ := cacheinject.InjectPromptCacheBreakpoints(convertedBytes)
		return injectedBytes, nil
	}
	return convertedBytes, nil
}

func ResponsesRequestStream(bodyBytes []byte) bool {
	return gjson.ParseBytes(bodyBytes).Get("stream").Bool()
}
