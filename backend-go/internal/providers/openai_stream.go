// OpenAI 流式响应脚手架与工具增量辅助，状态机仍在 chat_stream.go。
package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/insistanan/api-proxy/internal/types"
	"github.com/insistanan/api-proxy/internal/utils"
)

// HandleStreamResponse 处理流式响应
// 兼容旧签名：内部委托带 ctx 的版本，使用 context.Background()。
func (p *OpenAIProvider) HandleStreamResponse(body io.ReadCloser) (<-chan string, <-chan error, error) {
	return p.HandleStreamResponseCtx(context.Background(), body)
}

// HandleStreamResponseCtx 处理流式响应（支持客户端断连中止）。
// 协议转换逻辑在 chat_stream.go 的 chatToClaudeStreamState；本函数只负责
// streamPump 脚手架（事件/错误双通道、断连中止、SSE scanner）与状态机驱动。
func (p *OpenAIProvider) HandleStreamResponseCtx(ctx context.Context, body io.ReadCloser) (<-chan string, <-chan error, error) {
	pump := newStreamPump(ctx)
	eventChan, errChan := pump.eventChan, pump.errChan

	go func() {
		defer close(eventChan)
		defer close(errChan)
		defer utils.BindStreamBody(ctx, body)()

		send := pump.send
		fail := pump.fail

		state := newChatToClaudeStreamState()
		state.hideReasoning = p.hideReasoning
		state.parseEmbeddedReasoning = p.parseEmbeddedReasoning
		state.reasoningScope = p.reasoningScope
		scanner := pump.newScanner(body)

		for scanner.Scan() {
			for _, event := range state.ProcessLine(scanner.Text()) {
				if !send(event) {
					return
				}
			}
			// [DONE] 已完成收尾，立即关闭上游；不能等 EOF 再发一次终止事件。
			if state.messageStopEmitted {
				return
			}
			if state.Failed() || state.doneSeen {
				break
			}
		}
		if state.Failed() {
			// 上游错误事件：经 errChan 交给消费端统一写 error 事件并补协议终止序列，
			// 生产端不再伪造 message_stop。
			fail(state.streamError)
			return
		}
		if !pump.finishRead(state.Completed(), scanner.Err()) {
			return
		}
		for _, event := range state.Finish() {
			if !send(event) {
				return
			}
		}
		if state.Failed() {
			fail(state.streamError)
		}
	}()

	return eventChan, errChan, nil
}

// ToolCallAccumulator 工具调用累加器（Chat / Responses 上游流共用）。
type ToolCallAccumulator struct {
	ID                 string
	Name               string
	Arguments          string
	BlockIndex         int
	Started            bool
	StartEventEmitted  bool
	EmittedArgumentLen int
}

func buildOpenAIMessageDeltaEvent(stopReason string, usage types.Usage, hasUsage bool) string {
	// Cache fields are emitted for both the admin stream collector and the Anthropic client.
	// The client adds cache_read/cache_creation back when sizing the context window. That
	// addition is only correct because
	// normalizeOpenAIUsage has already reduced input_tokens to the uncached remainder --
	// the two must stay in lockstep: re-introducing total-style input_tokens here would
	// restore the double-count. Which client is on the other end is not knowable here, so do
	// not re-introduce a vendor name in this comment.
	usageMap := map[string]interface{}{
		"output_tokens": usage.OutputTokens,
	}
	if hasUsage {
		if usage.InputTokens > 0 {
			usageMap["input_tokens"] = usage.InputTokens
		}
		if usage.CacheCreationInputTokens > 0 {
			usageMap["cache_creation_input_tokens"] = usage.CacheCreationInputTokens
		}
		if usage.CacheReadInputTokens > 0 {
			usageMap["cache_read_input_tokens"] = usage.CacheReadInputTokens
			usageMap["input_tokens_details"] = map[string]interface{}{
				"cached_tokens": usage.CacheReadInputTokens,
			}
		}
		if usage.CacheCreation5mInputTokens > 0 {
			usageMap["cache_creation_5m_input_tokens"] = usage.CacheCreation5mInputTokens
		}
		if usage.CacheCreation1hInputTokens > 0 {
			usageMap["cache_creation_1h_input_tokens"] = usage.CacheCreation1hInputTokens
		}
		if usage.CacheTTL != "" {
			usageMap["cache_ttl"] = usage.CacheTTL
		}
	}
	deltaEvent := map[string]interface{}{
		"type": "message_delta",
		"delta": map[string]interface{}{
			"stop_reason":   stopReason,
			"stop_sequence": nil,
		},
		"usage": usageMap,
	}
	deltaJSON, _ := json.Marshal(deltaEvent)
	return fmt.Sprintf("event: message_delta\ndata: %s\n\n", deltaJSON)
}

func shouldFlushOpenAITextDelta(buffer string, latest string) bool {
	if len(buffer) >= 96 {
		return true
	}
	if strings.Contains(latest, "\n") {
		return true
	}
	trimmed := strings.TrimSpace(buffer)
	if trimmed == "" {
		return false
	}
	for _, r := range []rune(trimmed) {
		switch r {
		case '。', '！', '？', '；', '：', '.', '!', '?', ';', ':':
			return true
		}
	}
	return false
}

// processToolUsePart 处理工具使用部分
func processToolUsePart(id, name string, input interface{}, index int) []string {
	events := []string{}

	// content_block_start
	startEvent := map[string]interface{}{
		"type":  "content_block_start",
		"index": index,
		"content_block": map[string]interface{}{
			"type":  "tool_use",
			"id":    id,
			"name":  name,
			"input": map[string]interface{}{},
		},
	}
	startJSON, _ := json.Marshal(startEvent)
	events = append(events, fmt.Sprintf("event: content_block_start\ndata: %s\n\n", startJSON))

	// content_block_delta
	inputJSON, _ := json.Marshal(input)
	deltaEvent := map[string]interface{}{
		"type":  "content_block_delta",
		"index": index,
		"delta": map[string]string{
			"type":         "input_json_delta",
			"partial_json": string(inputJSON),
		},
	}
	deltaJSON, _ := json.Marshal(deltaEvent)
	events = append(events, fmt.Sprintf("event: content_block_delta\ndata: %s\n\n", deltaJSON))

	// content_block_stop
	stopEvent := map[string]interface{}{
		"type":  "content_block_stop",
		"index": index,
	}
	stopJSON, _ := json.Marshal(stopEvent)
	events = append(events, fmt.Sprintf("event: content_block_stop\ndata: %s\n\n", stopJSON))

	return events
}

// 辅助函数
