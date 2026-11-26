package codec

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestCompactContentArray(t *testing.T) {
	body := `{"tools":["Tool1","Tool2"],"messages":[{"role":"user","content":[{"type":"text","text":"` + strings.Repeat("x", 600) + `"},{"type":"tool_use","id":"c","name":"weather","input":{"location":"San Francisco","unit":"celsius"}}]},{"role":"assistant","content":[{"type":"tool_result","tool_use_id":"c","content":"18°C","is_error":false}]}]}`
	got := FormatJSONBytesForLog([]byte(body), 500)
	require.True(t, gjson.Valid(got), got)
	require.Equal(t, strings.Repeat("x", 200)+"...", gjson.Get(got, "messages.0.content.0.text").String())
	require.JSONEq(t, `{"location":"San Francisco","unit":"celsius"}`, gjson.Get(got, "messages.0.content.1.input").Raw)
	require.Equal(t, "18°C", gjson.Get(got, "messages.1.content.0.content").String())
	require.Contains(t, got, `"tools": ["Tool1", "Tool2"]`)
	require.NotContains(t, got, "�")
}

func TestLogFormattingRemovesSensitivePlaceholderID(t *testing.T) {
	placeholder := "MASKED_PHONE_7K4M2P7QAB2CD_PRESERVE_EXACTLY"
	body := []byte(`{"text":"` + placeholder + `"}`)
	for _, formatted := range []string{FormatJSONBytesForLog(body, 500), FormatJSONBytesRaw(body)} {
		require.NotContains(t, formatted, placeholder)
		require.Contains(t, formatted, "MASKED_PHONE_REDACTED")
	}
}
