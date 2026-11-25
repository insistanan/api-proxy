package converters

import (
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/insistanan/api-proxy/internal/protocol/contract"
	"github.com/insistanan/api-proxy/internal/protocol/types"
	"github.com/tidwall/gjson"
)

func (s *responsesStreamState) nextSeq() int {
	s.Seq++
	return s.Seq
}

func (s *responsesStreamState) start(id string, originalRequestJSON []byte) []string {
	if s.Started {
		return nil
	}
	s.Started = true
	s.ResponseID = id
	s.CreatedAt = time.Now().Unix()
	created := map[string]interface{}{"type": "response.created", "sequence_number": s.nextSeq(), "response": s.responseEnvelope("in_progress", originalRequestJSON)}
	inProgress := map[string]interface{}{"type": "response.in_progress", "sequence_number": s.nextSeq(), "response": s.responseEnvelope("in_progress", originalRequestJSON)}
	return []string{emitMapEvent("response.created", created), emitMapEvent("response.in_progress", inProgress)}
}

func (s *responsesStreamState) ensureStarted(originalRequestJSON []byte) []string {
	if s.Started {
		return nil
	}
	return s.start(fmt.Sprintf("resp_%d", time.Now().UnixNano()), originalRequestJSON)
}

func (s *responsesStreamState) responseEnvelope(status string, originalRequestJSON []byte) map[string]interface{} {
	resp := map[string]interface{}{"id": s.ResponseID, "object": "response", "created_at": s.CreatedAt, "status": status}
	if originalRequestJSON != nil {
		root := gjson.ParseBytes(originalRequestJSON)
		for _, key := range []string{"model", "instructions", "max_output_tokens", "parallel_tool_calls", "previous_response_id", "reasoning", "tool_choice", "tools", "top_p", "temperature", "metadata"} {
			if v := root.Get(key); v.Exists() {
				resp[key] = v.Value()
			}
		}
	}
	return resp
}

func (s *responsesStreamState) startClaudeBlock(index int, block gjson.Result) []string {
	blockType := block.Get("type").String()
	switch blockType {
	case "text":
		out := s.startTextBlock(index)
		return append(out, s.emitIndexedTextDelta(index, block.Get("text").String())...)
	case "thinking":
		out := s.startReasoningBlock(index)
		s.captureReasoningSignature(index, block.Get("signature").String())
		return append(out, s.emitIndexedReasoningDelta(index, block.Get("thinking").String())...)
	case "tool_use":
		callID := block.Get("id").String()
		name := block.Get("name").String()
		out := s.startFunctionBlock(index, callID, name)
		s.Blocks[index].InitialArgs = block.Get("input").Raw
		return out
	default:
		return nil
	}
}

func (s *responsesStreamState) deltaClaudeBlock(index int, delta gjson.Result) []string {
	deltaType := delta.Get("type").String()
	switch deltaType {
	case "text_delta":
		return s.emitIndexedTextDelta(index, delta.Get("text").String())
	case "thinking_delta":
		return s.emitIndexedReasoningDelta(index, delta.Get("thinking").String())
	case "signature_delta":
		return s.captureReasoningSignature(index, delta.Get("signature").String())
	case "input_json_delta":
		return s.emitIndexedFunctionArgsDelta(index, delta.Get("partial_json").String())
	default:
		return nil
	}
}

func (s *responsesStreamState) startTextBlock(index int) []string {
	itemID := fmt.Sprintf("msg_%s_%d", s.ResponseID, index)
	s.Blocks[index] = &responsesStreamBlock{ItemID: itemID, Type: "text"}
	item := map[string]interface{}{"id": itemID, "type": "message", "status": "in_progress", "role": "assistant", "content": []interface{}{}}
	return []string{emitMapEvent("response.output_item.added", map[string]interface{}{"type": "response.output_item.added", "sequence_number": s.nextSeq(), "output_index": index, "item": item})}
}

