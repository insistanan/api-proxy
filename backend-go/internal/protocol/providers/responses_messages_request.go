// Messages → Responses 请求转换：消息、工具与缓存参数。
package providers

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/insistanan/api-proxy/internal/infra/config"
	"github.com/insistanan/api-proxy/internal/infra/utils"
	"github.com/insistanan/api-proxy/internal/orchestration/upstreamcontrol"
	"github.com/insistanan/api-proxy/internal/protocol/converters"
	"github.com/insistanan/api-proxy/internal/protocol/types"
)

func claudeRequestToResponsesRequest(claudeReq *types.ClaudeRequest, upstream *config.UpstreamConfig, conversationID string, identity ...upstreamcontrol.Identity) (*claudeResponsesRequest, error) {
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
		applyPreviousResponseIDChain(req, claudeReq, upstream, conversationID, model, identity...)
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
