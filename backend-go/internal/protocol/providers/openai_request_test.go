package providers

import (
	"testing"

	"github.com/stretchr/testify/require"
	"insistanan/api-proxy/internal/infra/config"
)

func TestOpenAIProviderUsesSingleTokenField(t *testing.T) {
	for _, model := range []string{"gpt-4o", "kimi-2.6", "o3-mini", "gpt-5.2", "codex-o4-mini"} {
		t.Run(model, func(t *testing.T) {
			upstream := &config.UpstreamConfig{BaseURL: "https://api.example.com", ServiceType: "openai", ModelMapping: map[string][]string{"claude-x": {model}}}
			got := messagesProviderPayload(t, &OpenAIProvider{}, `{"model":"claude-x","max_tokens":64000,"messages":[]}`, upstream)
			tokenField, absent := "max_completion_tokens", "max_tokens"
			if model == "gpt-4o" {
				tokenField, absent = absent, tokenField
			}
			require.Equal(t, model, got.Get("model").String())
			require.Equal(t, int64(64000), got.Get(tokenField).Int())
			require.False(t, got.Get(absent).Exists())
		})
	}
}

func TestOpenAIProviderPreservesReasoningAndControls(t *testing.T) {
	for _, tc := range []struct {
		name, fields string
		want         map[string]string
		absent       string
	}{
		{"推理重放", `"messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"need inspect files"},{"type":"text","text":"I will inspect the files."}]}]`,
			map[string]string{"messages.#": "1", "messages.0.reasoning_content": `"need inspect files"`, "messages.0.content": `"I will inspect the files."`}, ""},
		{"流式用量", `"stream":true,"messages":[]`, map[string]string{"stream_options.include_usage": "true"}, ""},
		{"禁用已声明工具", `"tool_choice":"none","tools":[{"name":"lookup","input_schema":{"type":"object","properties":{}}}],"messages":[]`, map[string]string{"tool_choice": `"none"`}, ""},
		{"未声明工具", `"tool_choice":"none","messages":[]`, nil, "tool_choice"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := messagesProviderPayload(t, &OpenAIProvider{}, `{"model":"gpt-4o",`+tc.fields+`}`, &config.UpstreamConfig{BaseURL: "https://api.example.com", ServiceType: "openai"})
			for path, want := range tc.want {
				require.JSONEq(t, want, got.Get(path).Raw, path)
			}
			if tc.absent != "" {
				require.False(t, got.Get(tc.absent).Exists())
			}
		})
	}
}
