package converters

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"insistanan/api-proxy/internal/protocol/converters/togemini"
	"insistanan/api-proxy/internal/protocol/types"
)

// TestClaudeResponseToGemini_WithThoughtSignature 测试 Claude 响应转换时 thought_signature 的处理
func TestClaudeResponseToGemini_WithThoughtSignature(t *testing.T) {
	t.Run("不把 Claude signature 当作 Gemini 签名", func(t *testing.T) {
		// 测试场景 1: Claude 响应包含 signature
		claudeResp := map[string]interface{}{
			"stop_reason": "tool_use",
			"content": []interface{}{
				map[string]interface{}{
					"type":      "tool_use",
					"name":      "test_function",
					"input":     map[string]interface{}{"arg": "value"},
					"signature": "original_signature_from_claude",
				},
			},
		}

		geminiResp, err := togemini.ClaudeResponse(claudeResp)
		assert.NoError(t, err)
		assert.NotNil(t, geminiResp)
		assert.Len(t, geminiResp.Candidates, 1)
		assert.NotNil(t, geminiResp.Candidates[0].Content)
		assert.Len(t, geminiResp.Candidates[0].Content.Parts, 1)
		assert.NotNil(t, geminiResp.Candidates[0].Content.Parts[0].FunctionCall)
		assert.Empty(t, geminiResp.Candidates[0].Content.Parts[0].FunctionCall.ThoughtSignature)
		assert.Equal(t, "test_function", geminiResp.Candidates[0].Content.Parts[0].FunctionCall.Name)
		assert.Equal(t, map[string]interface{}{"arg": "value"}, geminiResp.Candidates[0].Content.Parts[0].FunctionCall.Args)
	})

	t.Run("缺少签名时不生成占位签名", func(t *testing.T) {
		// 测试场景 2: Claude 响应不包含 signature
		claudeResp := map[string]interface{}{
			"stop_reason": "tool_use",
			"content": []interface{}{
				map[string]interface{}{
					"type":  "tool_use",
					"name":  "test_function",
					"input": map[string]interface{}{"arg": "value"},
				},
			},
		}

		geminiResp, err := togemini.ClaudeResponse(claudeResp)
		assert.NoError(t, err)
		assert.NotNil(t, geminiResp)
		assert.Len(t, geminiResp.Candidates, 1)
		assert.NotNil(t, geminiResp.Candidates[0].Content)
		assert.Len(t, geminiResp.Candidates[0].Content.Parts, 1)
		assert.NotNil(t, geminiResp.Candidates[0].Content.Parts[0].FunctionCall)
		assert.Empty(t, geminiResp.Candidates[0].Content.Parts[0].FunctionCall.ThoughtSignature)
	})

	t.Run("空签名不转换为占位签名", func(t *testing.T) {
		// 测试场景 3: Claude 响应包含空 signature
		claudeResp := map[string]interface{}{
			"stop_reason": "tool_use",
			"content": []interface{}{
				map[string]interface{}{
					"type":      "tool_use",
					"name":      "test_function",
					"input":     map[string]interface{}{"arg": "value"},
					"signature": "",
				},
			},
		}

		geminiResp, err := togemini.ClaudeResponse(claudeResp)
		assert.NoError(t, err)
		assert.Empty(t, geminiResp.Candidates[0].Content.Parts[0].FunctionCall.ThoughtSignature)
	})
}

