// Responses → Messages 非流式响应转换。
package providers

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/insistanan/api-proxy/internal/protocol/contract"
	"github.com/insistanan/api-proxy/internal/protocol/converters"
	"github.com/insistanan/api-proxy/internal/protocol/types"
	"github.com/insistanan/api-proxy/internal/protocol/visionmedia"
)

func responsesResponseToClaude(resp *types.ResponsesResponse) (*types.ClaudeResponse, error) {
	if resp == nil || (resp.Status != "completed" && resp.Status != "incomplete") {
		return nil, fmt.Errorf("Responses 上游没有返回 completed 或 incomplete 响应")
	}
	body, err := json.Marshal(resp)
	if err != nil {
		return nil, err
	}
	if err := contract.ResponseContent("responses", body); err != nil {
		return nil, err
	}
	refused := false
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
			for _, block := range visionmedia.NormalizeContentBlocks(item.Content) {
				if block["type"] == "refusal" {
					text, _ := block["refusal"].(string)
					textParts = append(textParts, text)
					refused = true
					continue
				}
				if text, ok := visionmedia.ExtractTextFromBlock(block); ok && text != "" {
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
			} else {
				var err error
				input, err = contract.Arguments(item.Arguments, item.CallID)
				if err != nil {
					return nil, err
				}
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
	}
	claudeResp.StopReason = "end_turn"
	for _, content := range claudeResp.Content {
		if content.Type == "tool_use" {
			claudeResp.StopReason = "tool_use"
			break
		}
	}
	if resp.Status == "incomplete" {
		reason, _ := resp.IncompleteDetails["reason"].(string)
		completion, err := contract.ResponsesCompletion(resp.Status, reason)
		if err != nil {
			return nil, err
		}
		claudeResp.StopReason = completion.ClaudeStopReason(false)
	}
	if refused {
		claudeResp.StopReason = "refusal"
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
	return claudeResp, nil
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
