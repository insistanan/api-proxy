package converters

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/insistanan/api-proxy/internal/protocol/types"
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

// ============== extractTextFromContent 测试 ==============

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

// ============== OpenAI 转换器测试 ==============

func TestOpenAIChatConverter_WithMessageType(t *testing.T) {
	converter := &OpenAIChatConverter{}
	sess := &types.Session{
		ID:       "sess_test",
		Messages: []types.ResponsesItem{},
	}

	req := &types.ResponsesRequest{
		Model: "gpt-4",
		Input: []interface{}{
			map[string]interface{}{
				"type": "message",
				"role": "user",
				"content": []interface{}{
					map[string]interface{}{
						"type": "input_text",
						"text": "Hello from message type!",
					},
				},
			},
		},
	}

	result, err := converter.ToProviderRequest(sess, req)
	if err != nil {
		t.Fatalf("转换失败: %v", err)
	}

	resultMap := result.(map[string]interface{})
	messages := resultMap["messages"].([]map[string]interface{})

	if len(messages) != 1 {
		t.Fatalf("期望 1 条消息，实际为 %d", len(messages))
	}

	if messages[0]["role"] != "user" {
		t.Errorf("角色应该是 user")
	}
	if messages[0]["content"] != "Hello from message type!" {
		t.Errorf("内容不匹配，实际为 '%v'", messages[0]["content"])
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

	result, err := OpenAIChatResponseToResponses(resp, "sess_test")
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

func TestResponsesToOpenAIChatMessages_CollapseSystemAndCustomToolReplay(t *testing.T) {
	sess := &types.Session{
		ID: "sess_test",
		Messages: []types.ResponsesItem{
			{Type: "message", Role: "system", Content: "历史系统提示"},
			{Type: "custom_tool_call", CallID: "call_exec", Name: "code_exec", Content: "print('history')"},
			{Type: "custom_tool_call_output", CallID: "call_exec", Content: "done"},
		},
	}

	messages, err := ResponsesToOpenAIChatMessages(sess, []interface{}{
		map[string]interface{}{"type": "message", "role": "system", "content": "本次系统提示"},
	}, "顶层指令")
	if err != nil {
		t.Fatalf("转换失败: %v", err)
	}

	if len(messages) != 3 {
		t.Fatalf("期望 3 条消息，实际为 %d", len(messages))
	}
	if messages[0]["role"] != "system" {
		t.Fatalf("第一条应为 system，实际为 %v", messages[0]["role"])
	}
	systemContent, _ := messages[0]["content"].(string)
	if systemContent != "顶层指令\n\n历史系统提示\n\n本次系统提示" {
		t.Fatalf("system 折叠结果不匹配，实际为 %q", systemContent)
	}

	toolCalls, ok := messages[1]["tool_calls"].([]map[string]interface{})
	if !ok || len(toolCalls) != 1 {
		t.Fatalf("第二条消息应包含 1 个 tool_call")
	}
	args, _ := toolCalls[0]["function"].(map[string]interface{})["arguments"].(string)
	if args != "{\"input\":\"print('history')\"}" {
		t.Fatalf("custom_tool_call 回放参数不匹配，实际为 %s", args)
	}

	if messages[2]["role"] != "tool" {
		t.Fatalf("第三条消息应为 tool，实际为 %v", messages[2]["role"])
	}
}

func TestClaudeConverter_InvalidFunctionCallArguments(t *testing.T) {
	converter := &ClaudeConverter{}
	req := &types.ResponsesRequest{
		Model:           "claude-3-5-sonnet",
		MaxOutputTokens: 2048,
		Input: []interface{}{
			map[string]interface{}{
				"type":      "function_call",
				"call_id":   "call_bad",
				"name":      "lookup",
				"arguments": "{bad-json",
			},
		},
	}

	if _, err := converter.ToProviderRequest(nil, req); err == nil {
		t.Fatal("非法 function_call arguments 应返回错误")
	}
}

func TestResponsesToGemini_InvalidFunctionCallArguments(t *testing.T) {
	req := &types.ResponsesRequest{
		Model: "gemini-2.5-flash",
		Input: []interface{}{
			map[string]interface{}{
				"type":      "function_call",
				"call_id":   "call_bad",
				"name":      "lookup",
				"arguments": "{bad-json",
			},
		},
	}

	if _, err := ConvertResponsesToGeminiRequest("gemini-2.5-flash", nil, req); err == nil {
		t.Fatal("非法 function_call arguments 应返回错误")
	}
}

func TestResponsesToGemini_CustomToolCompatible(t *testing.T) {
	req := &types.ResponsesRequest{
		Model: "gemini-2.5-flash",
		Input: "Run code",
		Tools: []interface{}{
			map[string]interface{}{
				"type":        "custom",
				"name":        "code_exec",
				"description": "Execute code",
			},
		},
		ToolChoice: map[string]interface{}{
			"type": "custom",
			"name": "code_exec",
		},
	}

	body, err := ConvertResponsesToGeminiRequest("gemini-2.5-flash", nil, req)
	if err != nil {
		t.Fatalf("custom tool 对 Gemini 应兼容降级，实际报错: %v", err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("解析 Gemini 请求失败: %v", err)
	}

	tools, ok := result["tools"].([]interface{})
	if !ok || len(tools) == 0 {
		t.Fatal("Gemini 请求应包含 tools")
	}
	decls, ok := tools[0].(map[string]interface{})["functionDeclarations"].([]interface{})
	if !ok || len(decls) == 0 {
		t.Fatal("Gemini 请求应包含 functionDeclarations")
	}
	if decls[0].(map[string]interface{})["name"] != "code_exec" {
		t.Fatalf("tool 名称不匹配，实际为 %#v", decls[0].(map[string]interface{})["name"])
	}
	toolConfig, ok := result["toolConfig"].(map[string]interface{})
	if !ok {
		t.Fatal("Gemini 请求应包含 toolConfig")
	}
	cfg, ok := toolConfig["functionCallingConfig"].(map[string]interface{})
	if !ok || cfg["mode"] != "ANY" {
		t.Fatalf("tool choice 应降级为 ANY，实际为 %#v", toolConfig)
	}
}

func TestResponsesToGemini_PrunesToolConfigWithoutTools(t *testing.T) {
	req := &types.ResponsesRequest{
		Model:      "gemini-2.5-flash",
		Input:      "Hi",
		ToolChoice: "auto",
	}

	body, err := ConvertResponsesToGeminiRequest("gemini-2.5-flash", nil, req)
	if err != nil {
		t.Fatalf("转换失败: %v", err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("解析 Gemini 请求失败: %v", err)
	}
	if _, ok := result["toolConfig"]; ok {
		t.Fatal("没有 tools 时不应保留 toolConfig")
	}
}

func TestClaudeResponseToResponses_StopReasonMaxTokens(t *testing.T) {
	resp := map[string]interface{}{
		"model":       "claude-3-5-sonnet",
		"stop_reason": "max_tokens",
		"content": []interface{}{
			map[string]interface{}{
				"type": "text",
				"text": "partial",
			},
		},
	}

	result, err := ClaudeResponseToResponses(resp, "sess_test")
	if err != nil {
		t.Fatalf("转换失败: %v", err)
	}
	if result.Object != "response" {
		t.Errorf("object 不匹配")
	}
	if result.Status != "incomplete" {
		t.Errorf("max_tokens 应映射为 incomplete")
	}
}

func TestClaudeConverter_CustomToolCompatible(t *testing.T) {
	converter := &ClaudeConverter{}
	req := &types.ResponsesRequest{
		Model:           "claude-3-5-sonnet",
		MaxOutputTokens: 2048,
		Input:           "Run code",
		Tools: []interface{}{
			map[string]interface{}{
				"type":        "custom",
				"name":        "code_exec",
				"description": "Execute code",
			},
		},
		ToolChoice: map[string]interface{}{
			"type": "custom",
			"name": "code_exec",
		},
	}

	result, err := converter.ToProviderRequest(nil, req)
	if err != nil {
		t.Fatalf("custom tool 对 Claude 应兼容降级，实际报错: %v", err)
	}

	resultMap, ok := result.(map[string]interface{})
	if !ok {
		t.Fatal("结果不是 map[string]interface{}")
	}
	tools, ok := resultMap["tools"].([]types.ClaudeTool)
	if !ok || len(tools) != 1 {
		t.Fatalf("Claude 请求 tools 不匹配: %#v", resultMap["tools"])
	}
	if tools[0].Name != "code_exec" {
		t.Fatalf("Claude tool 名称不匹配，实际为 %s", tools[0].Name)
	}
	toolChoice, ok := resultMap["tool_choice"].(map[string]interface{})
	if !ok || toolChoice["type"] != "tool" || toolChoice["name"] != "code_exec" {
		t.Fatalf("Claude tool_choice 不匹配: %#v", resultMap["tool_choice"])
	}
}

func TestResponsesToOpenAIChatMessages_CustomToolOutputReplay(t *testing.T) {
	messages, err := ResponsesToOpenAIChatMessages(nil, []interface{}{
		map[string]interface{}{
			"type":    "custom_tool_call_output",
			"call_id": "call_exec",
			"output":  "done",
		},
	}, "")
	if err != nil {
		t.Fatalf("转换失败: %v", err)
	}
	if len(messages) != 1 {
		t.Fatalf("期望 1 条消息，实际为 %d", len(messages))
	}
	if messages[0]["role"] != "tool" {
		t.Fatalf("应转换为 tool 消息，实际为 %v", messages[0]["role"])
	}
	if messages[0]["content"] != "done" {
		t.Fatalf("tool 输出内容不匹配，实际为 %#v", messages[0]["content"])
	}
}

// ============== Claude 转换器测试 ==============

// ============== 工厂模式测试 ==============

// ============== 会话历史测试 ==============

func TestOpenAIChatConverter_WithSessionHistory(t *testing.T) {
	converter := &OpenAIChatConverter{}
	sess := &types.Session{
		ID: "sess_test",
		Messages: []types.ResponsesItem{
			{
				Type:    "message",
				Role:    "user",
				Content: "Previous user message",
			},
			{
				Type:    "message",
				Role:    "assistant",
				Content: "Previous assistant message",
			},
		},
	}

	req := &types.ResponsesRequest{
		Model: "gpt-4",
		Input: "New user message",
	}

	result, err := converter.ToProviderRequest(sess, req)
	if err != nil {
		t.Fatalf("转换失败: %v", err)
	}

	resultMap := result.(map[string]interface{})
	messages := resultMap["messages"].([]map[string]interface{})

	// 应该有 3 条消息：2 条历史 + 1 条新消息
	if len(messages) != 3 {
		t.Fatalf("期望 3 条消息，实际为 %d", len(messages))
	}

	// 检查顺序
	if messages[0]["content"] != "Previous user message" {
		t.Errorf("第一条消息内容不匹配")
	}
	if messages[1]["content"] != "Previous assistant message" {
		t.Errorf("第二条消息内容不匹配")
	}
	if messages[2]["content"] != "New user message" {
		t.Errorf("第三条消息内容不匹配")
	}
}

// ============== FinishReason 映射测试 ==============

func TestOpenAIChatConverter_ForwardsPromptCacheKey(t *testing.T) {
	converter := &OpenAIChatConverter{}
	sess := &types.Session{
		ID:       "sess_cache",
		Messages: []types.ResponsesItem{},
	}

	req := &types.ResponsesRequest{
		Model:                "gpt-4",
		Input:                "Hello!",
		PromptCacheKey:       "my-cache-key-123",
		PromptCacheRetention: "7d",
	}

	result, err := converter.ToProviderRequest(sess, req)
	if err != nil {
		t.Fatalf("转换失败: %v", err)
	}

	resultMap, ok := result.(map[string]interface{})
	if !ok {
		t.Fatal("结果不是 map[string]interface{}")
	}

	if resultMap["prompt_cache_key"] != "my-cache-key-123" {
		t.Errorf("prompt_cache_key 应转发，期望 my-cache-key-123，实际: %v", resultMap["prompt_cache_key"])
	}
	if resultMap["prompt_cache_retention"] != "7d" {
		t.Errorf("prompt_cache_retention 应转发，期望 7d，实际: %v", resultMap["prompt_cache_retention"])
	}
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
