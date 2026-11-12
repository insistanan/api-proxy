package utils

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// CanonicalJSON 将任意结构做结构化 JSON 归一化（键排序、稳定序列化），用于生成缓存键 / 指纹。
// 替代历史两份文件级复制（providers/responses_messages.go 的 canonicalJSON、
// converters/responses_protocol.go 的 canonicalJSONForCacheKey）。二者对真实输入行为一致，
// 本实现即其中的"通用型"版本（略去永不命中的富类型分支）。
func CanonicalJSON(v interface{}) string {
	switch value := v.(type) {
	case nil:
		return "null"
	case string:
		data, _ := json.Marshal(value)
		return string(data)
	case bool:
		if value {
			return "true"
		}
		return "false"
	case float64, float32, int, int64, int32, uint, uint64, uint32, json.Number:
		data, _ := json.Marshal(value)
		return string(data)
	case []interface{}:
		parts := make([]string, 0, len(value))
		for _, item := range value {
			parts = append(parts, CanonicalJSON(item))
		}
		return "[" + strings.Join(parts, ",") + "]"
	case map[string]interface{}:
		keys := make([]string, 0, len(value))
		for key := range value {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		parts := make([]string, 0, len(keys))
		for _, key := range keys {
			keyJSON, _ := json.Marshal(key)
			parts = append(parts, string(keyJSON)+":"+CanonicalJSON(value[key]))
		}
		return "{" + strings.Join(parts, ",") + "}"
	default:
		data, err := json.Marshal(value)
		if err != nil {
			return fmt.Sprint(value)
		}
		var normalized interface{}
		if err := json.Unmarshal(data, &normalized); err != nil {
			return string(data)
		}
		return CanonicalJSON(normalized)
	}
}
