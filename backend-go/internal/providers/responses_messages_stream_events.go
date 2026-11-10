// Responses → Messages 流事件构造：文本、思考、工具调用与用量收尾。
package providers

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/insistanan/api-proxy/internal/types"
	"github.com/tidwall/gjson"
)

func (s *responsesToClaudeStreamState) captureUpstreamResponseID(root gjson.Result) {
	if s.upstreamResponseID != "" {
		return
	}
	if responseID := root.Get("response.id").String(); responseID != "" {
		s.upstreamResponseID = responseID
		return
	}
	// response.created 等事件可能把 id 放在顶层
	if eventType := root.Get("type").String(); strings.HasPrefix(eventType, "response.") {
		if responseID := root.Get("id").String(); responseID != "" && strings.HasPrefix(responseID, "resp_") {
			s.upstreamResponseID = responseID
		}
	}
}

func (s *responsesToClaudeStreamState) ensureMessageStart(root gjson.Result) []string {
	if s.messageStarted {
		return nil
	}
	if model := root.Get("response.model").String(); model != "" {
		s.model = model
	} else if model := root.Get("model").String(); model != "" {
		s.model = model
	}
	s.messageStarted = true
	// Prefer any early usage if present; otherwise leave zeros (stream handler may estimate).
	// captureResponsesUsage 已把 s.inputTokens 收敛成 uncached 余量，这里直接用。
	startInputTokens := s.inputTokens
	// cache_* 同时供管理端采集与 Claude 客户端计算完整上下文占用，客户端出口保持原样。
	startUsage := map[string]interface{}{
		"input_tokens":  startInputTokens,
		"output_tokens": 0,
	}
	if s.cacheReadInputTokens > 0 {
		startUsage["cache_read_input_tokens"] = s.cacheReadInputTokens
		startUsage["input_tokens_details"] = map[string]interface{}{
			"cached_tokens": s.cacheReadInputTokens,
		}
	}
	if s.cacheCreationInputTokens > 0 {
		startUsage["cache_creation_input_tokens"] = s.cacheCreationInputTokens
	}
	return []string{buildClaudeSSE("message_start", map[string]interface{}{
		"type": "message_start",
		"message": map[string]interface{}{
			"id":            s.responseID,
			"type":          "message",
			"role":          "assistant",
			"content":       []interface{}{},
			"model":         s.model,
			"stop_reason":   nil,
			"stop_sequence": nil,
			"usage":         startUsage,
		},
	})}
}

func (s *responsesToClaudeStreamState) emitTextDelta(text string) []string {
	if text == "" {
		return nil
	}
	s.assistantText.WriteString(text)
	out := s.closeReasoningBlock()
	if !s.textBlockOpen {
		s.textBlockIndex = s.nextBlockIndex
		s.nextBlockIndex++
		s.textBlockOpen = true
		out = append(out, buildClaudeSSE("content_block_start", map[string]interface{}{
			"type":  "content_block_start",
			"index": s.textBlockIndex,
			"content_block": map[string]interface{}{
				"type": "text",
				"text": "",
			},
		}))
	}
	out = append(out, buildClaudeSSE("content_block_delta", map[string]interface{}{
		"type":  "content_block_delta",
		"index": s.textBlockIndex,
		"delta": map[string]interface{}{"type": "text_delta", "text": text},
	}))
	return out
}

func (s *responsesToClaudeStreamState) emitToolUse(root gjson.Result) []string {
	key := responsesToolCallKey(root)
	if key != "" && s.emittedToolCalls[key] {
		return nil
	}
	out := s.closeReasoningBlock()
	out = append(out, s.closeTextBlock()...)
	stored := s.toolCalls[key]
	callID := root.Get("item.call_id").String()
	if callID == "" {
		callID = root.Get("call_id").String()
	}
	if callID == "" && stored != nil {
		callID = stored.CallID
	}
	name := root.Get("item.name").String()
	if name == "" {
		name = root.Get("name").String()
	}
	if name == "" && stored != nil {
		name = stored.Name
	}
	arguments := root.Get("arguments").String()
	if arguments == "" {
		arguments = root.Get("item.arguments").String()
	}
	if arguments == "" {
		input := root.Get("item.input")
		if input.Exists() {
			if input.Type == gjson.String {
				inputJSON, _ := json.Marshal(map[string]string{"input": input.String()})
				arguments = string(inputJSON)
			} else if strings.TrimSpace(input.Raw) != "" {
				arguments = input.Raw
			}
		}
	}
	if arguments == "" && stored != nil {
		arguments = stored.Arguments
	}
	if callID == "" {
		callID = fmt.Sprintf("call_%d", s.nextBlockIndex)
	}
	var input interface{} = map[string]interface{}{}
	if arguments != "" {
		if err := json.Unmarshal([]byte(arguments), &input); err != nil && root.Get("item.type").String() == "custom_tool_call" {
			input = map[string]interface{}{"input": arguments}
		}
	}
	if stored != nil {
		stored.CallID = callID
		stored.Name = name
		stored.Arguments = arguments
	}
	index := s.nextBlockIndex
	s.nextBlockIndex++
	s.stopReason = "tool_use"
	if key != "" {
		s.emittedToolCalls[key] = true
	}
	return append(out, processToolUsePart(callID, name, input, index)...)
}

