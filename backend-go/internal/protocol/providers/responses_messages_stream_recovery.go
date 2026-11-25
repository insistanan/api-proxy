package providers

import (
	"encoding/json"
	"strings"

	"github.com/tidwall/gjson"
	"insistanan/api-proxy/internal/protocol/types"
)

// 同一条目可以先出现 item_id 或 output_index；得到对应关系后归并为同一个键。
type responsesPartKey struct {
	kind   string
	itemID string
	index  int
	part   int
}

func (s *responsesToClaudeStreamState) rememberItemIndex(root gjson.Result) {
	id := root.Get("item.id").String()
	if id == "" {
		id = root.Get("item_id").String()
	}
	if id != "" && root.Get("output_index").Exists() {
		s.bindItemIndex(id, int(root.Get("output_index").Int()))
	}
}

func (s *responsesToClaudeStreamState) bindItemIndex(id string, index int) {
	if id == "" {
		return
	}
	knownIndex, known := s.itemIndexes[id]
	if (known && knownIndex != index) || (s.indexItems[index] != "" && s.indexItems[index] != id) {
		s.failed, s.failedMessage = true, "Responses 输出条目的 ID 与索引发生冲突"
		return
	}
	s.itemIndexes[id], s.indexItems[index] = index, id
	for key, value := range s.deliveredParts {
		if key.itemID == "" && key.index == index {
			delete(s.deliveredParts, key)
			key.itemID, key.index = id, 0
			s.deliveredParts[key] = value
		}
	}
}

func (s *responsesToClaudeStreamState) partKey(kind, id string, index, part int) responsesPartKey {
	if id == "" {
		id = s.indexItems[index]
	}
	if id != "" {
		index = 0
	}
	return responsesPartKey{kind: kind, itemID: id, index: index, part: part}
}

func (s *responsesToClaudeStreamState) eventPartKey(root gjson.Result, kind string) responsesPartKey {
	part := int(root.Get("content_index").Int())
	if kind == "reasoning" {
		part = int(root.Get("summary_index").Int())
	}
	return s.partKey(kind, root.Get("item_id").String(), int(root.Get("output_index").Int()), part)
}

// 每个输出条目、内容块分别累计，终态仅补回尚未交付的后缀。
func (s *responsesToClaudeStreamState) deliverPart(key responsesPartKey, value string, reasoning, snapshot bool) []string {
	if snapshot {
		previous := s.deliveredParts[key]
		if !strings.HasPrefix(value, previous) {
			s.failed = true
			s.failedMessage = "Responses 完成内容与已交付增量不一致: " + key.kind + " " + key.itemID
			return nil
		}
		value = value[len(previous):]
	}
	if value == "" {
		return nil
	}
	s.deliveredParts[key] += value
	if reasoning {
		s.reasoning.WriteString(value)
		return s.emitReasoningDelta(value)
	}
	return s.emitTextDelta(value)
}

func (s *responsesToClaudeStreamState) emitCompletedOutput(root gjson.Result) []string {
	var out []string
	var completed []types.ResponsesItem
	for index, item := range root.Get("response.output").Array() {
		if parsed, ok := responsesStreamOutputItem(item); ok {
			completed = append(completed, parsed)
		}
		id := item.Get("id").String()
		s.bindItemIndex(id, index)
		if s.failed {
			return nil
		}
		switch item.Get("type").String() {
		case "message":
			content := item.Get("content")
			if content.Type == gjson.String {
				out = append(out, s.deliverPart(s.partKey("text", id, index, 0), content.String(), false, true)...)
			} else {
				for part, block := range content.Array() {
					switch block.Get("type").String() {
					case "refusal":
						s.stopReason = "refusal"
						out = append(out, s.deliverPart(s.partKey("text", id, index, part), block.Get("refusal").String(), false, true)...)
					case "text", "input_text", "output_text":
						out = append(out, s.deliverPart(s.partKey("text", id, index, part), block.Get("text").String(), false, true)...)
					}
				}
			}
		case "reasoning":
			for part, block := range item.Get("summary").Array() {
				out = append(out, s.deliverPart(s.partKey("reasoning", id, index, part), block.Get("text").String(), true, true)...)
			}
		case "function_call", "custom_tool_call":
			if root.Get("response.status").String() == "incomplete" && item.Get("status").String() != "completed" {
				continue
			}
			payload, err := json.Marshal(map[string]interface{}{"item": item.Value(), "output_index": index})
			if err != nil {
				s.failed = true
				s.failedMessage = err.Error()
				return nil
			}
			itemRoot := gjson.ParseBytes(payload)
			s.captureToolCall(itemRoot)
			out = append(out, s.emitToolUse(itemRoot)...)
		}
		if s.failed {
			return nil
		}
	}
	if len(completed) > 0 {
		s.completedOutput = completed
	}
	return out
}
