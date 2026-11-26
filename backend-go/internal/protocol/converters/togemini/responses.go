package togemini

import (
	"fmt"
	"strings"

	"github.com/tidwall/gjson"
	"insistanan/api-proxy/internal/protocol/contract"
	"insistanan/api-proxy/internal/protocol/types"
)

type responsesStreamCall struct {
	id        string
	name      string
	arguments strings.Builder
}

type responsesStreamState struct {
	calls          map[string]*responsesStreamCall
	emittedCalls   map[string]bool
	emittedText    bool
	emittedThought bool
	refused        bool
}

func newResponsesStreamState() *responsesStreamState {
	return &responsesStreamState{
		calls:        make(map[string]*responsesStreamCall),
		emittedCalls: make(map[string]bool),
	}
}

// ResponsesResponse 将 OpenAI Responses 非流式响应转换为 Gemini 格式。
func ResponsesResponse(body []byte) (*types.GeminiResponse, error) {
	if err := contract.ResponseContent("responses", body); err != nil {
		return nil, err
	}
	root := gjson.ParseBytes(body)
	status := root.Get("status").String()
	incompleteReason := root.Get("incomplete_details.reason").String()
	completion, err := contract.ResponsesCompletion(status, incompleteReason)
	if err != nil {
		return nil, err
	}

	parts := []types.GeminiPart{}
	refused := false
	for _, item := range root.Get("output").Array() {
		itemParts, itemRefused, err := responsesOutputItemParts(item)
		if err != nil {
			return nil, err
		}
		if itemRefused {
			refused = true
		}
		parts = append(parts, itemParts...)
	}

	finishReason := geminiFinishFromCompletion(completion)
	if refused {
		finishReason = "SAFETY"
	}

	geminiResp := &types.GeminiResponse{
		Candidates: []types.GeminiCandidate{{
			Content:      &types.GeminiContent{Parts: parts, Role: "model"},
			FinishReason: finishReason,
			Index:        0,
		}},
	}
	if usage := root.Get("usage"); usage.Exists() && usage.Type != gjson.Null {
		geminiResp.UsageMetadata, err = responseUsage(usage.Value())
		if err != nil {
			return nil, err
		}
	}
	return geminiResp, nil
}

func responsesOutputItemParts(item gjson.Result) ([]types.GeminiPart, bool, error) {
	var parts []types.GeminiPart
	refused := false
	switch item.Get("type").String() {
	case "reasoning":
		if text := extractResponsesReasoning(item); text != "" {
			parts = append(parts, types.GeminiPart{Text: text, Thought: true})
		}
	case "message":
		for _, block := range item.Get("content").Array() {
			switch block.Get("type").String() {
			case "refusal":
				refused = true
				if text := block.Get("refusal").String(); text != "" {
					parts = append(parts, types.GeminiPart{Text: text})
				}
			case "text", "output_text", "input_text":
				if text := block.Get("text").String(); text != "" {
					parts = append(parts, types.GeminiPart{Text: text})
				}
			}
		}
	case "function_call", "custom_tool_call":
		call, err := responsesItemToFunctionCall(item)
		if err != nil {
			return nil, false, err
		}
		parts = append(parts, types.GeminiPart{FunctionCall: call})
	}
	return parts, refused, nil
}

func responsesItemToFunctionCall(item gjson.Result) (*types.GeminiFunctionCall, error) {
	name := item.Get("name").String()
	callID := item.Get("call_id").String()
	if callID == "" {
		rawID := item.Get("id").String()
		callID = strings.TrimPrefix(strings.TrimPrefix(rawID, "fc_"), "ctc_")
	}
	var args map[string]interface{}
	var err error
	if item.Get("type").String() == "custom_tool_call" {
		args = map[string]interface{}{"input": item.Get("input").String()}
	} else {
		args, err = contract.Arguments(item.Get("arguments").String(), callID)
		if err != nil {
			return nil, err
		}
	}
	return &types.GeminiFunctionCall{ID: callID, Name: name, Args: args}, nil
}

func extractResponsesReasoning(item gjson.Result) string {
	var texts []string
	for _, field := range []string{"summary", "content"} {
		for _, entry := range item.Get(field).Array() {
			if text := entry.Get("text").String(); text != "" {
				texts = append(texts, text)
			}
		}
	}
	if len(texts) == 0 {
		if text := item.Get("text").String(); text != "" {
			texts = append(texts, text)
		}
	}
	return strings.Join(texts, "\n")
}

func geminiFinishFromCompletion(completion contract.Completion) string {
	switch completion.Reason {
	case "max_output_tokens":
		return "MAX_TOKENS"
	case "content_filter":
		return "SAFETY"
	default:
		return "STOP"
	}
}