// TestOpenAIResponseToGemini_WithThoughtSignature 测试 OpenAI 响应转换时 thought_signature 的处理
func TestOpenAIResponseToGemini_WithThoughtSignature(t *testing.T) {
	t.Run("Chat 转 Gemini 不生成签名", func(t *testing.T) {
		openaiResp := map[string]interface{}{
			"choices": []interface{}{
				map[string]interface{}{
					"finish_reason": "tool_calls",
					"message": map[string]interface{}{
						"tool_calls": []interface{}{
							map[string]interface{}{
								"function": map[string]interface{}{
									"name":      "test_function",
									"arguments": `{"arg":"value"}`,
								},
							},
						},
					},
				},
			},
		}

		geminiResp, err := togemini.ChatResponse(openaiResp)
		assert.NoError(t, err)
		assert.NotNil(t, geminiResp)
		assert.Len(t, geminiResp.Candidates, 1)
		assert.NotNil(t, geminiResp.Candidates[0].Content)
		assert.Len(t, geminiResp.Candidates[0].Content.Parts, 1)
		assert.NotNil(t, geminiResp.Candidates[0].Content.Parts[0].FunctionCall)
		assert.Empty(t, geminiResp.Candidates[0].Content.Parts[0].FunctionCall.ThoughtSignature)
	})

	t.Run("多个工具调用保持顺序且均不生成签名", func(t *testing.T) {
		openaiResp := map[string]interface{}{
			"choices": []interface{}{
				map[string]interface{}{
					"finish_reason": "tool_calls",
					"message": map[string]interface{}{
						"tool_calls": []interface{}{
							map[string]interface{}{
								"function": map[string]interface{}{
									"name":      "function1",
									"arguments": `{"arg1":"value1"}`,
								},
							},
							map[string]interface{}{
								"function": map[string]interface{}{
									"name":      "function2",
									"arguments": `{"arg2":"value2"}`,
								},
							},
						},
					},
				},
			},
		}

		geminiResp, err := togemini.ChatResponse(openaiResp)
		assert.NoError(t, err)
		assert.Len(t, geminiResp.Candidates[0].Content.Parts, 2)

		// 不生成签名，也不丢失工具名称。
		for _, part := range geminiResp.Candidates[0].Content.Parts {
			assert.NotNil(t, part.FunctionCall)
			assert.Empty(t, part.FunctionCall.ThoughtSignature)
		}
		assert.Equal(t, "function1", geminiResp.Candidates[0].Content.Parts[0].FunctionCall.Name)
		assert.Equal(t, "function2", geminiResp.Candidates[0].Content.Parts[1].FunctionCall.Name)
	})
}

func TestGeminiThinkingHistory_DroppedAcrossProtocols(t *testing.T) {
	input := &types.GeminiRequest{GenerationConfig: &types.GeminiGenerationConfig{MaxOutputTokens: 4096}, Contents: []types.GeminiContent{
		{Role: "model", Parts: []types.GeminiPart{{Thought: true, Text: "private-thought", ThoughtSignature: "gemini-signature"}}},
		{Role: "model", Parts: []types.GeminiPart{
			{Thought: true, Text: "private-thought", ThoughtSignature: "gemini-signature"},
			{Text: "visible-answer"},
			{FunctionCall: &types.GeminiFunctionCall{Name: "tool", Args: map[string]interface{}{"key": "value"}, ThoughtSignature: "gemini-signature"}},
		}},
	}}
	before, err := json.Marshal(input)
	require.NoError(t, err)
	for _, tc := range []struct {
		name    string
		convert func(*types.GeminiRequest, string) (map[string]interface{}, error)
	}{
		{"Claude", GeminiToClaudeRequest},
		{"Chat", GeminiToOpenAIRequest},
		{"Responses", GeminiToResponsesRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			converted, err := tc.convert(input, "model")
			require.NoError(t, err)
			actual, err := json.Marshal(converted)
			require.NoError(t, err)
			require.NotContains(t, string(actual), "private-thought")
			require.NotContains(t, string(actual), "gemini-signature")
			require.Contains(t, string(actual), "visible-answer")
			require.Contains(t, string(actual), `"name":"tool"`)
			if tc.name == "Responses" {
				require.Len(t, converted["input"], 2, "Responses 应输出一条助手消息加一条 function_call")
			} else {
				require.Len(t, converted["messages"], 1, "不能因删除纯思考部分而发送空助手消息")
			}
			after, err := json.Marshal(input)
			require.NoError(t, err)
			require.Equal(t, before, after)
		})
	}
}

