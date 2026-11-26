package respitems

import (
	"encoding/json"
	"fmt"
	"sort"

	"insistanan/api-proxy/internal/protocol/types"
)

func (c *ResponsesOutputCollector) entry(data, item map[string]interface{}, kind string) (*responseOutputEntry, error) {
	keys := []string{}
	if value, exists := data["output_index"]; exists {
		index, err := responsePartIndex(value, "output_index")
		if err != nil {
			return nil, err
		}
		keys = append(keys, fmt.Sprintf("index:%d", index))
	}
	identities := make(map[string]string, 2)
	for _, pair := range []struct {
		field  string
		prefix string
		value  interface{}
	}{
		{"item_id", "id", data["item_id"]}, {"item.id", "id", item["id"]},
		{"call_id", "call_id", data["call_id"]}, {"item.call_id", "call_id", item["call_id"]},
	} {
		if pair.value == nil {
			continue
		}
		value, ok := pair.value.(string)
		if !ok {
			return nil, fmt.Errorf("Responses %s 必须是字符串", pair.field)
		}
		if value == "" {
			continue
		}
		if previous := identities[pair.prefix]; previous != "" && previous != value {
			return nil, fmt.Errorf("Responses 同一事件的 %s 不一致", pair.field)
		}
		identities[pair.prefix] = value
		keys = append(keys, pair.prefix+":"+value)
	}
	var entry *responseOutputEntry
	for _, key := range keys {
		if found := c.aliases[key]; found != nil {
			if entry != nil && entry != found {
				return nil, fmt.Errorf("Responses item 身份指向不同条目")
			}
			entry = found
		}
	}
	if entry == nil && len(keys) == 0 {
		for _, candidate := range c.entries {
			if candidate.raw["type"] == kind {
				if entry != nil {
					return nil, fmt.Errorf("Responses 增量缺少唯一 item 身份")
				}
				entry = candidate
			}
		}
	}
	if entry == nil {
		entry = &responseOutputEntry{raw: map[string]interface{}{"type": kind}, parts: make(map[string]map[int]map[string]interface{})}
		if kind == "message" {
			entry.raw["role"] = "assistant"
		}
		c.entries = append(c.entries, entry)
	}
	if previous, _ := entry.raw["type"].(string); previous != kind {
		return nil, fmt.Errorf("Responses item 类型由 %q 变为 %q", previous, kind)
	}
	for field, value := range identities {
		if previous, _ := entry.raw[field].(string); previous != "" && previous != value {
			return nil, fmt.Errorf("Responses item 的 %s 发生变化", field)
		}
		entry.raw[field] = value
	}
	if value, exists := data["output_index"]; exists {
		index, err := responsePartIndex(value, "output_index")
		if err != nil {
			return nil, err
		}
		if entry.hasIndex && entry.index != index {
			return nil, fmt.Errorf("Responses item 的 output_index 发生变化")
		}
		entry.index, entry.hasIndex = index, true
	}
	for _, key := range keys {
		c.aliases[key] = entry
	}
	for _, field := range []string{"call_id", "name"} {
		if value, ok := data[field].(string); ok && value != "" {
			entry.raw[field] = value
		}
	}
	if id, ok := data["item_id"].(string); ok && id != "" {
		entry.raw["id"] = id
	}
	return entry, nil
}

func (e *responseOutputEntry) item() (types.ResponsesItem, error) {
	raw := make(map[string]interface{}, len(e.raw)+2)
	for key, value := range e.raw {
		raw[key] = value
	}
	for section, parts := range e.parts {
		if snapshot, exists := raw[section]; exists {
			if list, ok := snapshot.([]interface{}); !ok || len(list) > 0 {
				continue
			}
		}
		indices := make([]int, 0, len(parts))
		for index := range parts {
			indices = append(indices, index)
		}
		sort.Ints(indices)
		values := make([]interface{}, 0, len(parts))
		for _, index := range indices {
			values = append(values, parts[index])
		}
		raw[section] = values
	}
	return types.ParseResponsesItem(raw)
}

func responsePartIndex(value interface{}, field string) (int, error) {
	if value == nil && field != "output_index" {
		return 0, nil
	}
	var integer int64
	switch number := value.(type) {
	case json.Number:
		parsed, err := number.Int64()
		if err != nil {
			return 0, fmt.Errorf("Responses %s 必须是非负整数: %w", field, err)
		}
		integer = parsed
	case float64:
		integer = int64(number)
		if number != float64(integer) {
			return 0, fmt.Errorf("Responses %s 必须是非负整数", field)
		}
	default:
		return 0, fmt.Errorf("Responses %s 必须是非负整数", field)
	}
	if integer < 0 || int64(int(integer)) != integer {
		return 0, fmt.Errorf("Responses %s 超出有效范围", field)
	}
	return int(integer), nil
}
