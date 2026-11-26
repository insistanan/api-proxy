package providers

import (
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"insistanan/api-proxy/internal/infra/config"
	"insistanan/api-proxy/internal/protocol/types"
)

func messagesProviderPayload(t *testing.T, provider Provider, body string, upstream *config.UpstreamConfig) gjson.Result {
	t.Helper()
	c := newGinContext(http.MethodPost, "/v1/messages", []byte(body), nil)
	request, _, err := provider.ConvertToProviderRequest(c, upstream, "sk-test")
	require.NoError(t, err)
	defer request.Body.Close()
	raw, err := io.ReadAll(request.Body)
	require.NoError(t, err)
	require.True(t, gjson.ValidBytes(raw))
	return gjson.ParseBytes(raw)
}

func TestMessagesProvidersPreserveSamplingControls(t *testing.T) {
	body := `{"model":"m","max_tokens":100,"top_p":0.42,"stop_sequences":["END","STOP"],"messages":[{"role":"user","content":"hi"}]}`
	for _, tc := range []struct{ protocol, topP, stop string }{
		{"openai", "top_p", "stop"},
		{"responses", "top_p", "stop"},
		{"gemini", "generationConfig.topP", "generationConfig.stopSequences"},
	} {
		t.Run(tc.protocol, func(t *testing.T) {
			got := messagesProviderPayload(t, GetProvider(tc.protocol), body, &config.UpstreamConfig{BaseURL: "https://api.example.com", ServiceType: tc.protocol})
			require.Equal(t, 0.42, got.Get(tc.topP).Float())
			require.JSONEq(t, `["END","STOP"]`, got.Get(tc.stop).Raw)
		})
	}
}

func TestConvertTools_PreservesSchemaWithoutInventingType(t *testing.T) {
	tools := []types.ClaudeTool{{
		Name:        "noop",
		Description: "does nothing",
		InputSchema: map[string]interface{}{
			"properties": map[string]interface{}{
				"a": map[string]interface{}{"type": "string"},
			},
		},
	}}
	converted := (&OpenAIProvider{}).convertTools(tools)
	if len(converted) != 1 {
		t.Fatalf("转换后工具数 = %d", len(converted))
	}
	params, ok := converted[0].Function.Parameters.(map[string]interface{})
	if !ok {
		t.Fatalf("parameters 应为对象: %T", converted[0].Function.Parameters)
	}
	if _, exists := params["type"]; exists {
		t.Fatalf("不能凭空增加根 type: %v", params)
	}
	if _, has := params["properties"]; !has {
		t.Fatalf("根级应补空 properties: %v", params)
	}
}

func TestConvertToGeminiRequest_MapsThinkingToThinkingConfig(t *testing.T) {
	claudeReq := &types.ClaudeRequest{
		Model:     "gemini-2.5-flash",
		MaxTokens: 20000,
		Thinking: map[string]interface{}{
			"type":          "enabled",
			"budget_tokens": 16000,
		},
		Messages: []types.ClaudeMessage{{Role: "user", Content: "hi"}},
	}
	payload, err := (&GeminiProvider{}).convertToGeminiRequest(claudeReq, nil)
	if err != nil {
		t.Fatalf("convertToGeminiRequest() err = %v", err)
	}
	genConfig := payload["generationConfig"].(map[string]interface{})
	cfg, ok := genConfig["thinkingConfig"].(*types.GeminiThinkingConfig)
	if !ok {
		t.Fatalf("thinkingConfig 类型错误: %T", genConfig["thinkingConfig"])
	}
	if cfg.ThinkingBudget == nil || *cfg.ThinkingBudget <= 0 {
		t.Fatalf("thinkingBudget 应为正数（budget_tokens=16000 → xhigh 档），实际: %v", cfg.ThinkingBudget)
	}
}

func floatPointer(value float64) *float64 { return &value }