func (s *responsesToClaudeStreamState) captureToolCall(root gjson.Result) {
	key := responsesToolCallKey(root)
	if key == "" {
		return
	}
	itemType := root.Get("item.type").String()
	if itemType != "" && itemType != "function_call" && itemType != "custom_tool_call" {
		return
	}
	call := s.toolCalls[key]
	if call == nil {
		call = &responsesStreamToolCall{}
		s.toolCalls[key] = call
		s.toolCallOrder = append(s.toolCallOrder, key)
	}
	if callID := root.Get("item.call_id").String(); callID != "" {
		call.CallID = callID
	} else if callID := root.Get("call_id").String(); callID != "" {
		call.CallID = callID
	}
	if name := root.Get("item.name").String(); name != "" {
		call.Name = name
	} else if name := root.Get("name").String(); name != "" {
		call.Name = name
	}
	if arguments := root.Get("item.arguments").String(); arguments != "" {
		call.Arguments = arguments
	} else if arguments := root.Get("arguments").String(); arguments != "" {
		call.Arguments = arguments
	} else if input := root.Get("item.input"); input.Exists() {
		if input.Type == gjson.String {
			inputJSON, _ := json.Marshal(map[string]string{"input": input.String()})
			call.Arguments = string(inputJSON)
		} else if strings.TrimSpace(input.Raw) != "" {
			call.Arguments = input.Raw
		}
	}
}

func (s *responsesToClaudeStreamState) captureToolArgumentDelta(root gjson.Result) {
	key := responsesToolCallKey(root)
	if key == "" {
		return
	}
	call := s.toolCalls[key]
	if call == nil {
		call = &responsesStreamToolCall{}
		s.toolCalls[key] = call
		s.toolCallOrder = append(s.toolCallOrder, key)
	}
	if callID := root.Get("call_id").String(); callID != "" {
		call.CallID = callID
	}
	if name := root.Get("name").String(); name != "" {
		call.Name = name
	}
	if delta := root.Get("delta").String(); delta != "" {
		call.Arguments += delta
	}
}

// cacheReasoning 保存成功完成的 Responses 流式推理，供后续切换到严格 Chat
// 渠道时补回 reasoning_content。下一轮 Messages→Responses 转换默认跳过历史
// thinking，因此它不会进入上游上下文。
func (s *responsesToClaudeStreamState) cacheReasoning() {
	if s == nil || !s.completed || s.reasoning.Len() == 0 {
		return
	}

	content := make([]types.ClaudeContent, 0, len(s.toolCallOrder)+1)
	if s.assistantText.Len() > 0 {
		content = append(content, types.ClaudeContent{
			Type: "text",
			Text: s.assistantText.String(),
		})
	}
	for _, key := range s.toolCallOrder {
		call := s.toolCalls[key]
		if call == nil {
			continue
		}
		var input interface{} = map[string]interface{}{}
		if call.Arguments != "" {
			_ = json.Unmarshal([]byte(call.Arguments), &input)
		}
		content = append(content, types.ClaudeContent{
			Type:  "tool_use",
			ID:    call.CallID,
			Name:  call.Name,
			Input: input,
		})
	}
	CacheClaudeResponseReasoning(&types.ClaudeResponse{
		Role:    "assistant",
		Content: append([]types.ClaudeContent{{Type: "thinking", Thinking: s.reasoning.String()}}, content...),
	})
}

