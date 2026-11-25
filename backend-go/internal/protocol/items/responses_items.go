package respitems

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"

	"github.com/insistanan/api-proxy/internal/protocol/types"
)

// MergeResponsesItemsDedup 仅裁剪可证明的历史重放。无法匹配的内容作为增量保留；
// compaction 是协议明确规定的历史根，不使用角色、条数或正文形态猜测边界。
func MergeResponsesItemsDedup(history, current []types.ResponsesItem) []types.ResponsesItem {
	if ResponsesItemsContainCompaction(current) {
		return append([]types.ResponsesItem(nil), current...)
	}
	start, ok := ResponsesItemsPrefixMatch(history, current)
	if !ok {
		start = 0
	}
	merged := make([]types.ResponsesItem, 0, len(history)+len(current)-start)
	merged = append(merged, history...)
	return append(merged, current[start:]...)
}

// ResponsesItemsPrefixMatch 返回 current 中已存在于历史内的完整前缀长度。
// 客户端可以省略服务端 reasoning；显式重放的 reasoning 则必须逐项相等。
func ResponsesItemsPrefixMatch(history, current []types.ResponsesItem) (int, bool) {
	if len(history) == 0 || len(current) == 0 {
		return 0, false
	}
	for start := range history {
		end, stable, ok := matchResponsesPrefix(history[start:], current)
		if ok && (start == 0 || stable) {
			return end, true
		}
	}
	return 0, false
}

func matchResponsesPrefix(history, current []types.ResponsesItem) (end int, stable, ok bool) {
	matched := 0
	for _, previous := range history {
		// 只允许省略历史推理，不能略过客户端显式提供的不同推理。
		if isResponsesReasoningItem(previous) && (end >= len(current) || !isResponsesReasoningItem(current[end])) {
			continue
		}
		if end >= len(current) || !responsesItemEqual(previous, current[end]) {
			return 0, false, false
		}
		next := current[end]
		stable = stable || previous.ID != "" && previous.ID == next.ID || previous.CallID != "" && previous.CallID == next.CallID
		if !isResponsesReasoningItem(previous) {
			matched++
		}
		end++
	}
	return end, stable, matched > 0
}

// ResponsesItemsPrefixMatchRaw 共用同一比较规则，返回原始数组的下标供调用方裁剪。
func ResponsesItemsPrefixMatchRaw(history []types.ResponsesItem, raw []json.RawMessage) (int, bool) {
	current := make([]types.ResponsesItem, len(raw))
	for index := range raw {
		if err := json.Unmarshal(raw[index], &current[index]); err != nil {
			return 0, false
		}
	}
	return ResponsesItemsPrefixMatch(history, current)
}

func ResponsesItemsPrefixOverlap(history, current []types.ResponsesItem) int {
	end, _ := ResponsesItemsPrefixMatch(history, current)
	return end
}

func isResponsesReasoningItem(item types.ResponsesItem) bool { return item.Type == "reasoning" }

// ResponsesItemsContainCompaction 判断当前 input/output 是否包含 Responses
// compaction 控制项。该 item 的 encrypted_content 通常是不透明的压缩边界，
// 不能按普通消息追加到旧历史之后。
func ResponsesItemsContainCompaction(items []types.ResponsesItem) bool {
	for _, item := range items {
		if strings.EqualFold(strings.TrimSpace(item.Type), "compaction") {
			return true
		}
	}
	return false
}

// ResponsesItemsFromCompactionRoot 用于 /responses 服务端自动压缩的 output，
// 丢弃压缩根之前的旧 transcript。不能用于客户端重放的 /responses/compact
// 完整窗口：该窗口的 compaction 前置消息也属于新的有效上下文。
func ResponsesItemsFromCompactionRoot(items []types.ResponsesItem) []types.ResponsesItem {
	for index := len(items) - 1; index >= 0; index-- {
		if strings.EqualFold(strings.TrimSpace(items[index].Type), "compaction") {
			return append([]types.ResponsesItem(nil), items[index:]...)
		}
	}
	return append([]types.ResponsesItem(nil), items...)
}

