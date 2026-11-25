package converters

import (
	"encoding/json"
	"testing"

	"github.com/insistanan/api-proxy/internal/protocol/converters/togemini"
	"github.com/insistanan/api-proxy/internal/protocol/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
			require.Len(t, converted["messages"], 1, "不能因删除纯思考部分而发送空助手消息")
			after, err := json.Marshal(input)
			require.NoError(t, err)
			require.Equal(t, before, after)
		})
	}
}