func (s *responsesToClaudeStreamState) outputFingerprint() string {
	if s == nil || !s.completed {
		return ""
	}
	if len(s.completedOutput) > 0 {
		return responsesAssistantOutputFingerprint(s.completedOutput)
	}
	items := make([]types.ResponsesItem, 0, len(s.toolCallOrder)+1)
	if s.assistantText.Len() > 0 {
		items = append(items, types.ResponsesItem{
			Type: "message", Role: "assistant",
			Content: []interface{}{map[string]interface{}{
				"type": "output_text", "text": s.assistantText.String(),
			}},
		})
	}
	for _, key := range s.toolCallOrder {
		call := s.toolCalls[key]
		if call == nil {
			continue
		}
		items = append(items, types.ResponsesItem{
			Type: "function_call", CallID: call.CallID, Name: call.Name, Arguments: call.Arguments,
		})
	}
	return responsesAssistantOutputFingerprint(items)
}

func responsesToolCallKey(root gjson.Result) string {
	if itemID := root.Get("item.id").String(); itemID != "" {
		return itemID
	}
	if itemID := root.Get("item_id").String(); itemID != "" {
		return itemID
	}
	if outputIndex := root.Get("output_index"); outputIndex.Exists() {
		return fmt.Sprintf("output_%d", outputIndex.Int())
	}
	if callID := root.Get("item.call_id").String(); callID != "" {
		return callID
	}
	if callID := root.Get("call_id").String(); callID != "" {
		return callID
	}
	return ""
}

func (s *responsesToClaudeStreamState) closeTextBlock() []string {
	if !s.textBlockOpen {
		return nil
	}
	s.textBlockOpen = false
	return []string{buildClaudeSSE("content_block_stop", map[string]interface{}{
		"type":  "content_block_stop",
		"index": s.textBlockIndex,
	})}
}

func (s *responsesToClaudeStreamState) emitReasoningDelta(text string) []string {
	if text == "" {
		return nil
	}
	out := []string{}
	if !s.reasoningBlockOpen {
		s.reasoningBlockIndex = s.nextBlockIndex
		s.nextBlockIndex++
		s.reasoningBlockOpen = true
		out = append(out, buildClaudeSSE("content_block_start", map[string]interface{}{
			"type":  "content_block_start",
			"index": s.reasoningBlockIndex,
			"content_block": map[string]interface{}{
				"type":     "thinking",
				"thinking": "",
			},
		}))
	}
	s.reasoningEmittedLen += len(text)
	return append(out, buildClaudeSSE("content_block_delta", map[string]interface{}{
		"type":  "content_block_delta",
		"index": s.reasoningBlockIndex,
		"delta": map[string]interface{}{"type": "thinking_delta", "thinking": text},
	}))
}

func (s *responsesToClaudeStreamState) closeReasoningBlock() []string {
	if !s.reasoningBlockOpen {
		return nil
	}
	s.reasoningBlockOpen = false
	return []string{buildClaudeSSE("content_block_stop", map[string]interface{}{
		"type":  "content_block_stop",
		"index": s.reasoningBlockIndex,
	})}
}

func responsesStreamOutputItem(item gjson.Result) (types.ResponsesItem, bool) {
	if !item.Exists() || !item.IsObject() {
		return types.ResponsesItem{}, false
	}
	payload, err := json.Marshal(item.Value())
	if err != nil {
		return types.ResponsesItem{}, false
	}
	var parsed types.ResponsesItem
	if err := json.Unmarshal(payload, &parsed); err != nil || parsed.Type == "" {
		return types.ResponsesItem{}, false
	}
	if parsed.Content == nil {
		if parsed.Type == "custom_tool_call" {
			parsed.Content = item.Get("input").Value()
		} else {
			parsed.Content = item.Get("output").Value()
		}
	}
	return parsed, true
}

func (s *responsesToClaudeStreamState) emitMessageDelta() []string {
	if s.stopReason == "" {
		s.stopReason = "end_turn"
	}
	// s.inputTokens 已由 captureResponsesUsage 收敛成 uncached 余量。
	// cache_read_input_tokens 和 cache_creation_input_tokens 既给管理端采集，也会到达
	// Claude 客户端，让客户端按 Anthropic 契约求和 input_tokens + cache_read +
	// cache_creation 得到真实上下文占用。三者必须同步：把 input_tokens 改回总量会双计。
	clientInputTokens := s.inputTokens
	usageMap := map[string]interface{}{
		"output_tokens": s.outputTokens,
	}
	if clientInputTokens > 0 {
		usageMap["input_tokens"] = clientInputTokens
	}
	if s.cacheReadInputTokens > 0 {
		usageMap["cache_read_input_tokens"] = s.cacheReadInputTokens
		usageMap["input_tokens_details"] = map[string]interface{}{
			"cached_tokens": s.cacheReadInputTokens,
		}
	}
	if s.cacheCreationInputTokens > 0 {
		usageMap["cache_creation_input_tokens"] = s.cacheCreationInputTokens
	}
	return []string{buildClaudeSSE("message_delta", map[string]interface{}{
		"type": "message_delta",
		"delta": map[string]interface{}{
			"stop_reason":   s.stopReason,
			"stop_sequence": nil,
		},
		"usage": usageMap,
	})}
}

