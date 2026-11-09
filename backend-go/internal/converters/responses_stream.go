package converters

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/insistanan/api-proxy/internal/types"
	"github.com/insistanan/api-proxy/internal/utils"
	"github.com/tidwall/gjson"
)

type responsesStreamState struct {
	Seq               int
	Started           bool
	Completed         bool
	FinishPending     bool
	ResponseID        string
	CreatedAt         int64
	Model             string
	NextFunctionIndex int
	Blocks            map[int]*responsesStreamBlock
	Usage             types.ResponsesUsage
}

type responsesStreamBlock struct {
	ItemID    string
	Type      string
	Name      string
	CallID    string
	Signature string
	Text      strings.Builder
	Args      strings.Builder
}

func ConvertClaudeStreamToResponses(_ context.Context, model string, originalRequestJSON []byte, line []byte, state *any) ([]string, error) {
	st := ensureResponsesStreamState(state, model)
	if st.Completed {
		return nil, nil
	}
	data, isData := utils.SSEDataJSON(strings.TrimSpace(string(line)))
	if !isData {
		return nil, nil
	}

	root := gjson.Parse(data)
	if !gjson.Valid(data) || !root.IsObject() {
		return nil, fmt.Errorf("上游 Claude SSE 数据不是有效 JSON 对象")
	}
	eventType := root.Get("type").String()
	var out []string

	switch eventType {
	case "message_start":
		id := root.Get("message.id").String()
		if id == "" {
			id = fmt.Sprintf("resp_%d", time.Now().UnixNano())
		}
		out = append(out, st.start(id, originalRequestJSON)...)
		if usage := root.Get("message.usage"); usage.Exists() {
			st.captureUsage(usage)
		}
	case "content_block_start":
		out = append(out, st.ensureStarted(originalRequestJSON)...)
		idx := int(root.Get("index").Int())
		block := root.Get("content_block")
		out = append(out, st.startClaudeBlock(idx, block)...)
	case "content_block_delta":
		out = append(out, st.ensureStarted(originalRequestJSON)...)
		idx := int(root.Get("index").Int())
		delta := root.Get("delta")
		out = append(out, st.deltaClaudeBlock(idx, delta)...)
	case "content_block_stop":
		idx := int(root.Get("index").Int())
		out = append(out, st.stopBlock(idx)...)
	case "message_delta":
		if usage := root.Get("usage"); usage.Exists() {
			st.captureUsage(usage)
		}
	case "message_stop":
		out = append(out, st.closeAllBlocks()...)
		out = append(out, st.completed(originalRequestJSON)...)
	}
	return out, nil
}

func ConvertGeminiStreamToResponses(_ context.Context, model string, originalRequestJSON []byte, line []byte, state *any) ([]string, error) {
	st := ensureResponsesStreamState(state, model)
	if st.Completed {
		return nil, nil
	}
	data, isData := utils.SSEDataJSON(strings.TrimSpace(string(line)))
	if !isData {
		return nil, nil
	}
	var chunk types.GeminiStreamChunk
	if err := json.Unmarshal([]byte(data), &chunk); err != nil {
		return nil, fmt.Errorf("解析 Gemini 流式响应失败: %w", err)
	}
	// usage 必须在 candidates 循环之前无条件吸收，并且不能拿"有没有 usage"当定稿信号。
	// Gemini 流的每个 chunk 都可能带 usageMetadata（累积值），所以"见到 usage 就定稿"
	// 会在第一个 chunk 就发出 response.completed，把后续全部内容甩到定稿之后。
	// candidate.finishReason 只登记定稿状态；读取结束时再发送完成事件以吸收 usage 尾包。
	// last-wins 覆盖，不能改成"首个非空生效"：thinking 模型的早期 chunk 会给出只含
	// promptTokenCount/thoughtsTokenCount 的 usageMetadata（没有 candidatesTokenCount），
	// first-wins 会把 output_tokens 永久锁在 0。
	if chunk.UsageMetadata != nil {
		st.Usage.InputTokens = chunk.UsageMetadata.PromptTokenCount - chunk.UsageMetadata.CachedContentTokenCount
		if st.Usage.InputTokens < 0 {
			st.Usage.InputTokens = 0
		}
		// candidatesTokenCount 不含 thoughtsTokenCount，而 Responses 语义下 output_tokens
		// 含 reasoning，所以必须相加（三家语义对照见 types.ClaudeOutputTokensDetails）。
		// 与非流式 parseGeminiUsage 同口径，两条路径不允许分叉。
		st.Usage.OutputTokens = chunk.UsageMetadata.CandidatesTokenCount + chunk.UsageMetadata.ThoughtsTokenCount
		st.Usage.TotalTokens = st.Usage.InputTokens + st.Usage.OutputTokens
		if chunk.UsageMetadata.CachedContentTokenCount > 0 {
			st.Usage.CacheReadInputTokens = chunk.UsageMetadata.CachedContentTokenCount
			st.Usage.InputTokensDetails = &types.InputTokensDetails{CachedTokens: chunk.UsageMetadata.CachedContentTokenCount}
		}
		// 已计入 output_tokens，这里只作拆分展示。
		if chunk.UsageMetadata.ThoughtsTokenCount > 0 {
			st.Usage.OutputTokensDetails = &types.OutputTokensDetails{ReasoningTokens: chunk.UsageMetadata.ThoughtsTokenCount}
		}
	}
	if st.FinishPending {
		return nil, nil
	}
	out := st.ensureStarted(originalRequestJSON)

	for _, candidate := range chunk.Candidates {
		if candidate.Content != nil {
			for _, part := range candidate.Content.Parts {
				if part.Thought && part.Text != "" {
					out = append(out, st.emitReasoningDelta(part.Text)...)
					continue
				}
				if part.Text != "" {
					out = append(out, st.emitTextDelta(part.Text)...)
				}
				if part.FunctionCall != nil {
					args, _ := json.Marshal(part.FunctionCall.Args)
					out = append(out, st.emitFunctionCall(part.FunctionCall.Name, string(args))...)
				}
			}
		}
		if candidate.FinishReason != "" {
			out = append(out, st.closeAllBlocks()...)
			st.FinishPending = true
			break
		}
	}
	return out, nil
}

