package providers

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"insistanan/api-proxy/internal/protocol/types"
)

func TestGeminiFinishReason_ConsistentAcrossResponseModes(t *testing.T) {
	for _, tc := range []struct {
		name   string
		reason string
		tool   bool
		want   string
	}{
		{name: "完整正文", reason: "STOP", want: "end_turn"},
		{name: "完整工具", reason: "STOP", tool: true, want: "tool_use"},
		{name: "正文截断", reason: "MAX_TOKENS", want: "max_tokens"},
		{name: "工具截断", reason: "MAX_TOKENS", tool: true, want: "max_tokens"},
		{name: "拒绝正文", reason: "SAFETY", want: "refusal"},
		{name: "拒绝工具", reason: "SAFETY", tool: true, want: "refusal"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			parts := []any{map[string]any{"text": "partial text"}}
			if tc.tool {
				parts = append(parts, map[string]any{"functionCall": map[string]any{"id": "call-1", "name": "lookup", "args": map[string]any{"q": "test"}}})
			}
			body, err := json.Marshal(map[string]any{
				"candidates": []any{map[string]any{
					"index": 0, "content": map[string]any{"parts": parts, "role": "model"}, "finishReason": tc.reason,
				}},
				"usageMetadata": map[string]any{"promptTokenCount": 1, "candidatesTokenCount": 1},
			})
			require.NoError(t, err)
			provider := &GeminiProvider{}
			response, err := provider.ConvertToClaudeResponse(&types.ProviderResponse{StatusCode: 200, Body: body})
			require.NoError(t, err)
			require.Equal(t, tc.want, response.StopReason)
			stream := collectGeminiClaudeStream(t, "data: "+string(body)+"\n\n")
			require.Contains(t, stream, `"stop_reason":"`+tc.want+`"`)
		})
	}
}

func TestGeminiFinishReason_RejectsAbnormalCompletion(t *testing.T) {
	for _, reason := range []string{"MALFORMED_FUNCTION_CALL", "UNEXPECTED_TOOL_CALL", "OTHER", "UNKNOWN_REASON"} {
		body := `{"candidates":[{"finishReason":"` + reason + `","content":{"parts":[{"text":"partial"}]}}]}`
		provider := &GeminiProvider{}
		_, err := provider.ConvertToClaudeResponse(&types.ProviderResponse{StatusCode: 200, Body: []byte(body)})
		require.ErrorContains(t, err, reason)
		state := newGeminiToClaudeStreamState()
		state.ProcessChunk(body)
		require.ErrorContains(t, state.streamError, reason)
		require.Empty(t, state.Finish(), "错误响应不能发出正常完成事件")
	}
}

func TestGeminiFinishReason_PreservesTruncationWithoutContent(t *testing.T) {
	provider := &GeminiProvider{}
	response, err := provider.ConvertToClaudeResponse(&types.ProviderResponse{
		StatusCode: 200, Body: []byte(`{"candidates":[{"finishReason":"MAX_TOKENS"}]}`),
	})
	require.NoError(t, err)
	require.Equal(t, "max_tokens", response.StopReason)
}
