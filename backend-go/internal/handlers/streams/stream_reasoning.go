// 本文件负责流式响应内容的累积与推理内容缓存：
// captureReasoningContext 从每个 SSE 事件里累积 thinking / text / tool_use 参数到 Context；
// cacheClaudeReasoning 在流结束时把累积结果组装成助手响应，写入 providers 的推理缓存。
package streams

import (
	"encoding/json"
	"sort"

	"github.com/insistanan/api-proxy/internal/providers"
	"github.com/insistanan/api-proxy/internal/types"
)

func (ctx *Context) captureReasoningContext(data map[string]interface{}) {
	if ctx == nil || data == nil {
		return
	}
	if ctx.ToolCalls == nil {
		ctx.ToolCalls = make(map[int]*StreamToolCall)
	}
	index := 0
	if value, ok := data["index"].(float64); ok {
		index = int(value)
	}
	if contentBlock, ok := data["content_block"].(map[string]interface{}); ok {
		switch contentBlock["type"] {
		case "thinking":
			if thinking, _ := contentBlock["thinking"].(string); thinking != "" {
				ctx.Reasoning.WriteString(thinking)
			}
		case "text":
			if text, _ := contentBlock["text"].(string); text != "" {
				ctx.ResponseText.WriteString(text)
			}
		case "tool_use":
			call := ctx.ToolCalls[index]
			if call == nil {
				call = &StreamToolCall{}
				ctx.ToolCalls[index] = call
			}
			if id, _ := contentBlock["id"].(string); id != "" {
				call.ID = id
			}
			if name, _ := contentBlock["name"].(string); name != "" {
				call.Name = name
			}
		}
	}

	delta, ok := data["delta"].(map[string]interface{})
	if !ok {
		return
	}
	switch delta["type"] {
	case "thinking_delta":
		if thinking, _ := delta["thinking"].(string); thinking != "" {
			ctx.Reasoning.WriteString(thinking)
		}
	case "text_delta":
		if text, _ := delta["text"].(string); text != "" {
			ctx.ResponseText.WriteString(text)
		}
	case "input_json_delta":
		if partialJSON, _ := delta["partial_json"].(string); partialJSON != "" {
			call := ctx.ToolCalls[index]
			if call == nil {
				call = &StreamToolCall{}
				ctx.ToolCalls[index] = call
			}
			call.Arguments.WriteString(partialJSON)
		}
	}
}

func (ctx *Context) cacheClaudeReasoning() {
	if ctx == nil || ctx.Reasoning.Len() == 0 {
		return
	}
	indexes := make([]int, 0, len(ctx.ToolCalls))
	for index := range ctx.ToolCalls {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	content := make([]types.ClaudeContent, 0, len(indexes)+2)
	content = append(content, types.ClaudeContent{Type: "thinking", Thinking: ctx.Reasoning.String()})
	if ctx.ResponseText.Len() > 0 {
		content = append(content, types.ClaudeContent{Type: "text", Text: ctx.ResponseText.String()})
	}
	for _, index := range indexes {
		call := ctx.ToolCalls[index]
		if call == nil {
			continue
		}
		var input interface{} = map[string]interface{}{}
		if call.Arguments.Len() > 0 {
			_ = json.Unmarshal([]byte(call.Arguments.String()), &input)
		}
		content = append(content, types.ClaudeContent{
			Type:  "tool_use",
			ID:    call.ID,
			Name:  call.Name,
			Input: input,
		})
	}
	providers.CacheClaudeResponseReasoning(&types.ClaudeResponse{Role: "assistant", Content: content}, ctx.ReasoningScope)
}
