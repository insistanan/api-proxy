// Chat SSE → Claude SSE 流式状态机（Messages 入口 → OpenAI Chat 上游）。
//
// 从 openai.go 的 HandleStreamResponseCtx 巨型闭包重构为独立 struct，行为契约：
//   - 懒发送 message_start（首个带 model 的 chunk 时发出）。
//   - reasoning_content / reasoning / thinking 字段与 <think> 嵌入标签只登记
//     代理内部缓存（reasoning_content_cache），绝不转换为客户端可见的 thinking
//     block（d70d8be 决策：Cursor 会把内部推理回灌为客户端可见历史）。
//   - 文本增量经 splitEmbeddedReasoningDelta 拆出嵌入推理后聚合 flush。
//   - 工具调用按上游 index 聚合，延迟 start（id/name 未齐先缓存 arguments）。
//   - finish_reason 只关闭块并记 pendingStopReason（去重），message_delta 延迟
//     到流尾（[DONE] / scanner 结束）统一发送——保证其后到达的 usage chunk
//     不丢，usage 三桶换算在 normalizeOpenAIUsage。
//   - 上游错误只 fail（消费端 ProcessStreamEvents 统一写 error 事件并按协议
//     补终止序列），不再伪造 message_stop。
package providers

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/BenedictKing/api-proxy/internal/types"
	"github.com/BenedictKing/api-proxy/internal/utils"
	"github.com/google/uuid"
)

// chatToClaudeStreamState 聚合一次 Chat 上游流的全部转换状态。
// 单 goroutine 顺序使用，不加锁。
type chatToClaudeStreamState struct {
	// 块索引分配
	nextBlockIndex int

	// 文本块状态
	textBlockStarted bool
	textBlockIndex   int
	textDeltaBuffer  strings.Builder

	// reasoning / 正文累积（缓存用；assistantTextBuffer 刻意不复用
	// textDeltaBuffer，后者在 closeTextBlock 时会 Reset）
	reasoningDeltaBuffer  strings.Builder
	assistantTextBuffer   strings.Builder
	embeddedReasoningMode bool

	// 工具调用聚合：上游 index → 累加器
	toolCallAccumulator map[int]*ToolCallAccumulator
	// 供缓存快照的完整工具调用（index 有序导出）
	assistantToolCalls map[int]types.OpenAIToolCall

	// message_start / 收尾状态
	messageStartEmitted bool
	streamModel         string
	streamUsage         types.Usage
	hasStreamUsage      bool
	pendingStopReason   string
	messageDeltaEmitted bool
	messageStopEmitted  bool
	streamFailed        bool
	streamError         error
	doneSeen            bool
}

func newChatToClaudeStreamState() *chatToClaudeStreamState {
	return &chatToClaudeStreamState{
		textBlockIndex:      -1,
		toolCallAccumulator: make(map[int]*ToolCallAccumulator),
		assistantToolCalls:  make(map[int]types.OpenAIToolCall),
	}
}

