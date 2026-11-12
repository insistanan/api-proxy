// Responses → Messages 非流式响应转换。
package providers

import (
	"encoding/json"
	"strings"

	"github.com/insistanan/api-proxy/internal/infra/utils"
	"github.com/insistanan/api-proxy/internal/protocol/converters"
	"github.com/insistanan/api-proxy/internal/protocol/types"
)

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