func (s *responsesStreamState) startReasoningBlock(index int) []string {
	itemID := fmt.Sprintf("rs_%s_%d", s.ResponseID, index)
	s.Blocks[index] = &responsesStreamBlock{ItemID: itemID, Type: "reasoning"}
	item := map[string]interface{}{"id": itemID, "type": "reasoning", "status": "in_progress", "summary": []interface{}{}}
	return []string{emitMapEvent("response.output_item.added", map[string]interface{}{"type": "response.output_item.added", "sequence_number": s.nextSeq(), "output_index": index, "item": item})}
}

func (s *responsesStreamState) startFunctionBlock(index int, callID string, name string) []string {
	if callID == "" {
		callID = fmt.Sprintf("call_%d", index)
	}
	itemID := fmt.Sprintf("fc_%s", callID)
	s.Blocks[index] = &responsesStreamBlock{ItemID: itemID, Type: "function_call", Name: name, CallID: callID}
	item := buildChatToolCallOutputItem(callID, name, "{}", s.Tools)
	item["id"], item["status"] = itemID, "in_progress"
	if item["type"] == "custom_tool_call" {
		item["input"] = ""
	} else {
		item["arguments"] = ""
	}
	return []string{emitMapEvent("response.output_item.added", map[string]interface{}{"type": "response.output_item.added", "sequence_number": s.nextSeq(), "output_index": index, "item": item})}
}

// Gemini 没有 block index；按实际到达顺序分配连续的输出条目。
func (s *responsesStreamState) geminiBlock(kind string) (int, []string) {
	if s.ActiveGeminiKind == kind {
		return s.ActiveGeminiIndex, nil
	}
	var out []string
	if s.ActiveGeminiKind != "" {
		out = s.stopBlock(s.ActiveGeminiIndex)
	}
	index := s.NextOutputIndex
	s.NextOutputIndex++
	s.ActiveGeminiKind, s.ActiveGeminiIndex = kind, index
	return index, out
}
func (s *responsesStreamState) emitTextDelta(text string) []string {
	index, out := s.geminiBlock("text")
	return append(out, s.emitIndexedTextDelta(index, text)...)
}
func (s *responsesStreamState) emitReasoningDelta(text string) []string {
	index, out := s.geminiBlock("reasoning")
	return append(out, s.emitIndexedReasoningDelta(index, text)...)
}

func (s *responsesStreamState) emitIndexedTextDelta(index int, text string) []string {
	if text == "" {
		return nil
	}
	if _, ok := s.Blocks[index]; !ok {
		out := s.startTextBlock(index)
		out = append(out, s.emitIndexedTextDelta(index, text)...)
		return out
	}
	block := s.Blocks[index]
	block.Text.WriteString(text)
	payload := map[string]interface{}{"type": "response.output_text.delta", "sequence_number": s.nextSeq(), "item_id": block.ItemID, "output_index": index, "content_index": 0, "delta": text}
	return []string{emitMapEvent("response.output_text.delta", payload)}
}

func (s *responsesStreamState) emitIndexedReasoningDelta(index int, text string) []string {
	if text == "" {
		return nil
	}
	if _, ok := s.Blocks[index]; !ok {
		out := s.startReasoningBlock(index)
		out = append(out, s.emitIndexedReasoningDelta(index, text)...)
		return out
	}
	block := s.Blocks[index]
	block.Text.WriteString(text)
	payload := map[string]interface{}{"type": "response.reasoning_summary_text.delta", "sequence_number": s.nextSeq(), "item_id": block.ItemID, "output_index": index, "summary_index": 0, "delta": text}
	return []string{emitMapEvent("response.reasoning_summary_text.delta", payload)}
}

func (s *responsesStreamState) captureReasoningSignature(index int, signature string) []string {
	if signature == "" {
		return nil
	}
	var out []string
	if _, ok := s.Blocks[index]; !ok {
		out = append(out, s.startReasoningBlock(index)...)
	}
	if block := s.Blocks[index]; block != nil {
		block.Signature = signature
	}
	return out
}

