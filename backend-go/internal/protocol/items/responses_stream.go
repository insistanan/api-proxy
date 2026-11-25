package respitems

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/insistanan/api-proxy/internal/protocol/contract"
	"github.com/insistanan/api-proxy/internal/protocol/types"
)

// ResponsesOutputCollector 只归并协议事件，不负责读取、交付或持久化。
// 每个 output item 和 content/summary part 各自持有状态，ID 与 index 作为同一条目的别名。
type ResponsesOutputCollector struct {
	entries  []*responseOutputEntry
	aliases  map[string]*responseOutputEntry
	output   []types.ResponsesItem
	terminal bool
	err      error
}
type responseOutputEntry struct {
	raw      map[string]interface{}
	parts    map[string]map[int]map[string]interface{}
	done     bool
	index    int
	hasIndex bool
}

func NewResponsesOutputCollector() *ResponsesOutputCollector {
	return &ResponsesOutputCollector{aliases: make(map[string]*responseOutputEntry)}
}
func (c *ResponsesOutputCollector) Err() error      { return c.err }
func (c *ResponsesOutputCollector) Completed() bool { return c.terminal }
func (c *ResponsesOutputCollector) Consume(data map[string]interface{}) error {
	if c.err != nil {
		return c.err
	}
	c.err = c.consume(data)
	return c.err
}
func (c *ResponsesOutputCollector) consume(data map[string]interface{}) error {
	kind, _ := data["type"].(string)
	if kind == "response.completed" || kind == "response.incomplete" {
		response, ok := data["response"].(map[string]interface{})
		if !ok {
			return fmt.Errorf("Responses 终态缺少 response 对象")
		}
		if raw, exists := response["output"]; exists {
			output, ok := raw.([]interface{})
			if !ok {
				return fmt.Errorf("Responses 终态 output 必须是数组")
			}
			{
				items := make([]types.ResponsesItem, 0, len(output))
				for _, raw := range output {
					item, err := types.ParseResponsesItem(raw)
					if err != nil {
						return err
					}
					if err = validateCollectedItem(item); err != nil {
						return err
					}
					items = append(items, item)
				}
				c.output = items
			}
		}
		c.terminal = true
		return nil
	}
	itemMap, _ := data["item"].(map[string]interface{})
	itemKind := ""
	switch kind {
	case "response.output_item.added", "response.output_item.done":
		if itemMap == nil {
			return fmt.Errorf("%s 缺少 item", kind)
		}
		itemKind, _ = itemMap["type"].(string)
	case "response.output_text.delta", "response.output_text.done", "response.content_part.added", "response.content_part.done", "response.refusal.delta", "response.refusal.done":
		itemKind = "message"
	case "response.reasoning_summary_text.delta", "response.reasoning_summary_text.done", "response.reasoning_summary_part.added", "response.reasoning_summary_part.done":
		itemKind = "reasoning"
	case "response.function_call_arguments.delta", "response.function_call_arguments.done":
		itemKind = "function_call"
	case "response.custom_tool_call_input.delta", "response.custom_tool_call_input.done":
		itemKind = "custom_tool_call"
	default:
		return nil
	}
	entry, err := c.entry(data, itemMap, itemKind)
	if err != nil {
		return err
	}
	if itemMap != nil {
		// added/done 都是同一条目的快照；只覆盖实际出现的字段，保留扩展业务字段。
		for key, value := range itemMap {
			entry.raw[key] = value
			if key == "content" || key == "summary" {
				if kind == "response.output_item.done" {
					delete(entry.parts, key)
					continue
				}
				if parts, ok := value.([]interface{}); ok {
					entry.parts[key] = make(map[int]map[string]interface{})
					for index, raw := range parts {
						part, ok := raw.(map[string]interface{})
						if !ok {
							return fmt.Errorf("Responses %s 分片必须是对象", key)
						}
						entry.parts[key][index] = part
					}
					delete(entry.raw, key)
				}
			}
		}
		if kind == "response.output_item.done" {
			entry.done = entry.raw["status"] == nil || entry.raw["status"] == "completed"
			if entry.done {
				item, err := entry.item()
				if err != nil {
					return err
				}
				return validateCollectedItem(item)
			}
		}
		return nil
	}
	if strings.Contains(kind, "function_call_arguments") || strings.Contains(kind, "custom_tool_call_input") {
		field := "arguments"
		if itemKind == "custom_tool_call" {
			field = "input"
		}
		valueField := "delta"
		if strings.HasSuffix(kind, ".done") {
			valueField = field
		}
		value, ok := data[valueField].(string)
		if !ok {
			return fmt.Errorf("%s 的 %s 必须是字符串", kind, valueField)
		}
		if strings.HasSuffix(kind, ".delta") {
			previous, _ := entry.raw[field].(string)
			value = previous + value
		}
		entry.raw[field] = value
		return nil
	}
	section, indexField := "content", "content_index"
	if itemKind == "reasoning" {
		section, indexField = "summary", "summary_index"
	}
	index, err := responsePartIndex(data[indexField], indexField)
	if err != nil {
		return err
	}
	if entry.parts[section] == nil {
		entry.parts[section] = make(map[int]map[string]interface{})
	}
	part := entry.parts[section][index]
	if part == nil {
		part = map[string]interface{}{"type": "output_text", "text": ""}
		if section == "summary" {
			part["type"] = "summary_text"
		}
		entry.parts[section][index] = part
	}
	if rawPart, ok := data["part"].(map[string]interface{}); ok {
		for key, value := range rawPart {
			part[key] = value
		}
		return nil
	}
	field := "text"
	if strings.Contains(kind, "refusal") {
		field = "refusal"
		part["type"] = "refusal"
		delete(part, "text")
	}
	valueField := field
	if strings.HasSuffix(kind, ".delta") {
		valueField = "delta"
	}
	value, ok := data[valueField].(string)
	if !ok {
		return fmt.Errorf("%s 的 %s 必须是字符串", kind, valueField)
	}
	if strings.HasSuffix(kind, ".delta") {
		previous, _ := part[field].(string)
		value = previous + value
	}
	part[field] = value
	return nil
}

