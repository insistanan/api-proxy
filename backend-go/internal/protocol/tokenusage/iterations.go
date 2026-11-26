package tokenusage

import (
	"strings"

	"github.com/tidwall/gjson"
	"insistanan/api-proxy/internal/protocol/types"
)

// BillingUsage 只生成指标副本；iterations 已覆盖全部计费轮次，不再加顶层用量。
// 不写回客户端，否则会把累计计费量误当作压缩后的上下文大小。
func BillingUsage(usage *types.Usage) *types.Usage {
	if usage == nil {
		return nil
	}
	result := *usage
	if len(usage.Iterations) == 0 {
		return &result
	}
	result = types.Usage{}
	for _, iteration := range usage.Iterations {
		result.InputTokens += iteration.InputTokens
		result.OutputTokens += iteration.OutputTokens
		result.CacheCreationInputTokens += iteration.CacheCreationInputTokens
		result.CacheReadInputTokens += iteration.CacheReadInputTokens
		result.CacheCreation5mInputTokens += iteration.CacheCreation5mInputTokens
		result.CacheCreation1hInputTokens += iteration.CacheCreation1hInputTokens
	}
	return &result
}

// PreserveCompactionUsage 原生压缩合法返回零值或压缩后的窗口大小，不能按旧历史估算修补。
func PreserveCompactionUsage(body []byte) bool {
	if gjson.GetBytes(body, "compaction.type").String() == "summarize" {
		return true
	}
	for _, edit := range gjson.GetBytes(body, "context_management.edits").Array() {
		if typ := edit.Get("type").String(); typ == "compaction" || strings.HasPrefix(typ, "compact_") {
			return true
		}
	}
	return false
}