// captureResponsesUsage extracts usage/cache fields from Responses SSE payloads.
// OpenAI Responses commonly places usage under response.usage on response.completed:
//
//	input_tokens, output_tokens, input_tokens_details.cached_tokens
//
// Some gateways also emit top-level usage or cache_read_input_tokens.
func (s *responsesToClaudeStreamState) captureResponsesUsage(root gjson.Result) {
	usageNode := root.Get("response.usage")
	if !usageNode.Exists() {
		usageNode = root.Get("usage")
	}
	if !usageNode.Exists() {
		return
	}

	// reportedInput 保留"本次上游报出的原始 input"，减法绑定它而不是 s.inputTokens：
	// 一条流里 captureResponsesUsage 会被多次调用，绑定累积字段会重复相减。
	reportedInput := 0
	if v := usageNode.Get("input_tokens"); v.Exists() && v.Int() > 0 {
		reportedInput = int(v.Int())
	} else if v := usageNode.Get("prompt_tokens"); v.Exists() && v.Int() > 0 {
		reportedInput = int(v.Int())
	}
	if reportedInput > 0 {
		s.inputTokens = reportedInput
		s.hasUsage = true
	}
	if v := usageNode.Get("output_tokens"); v.Exists() && v.Int() > 0 {
		s.outputTokens = int(v.Int())
		s.hasUsage = true
	} else if v := usageNode.Get("completion_tokens"); v.Exists() && v.Int() > 0 {
		s.outputTokens = int(v.Int())
		s.hasUsage = true
	}

	// 缓存量按字段名分开保存 provenance：折叠成一个值就判不出 input_tokens 是否已含缓存。
	//   cacheReadAnthropic —— cache_read_input_tokens，上游本就按 uncached 报 input。
	//   cacheReadSubset    —— *_tokens_details.cached_tokens，OpenAI 语义 cached ⊆ input。
	cacheReadAnthropic := int64(0)
	cacheReadSubset := int64(0)
	if v := usageNode.Get("cache_read_input_tokens"); v.Exists() && v.Int() > 0 {
		cacheReadAnthropic = v.Int()
	}
	if v := usageNode.Get("input_tokens_details.cached_tokens"); v.Exists() && v.Int() > 0 {
		cacheReadSubset = v.Int()
	} else if v := usageNode.Get("prompt_tokens_details.cached_tokens"); v.Exists() && v.Int() > 0 {
		cacheReadSubset = v.Int()
	}
	cacheRead := cacheReadAnthropic
	if cacheRead == 0 {
		cacheRead = cacheReadSubset
	}
	if cacheRead > 0 {
		s.cacheReadInputTokens = int(cacheRead)
		s.hasUsage = true
	}

	if v := usageNode.Get("cache_creation_input_tokens"); v.Exists() && v.Int() > 0 {
		s.cacheCreationInputTokens = int(v.Int())
		s.hasUsage = true
	}

	// input_tokens 收敛到 Anthropic uncached 口径，判据与 normalizeOpenAIUsage 完全一致：
	// 只有当缓存量来自 subset 式字段名、且上游没给 Anthropic 式 cache_read_input_tokens 时，
	// input_tokens 才是含缓存的总量，必须减；上游给了 Anthropic 名就说明它已经拆好了，
	// 再减一次是双减。客户端按契约求和 input_tokens + cache_read 才回到真实占用。
	if reportedInput > 0 && cacheReadSubset > 0 && cacheReadAnthropic == 0 {
		s.inputTokens = reportedInput - int(cacheReadSubset)
		if s.inputTokens < 0 {
			// 上游异常回包（cached > input）。钳制到 0，不让负数流进客户端与指标。
			s.inputTokens = 0
		}
	}
}

func buildClaudeSSE(event string, data map[string]interface{}) string {
	dataJSON, _ := json.Marshal(data)
	return fmt.Sprintf("event: %s\ndata: %s\n\n", event, dataJSON)
}
