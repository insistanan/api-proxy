// Chat → Responses 流事件收尾：内容块关闭与 completed 事件构造。
package converters

import (
	"fmt"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// closeReasoningBlock 关闭 reasoning block
func (st *chatToResponsesState) closeReasoningBlock(nextSeq func() int) []string {
	if !st.ReasoningActive {
		return nil
	}

	var out []string
	full := st.ReasoningBuf.String()

	// response.reasoning_summary_text.done
	textDone := `{"type":"response.reasoning_summary_text.done","sequence_number":0,"item_id":"","output_index":0,"summary_index":0,"text":""}`
	textDone, _ = sjson.Set(textDone, "sequence_number", nextSeq())
	textDone, _ = sjson.Set(textDone, "item_id", st.ReasoningItemID)
	textDone, _ = sjson.Set(textDone, "output_index", st.ReasoningIndex)
	textDone, _ = sjson.Set(textDone, "text", full)
	out = append(out, emitResponsesEvent("response.reasoning_summary_text.done", textDone))

	// response.reasoning_summary_part.done
	partDone := `{"type":"response.reasoning_summary_part.done","sequence_number":0,"item_id":"","output_index":0,"summary_index":0,"part":{"type":"summary_text","text":""}}`
	partDone, _ = sjson.Set(partDone, "sequence_number", nextSeq())
	partDone, _ = sjson.Set(partDone, "item_id", st.ReasoningItemID)
	partDone, _ = sjson.Set(partDone, "output_index", st.ReasoningIndex)
	partDone, _ = sjson.Set(partDone, "part.text", full)
	out = append(out, emitResponsesEvent("response.reasoning_summary_part.done", partDone))

	// response.output_item.done for reasoning
	itemDone := `{"type":"response.output_item.done","sequence_number":0,"output_index":0,"item":{"id":"","type":"reasoning","status":"completed","summary":[]}}`
	itemDone, _ = sjson.Set(itemDone, "sequence_number", nextSeq())
	itemDone, _ = sjson.Set(itemDone, "output_index", st.ReasoningIndex)
	itemDone, _ = sjson.Set(itemDone, "item.id", st.ReasoningItemID)
	itemDone, _ = sjson.Set(itemDone, "item.summary", []interface{}{map[string]interface{}{"type": "summary_text", "text": full}})
	// 保存原始推理与展示摘要的区别，客户端或本地会话重放时只回传 reasoning_text。
	itemDone, _ = sjson.Set(itemDone, "item.content", []interface{}{map[string]interface{}{"type": "reasoning_text", "text": full}})
	out = append(out, emitResponsesEvent("response.output_item.done", itemDone))

	st.ReasoningActive = false
	return out
}

// closeTextBlock 关闭 text block
func (st *chatToResponsesState) closeTextBlock(nextSeq func() int) []string {
	if !st.InTextBlock {
		return nil
	}

	var out []string
	outputIndex := 0
	if st.ReasoningPartAdded {
		outputIndex = 1
	}

	// response.output_text.done
	done := `{"type":"response.output_text.done","sequence_number":0,"item_id":"","output_index":0,"content_index":0,"text":"","logprobs":[]}`
	done, _ = sjson.Set(done, "sequence_number", nextSeq())
	done, _ = sjson.Set(done, "item_id", st.CurrentMsgID)
	done, _ = sjson.Set(done, "output_index", outputIndex)
	done, _ = sjson.Set(done, "text", st.TextBuf.String())
	out = append(out, emitResponsesEvent("response.output_text.done", done))

	// response.content_part.done
	partDone := `{"type":"response.content_part.done","sequence_number":0,"item_id":"","output_index":0,"content_index":0,"part":{"type":"output_text","annotations":[],"logprobs":[],"text":""}}`
	partDone, _ = sjson.Set(partDone, "sequence_number", nextSeq())
	partDone, _ = sjson.Set(partDone, "item_id", st.CurrentMsgID)
	partDone, _ = sjson.Set(partDone, "output_index", outputIndex)
	partDone, _ = sjson.Set(partDone, "part.text", st.TextBuf.String())
	out = append(out, emitResponsesEvent("response.content_part.done", partDone))

	// response.output_item.done for message
	final := `{"type":"response.output_item.done","sequence_number":0,"output_index":0,"item":{"id":"","type":"message","status":"completed","content":[{"type":"output_text","annotations":[],"logprobs":[],"text":""}],"role":"assistant"}}`
	final, _ = sjson.Set(final, "sequence_number", nextSeq())
	final, _ = sjson.Set(final, "output_index", outputIndex)
	final, _ = sjson.Set(final, "item.id", st.CurrentMsgID)
	final, _ = sjson.Set(final, "item.content.0.text", st.TextBuf.String())
	out = append(out, emitResponsesEvent("response.output_item.done", final))

	st.InTextBlock = false
	return out
}

// closeFuncBlocks 关闭所有 function call blocks
func (st *chatToResponsesState) closeFuncBlocks(nextSeq func() int) []string {
	if !st.InFuncBlock || len(st.FuncArgsBuf) == 0 {
		return nil
	}

	var out []string

	// 收集并排序索引
	idxs := make([]int, 0, len(st.FuncArgsBuf))
	for idx := range st.FuncArgsBuf {
		idxs = append(idxs, idx)
	}
	// 简单排序
	for i := 0; i < len(idxs); i++ {
		for j := i + 1; j < len(idxs); j++ {
			if idxs[j] < idxs[i] {
				idxs[i], idxs[j] = idxs[j], idxs[i]
			}
		}
	}

	for _, idx := range idxs {
		args := "{}"
		if buf := st.FuncArgsBuf[idx]; buf != nil && buf.Len() > 0 {
			args = buf.String()
		}
		callID := st.FuncCallIDs[idx]
		name := st.FuncNames[idx]
		itemID, itemType, itemName, namespace, execution, argumentValue := classifyChatToolCall(name, callID, args, st.CustomTools)
		// 锁定类型优先：added/delta 阶段已按锁定类型发事件，收尾必须一致。
		// 名称后到的退化场景（锁定 function、分类结果 custom）按 function_call
		// 收尾，宁可客户端按 JSON 参数解析也不混发事件类型。
		if !st.lockChatToolIsCustom(idx, name) && itemType == "custom_tool_call" {
			itemID = fmt.Sprintf("fc_%s", callID)
			itemType = "function_call"
			itemName = name
			argumentValue = args
		}

		// 计算 output_index
		outputIndex := idx
		if st.ReasoningPartAdded {
			outputIndex += 1
		}
		if st.CurrentMsgID != "" {
			outputIndex += 1
		}

		if itemType == "custom_tool_call" {
			// response.custom_tool_call_input.done：自定义工具的参数终止事件，
			// input 携带还原后的自由文本（与 function_call_arguments.done 互斥）。
			ctcDone := `{"type":"response.custom_tool_call_input.done","sequence_number":0,"item_id":"","output_index":0,"input":""}`
			ctcDone, _ = sjson.Set(ctcDone, "sequence_number", nextSeq())
			ctcDone, _ = sjson.Set(ctcDone, "item_id", itemID)
			ctcDone, _ = sjson.Set(ctcDone, "output_index", outputIndex)
			if input, ok := argumentValue.(string); ok {
				ctcDone, _ = sjson.Set(ctcDone, "input", input)
			}
			out = append(out, emitResponsesEvent("response.custom_tool_call_input.done", ctcDone))
		} else {
			// response.function_call_arguments.done（function 与 tool_search 沿用）
			fcDone := `{"type":"response.function_call_arguments.done","sequence_number":0,"item_id":"","name":"","output_index":0,"arguments":""}`
			fcDone, _ = sjson.Set(fcDone, "sequence_number", nextSeq())
			fcDone, _ = sjson.Set(fcDone, "item_id", itemID)
			if itemName != "" {
				fcDone, _ = sjson.Set(fcDone, "name", itemName)
			} else {
				fcDone, _ = sjson.Set(fcDone, "name", openAIChatToolSearchName)
			}
			fcDone, _ = sjson.Set(fcDone, "output_index", outputIndex)
			fcDone, _ = sjson.Set(fcDone, "arguments", args)
			out = append(out, emitResponsesEvent("response.function_call_arguments.done", fcDone))
		}

		// response.output_item.done for function/custom tool call
		itemDone := `{"type":"response.output_item.done","sequence_number":0,"output_index":0,"item":{"id":"","type":"function_call","status":"completed","arguments":"","call_id":"","name":""}}`
		itemDone, _ = sjson.Set(itemDone, "sequence_number", nextSeq())
		itemDone, _ = sjson.Set(itemDone, "output_index", outputIndex)
		itemDone, _ = sjson.Set(itemDone, "item.id", itemID)
		itemDone, _ = sjson.Set(itemDone, "item.type", itemType)
		itemDone, _ = sjson.Set(itemDone, "item.call_id", callID)
		if itemType == "custom_tool_call" {
			itemDone, _ = sjson.Set(itemDone, "item.name", itemName)
			itemDone, _ = sjson.Delete(itemDone, "item.arguments")
			itemDone, _ = sjson.Set(itemDone, "item.input", argumentValue)
		} else if itemType == "tool_search_call" {
			itemDone, _ = sjson.Delete(itemDone, "item.name")
			itemDone, _ = sjson.Set(itemDone, "item.execution", execution)
			itemDone, _ = sjson.Set(itemDone, "item.arguments", argumentValue)
		} else {
			itemDone, _ = sjson.Set(itemDone, "item.name", itemName)
			itemDone, _ = sjson.Set(itemDone, "item.arguments", argumentValue)
			if namespace != "" {
				itemDone, _ = sjson.Set(itemDone, "item.namespace", namespace)
			}
		}
		out = append(out, emitResponsesEvent("response.output_item.done", itemDone))
	}

	st.InFuncBlock = false
	return out
}

// generateCompletedEvents 生成完成事件
func (st *chatToResponsesState) generateCompletedEvents(originalRequestRawJSON []byte) []string {
	if st.Completed || st.Failed || st.FirstChunk {
		return nil
	}
	st.Completed = true
	var out []string
	nextSeq := func() int { st.Seq++; return st.Seq }

	// 先关闭所有打开的 blocks
	if st.ReasoningActive {
		out = append(out, st.closeReasoningBlock(nextSeq)...)
	}
	if st.InTextBlock {
		out = append(out, st.closeTextBlock(nextSeq)...)
	}
	if st.InFuncBlock {
		out = append(out, st.closeFuncBlocks(nextSeq)...)
	}

	// 构建 response.completed
	completed := `{"type":"response.completed","sequence_number":0,"response":{"id":"","object":"response","created_at":0,"status":"completed","background":false,"error":null}}`
	completed, _ = sjson.Set(completed, "sequence_number", nextSeq())
	completed, _ = sjson.Set(completed, "response.id", st.ResponseID)
	completed, _ = sjson.Set(completed, "response.created_at", st.CreatedAt)

	// 注入原始请求字段
	if originalRequestRawJSON != nil {
		req := gjson.ParseBytes(originalRequestRawJSON)
		if v := req.Get("instructions"); v.Exists() {
			completed, _ = sjson.Set(completed, "response.instructions", v.String())
		}
		if v := req.Get("max_output_tokens"); v.Exists() {
			completed, _ = sjson.Set(completed, "response.max_output_tokens", v.Int())
		}
		if v := req.Get("model"); v.Exists() {
			completed, _ = sjson.Set(completed, "response.model", v.String())
		}
		if v := req.Get("parallel_tool_calls"); v.Exists() {
			completed, _ = sjson.Set(completed, "response.parallel_tool_calls", v.Bool())
		}
		if v := req.Get("previous_response_id"); v.Exists() {
			completed, _ = sjson.Set(completed, "response.previous_response_id", v.String())
		}
		if v := req.Get("reasoning"); v.Exists() {
			completed, _ = sjson.Set(completed, "response.reasoning", v.Value())
		}
		if v := req.Get("temperature"); v.Exists() {
			completed, _ = sjson.Set(completed, "response.temperature", v.Float())
		}
		if v := req.Get("tool_choice"); v.Exists() {
			completed, _ = sjson.Set(completed, "response.tool_choice", v.Value())
		}
		if v := req.Get("tools"); v.Exists() {
			completed, _ = sjson.Set(completed, "response.tools", v.Value())
		}
		if v := req.Get("top_p"); v.Exists() {
			completed, _ = sjson.Set(completed, "response.top_p", v.Float())
		}
		if v := req.Get("metadata"); v.Exists() {
			completed, _ = sjson.Set(completed, "response.metadata", v.Value())
		}
	}

	// 构建 output 数组
	var outputs []interface{}

	// reasoning item（如果有）
	if st.ReasoningBuf.Len() > 0 || st.ReasoningPartAdded {
		r := map[string]interface{}{
			"id":     st.ReasoningItemID,
			"type":   "reasoning",
			"status": "completed",
			"summary": []interface{}{map[string]interface{}{
				"type": "summary_text",
				"text": st.ReasoningBuf.String(),
			}},
			"content": []interface{}{map[string]interface{}{"type": "reasoning_text", "text": st.ReasoningBuf.String()}},
		}
		outputs = append(outputs, r)
	}

	// message item（如果有文本）
	if st.TextBuf.Len() > 0 || st.CurrentMsgID != "" {
		m := map[string]interface{}{
			"id":     st.CurrentMsgID,
			"type":   "message",
			"status": "completed",
			"content": []interface{}{map[string]interface{}{
				"type":        "output_text",
				"annotations": []interface{}{},
				"logprobs":    []interface{}{},
				"text":        st.TextBuf.String(),
			}},
			"role": "assistant",
		}
		outputs = append(outputs, m)
	}

	// function_call items
	if len(st.FuncArgsBuf) > 0 {
		idxs := make([]int, 0, len(st.FuncArgsBuf))
		for idx := range st.FuncArgsBuf {
			idxs = append(idxs, idx)
		}
		for i := 0; i < len(idxs); i++ {
			for j := i + 1; j < len(idxs); j++ {
				if idxs[j] < idxs[i] {
					idxs[i], idxs[j] = idxs[j], idxs[i]
				}
			}
		}
		for _, idx := range idxs {
			args := ""
			if b := st.FuncArgsBuf[idx]; b != nil {
				args = b.String()
			}
			if args == "" {
				args = "{}"
			}
			callID := st.FuncCallIDs[idx]
			name := st.FuncNames[idx]
			// 类型尊重流式锁定（与 output_item.done 同一规则），保证 completed
			// 的 item 类型与流中事件一致。
			item := buildChatToolCallOutputItemWithLock(callID, name, args, st.CustomTools, !st.lockChatToolIsCustom(idx, name))
			outputs = append(outputs, item)
		}
	}

	if len(outputs) > 0 {
		completed, _ = sjson.Set(completed, "response.output", outputs)
	}

	// 添加 usage（完整支持多格式详细字段，参考 claude-code-hub）
	reasoningTokens := st.ReasoningTokens
	if reasoningTokens == 0 && st.ReasoningBuf.Len() > 0 {
		reasoningTokens = int64(st.ReasoningBuf.Len() / 4)
	}

	// 始终添加基础 usage 字段，即使值为 0
	// 这样 handler 可以检测到 usage 存在，并在需要时用本地估算值替换 0 值
	// 参见 handler.go 中的 patchResponsesCompletedEventUsage 和 injectResponsesUsageToCompletedEvent
	completed, _ = sjson.Set(completed, "response.usage.input_tokens", st.InputTokens)
	completed, _ = sjson.Set(completed, "response.usage.output_tokens", st.OutputTokens)
	total := st.InputTokens + st.OutputTokens
	completed, _ = sjson.Set(completed, "response.usage.total_tokens", total)

	// 可选的详情字段，仅在有值时添加
	// input_tokens_details
	if st.CachedTokens > 0 {
		completed, _ = sjson.Set(completed, "response.usage.input_tokens_details.cached_tokens", st.CachedTokens)
	}

	// output_tokens_details
	if reasoningTokens > 0 {
		completed, _ = sjson.Set(completed, "response.usage.output_tokens_details.reasoning_tokens", reasoningTokens)
	}

	// Claude 缓存 TTL 细分字段
	if st.CacheCreationTokens > 0 {
		completed, _ = sjson.Set(completed, "response.usage.cache_creation_input_tokens", st.CacheCreationTokens)
	}
	if st.CacheCreation5mTokens > 0 {
		completed, _ = sjson.Set(completed, "response.usage.cache_creation_5m_input_tokens", st.CacheCreation5mTokens)
	}
	if st.CacheCreation1hTokens > 0 {
		completed, _ = sjson.Set(completed, "response.usage.cache_creation_1h_input_tokens", st.CacheCreation1hTokens)
	}
	if st.CachedTokens > 0 {
		completed, _ = sjson.Set(completed, "response.usage.cache_read_input_tokens", st.CachedTokens)
	}
	if st.CacheTTL != "" {
		completed, _ = sjson.Set(completed, "response.usage.cache_ttl", st.CacheTTL)
	}

	out = append(out, emitResponsesEvent("response.completed", completed))
	return out
}
