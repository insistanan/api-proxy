package responses

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestDeliveredToolCollectorRequiresCompleteParameters(t *testing.T) {
	for _, tc := range []struct {
		name, item string
		want       bool
	}{
		{"自定义工具", `{"type":"custom_tool_call","call_id":"a","name":"exec","input":"command"}`, true},
		{"函数工具", `{"type":"function_call","call_id":"a","name":"exec","arguments":"{}"}`, true},
		{"参数不完整", `{"type":"function_call","call_id":"a","name":"exec","arguments":"{"}`, false},
		{"身份缺失", `{"type":"function_call","status":"completed","arguments":"{}"}`, false},
		{"普通消息", `{"type":"message","status":"completed"}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := newResponsesStreamSessionCollector()
			c.consumeEvent("data: {\"type\":\"response.output_item.done\",\"item\":" + tc.item + "}\n\n")
			require.Equal(t, tc.want, c.output.HasCompletedTool())
		})
	}
}