func (s *Stream) feedResponses(root gjson.Result, payload []byte) ([]types.GeminiStreamChunk, error) {
	if s.responses == nil {
		s.responses = newResponsesStreamState()
	}
	if err := contract.ResponseContent("responses", payload); err != nil {
		return nil, err
	}
	eventType := root.Get("type").String()
	if eventType == "response.failed" || root.Get("response.status").String() == "failed" {
		return nil, fmt.Errorf("上游流返回错误: %s", payload)
	}

	var chunks []types.GeminiStreamChunk
	var parts []types.GeminiPart

	if usage := root.Get("response.usage"); usage.Exists() && usage.Type != gjson.Null {
		metadata, err := s.updateUsage(usage)
		if err != nil {
			return nil, err
		}
		if metadata != nil {
			chunks = append(chunks, types.GeminiStreamChunk{UsageMetadata: metadata})
		}
	} else if usage := root.Get("usage"); usage.Exists() && usage.Type != gjson.Null {
		metadata, err := s.updateUsage(usage)
		if err != nil {
			return nil, err
		}
		if metadata != nil {
			chunks = append(chunks, types.GeminiStreamChunk{UsageMetadata: metadata})
		}
	}

	switch eventType {
	case "response.output_text.delta":
		if text := root.Get("delta").String(); text != "" {
			s.responses.emittedText = true
			parts = append(parts, types.GeminiPart{Text: text})
		}
	case "response.refusal.delta":
		s.responses.refused = true
		if text := root.Get("delta").String(); text != "" {
			s.responses.emittedText = true
			parts = append(parts, types.GeminiPart{Text: text})
		}
	case "response.reasoning_summary_text.delta", "response.reasoning_text.delta":
		if text := root.Get("delta").String(); text != "" {
			s.responses.emittedThought = true
			parts = append(parts, types.GeminiPart{Text: text, Thought: true})
		}
	case "response.output_item.added":
		item := root.Get("item")
		if itemType := item.Get("type").String(); itemType == "function_call" || itemType == "custom_tool_call" {
			key := responsesCallKey(item, root)
			call := s.ensureResponsesCall(key)
			if id := item.Get("call_id").String(); id != "" {
				call.id = id
			} else if id := item.Get("id").String(); id != "" && call.id == "" {
				call.id = strings.TrimPrefix(strings.TrimPrefix(id, "fc_"), "ctc_")
			}
			if name := item.Get("name").String(); name != "" {
				call.name = name
			}
		}
	case "response.function_call_arguments.delta", "response.custom_tool_call_input.delta":
		key := responsesCallKey(root.Get("item"), root)
		call := s.ensureResponsesCall(key)
		call.arguments.WriteString(root.Get("delta").String())
	case "response.output_item.done":
		item := root.Get("item")
		itemType := item.Get("type").String()
		if itemType == "function_call" || itemType == "custom_tool_call" {
			key := responsesCallKey(item, root)
			if !s.responses.emittedCalls[key] {
				call, err := s.finishResponsesCall(key, item)
				if err != nil {
					return nil, err
				}
				parts = append(parts, types.GeminiPart{FunctionCall: call})
			}
		}
	case "response.completed", "response.incomplete":
		status := strings.TrimPrefix(eventType, "response.")
		completion, err := contract.ResponsesCompletion(status, root.Get("response.incomplete_details.reason").String())
		if err != nil {
			return nil, err
		}
		// 某些上游仅在 response.completed 的 output 中返回最终文本或工具调用，补充未发出的部分。
		for _, item := range root.Get("response.output").Array() {
			itemType := item.Get("type").String()
			switch itemType {
			case "function_call", "custom_tool_call":
				key := responsesCallKey(item, root)
				if !s.responses.emittedCalls[key] {
					call, err := s.finishResponsesCall(key, item)
					if err != nil {
						return nil, err
					}
					parts = append(parts, types.GeminiPart{FunctionCall: call})
				}
			case "message":
				if !s.responses.emittedText {
					itemParts, itemRefused, err := responsesOutputItemParts(item)
					if err != nil {
						return nil, err
					}
					if itemRefused {
						s.responses.refused = true
					}
					parts = append(parts, itemParts...)
				}
			case "reasoning":
				if !s.responses.emittedThought {
					itemParts, _, err := responsesOutputItemParts(item)
					if err != nil {
						return nil, err
					}
					parts = append(parts, itemParts...)
				}
			}
		}
		s.stopReason = geminiFinishFromCompletion(completion)
		if s.responses.refused {
			s.stopReason = "SAFETY"
		}
		s.terminated = true
	}

	if len(parts) > 0 {
		chunks = append(chunks, contentChunk(parts))
	}
	return chunks, nil
}

func (s *Stream) ensureResponsesCall(key string) *responsesStreamCall {
	call := s.responses.calls[key]
	if call == nil {
		call = &responsesStreamCall{}
		s.responses.calls[key] = call
	}
	return call
}

func (s *Stream) finishResponsesCall(key string, item gjson.Result) (*types.GeminiFunctionCall, error) {
	stored := s.responses.calls[key]
	delete(s.responses.calls, key)
	s.responses.emittedCalls[key] = true

	callID := item.Get("call_id").String()
	if callID == "" && stored != nil {
		callID = stored.id
	}
	if callID == "" {
		rawID := item.Get("id").String()
		callID = strings.TrimPrefix(strings.TrimPrefix(rawID, "fc_"), "ctc_")
	}
	name := item.Get("name").String()
	if name == "" && stored != nil {
		name = stored.name
	}
	if name == "" {
		return nil, fmt.Errorf("上游 Responses 工具调用缺少名称")
	}
	var args map[string]interface{}
	var err error
	if item.Get("type").String() == "custom_tool_call" {
		rawInput := item.Get("input").String()
		if rawInput == "" && stored != nil {
			rawInput = stored.arguments.String()
		}
		args = map[string]interface{}{"input": rawInput}
	} else {
		rawArgs := item.Get("arguments").String()
		if rawArgs == "" && stored != nil {
			rawArgs = stored.arguments.String()
		}
		args, err = contract.Arguments(rawArgs, callID)
		if err != nil {
			return nil, err
		}
	}
	return &types.GeminiFunctionCall{ID: callID, Name: name, Args: args}, nil
}

func responsesCallKey(item, root gjson.Result) string {
	for _, value := range []string{
		item.Get("call_id").String(),
		item.Get("id").String(),
		root.Get("item_id").String(),
		root.Get("call_id").String(),
	} {
		if value != "" {
			return value
		}
	}
	return fmt.Sprintf("idx_%d", root.Get("output_index").Int())
}
