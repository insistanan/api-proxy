package providers

import (
	"context"
	"github.com/insistanan/api-proxy/internal/protocol/codec"
	"io"
)

// HandleStreamResponseCtx 处理流式响应（支持客户端断连中止）。
// 协议转换逻辑在 gemini_claude_stream.go 的 geminiToClaudeStreamState；本函数
// 只负责 streamPump 脚手架（事件/错误双通道、断连中止、SSE scanner）与状态机驱动。
func (p *GeminiProvider) HandleStreamResponseCtx(ctx context.Context, body io.ReadCloser) (<-chan string, <-chan error, error) {
	pump := newStreamPump(ctx)
	eventChan, errChan := pump.eventChan, pump.errChan
	shadowProviderID := p.shadowProviderID
	shadowSessionID := p.shadowSessionID

	go func() {
		defer close(eventChan)
		defer close(errChan)
		defer codec.BindStreamBody(ctx, body)()

		send := pump.send
		fail := pump.fail

		state := newGeminiToClaudeStreamState()
		scanner := pump.newScanner(body)

		for scanner.Scan() {
			line, err := scanner.DataLine()
			if err != nil {
				fail(err)
				return
			}
			if payload, isData := codec.ParseSSEDataLine(line); isData && payload == codec.SSEDoneMarker {
				break
			}
			jsonStr, isData := parseGeminiSSEDataLine(line)
			if !isData {
				continue
			}
			for _, event := range state.ProcessChunk(jsonStr) {
				if !send(event) {
					return
				}
			}
			if state.streamError != nil {
				break
			}
		}
		if state.streamError != nil {
			fail(state.streamError)
			return
		}
		if !pump.finishRead(state.pendingStopReason != "", scanner.Err()) {
			return
		}
		for _, event := range state.Finish() {
			if !send(event) {
				return
			}
		}
		if len(state.shadowParts) > 0 {
			p.cacheLease.Store(func() {
				defaultGeminiShadowStore.Record(shadowProviderID, shadowSessionID, GeminiShadowTurn{AssistantContent: map[string]interface{}{"parts": state.shadowParts}, ToolCalls: state.shadowToolCalls})
			})
		}

	}()

	return eventChan, errChan, nil
}
