package messages

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"

	"github.com/BenedictKing/api-proxy/internal/types"
	"github.com/BenedictKing/api-proxy/internal/utils"
)

// claudeUsageRewrite 记录非流式 Claude 响应里，客户端 usage 相对上游原文改过的部分。
// 没有任何改写时必须原样下发，不能用结构体重新序列化。
type claudeUsageRewrite struct {
	injectUsage bool
	patchInput  bool
	patchOutput bool
	clearCache  bool
}

func (r claudeUsageRewrite) changed() bool {
	return r.injectUsage || r.patchInput || r.patchOutput || r.clearCache
}

// claudePassthroughBody 为 Claude 上游的非流式响应保留未知字段。
// usage 未改写时返回原始字节，safeguard_results 和 tool_use.id 都保持上游原样。
// 发生估算、虚假值补全或 sanity 校正时，只改 usage 中对应字段，不整份替换 usage。
func claudePassthroughBody(original []byte, usage *types.Usage, rewrite claudeUsageRewrite) ([]byte, error) {
	if !rewrite.changed() {
		return original, nil
	}
	if usage == nil {
		return nil, fmt.Errorf("Claude 响应 usage 校正缺少客户端 usage")
	}

	decoder := json.NewDecoder(bytes.NewReader(original))
	decoder.UseNumber()
	var root map[string]interface{}
	if err := decoder.Decode(&root); err != nil {
		return nil, fmt.Errorf("解析 Claude 响应以保留未知字段失败: %w", err)
	}
	if root == nil {
		return nil, fmt.Errorf("Claude 响应不是 JSON 对象，无法在保留未知字段的同时校正 usage")
	}
	var trailing interface{}
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("Claude 响应包含多个顶层 JSON 值，拒绝重编码以免丢失字段")
		}
		return nil, fmt.Errorf("解析 Claude 响应尾部失败: %w", err)
	}

	usageObj, hasUsageObj := root["usage"].(map[string]interface{})
	if rewrite.injectUsage {
		if !hasUsageObj || usageObj == nil {
			usageObj = map[string]interface{}{}
		}
		usageObj["input_tokens"] = usage.InputTokens
		usageObj["output_tokens"] = usage.OutputTokens
		root["usage"] = usageObj
	} else if !hasUsageObj || usageObj == nil {
		return nil, fmt.Errorf("Claude 响应的 usage 不是对象，拒绝整份替换以免丢失未知 usage 字段")
	} else {
		if rewrite.patchInput {
			usageObj["input_tokens"] = usage.InputTokens
		}
		if rewrite.patchOutput {
			usageObj["output_tokens"] = usage.OutputTokens
		}
	}
	if rewrite.clearCache {
		delete(usageObj, "cache_read_input_tokens")
		delete(usageObj, "cache_creation_input_tokens")
		delete(usageObj, "cache_creation_5m_input_tokens")
		delete(usageObj, "cache_creation_1h_input_tokens")
		delete(usageObj, "cache_ttl")
	}

	encoded, err := utils.MarshalJSONNoEscape(root)
	if err != nil {
		return nil, fmt.Errorf("序列化 Claude 透传响应失败: %w", err)
	}
	return encoded, nil
}