// ProcessLine 处理一行上游 SSE，返回要发给客户端的 Claude SSE 事件串。
// 返回的每个元素都是完整事件（含 event: 前缀与尾部空行）。
func (s *chatToClaudeStreamState) ProcessLine(line string) []string {
	if s.streamFailed || s.messageStopEmitted {
		return nil
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return nil
	}
	jsonStr, isData := utils.ParseSSEDataLine(line)
	if !isData || jsonStr == "" {
		return nil
	}
	if jsonStr == utils.SSEDoneMarker {
		s.doneSeen = true
		return s.Finish()
	}

	var chunk map[string]interface{}
	if err := json.Unmarshal([]byte(jsonStr), &chunk); err != nil {
		s.streamFailed = true
		s.streamError = fmt.Errorf("解析 Chat SSE 数据失败: %w", err)
		return nil
	}
	if chunk == nil {
		s.streamFailed = true
		s.streamError = fmt.Errorf("上游 Chat SSE 数据不是 JSON 对象")
		return nil
	}

	if mergeOpenAIUsageFromChunk(chunk, &s.streamUsage) {
		s.hasStreamUsage = true
	}

	// 上游错误：只标记失败，事件由消费端统一写（ProcessStreamEvents 的
	// BuildStreamErrorEvent 会按协议补终止序列）。不再伪造 message_stop。
	if upstreamError, ok := chunk["error"]; ok && upstreamError != nil {
		s.streamFailed = true
		s.streamError = NewUpstreamStreamFailedError(utils.IsUpstreamModelCapacityErrorBody([]byte(jsonStr)), jsonStr)
		return nil
	}
	// finish_reason 后只吸收 usage，不能重新打开正文或工具块。
	if s.pendingStopReason != "" {
		return nil
	}

	var out []string
	choices, ok := chunk["choices"].([]interface{})
	if !ok || len(choices) == 0 {
		return nil
	}
	choice, ok := choices[0].(map[string]interface{})
	if !ok {
		return nil
	}

	// 首个有效 chunk 发 message_start
	if !s.messageStartEmitted {
		if m, ok := chunk["model"].(string); ok {
			s.streamModel = m
		}
		out = append(out, s.emitMessageStart())
	}

	delta, ok := choice["delta"].(map[string]interface{})
	if !ok {
		delta = nil
	}

	// reasoning 只登记缓存，不发给客户端（含 reasoning 别名兼容）。
	if reasoningContent := extractOpenAIReasoningContent(delta); reasoningContent != "" {
		s.reasoningDeltaBuffer.WriteString(reasoningContent)
	}

	// 文本内容
	if content, ok := delta["content"].(string); ok && content != "" {
		visibleContent, embeddedReasoning := splitEmbeddedReasoningDelta(content, &s.embeddedReasoningMode)
		if embeddedReasoning != "" {
			s.reasoningDeltaBuffer.WriteString(embeddedReasoning)
		}
		if visibleContent != "" {
			s.assistantTextBuffer.WriteString(visibleContent)
			if !s.textBlockStarted {
				s.textBlockIndex = s.nextBlockIndex
				s.nextBlockIndex++
				s.textBlockStarted = true
				out = append(out, s.emitTextBlockStart())
			}
			s.textDeltaBuffer.WriteString(visibleContent)
			if shouldFlushOpenAITextDelta(s.textDeltaBuffer.String(), visibleContent) {
				out = append(out, s.flushTextDelta()...)
			}
		}
	}

	// 工具调用。空 tool_calls: [] 不能关闭文本块（部分上游在文本 delta 中携带），
	// 否则 Claude Code 会把连续文本拆成多个 content block。
	if toolCalls, ok := delta["tool_calls"].([]interface{}); ok && len(toolCalls) > 0 {
		out = append(out, s.closeTextBlock()...)
		for _, tc := range toolCalls {
			toolCall, ok := tc.(map[string]interface{})
			if !ok {
				continue
			}
			index := 0
			if idx, ok := toolCall["index"].(float64); ok {
				index = int(idx)
			}
			acc, exists := s.toolCallAccumulator[index]
			if !exists {
				acc = &ToolCallAccumulator{BlockIndex: -1}
				s.toolCallAccumulator[index] = acc
			}
			if id, ok := toolCall["id"].(string); ok {
				acc.ID = id
			}
			if function, ok := toolCall["function"].(map[string]interface{}); ok {
				if name, ok := function["name"].(string); ok {
					acc.Name = name
				}
				if args, ok := function["arguments"].(string); ok {
					acc.Arguments += args
				}
			}
			s.assistantToolCalls[index] = types.OpenAIToolCall{
				ID:   acc.ID,
				Type: "function",
				Function: types.OpenAIToolCallFunction{
					Name:      acc.Name,
					Arguments: acc.Arguments,
				},
			}
			if ensureChatToolCallStarted(s, acc) {
				out = append(out, emitPendingChatToolArgumentDelta(acc)...)
			}
		}
	}

	// finish_reason：只关闭块 + 记 stopReason（首个生效），message_delta 延迟到流尾。
	// 不再因 tool_use 提前结束流——其后可能还有 usage chunk。
	if finishReason, ok := choice["finish_reason"].(string); ok && finishReason != "" && finishReason != "none" && finishReason != "null" {
		out = append(out, s.closeTextBlock()...)
		out = append(out, s.closeAllToolCalls()...)
		if s.pendingStopReason == "" {
			stopReason := chatStopReasonFromFinish(finishReason)
			s.pendingStopReason = stopReason
		}
	}

	return out
}

