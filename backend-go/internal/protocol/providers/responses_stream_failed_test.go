package providers

import (
	"context"
	"io"
	"strings"
	"testing"
)

// 反证切片 4 修复：response.failed 事件必须经 errChan 上报且不伪造 message_stop。
// 旧实现只查顶层 error 字段，response.failed（error 在 response.error 下）会走
// finish() 正常收尾，客户端把失败当成功终止。
func TestResponsesStream_ResponseFailedDoesNotFakeMessageStop(t *testing.T) {
	body := strings.Join([]string{
		`data: {"type":"response.output_text.delta","delta":"partial"}`,
		`data: {"type":"response.failed","response":{"error":{"code":"server_error","message":"boom"}}}`,
		``,
	}, "\n")

	p := &MessagesResponsesProvider{}
	eventChan, errChan, err := p.HandleStreamResponseCtx(context.Background(), io.NopCloser(strings.NewReader(body)))
	if err != nil {
		t.Fatalf("HandleStreamResponseCtx() err = %v", err)
	}
	var events strings.Builder
	for event := range eventChan {
		events.WriteString(event)
	}
	streamErr := <-errChan
	if streamErr == nil {
		t.Fatalf("response.failed 应通过 errChan 上报")
	}
	if _, ok := IsUpstreamStreamFailed(streamErr); !ok {
		t.Fatalf("错误应为 UpstreamStreamFailedError: %v", streamErr)
	}
	got := events.String()
	if strings.Contains(got, "message_stop") {
		t.Fatalf("失败流不应伪造 message_stop; events:\n%s", got)
	}
}

// 容量类 response.failed 标记 Capacity=true，供 proxycore 同候选重试。
func TestResponsesStream_CapacityFailedMarked(t *testing.T) {
	state := newResponsesToClaudeStreamState()
	state.processLine(`data: {"type":"response.failed","response":{"error":{"code":"rate_limit_exceeded","message":"selected model is at capacity, please retry"}}}`)
	if !state.failed || !state.capacityError {
		t.Fatalf("容量错误应标记 failed+capacityError: %+v", state)
	}
	if state.failedMessage != "" {
		t.Fatalf("容量错误不应带 failedMessage（走同候选重试而非上报正文）: %q", state.failedMessage)
	}
}

// 普通 response.failed 不标记容量。
func TestResponsesStream_NonCapacityFailed(t *testing.T) {
	state := newResponsesToClaudeStreamState()
	state.processLine(`data: {"type":"response.failed","response":{"error":{"code":"server_error","message":"boom"}}}`)
	if !state.failed || state.capacityError {
		t.Fatalf("非容量错误应标记 failed 且不标 capacity: %+v", state)
	}
	if !strings.Contains(state.failedMessage, "boom") {
		t.Fatalf("failedMessage 应保存错误信封: %q", state.failedMessage)
	}
}