func (s *responsesStreamState) emitFunctionCall(callID, name, args string) []string {
	var out []string
	if s.ActiveGeminiKind != "" {
		out = s.stopBlock(s.ActiveGeminiIndex)
		s.ActiveGeminiKind = ""
	}
	index := s.NextOutputIndex
	s.NextOutputIndex++
	if callID == "" {
		callID = fmt.Sprintf("call_%d", index)
	}
	out = append(out, s.startFunctionBlock(index, callID, name)...)
	return append(out, s.emitIndexedFunctionArgsDelta(index, args)...)
}

func (s *responsesStreamState) emitIndexedFunctionArgsDelta(index int, delta string) []string {
	if delta == "" {
		return nil
	}
	block := s.Blocks[index]
	if block == nil {
		return nil
	}
	block.Args.WriteString(delta)
	if spec, ok := s.Tools[block.Name]; ok && spec.Kind != responseToolKindFunction {
		return nil
	}
	payload := map[string]interface{}{"type": "response.function_call_arguments.delta", "sequence_number": s.nextSeq(), "item_id": block.ItemID, "output_index": index, "delta": delta}
	return []string{emitMapEvent("response.function_call_arguments.delta", payload)}
}

func (s *responsesStreamState) stopBlock(index int) []string {
	block, ok := s.Blocks[index]
	if !ok {
		return nil
	}
	delete(s.Blocks, index)
	switch block.Type {
	case "text":
		done := map[string]interface{}{"type": "response.output_text.done", "sequence_number": s.nextSeq(), "item_id": block.ItemID, "output_index": index, "content_index": 0, "text": block.Text.String()}
		item := map[string]interface{}{"id": block.ItemID, "type": "message", "status": s.Completion.Status, "role": "assistant", "content": []interface{}{map[string]interface{}{"type": "output_text", "text": block.Text.String()}}}
		s.Output[index] = item
		return []string{emitMapEvent("response.output_text.done", done), emitMapEvent("response.output_item.done", map[string]interface{}{"type": "response.output_item.done", "sequence_number": s.nextSeq(), "output_index": index, "item": item})}
	case "reasoning":
		text := block.Text.String()
		done := map[string]interface{}{"type": "response.reasoning_summary_text.done", "sequence_number": s.nextSeq(), "item_id": block.ItemID, "output_index": index, "summary_index": 0, "text": text}
		item := map[string]interface{}{"id": block.ItemID, "type": "reasoning", "status": s.Completion.Status, "summary": []interface{}{map[string]interface{}{"type": "summary_text", "text": block.Text.String()}}}
		if block.Signature != "" {
			item["encrypted_content"] = block.Signature
		}
		s.Output[index] = item
		return []string{emitMapEvent("response.reasoning_summary_text.done", done), emitMapEvent("response.output_item.done", map[string]interface{}{"type": "response.output_item.done", "sequence_number": s.nextSeq(), "output_index": index, "item": item})}
	case "function_call":
		args, err := contract.StreamArguments(block.InitialArgs, block.Args.String(), block.CallID)
		if err != nil {
			s.Err = err
			return nil
		}
		item, err := restoreResponseToolItem(types.ResponsesItem{ID: block.ItemID, Type: "function_call", Status: s.Completion.Status, CallID: block.CallID, Name: block.Name, Arguments: args}, s.Tools)
		if err != nil {
			s.Err = err
			return nil
		}
		s.Output[index] = item
		event, field, value := "response.function_call_arguments.done", "arguments", interface{}(args)
		var out []string
		if item.Type == "custom_tool_call" {
			event, field, value = "response.custom_tool_call_input.done", "input", item.Content
			out = append(out, emitMapEvent("response.custom_tool_call_input.delta", map[string]interface{}{"type": "response.custom_tool_call_input.delta", "sequence_number": s.nextSeq(), "item_id": item.ID, "output_index": index, "delta": value}))
		}
		if item.Type != "tool_search_call" {
			out = append(out, emitMapEvent(event, map[string]interface{}{"type": event, "sequence_number": s.nextSeq(), "item_id": item.ID, "output_index": index, field: value}))
		}
		out = append(out, emitMapEvent("response.output_item.done", map[string]interface{}{"type": "response.output_item.done", "sequence_number": s.nextSeq(), "output_index": index, "item": item}))
		return out
	}
	return nil
}

