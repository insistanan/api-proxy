package chat

import (
	"encoding/json"

	"github.com/insistanan/api-proxy/internal/types"
	"github.com/insistanan/api-proxy/internal/utils"
)

func extractChatUsage(bodyBytes []byte) *types.Usage {
	var envelope struct {
		Usage *struct {
			PromptTokens       int `json:"prompt_tokens"`
			CompletionTokens   int `json:"completion_tokens"`
			InputTokens        int `json:"input_tokens"`
			OutputTokens       int `json:"output_tokens"`
			TotalTokens        int `json:"total_tokens"`
			PromptTokenDetails struct {
				CachedTokens int `json:"cached_tokens"`
			} `json:"prompt_tokens_details"`
			InputTokenDetails struct {
				CachedTokens int `json:"cached_tokens"`
			} `json:"input_tokens_details"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(bodyBytes, &envelope); err != nil || envelope.Usage == nil {
		return nil
	}

	cacheReadTokens := envelope.Usage.PromptTokenDetails.CachedTokens
	if cacheReadTokens <= 0 {
		cacheReadTokens = envelope.Usage.InputTokenDetails.CachedTokens
	}
	inputTokens := envelope.Usage.PromptTokens
	if inputTokens <= 0 {
		inputTokens = envelope.Usage.InputTokens
	}
	if cacheReadTokens > 0 && inputTokens > cacheReadTokens {
		inputTokens -= cacheReadTokens
	}
	outputTokens := envelope.Usage.CompletionTokens
	if outputTokens <= 0 {
		outputTokens = envelope.Usage.OutputTokens
	}

	return &types.Usage{
		InputTokens:          inputTokens,
		OutputTokens:         outputTokens,
		CacheReadInputTokens: cacheReadTokens,
		PromptTokens:         inputTokens,
		CompletionTokens:     outputTokens,
	}
}

func extractChatUsageFromSSELine(line string) *types.Usage {
	data, ok := utils.SSEDataJSON(line)
	if !ok {
		return nil
	}
	return extractChatUsage([]byte(data))
}

func mergeChatUsage(current, next *types.Usage) *types.Usage {
	if next == nil {
		return current
	}
	if current == nil {
		return next
	}
	if next.InputTokens > 0 {
		current.InputTokens = next.InputTokens
	}
	if next.OutputTokens > 0 {
		current.OutputTokens = next.OutputTokens
	}
	if next.CacheReadInputTokens > 0 {
		current.CacheReadInputTokens = next.CacheReadInputTokens
	}
	if next.PromptTokens > 0 {
		current.PromptTokens = next.PromptTokens
	}
	if next.CompletionTokens > 0 {
		current.CompletionTokens = next.CompletionTokens
	}
	return current
}
