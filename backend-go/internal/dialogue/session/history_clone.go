package session

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"

	"insistanan/api-proxy/internal/protocol/types"
)

func cloneHistoryItem(item types.ResponsesItem) (types.ResponsesItem, error) {
	copy := item
	for _, pair := range []struct {
		from any
		to   *any
	}{
		{item.Content, &copy.Content}, {item.Summary, &copy.Summary}, {item.Tools, &copy.Tools},
		{item.EncryptedContent, &copy.EncryptedContent}, {item.Action, &copy.Action},
	} {
		value, err := cloneHistoryValue(pair.from)
		if err != nil {
			return types.ResponsesItem{}, err
		}
		*pair.to = value
	}
	if item.ToolUse != nil {
		tool := *item.ToolUse
		input, err := cloneHistoryValue(tool.Input)
		if err != nil {
			return types.ResponsesItem{}, err
		}
		tool.Input = input
		copy.ToolUse = &tool
	}
	if item.Extra != nil {
		copy.Extra = make(map[string]json.RawMessage, len(item.Extra))
		for key, value := range item.Extra {
			if len(value) > 0 && !json.Valid(value) {
				return types.ResponsesItem{}, fmt.Errorf("扩展字段 %s 不是有效 JSON", key)
			}
			copy.Extra[key] = bytes.Clone(value)
		}
	}
	return copy, nil
}

// 已解码的 JSON 历史直接复制可变容器；字符串不可变，可以安全共享。
// 内部调用方提供的类型化结构仍按其 JSON 契约转换，错误继续向上返回。
func cloneHistoryValue(value any) (any, error) {
	switch value := value.(type) {
	case nil, string, bool:
		return value, nil
	case float64:
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return nil, fmt.Errorf("历史包含非有限数值")
		}
		return value, nil
	case []any:
		if value == nil {
			return []any(nil), nil
		}
		copy := make([]any, len(value))
		for i, item := range value {
			next, err := cloneHistoryValue(item)
			if err != nil {
				return nil, err
			}
			copy[i] = next
		}
		return copy, nil
	case map[string]any:
		if value == nil {
			return map[string]any(nil), nil
		}
		copy := make(map[string]any, len(value))
		for key, item := range value {
			next, err := cloneHistoryValue(item)
			if err != nil {
				return nil, err
			}
			copy[key] = next
		}
		return copy, nil
	default:
		data, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		var copy any
		err = json.Unmarshal(data, &copy)
		return copy, err
	}
}
