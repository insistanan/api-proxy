package providers

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/insistanan/api-proxy/internal/protocol/types"
	"github.com/tidwall/gjson"
)

func (s *responsesToClaudeStreamState) rememberItemIndex(root gjson.Result) {
	id := root.Get("item.id").String()
	if id == "" {
		id = root.Get("item_id").String()
	}
	if id != "" && root.Get("output_index").Exists() {
		s.itemIndexes[id] = int(root.Get("output_index").Int())
	}
}

func (s *responsesToClaudeStreamState) eventPartKey(root gjson.Result, kind string) string {
	index := int(root.Get("output_index").Int())
	if id := root.Get("item_id").String(); id != "" {
		if known, ok := s.itemIndexes[id]; ok {
			index = known
		}
	}
	part := root.Get("content_index").Int()
	if kind == "reasoning" {
		part = root.Get("summary_index").Int()
	}
	return fmt.Sprintf("%s:%d:%d", kind, index, part)
}

// 每个输出条目、内容块分别累计，终态仅补回尚未交付的后缀。
func (s *responsesToClaudeStreamState) deliverPart(key, value string, reasoning, snapshot bool) []string {
	if snapshot {
		previous := s.deliveredParts[key]
		if !strings.HasPrefix(value, previous) {
			s.failed = true
			s.failedMessage = "Responses 完成内容与已交付增量不一致: " + key
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
		if known, ok := s.itemIndexes[item.Get("id").String()]; ok {
			index = known
		}
		switch item.Get("type").String() {
		case "message":
			content := item.Get("content")
			if content.Type == gjson.String {
				out = append(out, s.deliverPart(fmt.Sprintf("text:%d:0", index), content.String(), false, true)...)
			} else {
				for part, block := range content.Array() {
					switch block.Get("type").String() {
					case "text", "input_text", "output_text":
						out = append(out, s.deliverPart(fmt.Sprintf("text:%d:%d", index, part), block.Get("text").String(), false, true)...)
					}
				}
			}
		case "reasoning":
			for part, block := range item.Get("summary").Array() {
				out = append(out, s.deliverPart(fmt.Sprintf("reasoning:%d:%d", index, part), block.Get("text").String(), true, true)...)
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
