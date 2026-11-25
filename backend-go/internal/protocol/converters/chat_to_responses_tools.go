package converters

import (
	"fmt"
	"strings"

	"github.com/tidwall/gjson"
)

// 工具名称可能晚于 ID 到达。在身份齐全前累计参数，避免先猜成 function，
// 再在完成时改成 custom 或 tool_search，导致同一次调用的事件类型发生变化。
func (st *chatToResponsesState) receiveToolCalls(calls gjson.Result, nextSeq func() int) []string {
	if st.ToolAdded == nil {
		st.ToolAdded = make(map[int]bool)
		st.ToolArgumentSent = make(map[int]int)
	}
	var out []string
	for _, call := range calls.Array() {
		index := int(call.Get("index").Int())
		out = append(out, st.closeReasoningBlock(nextSeq)...)
		out = append(out, st.closeTextBlock(nextSeq)...)
		st.InFuncBlock = true
		if st.FuncArgsBuf[index] == nil {
			st.FuncArgsBuf[index] = &strings.Builder{}
		}
		if id := call.Get("id").String(); id != "" {
			if st.ToolAdded[index] && st.FuncCallIDs[index] != id {
				st.Err = fmt.Errorf("Chat 工具 index=%d 的调用 ID 在交付后改变", index)
				return nil
			}
			st.FuncCallIDs[index] = id
		}
		if name := call.Get("function.name").String(); name != "" {
			if st.ToolAdded[index] && st.FuncNames[index] != name {
				st.Err = fmt.Errorf("Chat 工具 index=%d 的名称在交付后改变", index)
				return nil
			}
			st.FuncNames[index] = name
		}
		st.FuncArgsBuf[index].WriteString(call.Get("function.arguments").String())
		if st.FuncCallIDs[index] == "" || st.FuncNames[index] == "" {
			continue
		}
		item := buildChatToolCallOutputItem(st.FuncCallIDs[index], st.FuncNames[index], "{}", st.CustomTools)
		outputIndex := index
		if st.ReasoningPartAdded {
			outputIndex++
		}
		if st.CurrentMsgID != "" {
			outputIndex++
		}
		if !st.ToolAdded[index] {
			item["status"] = "in_progress"
			if item["type"] == "function_call" {
				item["arguments"] = ""
			}
			out = append(out, emitMapEvent("response.output_item.added", map[string]interface{}{
				"type": "response.output_item.added", "sequence_number": nextSeq(), "output_index": outputIndex, "item": item,
			}))
			st.ToolAdded[index] = true
		}
		// custom 的 input 和 tool_search 的对象参数必须在 JSON 完整后交付。
		if item["type"] != "function_call" {
			continue
		}
		arguments := st.FuncArgsBuf[index].String()
		if sent := st.ToolArgumentSent[index]; sent < len(arguments) {
			out = append(out, emitMapEvent("response.function_call_arguments.delta", map[string]interface{}{
				"type": "response.function_call_arguments.delta", "sequence_number": nextSeq(),
				"item_id": item["id"], "output_index": outputIndex, "delta": arguments[sent:],
			}))
			st.ToolArgumentSent[index] = len(arguments)
		}
	}
	return out
}
