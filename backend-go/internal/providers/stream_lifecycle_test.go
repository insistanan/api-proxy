package providers

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// 协议完成信号与底层网络错误组合验证，避免依赖某个平台的错误文本。
func TestProviderStreamCompletion(t *testing.T) {
	protocols := []struct {
		name     string
		provider Provider
		prefix   string
		terminal string
	}{
		{"Claude", &ClaudeProvider{}, "data: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"hello\"}}\n\n", `data: {"type":"message_stop"}`},
		{"Chat", &OpenAIProvider{}, "data: {\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hello\"}}]}\n\n", `data: {"choices":[{"index":0,"finish_reason":"stop"}]}`},
		{"Gemini", &GeminiProvider{}, "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"hello\"}]}}]}\n\n", `data: {"candidates":[{"finishReason":"STOP","content":{}}]}`},
		{"Responses", &MessagesResponsesProvider{}, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\n", `data: {"type":"response.completed","response":{"status":"completed","output":[]}}`},
	}
	for _, protocol := range protocols {
		t.Run(protocol.name, func(t *testing.T) {
			for _, readErr := range []error{io.EOF, errors.New("read tcp: connection reset by peer"), errors.New("read tcp: wsarecv: An existing connection was forcibly closed by the remote host.")} {
				for _, complete := range []bool{false, true} {
					t.Run(readErr.Error()+"/"+map[bool]string{false: "截断", true: "完成"}[complete], func(t *testing.T) {
						sse := protocol.prefix
						if complete {
							sse += protocol.terminal
						}
						body := &claudeStreamResetBody{Reader: strings.NewReader(sse), err: readErr}
						events, failures, err := protocol.provider.HandleStreamResponseCtx(context.Background(), body)
						require.NoError(t, err)
						var output strings.Builder
						for event := range events {
							output.WriteString(event)
						}
						var streamErrors []error
						for failure := range failures {
							streamErrors = append(streamErrors, failure)
						}
						require.True(t, body.closed)
						if complete {
							require.Empty(t, streamErrors)
							require.Equal(t, 1, strings.Count(output.String(), "\"type\":\"message_stop\""))
						} else {
							require.Len(t, streamErrors, 1)
							if errors.Is(readErr, io.EOF) {
								require.ErrorIs(t, streamErrors[0], io.ErrUnexpectedEOF)
							} else {
								require.ErrorIs(t, streamErrors[0], readErr)
							}
							require.NotContains(t, output.String(), `"type":"message_stop"`)
						}
					})
				}
			}
		})
	}
}

func TestConvertedProviderInvalidStream(t *testing.T) {
	for _, provider := range []Provider{&OpenAIProvider{}, &GeminiProvider{}, &MessagesResponsesProvider{}} {
		for _, payload := range []string{`{broken`, `null`, `{"error":{"message":"upstream boom"}}`} {
			events, failures, err := provider.HandleStreamResponseCtx(context.Background(), io.NopCloser(strings.NewReader("data: "+payload+"\n\n")))
			require.NoError(t, err)
			var output strings.Builder
			for event := range events {
				output.WriteString(event)
			}
			var streamErrors []error
			for failure := range failures {
				streamErrors = append(streamErrors, failure)
			}
			require.Len(t, streamErrors, 1)
			require.NotContains(t, output.String(), `"type":"message_stop"`)
		}
	}
}

type providerBlockingBody struct {
	started   chan struct{}
	closed    chan struct{}
	startOnce sync.Once
	closeOnce sync.Once
}

func (b *providerBlockingBody) Read([]byte) (int, error) {
	b.startOnce.Do(func() { close(b.started) })
	<-b.closed
	return 0, io.ErrClosedPipe
}

func (b *providerBlockingBody) Close() error {
	b.closeOnce.Do(func() { close(b.closed) })
	return nil
}

func TestProviderCancelClosesBodyAndChannels(t *testing.T) {
	for _, provider := range []Provider{&ClaudeProvider{}, &OpenAIProvider{}, &GeminiProvider{}, &MessagesResponsesProvider{}} {
		ctx, cancel := context.WithCancel(context.Background())
		body := &providerBlockingBody{started: make(chan struct{}), closed: make(chan struct{})}
		events, failures, err := provider.HandleStreamResponseCtx(ctx, body)
		require.NoError(t, err)
		select {
		case <-body.started:
		case <-time.After(time.Second):
			cancel()
			t.Fatal("上游读取未启动")
		}
		cancel()
		select {
		case _, open := <-events:
			require.False(t, open, "取消后必须关闭事件通道")
		case <-time.After(time.Second):
			t.Fatal("取消后事件通道仍未退出")
		}
		select {
		case _, open := <-failures:
			require.False(t, open, "取消不能误记上游故障")
		case <-time.After(time.Second):
			t.Fatal("取消后错误通道仍未退出")
		}
	}
}

func TestConvertedStreamIgnoresContentAfterFinish(t *testing.T) {
	chat := newChatToClaudeStreamState()
	chat.ProcessLine(`data: {"choices":[{"delta":{"content":"hello"},"finish_reason":"stop"}]}`)
	require.Empty(t, chat.ProcessLine(`data: {"choices":[{"delta":{"content":"late"}}],"usage":{"prompt_tokens":10,"completion_tokens":8}}`))
	chatFinal := strings.Join(chat.Finish(), "")
	require.Contains(t, chatFinal, `"output_tokens":8`)
	require.Empty(t, chat.Finish())

	gemini := newGeminiToClaudeStreamState()
	gemini.ProcessChunk(`{"candidates":[{"content":{"parts":[{"text":"hello"}]},"finishReason":"STOP"}]}`)
	require.Empty(t, gemini.ProcessChunk(`{"candidates":[{"content":{"parts":[{"text":"late"}]}}],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":8}}`))
	geminiFinal := strings.Join(gemini.Finish("", ""), "")
	require.Contains(t, geminiFinal, `"output_tokens":8`)
	require.Empty(t, gemini.Finish("", ""))

	responses := newResponsesToClaudeStreamState()
	responsesFinal := responses.processLine(`data: {"type":"response.incomplete","response":{"status":"incomplete","output":[]}}`)
	require.Empty(t, responses.processLine(`data: {"type":"response.output_text.delta","delta":"late"}`))
	responsesFinal = append(responsesFinal, responses.finish()...)
	require.Contains(t, strings.Join(responsesFinal, ""), `"stop_reason":"max_tokens"`)
	require.Empty(t, responses.finish())
}
