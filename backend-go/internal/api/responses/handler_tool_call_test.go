package responses

import "testing"

func TestHasDeliveredResponsesToolCall(t *testing.T) {
	tests := []struct {
		name  string
		event string
		want  bool
	}{
		{
			name:  "custom tool call completed",
			event: "event: response.output_item.done\ndata: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"custom_tool_call\",\"status\":\"completed\"}}\n\n",
			want:  true,
		},
		{
			name:  "function call completed",
			event: "event: response.output_item.done\ndata: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"function_call\",\"status\":\"completed\"}}\n\n",
			want:  true,
		},
		{
			name:  "function call still streaming",
			event: "event: response.function_call_arguments.delta\ndata: {\"type\":\"response.function_call_arguments.delta\",\"delta\":\"{}\"}\n\n",
			want:  false,
		},
		{
			name:  "message item completed",
			event: "event: response.output_item.done\ndata: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"message\",\"status\":\"completed\"}}\n\n",
			want:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := hasDeliveredResponsesToolCall(tt.event); got != tt.want {
				t.Fatalf("hasDeliveredResponsesToolCall() = %v, want %v", got, tt.want)
			}
		})
	}
}