func (s *responsesStreamState) closeAllBlocks() []string {
	var out []string
	indexes := make([]int, 0, len(s.Blocks))
	for idx := range s.Blocks {
		indexes = append(indexes, idx)
	}
	sort.Ints(indexes)
	for _, idx := range indexes {
		out = append(out, s.stopBlock(idx)...)
	}
	return out
}

func (s *responsesStreamState) completed(originalRequestJSON []byte) []string {
	if s.Completed || s.Err != nil {
		return nil
	}
	s.Completed = true
	resp := s.responseEnvelope(s.Completion.Status, originalRequestJSON)
	s.Completion.Apply(resp)
	indices := make([]int, 0, len(s.Output))
	for index := range s.Output {
		indices = append(indices, index)
	}
	sort.Ints(indices)
	output := make([]interface{}, 0, len(indices))
	for _, index := range indices {
		output = append(output, s.Output[index])
	}
	resp["output"], resp["usage"] = output, s.Usage
	event := "response." + s.Completion.Status
	payload := map[string]interface{}{"type": event, "sequence_number": s.nextSeq(), "response": resp}
	return []string{emitMapEvent(event, payload)}
}

func (s *responsesStreamState) captureUsage(usage gjson.Result) {
	if v := usage.Get("input_tokens"); v.Exists() {
		s.Usage.InputTokens = int(v.Int())
	}
	if v := usage.Get("output_tokens"); v.Exists() {
		s.Usage.OutputTokens = int(v.Int())
	}
	if v := usage.Get("cache_creation_input_tokens"); v.Exists() {
		s.Usage.CacheCreationInputTokens = int(v.Int())
	}
	if v := usage.Get("cache_creation_5m_input_tokens"); v.Exists() {
		s.Usage.CacheCreation5mInputTokens = int(v.Int())
	}
	if v := usage.Get("cache_creation_1h_input_tokens"); v.Exists() {
		s.Usage.CacheCreation1hInputTokens = int(v.Int())
	}
	if v := usage.Get("cache_read_input_tokens"); v.Exists() {
		s.Usage.CacheReadInputTokens = int(v.Int())
	}
	if v := usage.Get("input_tokens_details.cached_tokens"); v.Exists() && v.Int() > 0 {
		cachedTokens := int(v.Int())
		if s.Usage.CacheReadInputTokens == 0 {
			s.Usage.CacheReadInputTokens = cachedTokens
		}
		if s.Usage.InputTokens > cachedTokens && usage.Get("cache_read_input_tokens").Exists() == false {
			s.Usage.InputTokens -= cachedTokens
		}
	}
	if v := usage.Get("prompt_tokens_details.cached_tokens"); v.Exists() && v.Int() > 0 && s.Usage.CacheReadInputTokens == 0 {
		cachedTokens := int(v.Int())
		s.Usage.CacheReadInputTokens = cachedTokens
		if s.Usage.InputTokens > cachedTokens {
			s.Usage.InputTokens -= cachedTokens
		}
	}
	if s.Usage.CacheCreation5mInputTokens > 0 && s.Usage.CacheCreation1hInputTokens > 0 {
		s.Usage.CacheTTL = "mixed"
	} else if s.Usage.CacheCreation1hInputTokens > 0 {
		s.Usage.CacheTTL = "1h"
	} else if s.Usage.CacheCreation5mInputTokens > 0 {
		s.Usage.CacheTTL = "5m"
	}
	if s.Usage.CacheReadInputTokens > 0 {
		s.Usage.InputTokensDetails = &types.InputTokensDetails{CachedTokens: s.Usage.CacheReadInputTokens}
	}
	s.Usage.TotalTokens = s.Usage.InputTokens + s.Usage.OutputTokens
}

func emitMapEvent(event string, payload map[string]interface{}) string {
	data, _ := json.Marshal(payload)
	return emitResponsesEvent(event, string(data))
}
