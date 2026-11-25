package providers

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestProvidersReadWholeSSEEvents(t *testing.T) {
	for _, tc := range []struct {
		name     string
		provider interface {
			HandleStreamResponseCtx(context.Context, io.ReadCloser) (<-chan string, <-chan error, error)
		}
		first, last string
	}{
		{"claude", &ClaudeProvider{}, `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hello!"}}`, `{"type":"message_stop"}`},
		{"chat", &OpenAIProvider{}, `{"choices":[{"delta":{"content":"hello!"}}]}`, `{"choices":[{"finish_reason":"stop"}]}`},
		{"gemini", &GeminiProvider{}, `{"candidates":[{"content":{"parts":[{"text":"hello!"}]}}]}`, `{"candidates":[{"finishReason":"STOP"}]}`},
		{"responses", &MessagesResponsesProvider{}, `{"type":"response.output_text.delta","item_id":"m","output_index":0,"content_index":0,"delta":"hello!"}`, `{"type":"response.completed","response":{"id":"r","status":"completed","output":[]}}`},
	} {
		for _, failed := range []bool{false, true} {
			t.Run(tc.name+map[bool]string{true: "/error", false: "/complete"}[failed], func(t *testing.T) {
				split := func(s string) string { return "data: {\r\ndata: " + s[1:] + "\r\n\r\n" }
				tail := tc.last
				if failed {
					tail = `{"error":{"message":"broken upstream"}}`
				}
				body := ": heartbeat\r\n\r\n" + split(tc.first) + strings.TrimRight(split(tail), "\r\n")
				events, failures, err := tc.provider.HandleStreamResponseCtx(context.Background(), io.NopCloser(strings.NewReader(body)))
				require.NoError(t, err)
				var output strings.Builder
				for event := range events {
					output.WriteString(event)
				}
				var gotErr error
				for err := range failures {
					gotErr = err
				}
				require.Contains(t, output.String(), "hello!")
				if failed {
					require.ErrorContains(t, gotErr, "broken upstream")
					require.NotContains(t, output.String(), `"type":"message_stop"`)
				} else {
					require.NoError(t, gotErr)
					require.Contains(t, output.String(), `"type":"message_stop"`)
				}
			})
		}
	}
}