// ResponsesTurnItems 区分客户端完整压缩窗口与本轮服务端自动压缩。
// 客户端带 compaction 时整体保留 input；output 再次压缩时以最新根替换旧窗口。
func ResponsesTurnItems(input, output []types.ResponsesItem) []types.ResponsesItem {
	if ResponsesItemsContainCompaction(output) {
		return ResponsesItemsFromCompactionRoot(output)
	}
	items := make([]types.ResponsesItem, 0, len(input)+len(output))
	items = append(items, input...)
	return append(items, output...)
}

// ResponsesItemsContainCompactionFromInput 处理 ResponsesRequest.Input 在
// json.Unmarshal 后的两种形态：string 或 []interface{}。它只用于判断是否
// 需要切换会话根，不会修改 input，也不会把不透明字段重新编码。
func ResponsesItemsContainCompactionFromInput(input interface{}) bool {
	switch value := input.(type) {
	case []types.ResponsesItem:
		return ResponsesItemsContainCompaction(value)
	case []interface{}:
		for _, rawItem := range value {
			item, ok := rawItem.(map[string]interface{})
			if !ok {
				continue
			}
			typ, _ := item["type"].(string)
			if strings.EqualFold(strings.TrimSpace(typ), "compaction") {
				return true
			}
		}
	}
	return false
}

// ResponsesRawItemsContainCompaction 是原生 Responses 透传路径使用的版本。
// 原始 item 必须保留原样，这里只读取 type，不重新编码任何字段。
func ResponsesRawItemsContainCompaction(items []json.RawMessage) bool {
	for _, rawItem := range items {
		var item map[string]interface{}
		if err := json.Unmarshal(rawItem, &item); err != nil {
			continue
		}
		typ, _ := item["type"].(string)
		if strings.EqualFold(strings.TrimSpace(typ), "compaction") {
			return true
		}
	}
	return false
}

func responsesItemsEqual(left, right []types.ResponsesItem) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if !responsesItemEqual(left[index], right[index]) {
			return false
		}
	}
	return true
}

func responsesItemRawEqual(left types.ResponsesItem, raw json.RawMessage) bool {
	var right types.ResponsesItem
	return json.Unmarshal(raw, &right) == nil && responsesItemEqual(left, right)
}

func responsesItemEqual(left, right types.ResponsesItem) bool {
	a, err := canonicalResponsesItem(left)
	if err != nil {
		return false
	}
	b, err := canonicalResponsesItem(right)
	return err == nil && reflect.DeepEqual(a, b)
}

// canonicalResponsesItem 只规范协议定义的等价位置。工具参数、未知扩展字段和
// 嵌套业务对象保持完整，不能递归删除恰好叫 annotations/logprobs 的业务键。
func canonicalResponsesItem(item types.ResponsesItem) (map[string]interface{}, error) {
	if item.Type == "tool_call" && item.ToolUse != nil {
		arguments, err := json.Marshal(item.ToolUse.Input)
		if err != nil {
			return nil, err
		}
		item.Type, item.CallID, item.Name, item.Arguments = "function_call", item.ToolUse.ID, item.ToolUse.Name, string(arguments)
		item.ToolUse = nil
	}
	if item.Type == "" || item.Type == "text" {
		item.Type = "message"
	}
	if item.Type == "message" && item.Role == "" {
		item.Role = "user"
	}
	encoded, err := json.Marshal(item)
	if err != nil {
		return nil, err
	}
	var result map[string]interface{}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	if err := decoder.Decode(&result); err != nil {
		return nil, err
	}
	delete(result, "id")
	delete(result, "status")
	if item.Type == "message" {
		result["content"] = canonicalMessageContent(result["content"])
	}
	if item.Type == "function_call" {
		if text, ok := result["arguments"].(string); ok {
			var arguments interface{}
			decoder := json.NewDecoder(strings.NewReader(text))
			decoder.UseNumber()
			if json.Valid([]byte(text)) && decoder.Decode(&arguments) == nil {
				result["arguments"] = arguments
			}
		}
	}
	return result, nil
}

func canonicalMessageContent(content interface{}) interface{} {
	blocks, ok := content.([]interface{})
	if !ok || len(blocks) == 0 {
		return content
	}
	var text strings.Builder
	for _, raw := range blocks {
		block, ok := raw.(map[string]interface{})
		if !ok || len(block) != 2 {
			return content
		}
		kind, _ := block["type"].(string)
		value, ok := block["text"].(string)
		if !ok || (kind != "input_text" && kind != "output_text" && kind != "text") {
			return content
		}
		text.WriteString(value)
	}
	return text.String()
}
