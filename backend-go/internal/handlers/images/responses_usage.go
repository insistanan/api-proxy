package images

import (
	"encoding/json"
	"fmt"

	"github.com/BenedictKing/api-proxy/internal/types"
)

type responsesImageUsageDetails struct {
	CachedTokens int `json:"cached_tokens"`
}

// responsesImageUsageWire 只描述 Responses 图片完成事件实际出现的用量字段。
// 不能直接反序列化到 types.Usage：OpenAI 的 input_tokens_details.cached_tokens
// 在日志里对应缓存读取，而不是 Claude 的 cache_read_input_tokens 字段名。
type responsesImageUsageWire struct {
	InputTokens                int                         `json:"input_tokens"`
	OutputTokens               int                         `json:"output_tokens"`
	PromptTokens               int                         `json:"prompt_tokens"`
	CompletionTokens           int                         `json:"completion_tokens"`
	CacheCreationInputTokens   int                         `json:"cache_creation_input_tokens"`
	CacheReadInputTokens       int                         `json:"cache_read_input_tokens"`
	CacheCreation5mInputTokens int                         `json:"cache_creation_5m_input_tokens"`
	CacheCreation1hInputTokens int                         `json:"cache_creation_1h_input_tokens"`
	CacheTTL                   string                      `json:"cache_ttl"`
	InputTokensDetails         *responsesImageUsageDetails `json:"input_tokens_details"`
	PromptTokensDetails        *responsesImageUsageDetails `json:"prompt_tokens_details"`
}

func responsesImageUsage(raw json.RawMessage) (*types.Usage, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var wire responsesImageUsageWire
	if err := json.Unmarshal(raw, &wire); err != nil {
		return nil, fmt.Errorf("Responses 图片用量不是有效对象: %w", err)
	}
	usage := &types.Usage{
		InputTokens:                wire.InputTokens,
		OutputTokens:               wire.OutputTokens,
		PromptTokens:               wire.PromptTokens,
		CompletionTokens:           wire.CompletionTokens,
		CacheCreationInputTokens:   wire.CacheCreationInputTokens,
		CacheReadInputTokens:       wire.CacheReadInputTokens,
		CacheCreation5mInputTokens: wire.CacheCreation5mInputTokens,
		CacheCreation1hInputTokens: wire.CacheCreation1hInputTokens,
		CacheTTL:                   wire.CacheTTL,
	}
	if usage.InputTokens == 0 {
		usage.InputTokens = usage.PromptTokens
	}
	if usage.OutputTokens == 0 {
		usage.OutputTokens = usage.CompletionTokens
	}
	cachedTokens := 0
	if wire.InputTokensDetails != nil {
		cachedTokens = wire.InputTokensDetails.CachedTokens
	} else if wire.PromptTokensDetails != nil {
		cachedTokens = wire.PromptTokensDetails.CachedTokens
	}
	if cachedTokens > 0 && usage.CacheReadInputTokens == 0 {
		usage.CacheReadInputTokens = cachedTokens
	}
	return usage, nil
}
