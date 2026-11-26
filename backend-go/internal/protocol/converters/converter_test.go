package converters

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"insistanan/api-proxy/internal/protocol/types"
)

func TestConvertResponsesPassthroughRequest_ModelMappingPreservesBody(t *testing.T) {
	body := []byte("{\n  \"input\" : [{\"text\":\"\\u003ctag\\u003e\"}],\n  \"model\" : \"gpt-terra\",\n  \"number\": 1.0\n}")
	want := []byte("{\n  \"input\" : [{\"text\":\"\\u003ctag\\u003e\"}],\n  \"model\" : \"gpt-sol\",\n  \"number\": 1.0\n}")

	got, err := convertResponsesPassthroughRequest("gpt-sol", body, nil)
	if err != nil {
		t.Fatalf("模型映射失败: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("模型映射改动了 model 之外的请求字节\nwant: %s\n got: %s", want, got)
	}
}

func TestConvertResponsesPassthroughRequest_NoChangeReturnsOriginalBody(t *testing.T) {
	body := []byte(`{ "model" : "gpt-sol", "input" : "keep formatting" }`)

	got, err := convertResponsesPassthroughRequest("gpt-sol", body, nil)
	if err != nil {
		t.Fatalf("Responses 透传失败: %v", err)
	}
	if !bytes.Equal(got, body) {
		t.Fatalf("未修改请求不应被重新序列化\nwant: %s\n got: %s", body, got)
	}
}

func TestExtractUsageMetrics_ClaudeCacheReadDoesNotDoubleSubtract(t *testing.T) {
	usage := ExtractUsageMetrics(map[string]interface{}{
		"input_tokens":            100,
		"output_tokens":           20,
		"cache_read_input_tokens": 900,
		"input_tokens_details": map[string]interface{}{
			"cached_tokens": 900,
		},
	})

	if usage.InputTokens != 100 {
		t.Fatalf("expected input_tokens to stay 100, got %d", usage.InputTokens)
	}
	if usage.CacheReadInputTokens != 900 {
		t.Fatalf("expected cache_read_input_tokens=900, got %d", usage.CacheReadInputTokens)
	}
}

// Gemini 的 candidatesTokenCount 不含 thoughtsTokenCount（官方等式：
// totalTokenCount = promptTokenCount + candidatesTokenCount + thoughtsTokenCount），
// 而 Responses/Anthropic 两家的 output_tokens 都**含**推理 token。所以出口必须相加，
// 否则 thinking 模型的输出被系统性低报。证据与三家语义对照见
// types.ClaudeOutputTokensDetails。
func TestExtractUsageMetrics_GeminiThoughtsCountedIntoOutput(t *testing.T) {
	usage := ExtractUsageMetrics(map[string]interface{}{
		"promptTokenCount":     27,
		"candidatesTokenCount": 45,
		"thoughtsTokenCount":   31,
		"totalTokenCount":      103,
	})

	if usage.OutputTokens != 76 {
		t.Fatalf("output_tokens 应为 45+31=76，实际 %d", usage.OutputTokens)
	}
	if usage.OutputTokensDetails == nil || usage.OutputTokensDetails.ReasoningTokens != 31 {
		t.Fatalf("reasoning_tokens 应单列为 31，实际 %+v", usage.OutputTokensDetails)
	}
	if usage.InputTokens != 27 {
		t.Fatalf("input_tokens 应为 27，实际 %d", usage.InputTokens)
	}
	if usage.TotalTokens != 103 {
		t.Fatalf("total_tokens 应为 27+76=103，实际 %d", usage.TotalTokens)
	}
}

// 缓存与推理同时出现：input 侧减缓存、output 侧加推理，两个方向互不干扰。
func TestExtractUsageMetrics_GeminiCachedAndThoughtsTogether(t *testing.T) {
	usage := ExtractUsageMetrics(map[string]interface{}{
		"promptTokenCount":        1000,
		"cachedContentTokenCount": 900,
		"candidatesTokenCount":    20,
		"thoughtsTokenCount":      7,
	})

	if usage.InputTokens != 100 {
		t.Fatalf("input_tokens 应为 1000-900=100，实际 %d", usage.InputTokens)
	}
	if usage.OutputTokens != 27 {
		t.Fatalf("output_tokens 应为 20+7=27，实际 %d", usage.OutputTokens)
	}
	if usage.CacheReadInputTokens != 900 {
		t.Fatalf("cache_read_input_tokens 应为 900，实际 %d", usage.CacheReadInputTokens)
	}
}

func TestOpenAIChatResponseToResponses_WithReasoningAndToolCalls(t *testing.T) {
	resp := map[string]interface{}{
		"model": "gpt-4o",
		"choices": []interface{}{
			map[string]interface{}{
				"finish_reason": "tool_calls",
				"message": map[string]interface{}{
					"role":              "assistant",
					"reasoning_content": "I should call a tool.",
					"content":           "Checking now.",
					"tool_calls": []interface{}{
						map[string]interface{}{
							"id":   "call_lookup",
							"type": "function",
							"function": map[string]interface{}{
								"name":      "lookup",
								"arguments": "{\"q\":\"test\"}",
							},
						},
					},
				},
			},
		},
	}

	result, err := convertChatResponseFixture(resp, "sess_test")
	if err != nil {
		t.Fatalf("转换失败: %v", err)
	}

	if result.Object != "response" {
		t.Errorf("object 不匹配")
	}
	if result.Status != "completed" {
		t.Errorf("status 不匹配")
	}
	if len(result.Output) != 3 {
		t.Fatalf("期望 3 个 output item，实际为 %d", len(result.Output))
	}
	if result.Output[0].Type != "reasoning" {
		t.Errorf("第一个 item 应为 reasoning")
	}
	if result.Output[1].Type != "message" || result.Output[1].Role != "assistant" {
		t.Errorf("第二个 item 应为 assistant message")
	}
	if result.Output[2].Type != "function_call" || result.Output[2].CallID != "call_lookup" || result.Output[2].Name != "lookup" {
		t.Errorf("第三个 item 应为 function_call")
	}
}

func TestResponsesToChatDispatcher_SystemAndCustomHistory(t *testing.T) {
	sess := &types.Session{Messages: []types.ResponsesItem{
		{Type: "message", Role: "system", Content: "历史系统提示"},
		{Type: "custom_tool_call", CallID: "call_exec", Name: "code_exec", Content: "print('history')"},
		{Type: "custom_tool_call_output", CallID: "call_exec", Content: "done"},
	}}
	body := []byte(`{"model":"m","instructions":"顶层指令","input":"下一轮"}`)
	var request types.ResponsesRequest
	require.NoError(t, json.Unmarshal(body, &request))
	converted, err := ConvertResponsesRequestToUpstream("openai", "m", body, false, sess, &request, nil)
	require.NoError(t, err)
	messages := gjson.GetBytes(converted, "messages")
	require.Len(t, messages.Array(), 5)
	require.Contains(t, messages.Get("0.content").String(), "顶层指令")
	require.Contains(t, messages.Get("1.content").String(), "历史系统提示")
	require.JSONEq(t, `{"input":"print('history')"}`, messages.Get("2.tool_calls.0.function.arguments").String())
	require.Equal(t, "done", messages.Get("3.content").String())
}

func TestCustomToolsMapToTargetFunctionSchema(t *testing.T) {
	body := `{"model":"m","max_output_tokens":2048,"input":"Run code","tools":[{"type":"custom","name":"code_exec","description":"Execute code"}],"tool_choice":{"type":"custom","name":"code_exec"}}`
	for _, tc := range []struct{ target, toolPath, choicePath, choice string }{
		{"claude", "tools.0", "tool_choice", `{"type":"tool","name":"code_exec"}`},
		{"gemini", "tools.0.functionDeclarations.0", "toolConfig.functionCallingConfig", `{"mode":"ANY","allowedFunctionNames":["code_exec"]}`},
	} {
		t.Run(tc.target, func(t *testing.T) {
			got, err := dispatchRequest(t, tc.target, body, nil)
			require.NoError(t, err)
			require.Equal(t, "code_exec", gjson.GetBytes(got, tc.toolPath+".name").String())
			require.Contains(t, gjson.GetBytes(got, tc.toolPath+".description").String(), "Execute code")
			require.JSONEq(t, tc.choice, gjson.GetBytes(got, tc.choicePath).Raw)
		})
	}
}

func TestResponsesToGemini_PrunesToolConfigWithoutTools(t *testing.T) {
	got, err := dispatchRequest(t, "gemini", `{"model":"m","input":"Hi","tool_choice":"auto"}`, nil)
	require.NoError(t, err)
	require.False(t, gjson.GetBytes(got, "toolConfig").Exists())
}

func TestResponsesToChatDispatcher_CustomToolOutputReplay(t *testing.T) {
	converted, err := dispatchRequest(t, "openai", `{"model":"m","input":[{"type":"custom_tool_call_output","call_id":"call_exec","output":"done"}]}`, nil)
	require.NoError(t, err)
	require.Equal(t, "tool", gjson.GetBytes(converted, "messages.0.role").String())
	require.Equal(t, "done", gjson.GetBytes(converted, "messages.0.content").String())
}

func TestResponsesToChatDispatcher_WithSessionHistory(t *testing.T) {
	sess := &types.Session{Messages: []types.ResponsesItem{
		{Type: "message", Role: "user", Content: "Previous user message"},
		{Type: "message", Role: "assistant", Content: "Previous assistant message"},
	}}
	body := []byte(`{"model":"m","input":"New user message"}`)
	var request types.ResponsesRequest
	require.NoError(t, json.Unmarshal(body, &request))
	got, err := ConvertResponsesRequestToUpstream("openai", "m", body, false, sess, &request, nil)
	require.NoError(t, err)
	require.JSONEq(t, `[{"role":"user","content":"Previous user message"},{"role":"assistant","content":"Previous assistant message"},{"role":"user","content":"New user message"}]`, gjson.GetBytes(got, "messages").Raw)
}

func TestResponsesToChatDispatcher_ForwardsPromptCacheKey(t *testing.T) {
	got, err := dispatchRequest(t, "openai", `{"model":"m","input":"Hello!","prompt_cache_key":"cache-key","prompt_cache_retention":"7d"}`, nil)
	require.NoError(t, err)
	require.Equal(t, "cache-key", gjson.GetBytes(got, "prompt_cache_key").String())
	require.Equal(t, "7d", gjson.GetBytes(got, "prompt_cache_retention").String())
}

func TestResponsesPassthroughPreservesImageGenerationFields(t *testing.T) {
	body := []byte(`{"model":"gpt-image-1","stream":true,"tools":[{"type":"image_generation","n":3,"quality":"high","size":"1536x1024","background":"transparent","output_format":"png","output_compression":80,"moderation":"low","partial_images":2,"input_fidelity":"high","input_image_mask":{"image_url":"data:image/png;base64,mask"},"action":"edit"}],"tool_choice":{"type":"image_generation"},"metadata":{"trace":"keep"}}`)
	got, err := convertResponsesPassthroughRequest("gpt-image-1", body, nil)
	if err != nil {
		t.Fatalf("透传生图请求失败: %v", err)
	}
	var payload map[string]interface{}
	if err := json.Unmarshal(got, &payload); err != nil {
		t.Fatalf("透传结果不是有效 JSON: %v", err)
	}
	tools, ok := payload["tools"].([]interface{})
	if !ok || len(tools) != 1 {
		t.Fatalf("生图工具字段丢失: %#v", payload["tools"])
	}
	tool, ok := tools[0].(map[string]interface{})
	if !ok {
		t.Fatalf("生图工具不是对象: %#v", tools[0])
	}
	for key, want := range map[string]interface{}{
		"n": float64(3), "quality": "high", "size": "1536x1024", "background": "transparent",
		"output_format": "png", "output_compression": float64(80), "moderation": "low",
		"partial_images": float64(2), "input_fidelity": "high", "action": "edit",
	} {
		if tool[key] != want {
			t.Errorf("工具字段 %s 被改写或丢失: got %#v, want %#v", key, tool[key], want)
		}
	}
	if _, ok := tool["input_image_mask"].(map[string]interface{}); !ok {
		t.Errorf("input_image_mask 被丢失: %#v", tool["input_image_mask"])
	}
	if _, ok := payload["tool_choice"].(map[string]interface{}); !ok {
		t.Errorf("tool_choice 被丢失: %#v", payload["tool_choice"])
	}
}