func ensureResponsesStreamState(state *any, model string) *responsesStreamState {
	if *state == nil {
		*state = &responsesStreamState{Model: model, Blocks: map[int]*responsesStreamBlock{}}
	}
	return (*state).(*responsesStreamState)
}

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
		return s.startTextBlock(index)
	case "thinking":
		return s.startReasoningBlock(index)
	case "tool_use":
		callID := block.Get("id").String()
		name := block.Get("name").String()
		return s.startFunctionBlock(index, callID, name)
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
	item := map[string]interface{}{"id": itemID, "type": "function_call", "status": "in_progress", "arguments": "", "call_id": callID, "name": name}
	return []string{emitMapEvent("response.output_item.added", map[string]interface{}{"type": "response.output_item.added", "sequence_number": s.nextSeq(), "output_index": index, "item": item})}
}

func (s *responsesStreamState) emitTextDelta(text string) []string {
	return s.emitIndexedTextDelta(0, text)
}

func (s *responsesStreamState) emitReasoningDelta(text string) []string {
	return s.emitIndexedReasoningDelta(1, text)
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

func (s *responsesStreamState) emitFunctionCall(name string, args string) []string {
	index := 2 + s.NextFunctionIndex
	callID := fmt.Sprintf("call_%d", s.NextFunctionIndex)
	s.NextFunctionIndex++
	out := s.startFunctionBlock(index, callID, name)
	out = append(out, s.emitIndexedFunctionArgsDelta(index, args)...)
	out = append(out, s.stopBlock(index)...)
	return out
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
		item := map[string]interface{}{"id": block.ItemID, "type": "message", "status": "completed", "role": "assistant", "content": []interface{}{map[string]interface{}{"type": "output_text", "text": block.Text.String()}}}
		return []string{emitMapEvent("response.output_text.done", done), emitMapEvent("response.output_item.done", map[string]interface{}{"type": "response.output_item.done", "sequence_number": s.nextSeq(), "output_index": index, "item": item})}
	case "reasoning":
		text := block.Text.String()
		done := map[string]interface{}{"type": "response.reasoning_summary_text.done", "sequence_number": s.nextSeq(), "item_id": block.ItemID, "output_index": index, "summary_index": 0, "text": text}
		item := map[string]interface{}{"id": block.ItemID, "type": "reasoning", "status": "completed", "summary": []interface{}{map[string]interface{}{"type": "summary_text", "text": block.Text.String()}}}
		if block.Signature != "" {
			item["encrypted_content"] = block.Signature
		}
		return []string{emitMapEvent("response.reasoning_summary_text.done", done), emitMapEvent("response.output_item.done", map[string]interface{}{"type": "response.output_item.done", "sequence_number": s.nextSeq(), "output_index": index, "item": item})}
	case "function_call":
		args := block.Args.String()
		if args == "" {
			args = "{}"
		}
		argsDone := map[string]interface{}{"type": "response.function_call_arguments.done", "sequence_number": s.nextSeq(), "item_id": block.ItemID, "name": block.Name, "output_index": index, "arguments": args}
		item := map[string]interface{}{"id": block.ItemID, "type": "function_call", "status": "completed", "arguments": args, "call_id": block.CallID, "name": block.Name}
		return []string{emitMapEvent("response.function_call_arguments.done", argsDone), emitMapEvent("response.output_item.done", map[string]interface{}{"type": "response.output_item.done", "sequence_number": s.nextSeq(), "output_index": index, "item": item})}
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
	if s.Completed {
		return nil
	}
	s.Completed = true
	resp := s.responseEnvelope("completed", originalRequestJSON)
	resp["usage"] = s.Usage
	payload := map[string]interface{}{"type": "response.completed", "sequence_number": s.nextSeq(), "response": resp}
	return []string{emitMapEvent("response.completed", payload)}
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
