package mcpsync

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"reflect"
	"slices"
)

// decodeJSONObject 解析 JSON 对象。空字节视为空对象；使用 UseNumber 保留数字原样，避免写回时精度变化。
func decodeJSONObject(raw []byte) (map[string]any, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return map[string]any{}, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var root map[string]any
	if err := decoder.Decode(&root); err != nil {
		return nil, fmt.Errorf("JSON 格式无效: %w", err)
	}
	if root == nil {
		return nil, fmt.Errorf("顶层必须是 JSON 对象")
	}
	return root, nil
}

// marshalJSON 以两空格缩进输出，并保留 <、>、& 原样（URL 中常见，避免被转义成 < 等形式）。
func marshalJSON(value any) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		return nil, fmt.Errorf("序列化 JSON 失败: %w", err)
	}
	return buffer.Bytes(), nil
}

// objectField 读取对象字段。字段不存在返回 nil；字段存在但不是对象时返回错误，避免覆盖用户数据。
func objectField(root map[string]any, key string) (map[string]any, error) {
	raw, exists := root[key]
	if !exists {
		return nil, nil
	}
	object, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s 不是 JSON 对象", key)
	}
	return object, nil
}

// anyStringMap 把字符串 map 转为 JSON 解码后的同构结构，便于与磁盘内容做 reflect.DeepEqual 比较。
func anyStringMap(values map[string]string) map[string]any {
	result := make(map[string]any, len(values))
	for key, value := range values {
		result[key] = value
	}
	return result
}

// anyStringList 与 anyStringMap 同理，输出 []any 而不是 []string。
func anyStringList(values []string) []any {
	result := make([]any, 0, len(values))
	for _, value := range values {
		result = append(result, value)
	}
	return result
}

// applyManagedEntries 把托管名称对应的条目写入 entries（JSON 对象的字段表）。
// 启用的条目按 build 生成后比较写入；未启用但仍存在的托管条目被删除。返回是否有实际变化。
func applyManagedEntries(entries map[string]any, desired map[string]Server, managed map[string]struct{}, agent AgentKey, build func(Server) map[string]any) bool {
	changed := false
	for name := range managed {
		server, enabled := desired[name]
		_, exists := entries[name]
		if !enabled {
			if exists {
				delete(entries, name)
				changed = true
			}
			continue
		}
		current, _ := entries[name].(map[string]any)
		if existing, enabled, err := decodeEntry(name, current, agent); err == nil && enabled && sameConfig(existing, server) {
			continue
		}
		entry := mergeEntry(current, server, agent, build)
		if !exists || !reflect.DeepEqual(entries[name], entry) {
			entries[name] = entry
			changed = true
		}
	}
	return changed
}

// sortedKeys 返回 map 的键的稳定顺序，保证每次生成的内容一致，避免无意义的写入。
func sortedKeys[V any](values map[string]V) []string {
	return slices.Sorted(maps.Keys(values))
}
