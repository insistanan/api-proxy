package codec

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSimplifyToolsArray(t *testing.T) {
	for _, tc := range []struct{ name, input, expected string }{
		{"Claude", `{"tools":[{"name":"get_weather","description":"weather"},{"name":"search","description":"search"}]}`, `{"tools":["get_weather","search"]}`},
		{"OpenAI", `{"tools":[{"type":"function","function":{"name":"calculator","description":"math"}}]}`, `{"tools":["calculator"]}`},
		{"保留其他字段", `{"model":"claude-3","messages":["hello"]}`, `{"model":"claude-3","messages":["hello"]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var input any
			require.NoError(t, json.Unmarshal([]byte(tc.input), &input))
			got, err := json.Marshal(SimplifyToolsArray(input))
			require.NoError(t, err)
			require.JSONEq(t, tc.expected, string(got))
		})
	}
}

func TestMaskAPIKey(t *testing.T) {
	for _, tc := range []struct{ input, expected string }{
		{"", ""}, {"abc12", "***"}, {"abc123", "abc***23"},
		{"sk-1234567890abcdef", "sk-12345***bcdef"}, {"key", "***"},
	} {
		require.Equal(t, tc.expected, MaskAPIKey(tc.input))
	}
}
