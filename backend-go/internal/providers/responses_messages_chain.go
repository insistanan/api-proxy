// Messages → Responses 会话链：身份解析、历史边界、持久化和输出指纹。
package providers

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/insistanan/api-proxy/internal/config"
	"github.com/insistanan/api-proxy/internal/session"
	"github.com/insistanan/api-proxy/internal/types"
	"github.com/insistanan/api-proxy/internal/upstreamcontrol"
	"github.com/insistanan/api-proxy/internal/utils"
	"github.com/tidwall/gjson"
)

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
	identity ...upstreamcontrol.Identity,
) {
	if req == nil || claudeReq == nil || conversationID == "" {
		return
	}
	state, ok := session.DefaultResponseChainManager().Get(conversationID)
	if !ok || state.ResponseID == "" {
		return
	}
	if len(identity) > 0 && state.UpstreamIdentity != identity[0] {
		// Messages 输入携带当前完整历史；换 URL/协议/Key 后清链并发送全量。
		session.DefaultResponseChainManager().Clear(conversationID)
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

func rememberResponsesChainFromBody(conversationID string, claudeReq *types.ClaudeRequest, upstream *config.UpstreamConfig, model string, body []byte, identity ...upstreamcontrol.Identity) {
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
		responsesAssistantOutputFingerprint(response.Output), identity...)
}

// rememberResponsesChain 将上游 response id 与当前 messages 指纹写入会话链。
func rememberResponsesChain(conversationID string, claudeReq *types.ClaudeRequest, upstream *config.UpstreamConfig, model string, responseID, outputFingerprint string, identity ...upstreamcontrol.Identity) {
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
	owner := upstreamcontrol.Identity{}
	if len(identity) > 0 {
		owner = identity[0]
	}
	session.DefaultResponseChainManager().Set(conversationID, session.ResponseChainState{
		UpstreamIdentity:   owner,
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
