package providers

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/insistanan/api-proxy/internal/infra/config"
	"github.com/insistanan/api-proxy/internal/protocol/types"
)

// 反证切片 5：stop_sequences / top_p 直传 Chat 上游。
func TestConvertToProviderRequest_PassesTopPAndStop(t *testing.T) {
	body := `{"model":"gpt-4o","max_tokens":100,"top_p":0.42,"stop_sequences":["END","STOP"],"messages":[{"role":"user","content":"hi"}]}`
	c := newGinContext(http.MethodPost, "/v1/messages", []byte(body), nil)
	upstream := &config.UpstreamConfig{BaseURL: "https://api.example.com"}

	req, _, err := (&OpenAIProvider{}).ConvertToProviderRequest(c, upstream, "sk-test")
	if err != nil {
		t.Fatalf("ConvertToProviderRequest() err = %v", err)
	}
	var payload map[string]interface{}
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		t.Fatalf("解码上游请求失败: %v", err)
	}
	if got, ok := payload["top_p"].(float64); !ok || got != 0.42 {
		t.Fatalf("top_p 应为 0.42，实际: %v", payload["top_p"])
	}
	stops, ok := payload["stop"].([]interface{})
	if !ok || len(stops) != 2 || stops[0] != "END" {
		t.Fatalf("stop 应为 [END STOP]，实际: %v", payload["stop"])
	}
}

// 反证切片 5：o-series / gpt-5 目标模型用 max_completion_tokens 而非 max_tokens。
func TestConvertToProviderRequest_UsesMaxCompletionTokensForReasoningModels(t *testing.T) {
	for _, model := range []string{"o3-mini", "gpt-5.2", "codex-o4-mini"} {
		body := `{"model":"claude-x","max_tokens":100,"messages":[{"role":"user","content":"hi"}]}`
		c := newGinContext(http.MethodPost, "/v1/messages", []byte(body), nil)
		upstream := &config.UpstreamConfig{
			BaseURL:      "https://api.example.com",
			ModelMapping: map[string][]string{"claude-x": {model}},
		}

		req, _, err := (&OpenAIProvider{}).ConvertToProviderRequest(c, upstream, "sk-test")
		if err != nil {
			t.Fatalf("ConvertToProviderRequest(%s) err = %v", model, err)
		}
		var payload map[string]interface{}
		if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
			t.Fatalf("解码上游请求失败: %v", err)
		}
		if _, has := payload["max_tokens"]; has {
			t.Fatalf("模型 %s 不应携带 max_tokens: %v", model, payload)
		}
		if got, ok := payload["max_completion_tokens"].(float64); !ok || got != 100 {
			t.Fatalf("模型 %s 应携带 max_completion_tokens=100，实际: %v", model, payload["max_completion_tokens"])
		}
	}
}

// 反证切片 5：工具 schema 缺根 type 时补最小 object schema。
func TestConvertTools_FillsMissingRootObjectType(t *testing.T) {
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
	if params["type"] != "object" {
		t.Fatalf("根级 type 应补为 object，实际: %v", params["type"])
	}
	if _, has := params["properties"]; !has {
		t.Fatalf("根级应补空 properties: %v", params)
	}
}

// 反证切片 5：Messages→Responses 上游直传 top_p / stop。
func TestConvertToProviderRequest_ResponsesUpstreamPassesTopPAndStop(t *testing.T) {
	body := `{"model":"gpt-5.2","max_tokens":100,"top_p":0.3,"stop_sequences":["DONE"],"messages":[{"role":"user","content":"hi"}]}`
	c := newGinContext(http.MethodPost, "/v1/messages", []byte(body), nil)
	upstream := &config.UpstreamConfig{BaseURL: "https://api.openai.com", ServiceType: "responses"}

	_, raw, err := (&MessagesResponsesProvider{}).ConvertToProviderRequest(c, upstream, "sk-test")
	_ = raw
	if err != nil {
		t.Fatalf("ConvertToProviderRequest() err = %v", err)
	}
	// claudeResponsesRequest 经 remember/apply 链后写进 http.Request；这里直接验证
	// 转换函数层。
	claudeReq := &types.ClaudeRequest{
		Model:         "gpt-5.2",
		MaxTokens:     100,
		TopP:          0.3,
		StopSequences: []string{"DONE"},
	}
	req, err := claudeRequestToResponsesRequest(claudeReq, upstream, "conv-1")
	if err != nil {
		t.Fatalf("claudeRequestToResponsesRequest() err = %v", err)
	}
	if req.TopP != 0.3 {
		t.Fatalf("TopP 应为 0.3，实际: %v", req.TopP)
	}
	if len(req.Stop) != 1 || req.Stop[0] != "DONE" {
		t.Fatalf("Stop 应为 [DONE]，实际: %v", req.Stop)
	}
}

// 反证切片 5：Messages→Gemini 上游直传 topP / stopSequences。
func TestConvertToGeminiRequest_PassesTopPAndStop(t *testing.T) {
	claudeReq := &types.ClaudeRequest{
		Model:         "gemini-2.5-pro",
		MaxTokens:     100,
		TopP:          0.7,
		StopSequences: []string{"STOP_1"},
		Messages:      []types.ClaudeMessage{{Role: "user", Content: "hi"}},
	}
	payload, err := (&GeminiProvider{}).convertToGeminiRequest(claudeReq, nil)
	if err != nil {
		t.Fatalf("convertToGeminiRequest() err = %v", err)
	}
	genConfig, ok := payload["generationConfig"].(map[string]interface{})
	if !ok {
		t.Fatalf("缺少 generationConfig: %v", payload)
	}
	if got, ok := genConfig["topP"].(float64); !ok || got != 0.7 {
		t.Fatalf("topP 应为 0.7，实际: %v", genConfig["topP"])
	}
	stops, ok := genConfig["stopSequences"].([]string)
	if !ok || len(stops) != 1 || stops[0] != "STOP_1" {
		t.Fatalf("stopSequences 应为 [STOP_1]，实际: %v", genConfig["stopSequences"])
	}
}

// 反证切片 5：thinking 配置转换为 Gemini thinkingConfig（2.5 预算表）。
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
