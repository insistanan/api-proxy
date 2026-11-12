package providers

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// 模拟已收到部分 SSE 后，上游连接被 Windows 网络栈报告为强制关闭。
type claudeStreamResetBody struct {
	*strings.Reader
	err    error
	closed bool
}

func (b *claudeStreamResetBody) Read(p []byte) (int, error) {
	n, err := b.Reader.Read(p)
	if err == io.EOF {
		return n, b.err
	}
	return n, err
}

func (b *claudeStreamResetBody) Close() error {
	b.closed = true
	return nil
}

func TestClaudeProviderHandleStreamResponse_TerminalEventBeforeConnectionReset(t *testing.T) {
	resetErr := errors.New("read tcp: wsarecv: An existing connection was forcibly closed by the remote host.")
	prefix := "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"回复正文\"}}\n\n"
	cases := []struct {
		name     string
		tail     string
		complete bool
	}{
		{name: "完整终止事件后重置", tail: "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n", complete: true},
		{name: "只通过data识别终止", tail: "data:{\"type\": \"message_stop\"}\n\n", complete: true},
		{name: "终止事件缺少尾部空行", tail: "data: {\"type\":\"message_stop\"}", complete: true},
		{name: "终止后的额外事件不再转发", tail: "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\ndata: {\"type\":\"error\",\"message\":\"late error\"}\n\n", complete: true},
		{name: "正文中途重置"},
		{name: "工具stop_reason不能代替终止事件", tail: "data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\"}}\n\n"},
		{name: "截断的终止JSON仍需报错", tail: "event: message_stop\ndata: {\"type\":\"message_stop\""},
		{name: "正文提及message_stop不算结束", tail: "data: {\"type\":\"content_block_delta\",\"delta\":{\"text\":\"message_stop\"}}\n\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := &claudeStreamResetBody{Reader: strings.NewReader(prefix + tc.tail), err: resetErr}
			events, failures, err := (&ClaudeProvider{}).HandleStreamResponseCtx(context.Background(), body)
			require.NoError(t, err)
			var output strings.Builder
			for event := range events {
				output.WriteString(event)
			}
			var streamErrors []error
			for failure := range failures {
				streamErrors = append(streamErrors, failure)
			}
			require.True(t, body.closed, "处理结束后必须关闭上游响应体")
			require.Contains(t, output.String(), "回复正文")
			if tc.complete {
				require.Empty(t, streamErrors, "完整回复后的断连不应报错")
				require.NotContains(t, output.String(), "late error")
			} else {
				require.Len(t, streamErrors, 1, "中途断连不能吞掉错误")
				require.ErrorIs(t, streamErrors[0], resetErr)
			}
		})
	}
}