// Finish 流结束（[DONE] 或 scanner EOF）时的统一收尾，只执行一次。
func (s *chatToClaudeStreamState) Finish() []string {
	if s.streamFailed || !s.Completed() {
		// 错误流：不伪造成功收尾（message_delta/message_stop 由错误路径处理）。
		return nil
	}
	if s.messageStopEmitted {
		return nil
	}
	s.messageStopEmitted = true

	// 缓存本轮完整 assistant 消息。工具调用回合通常没有文本，仍需用其
	// 工具调用内容关联 reasoning_content，供 Claude Code 下一轮回传时补回。
	if s.reasoningDeltaBuffer.Len() > 0 {
		storeReasoningForAssistantMessage(types.OpenAIMessage{
			Role:      "assistant",
			Content:   s.assistantTextBuffer.String(),
			ToolCalls: snapshotChatAssistantToolCalls(s.assistantToolCalls),
		}, s.reasoningDeltaBuffer.String())
	}

	out := s.closeTextBlock()
	out = append(out, s.closeAllToolCalls()...)
	if s.messageStartEmitted {
		if s.pendingStopReason == "" {
			s.pendingStopReason = "end_turn"
		}
		if delta := s.emitMessageDelta(); delta != "" {
			out = append(out, delta)
		}
	}
	out = append(out, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
	return out
}

// Failed 报告本次流是否以错误告终（上游 error 事件）。
func (s *chatToClaudeStreamState) Failed() bool { return s.streamFailed }

// Completed 要求有效消息与协议定稿信号，允许兼容上游仅用 [DONE] 收尾。
func (s *chatToClaudeStreamState) Completed() bool {
	return s.messageStartEmitted && (s.doneSeen || s.pendingStopReason != "")
}

// SnapshotAssistantMessage 导出本轮 assistant 消息（供错误路径登记缓存等用途）。
func chatStopReasonFromFinish(finishReason string) string {
	switch finishReason {
	case "tool_calls", "function_call":
		return "tool_use"
	case "length":
		return "max_tokens"
	default:
		return "end_turn"
	}
}

func (s *chatToClaudeStreamState) emitMessageStart() string {
	msgStart := map[string]interface{}{
		"type": "message_start",
		"message": map[string]interface{}{
			"id":            fmt.Sprintf("msg_%s", uuid.New().String()),
			"type":          "message",
			"role":          "assistant",
			"content":       []interface{}{},
			"model":         s.streamModel,
			"stop_reason":   nil,
			"stop_sequence": nil,
			"usage":         map[string]interface{}{"input_tokens": 0, "output_tokens": 1},
		},
	}
	startJSON, _ := json.Marshal(msgStart)
	s.messageStartEmitted = true
	return fmt.Sprintf("event: message_start\ndata: %s\n\n", startJSON)
}

func (s *chatToClaudeStreamState) emitTextBlockStart() string {
	startEvent := map[string]interface{}{
		"type":  "content_block_start",
		"index": s.textBlockIndex,
		"content_block": map[string]string{
			"type": "text",
			"text": "",
		},
	}
	startJSON, _ := json.Marshal(startEvent)
	return fmt.Sprintf("event: content_block_start\ndata: %s\n\n", startJSON)
}

func (s *chatToClaudeStreamState) emitTextDelta(text string) string {
	if text == "" {
		return ""
	}
	deltaEvent := map[string]interface{}{
		"type":  "content_block_delta",
		"index": s.textBlockIndex,
		"delta": map[string]string{
			"type": "text_delta",
			"text": text,
		},
	}
	deltaJSON, _ := json.Marshal(deltaEvent)
	return fmt.Sprintf("event: content_block_delta\ndata: %s\n\n", deltaJSON)
}

func (s *chatToClaudeStreamState) flushTextDelta() []string {
	if !s.textBlockStarted || s.textDeltaBuffer.Len() == 0 {
		return nil
	}
	event := s.emitTextDelta(s.textDeltaBuffer.String())
	s.textDeltaBuffer.Reset()
	if event == "" {
		return nil
	}
	return []string{event}
}

func (s *chatToClaudeStreamState) closeTextBlock() []string {
	if !s.textBlockStarted {
		return nil
	}
	out := s.flushTextDelta()
	stopEvent := map[string]interface{}{
		"type":  "content_block_stop",
		"index": s.textBlockIndex,
	}
	stopJSON, _ := json.Marshal(stopEvent)
	out = append(out, fmt.Sprintf("event: content_block_stop\ndata: %s\n\n", stopJSON))
	s.textBlockStarted = false
	s.textBlockIndex = -1
	return out
}

// emitChatToolCallStart 发送工具调用 content_block_start（要求 acc 已分配块索引）。
func emitChatToolCallStart(acc *ToolCallAccumulator) string {
	startEvent := map[string]interface{}{
		"type":  "content_block_start",
		"index": acc.BlockIndex,
		"content_block": map[string]interface{}{
			"type":  "tool_use",
			"id":    acc.ID,
			"name":  acc.Name,
			"input": map[string]interface{}{},
		},
	}
	startJSON, _ := json.Marshal(startEvent)
	return fmt.Sprintf("event: content_block_start\ndata: %s\n\n", startJSON)
}

func emitChatToolArgumentDelta(acc *ToolCallAccumulator, partialJSON string) string {
	if partialJSON == "" {
		return ""
	}
	deltaEvent := map[string]interface{}{
		"type":  "content_block_delta",
		"index": acc.BlockIndex,
		"delta": map[string]string{
			"type":         "input_json_delta",
			"partial_json": partialJSON,
		},
	}
	deltaJSON, _ := json.Marshal(deltaEvent)
	return fmt.Sprintf("event: content_block_delta\ndata: %s\n\n", deltaJSON)
}

func emitChatContentBlockStop(index int) string {
	stopEvent := map[string]interface{}{
		"type":  "content_block_stop",
		"index": index,
	}
	stopJSON, _ := json.Marshal(stopEvent)
	return fmt.Sprintf("event: content_block_stop\ndata: %s\n\n", stopJSON)
}

// ensureChatToolCallStarted 延迟 start：id/name 未齐时不发 start 事件，
// arguments 先缓存在 acc；凑齐后分配块索引、发 start 并补发已缓存的 arguments。
func ensureChatToolCallStarted(s *chatToClaudeStreamState, acc *ToolCallAccumulator) bool {
	if acc == nil {
		return false
	}
	if acc.Started {
		return true
	}
	if acc.ID == "" || acc.Name == "" {
		return false
	}
	acc.BlockIndex = s.nextBlockIndex
	s.nextBlockIndex++
	acc.Started = true
	return true
}

// emitPendingChatToolArgumentDelta 发出 acc 中尚未下发的 arguments 增量。
// 调用前提：ensureChatToolCallStarted 已返回 true（start 事件已发或本调用链发出）。
func emitPendingChatToolArgumentDelta(acc *ToolCallAccumulator) []string {
	if acc == nil || !acc.Started {
		return nil
	}
	var out []string
	if !acc.StartEventEmitted {
		out = append(out, emitChatToolCallStart(acc))
		acc.StartEventEmitted = true
	}
	if acc.EmittedArgumentLen >= len(acc.Arguments) {
		return out
	}
	if delta := emitChatToolArgumentDelta(acc, acc.Arguments[acc.EmittedArgumentLen:]); delta != "" {
		out = append(out, delta)
	}
	acc.EmittedArgumentLen = len(acc.Arguments)
	return out
}

func (s *chatToClaudeStreamState) closeToolCall(index int) []string {
	acc := s.toolCallAccumulator[index]
	if acc == nil {
		return nil
	}
	var out []string
	if ensureChatToolCallStarted(s, acc) {
		out = append(out, emitPendingChatToolArgumentDelta(acc)...)
		out = append(out, emitChatContentBlockStop(acc.BlockIndex))
	}
	delete(s.toolCallAccumulator, index)
	return out
}

func (s *chatToClaudeStreamState) closeAllToolCalls() []string {
	if len(s.toolCallAccumulator) == 0 {
		return nil
	}
	indexes := make([]int, 0, len(s.toolCallAccumulator))
	for index := range s.toolCallAccumulator {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	var out []string
	for _, index := range indexes {
		out = append(out, s.closeToolCall(index)...)
	}
	return out
}

func snapshotChatAssistantToolCalls(calls map[int]types.OpenAIToolCall) []types.OpenAIToolCall {
	if len(calls) == 0 {
		return nil
	}
	indexes := make([]int, 0, len(calls))
	for index := range calls {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	toolCalls := make([]types.OpenAIToolCall, 0, len(indexes))
	for _, index := range indexes {
		toolCalls = append(toolCalls, calls[index])
	}
	return toolCalls
}

func (s *chatToClaudeStreamState) emitMessageDelta() string {
	if s.messageDeltaEmitted {
		return ""
	}
	if s.pendingStopReason == "" {
		s.pendingStopReason = "end_turn"
	}
	s.messageDeltaEmitted = true
	return buildOpenAIMessageDeltaEvent(s.pendingStopReason, s.streamUsage, s.hasStreamUsage)
}
