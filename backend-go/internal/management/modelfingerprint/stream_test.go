package modelfingerprint

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStreamRequiresCompletionAndExcludesThinking(t *testing.T) {
	for _, test := range []struct {
		name, protocol, stream, text string
		wantError                    bool
	}{
		{"Chat完整", "openai", "data:{\"model\":\"real\",\"choices\":[{\"delta\":{\"reasoning_content\":\"999\",\"content\":\"1,2,3\"},\"finish_reason\":\"stop\"}]}\n\ndata:[DONE]\n\n", "1,2,3", false},
		{"Chat断流", "openai", "data:{\"choices\":[{\"delta\":{\"content\":\"1,2,3\"}}]}\n\n", "1,2,3", true},
		{"错误事件", "openai", "data:{\"error\":{\"message\":\"上游失败\"}}\n\n", "", true},
		{"非法JSON", "openai", "data:broken\n\n", "", true},
		{"Gemini思考隔离", "gemini", "data:{\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"999\",\"thought\":true},{\"text\":\"1,2,3\"}]},\"finishReason\":\"STOP\"}]}\n\n", "1,2,3", false},
		{"Messages完整", "claude", "data:{\"type\":\"content_block_delta\",\"delta\":{\"type\":\"thinking_delta\",\"thinking\":\"999\"}}\n\ndata:{\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"1,2,3\"}}\n\ndata:{\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"}}\n\ndata:{\"type\":\"message_stop\"}\n\n", "1,2,3", false},
		{"Responses失败", "responses", "data:{\"type\":\"response.failed\",\"response\":{\"error\":{\"message\":\"失败\"}}}\n\n", "", true},
		{"Responses完整", "responses", "data:{\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output\":[{\"type\":\"message\",\"content\":[{\"type\":\"output_text\",\"text\":\"1,2,3\"}]}]}}\n\n", "1,2,3", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			text, _, _, err := readStream(context.Background(), strings.NewReader(test.stream), test.protocol)
			if test.wantError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, test.text, text)
		})
	}
}
