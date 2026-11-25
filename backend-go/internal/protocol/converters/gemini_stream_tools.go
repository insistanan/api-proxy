package converters

import (
	"fmt"
	"sort"
	"strings"

	"github.com/insistanan/api-proxy/internal/protocol/contract"
	"github.com/insistanan/api-proxy/internal/protocol/types"
	"github.com/tidwall/gjson"
)

type geminiStreamCall struct {
	id        string
	name      string
	initial   string
	arguments strings.Builder
}

// GeminiStreamTools 按上游 block/tool index 累积工具，完整校验后才交付 functionCall。
// HTTP 读取、取消、用量和写出仍由 API 入口负责。
type GeminiStreamTools struct{ calls map[int]*geminiStreamCall }

func (s *GeminiStreamTools) Feed(protocol string, payload []byte) ([]types.GeminiPart, error) {
	if s.calls == nil {
		s.calls = make(map[int]*geminiStreamCall)
	}
	root := gjson.ParseBytes(payload)
	if protocol == "claude" {
		index := int(root.Get("index").Int())
		switch root.Get("type").String() {
		case "content_block_start":
			block := root.Get("content_block")
			if block.Get("type").String() == "tool_use" {
				s.calls[index] = &geminiStreamCall{id: block.Get("id").String(), name: block.Get("name").String(), initial: block.Get("input").Raw}
			}
		case "content_block_delta":
			if call := s.calls[index]; call != nil {
				call.arguments.WriteString(root.Get("delta.partial_json").String())
			}
		case "content_block_stop":
			if _, exists := s.calls[index]; exists {
				return s.finish([]int{index})
			}
		case "message_stop":
			if len(s.calls) > 0 {
				return nil, fmt.Errorf("Claude 流结束时工具块尚未关闭")
			}
		}
		return nil, nil
	}
	choices := root.Get("choices").Array()
	if len(choices) > 1 {
		return nil, fmt.Errorf("Chat → Gemini 流只支持一个候选")
	}
	for _, choice := range choices {
		if choice.Get("index").Int() != 0 {
			return nil, fmt.Errorf("Chat → Gemini 出现额外候选")
		}
		for _, delta := range choice.Get("delta.tool_calls").Array() {
			index := int(delta.Get("index").Int())
			call := s.calls[index]
			if call == nil {
				call = &geminiStreamCall{}
				s.calls[index] = call
			}
			if id := delta.Get("id").String(); id != "" {
				call.id = id
			}
			if name := delta.Get("function.name").String(); name != "" {
				call.name += name
			}
			call.arguments.WriteString(delta.Get("function.arguments").String())
		}
		if choice.Get("finish_reason").String() != "" {
			indices := make([]int, 0, len(s.calls))
			for index := range s.calls {
				indices = append(indices, index)
			}
			sort.Ints(indices)
			return s.finish(indices)
		}
	}
	return nil, nil
}

func (s *GeminiStreamTools) finish(indices []int) ([]types.GeminiPart, error) {
	parts := make([]types.GeminiPart, 0, len(indices))
	for _, index := range indices {
		call := s.calls[index]
		if call.id == "" || call.name == "" {
			return nil, fmt.Errorf("上游工具调用缺少 ID 或 name")
		}
		arguments := call.arguments.String()
		if arguments == "" {
			arguments = call.initial
		}
		parsed, err := contract.Arguments(arguments, call.id)
		if err != nil {
			return nil, err
		}
		parts = append(parts, types.GeminiPart{FunctionCall: &types.GeminiFunctionCall{ID: call.id, Name: call.name, Args: parsed}})
		delete(s.calls, index)
	}
	return parts, nil
}

func (s *GeminiStreamTools) CheckComplete() error {
	if len(s.calls) != 0 {
		return fmt.Errorf("上游流结束时仍有未完成工具参数")
	}
	return nil
}