func TestGeminiToResponsesRequestAndResponse(t *testing.T) {
	temp := 0.5
	req := &types.GeminiRequest{
		SystemInstruction: &types.GeminiContent{Parts: []types.GeminiPart{{Text: "系统提示"}}},
		Contents: []types.GeminiContent{
			{Role: "user", Parts: []types.GeminiPart{{Text: "查天气"}}},
			{Role: "model", Parts: []types.GeminiPart{{FunctionCall: &types.GeminiFunctionCall{ID: "call_1", Name: "get_weather", Args: map[string]interface{}{"city": "Shanghai"}}}}},
			{Role: "user", Parts: []types.GeminiPart{{FunctionResponse: &types.GeminiFunctionResponse{ID: "call_1", Name: "get_weather", Response: map[string]interface{}{"temp": 22}}}}},
		},
		Tools:            []types.GeminiTool{{FunctionDeclarations: []types.GeminiFunctionDeclaration{{Name: "get_weather", Description: "查询天气"}}}},
		ToolConfig:       &types.GeminiToolConfig{FunctionCallingConfig: &types.GeminiFunctionCallingConfig{Mode: "ANY", AllowedFunctionNames: []string{"get_weather"}}},
		GenerationConfig: &types.GeminiGenerationConfig{MaxOutputTokens: 1024, Temperature: &temp, ThinkingConfig: &types.GeminiThinkingConfig{ThinkingLevel: "high"}},
	}
	converted, err := GeminiToResponsesRequest(req, "gpt-5")
	require.NoError(t, err)
	require.Equal(t, "gpt-5", converted["model"])
	require.Equal(t, "系统提示", converted["instructions"])
	require.Equal(t, 1024, converted["max_output_tokens"])
	require.Equal(t, map[string]interface{}{"effort": "high"}, converted["reasoning"])
	require.Equal(t, map[string]interface{}{"type": "function", "name": "get_weather"}, converted["tool_choice"])
	inputItems, ok := converted["input"].([]interface{})
	require.True(t, ok)
	require.Len(t, inputItems, 3)

	respJSON := []byte(`{
		"status": "completed",
		"output": [
			{"type": "reasoning", "summary": [{"type": "summary_text", "text": "先思考"}]},
			{"type": "message", "role": "assistant", "content": [{"type": "output_text", "text": "上海 22 度"}]},
			{"type": "function_call", "call_id": "call_2", "name": "notify", "arguments": "{\"ok\":true}"}
		],
		"usage": {"input_tokens": 120, "output_tokens": 30, "input_tokens_details": {"cached_tokens": 40}}
	}`)
	geminiResp, err := togemini.ResponsesResponse(respJSON)
	require.NoError(t, err)
	require.Len(t, geminiResp.Candidates, 1)
	require.Equal(t, "STOP", geminiResp.Candidates[0].FinishReason)
	require.Len(t, geminiResp.Candidates[0].Content.Parts, 3)
	require.True(t, geminiResp.Candidates[0].Content.Parts[0].Thought)
	require.Equal(t, "先思考", geminiResp.Candidates[0].Content.Parts[0].Text)
	require.Equal(t, "上海 22 度", geminiResp.Candidates[0].Content.Parts[1].Text)
	require.Equal(t, "notify", geminiResp.Candidates[0].Content.Parts[2].FunctionCall.Name)
	require.Equal(t, map[string]interface{}{"ok": true}, geminiResp.Candidates[0].Content.Parts[2].FunctionCall.Args)
	require.Equal(t, 120, geminiResp.UsageMetadata.PromptTokenCount)
	require.Equal(t, 40, geminiResp.UsageMetadata.CachedContentTokenCount)
	require.Equal(t, 30, geminiResp.UsageMetadata.CandidatesTokenCount)
}
