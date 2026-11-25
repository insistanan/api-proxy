package tokenusage

import (
	"encoding/json"

	"insistanan/api-proxy/internal/protocol/types"
)

// ExtractJSONUsage 从原样透传响应体中提取 usage 字段，并统一 Prompt/Completion 与 Input/Output 命名。
// 解析失败或缺少 usage 时返回 nil，由调用方决定兜底策略。
func ExtractJSONUsage(bodyBytes []byte) *types.Usage {
	var envelope struct {
		Usage json.RawMessage `json:"usage"`
	}
	if err := json.Unmarshal(bodyBytes, &envelope); err != nil {
		return nil
	}
	usage, err := ParseJSONUsage(envelope.Usage)
	if err != nil {
		return nil
	}
	return usage
}

// ParseJSONUsage 把 OpenAI 输入总量中的缓存明细拆出，保留单独回报的 Claude 缓存量。
func ParseJSONUsage(raw []byte) (*types.Usage, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	type tokenDetails struct {
		CachedTokens int `json:"cached_tokens"`
	}
	var wire *struct {
		types.Usage
		PromptDetails tokenDetails `json:"prompt_tokens_details"`
		InputDetails  tokenDetails `json:"input_tokens_details"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil || wire == nil {
		return nil, err
	}
	usage := &wire.Usage
	if usage.InputTokens == 0 && usage.PromptTokens > 0 {
		usage.InputTokens = usage.PromptTokens
	}
	if usage.OutputTokens == 0 && usage.CompletionTokens > 0 {
		usage.OutputTokens = usage.CompletionTokens
	}
	cached := wire.InputDetails.CachedTokens
	if cached <= 0 {
		cached = wire.PromptDetails.CachedTokens
	}
	if cached > 0 && usage.CacheReadInputTokens == 0 {
		usage.CacheReadInputTokens = cached
		usage.UnattributedCacheInput = UnattributedCacheInput(usage.InputTokens, cached)
		if usage.UnattributedCacheInput == nil {
			usage.InputTokens -= cached
			usage.PromptTokens = usage.InputTokens
		}
	}
	return usage, nil
}
