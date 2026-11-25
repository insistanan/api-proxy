package respitems

import (
	"fmt"
	"strings"

	"insistanan/api-proxy/internal/protocol/types"
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
			items := make([]types.ResponsesItem, 0, len(output))
			for _, raw := range output {
				item, err := types.ParseResponsesItem(raw)
				if err != nil {
					return err
				}
				items = append(items, item)
			}
			var err error
			c.output, err = ResponsesHistoryOutput(items, kind == "response.incomplete")
			if err != nil {
				return err
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