func (c *ResponsesOutputCollector) entry(data, item map[string]interface{}, kind string) (*responseOutputEntry, error) {
	keys := []string{}
	for _, pair := range []struct {
		prefix string
		value  interface{}
	}{
		{"index", data["output_index"]}, {"id", data["item_id"]}, {"id", item["id"]}, {"call", data["call_id"]}, {"call", item["call_id"]},
	} {
		if pair.value != nil && pair.value != "" {
			keys = append(keys, fmt.Sprintf("%s:%v", pair.prefix, pair.value))
		}
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

func validateCollectedItem(item types.ResponsesItem) error {
	switch item.Type {
	case "function_call":
		return contract.RequestToolCall(item.CallID, item.Name, item.Arguments)
	case "custom_tool_call":
		if item.CallID == "" || item.Name == "" {
			return fmt.Errorf("完整 custom_tool_call 缺少调用身份")
		}
		if _, ok := item.Content.(string); !ok {
			return fmt.Errorf("完整 custom_tool_call 的 input 必须是字符串")
		}
	}
	return nil
}

// Items 未终止时仅返回连续完成的前缀，不能因某个工具完成而带上其他半截条目。
func (c *ResponsesOutputCollector) Items() ([]types.ResponsesItem, error) {
	if c.err != nil {
		return nil, c.err
	}
	if c.output != nil {
		return append([]types.ResponsesItem(nil), c.output...), nil
	}
	entries := append([]*responseOutputEntry(nil), c.entries...)
	sort.SliceStable(entries, func(i, j int) bool {
		return entries[i].hasIndex && entries[j].hasIndex && entries[i].index < entries[j].index
	})
	result := make([]types.ResponsesItem, 0, len(entries))
	for index, entry := range entries {
		if !c.terminal && entry.hasIndex && entry.index != index {
			break
		}
		if !c.terminal && !entry.done {
			break
		}
		item, err := entry.item()
		if err != nil {
			return nil, err
		}
		if err = validateCollectedItem(item); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, nil
}

// Snapshot 只供用量估计，未完成条目不得经此入口进入持久化。
func (c *ResponsesOutputCollector) Snapshot() ([]types.ResponsesItem, error) {
	if c.err != nil {
		return nil, c.err
	}
	result := make([]types.ResponsesItem, 0, len(c.entries))
	for _, entry := range c.entries {
		item, err := entry.item()
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, nil
}

// HasCompletedTool 只报告经过完整参数校验的 done 条目，调用者负责先确认交付成功。
func (c *ResponsesOutputCollector) HasCompletedTool() bool {
	if c.err != nil {
		return false
	}
	for _, entry := range c.entries {
		if entry.done && (entry.raw["type"] == "function_call" || entry.raw["type"] == "custom_tool_call") {
			return true
		}
	}
	return false
}

func responsePartIndex(value interface{}, field string) (int, error) {
	if value == nil {
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
