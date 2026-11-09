package utils

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/insistanan/api-proxy/internal/types"
)

func TestEstimateTokens(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		expected int
	}{
		{"empty", "", 0},
		{"english", "Hello world", 3}, // ~11 chars / 3.5 = ~3
		{"chinese", "你好世界", 2},        // 4 chars / 1.5 = ~2.7 -> 3
		{"mixed", "Hello 你好", 3},      // 5 other + 2 cjk = ~1.4 + ~1.3 = ~3
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := EstimateTokens(tt.text)
			// 允许 ±2 的误差
			if result < tt.expected-2 || result > tt.expected+2 {
				t.Errorf("EstimateTokens(%q) = %d, want ~%d", tt.text, result, tt.expected)
			}
		})
	}
}

func TestEstimateResponsesRequestTokens(t *testing.T) {
	tests := []struct {
		name        string
		request     map[string]interface{}
		minExpected int
	}{
		{
			name: "simple_string_input",
			request: map[string]interface{}{
				"model": "gpt-4",
				"input": "Hello, how are you?",
			},
			minExpected: 5,
		},
		{
			name: "with_instructions",
			request: map[string]interface{}{
				"model":        "gpt-4",
				"instructions": "You are a helpful assistant.",
				"input":        "Hello",
			},
			minExpected: 8,
		},
		{
			name: "with_array_input",
			request: map[string]interface{}{
				"model": "gpt-4",
				"input": []interface{}{
					map[string]interface{}{
						"type":    "message",
						"role":    "user",
						"content": "Hello, how are you today?",
					},
				},
			},
			minExpected: 6,
		},
		{
			name: "with_tools",
			request: map[string]interface{}{
				"model": "gpt-4",
				"input": "Use the tool",
				"tools": []interface{}{
					map[string]interface{}{"name": "search"},
					map[string]interface{}{"name": "compute"},
				},
			},
			minExpected: 300, // 2 tools * 150 = 300
		},
		{
			name: "with_custom_tool_call_output",
			request: map[string]interface{}{
				"model": "gpt-5.6-luna",
				"input": []interface{}{
					map[string]interface{}{
						"type":   "custom_tool_call_output",
						"output": strings.Repeat("tool result ", 3000),
					},
				},
			},
			minExpected: 8000,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bodyBytes, _ := json.Marshal(tt.request)
			result := EstimateResponsesRequestTokens(bodyBytes)
			if result < tt.minExpected {
				t.Errorf("EstimateResponsesRequestTokens() = %d, want >= %d", result, tt.minExpected)
			}
		})
	}
}

func TestEstimateResponsesOutputTokens(t *testing.T) {
	tests := []struct {
		name        string
		output      interface{}
		minExpected int
	}{
		{
			name:        "nil_output",
			output:      nil,
			minExpected: 0,
		},
		{
			name: "message_with_text",
			output: []interface{}{
				map[string]interface{}{
					"type": "message",
					"content": []interface{}{
						map[string]interface{}{
							"type": "output_text",
							"text": "Hello, I am doing well!",
						},
					},
				},
			},
			minExpected: 5,
		},
		{
			name: "function_call",
			output: []interface{}{
				map[string]interface{}{
					"type":      "function_call",
					"name":      "search",
					"arguments": `{"query": "weather"}`,
				},
			},
			minExpected: 5,
		},
		{
			name: "reasoning_with_summary",
			output: []interface{}{
				map[string]interface{}{
					"type": "reasoning",
					"summary": []interface{}{
						map[string]interface{}{
							"type": "summary_text",
							"text": "This is my reasoning process",
						},
					},
				},
			},
			minExpected: 5,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := EstimateResponsesOutputTokens(tt.output)
			if result < tt.minExpected {
				t.Errorf("EstimateResponsesOutputTokens() = %d, want >= %d", result, tt.minExpected)
			}
		})
	}
}

func TestEstimateResponsesOutputTokensWithTypedItems(t *testing.T) {
	// 测试 []types.ResponsesItem 类型的直接处理
	items := []types.ResponsesItem{
		{
			Type:    "message",
			Role:    "assistant",
			Content: "Hello, I am doing well!",
		},
		{
			Type: "text",
			Content: []types.ContentBlock{
				{Type: "output_text", Text: "This is output text"},
			},
		},
		{
			Type:      "function_call",
			Name:      "search",
			Arguments: `{"query":"weather"}`,
		},
		{
			Type: "reasoning",
			Summary: []interface{}{
				map[string]interface{}{"type": "summary_text", "text": "Reasoning summary"},
			},
		},
	}

	result := EstimateResponsesOutputTokens(items)
	if result < 10 {
		t.Errorf("EstimateResponsesOutputTokens([]types.ResponsesItem) = %d, want >= 10", result)
	}
}

func TestEstimateRequestTokens(t *testing.T) {
	tests := []struct {
		name        string
		request     map[string]interface{}
		minExpected int
	}{
		{
			name: "messages_api_request",
			request: map[string]interface{}{
				"model":  "claude-3",
				"system": "You are a helpful assistant.",
				"messages": []interface{}{
					map[string]interface{}{
						"role":    "user",
						"content": "Hello!",
					},
				},
			},
			minExpected: 8,
		},
		{
			name: "with_system_array",
			request: map[string]interface{}{
				"model": "claude-3",
				"system": []interface{}{
					map[string]interface{}{
						"type": "text",
						"text": "You are helpful.",
					},
				},
			},
			minExpected: 4,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bodyBytes, _ := json.Marshal(tt.request)
			result := EstimateRequestTokens(bodyBytes)
			if result < tt.minExpected {
				t.Errorf("EstimateRequestTokens() = %d, want >= %d", result, tt.minExpected)
			}
		})
	}
}

func TestEstimateGeminiRequestTokensSupportsWrappedRequest(t *testing.T) {
	direct := `{"contents":[{"parts":[{"text":"hello world"}]}],"systemInstruction":{"parts":[{"text":"请用中文回答"}]},"tools":[{"functionDeclarations":[{"name":"search"},{"name":"calculate"}]}]}`
	want, err := EstimateGeminiRequestTokens([]byte(direct))
	if err != nil {
		t.Fatalf("直接请求估算失败: %v", err)
	}
	if want < 300 {
		t.Fatalf("两个工具声明未计入: tokens = %d", want)
	}
	for _, body := range []string{
		`{"generateContentRequest":` + direct + `}`,
		`{"contents":[{"parts":[{"text":"不应重复计入"}]}],"generateContentRequest":` + direct + `}`,
	} {
		got, err := EstimateGeminiRequestTokens([]byte(body))
		if err != nil {
			t.Fatalf("包装请求估算失败: %v", err)
		}
		if got != want {
			t.Fatalf("包装请求计数 = %d，直接请求计数 = %d", got, want)
		}
	}
	got, err := EstimateGeminiRequestTokens([]byte(`{"contents":[]}`))
	if err != nil || got != 0 {
		t.Fatalf("空内容数组应可计数: tokens = %d, err = %v", got, err)
	}
}

func TestEstimateGeminiRequestTokensRejectsInvalidStructure(t *testing.T) {
	for _, body := range []string{
		``, `null`, `[]`, `{}`, `{"contents":`,
		`{"contents":null}`, `{"contents":"hello"}`, `{"contents":[null]}`,
		`{"contents":[{"parts":[]}]}`, `{"contents":[{"parts":{}}]}`,
		`{"contents":[{"parts":[null]}]}`, `{"contents":[{"parts":[{}]}]}`,
		`{"contents":[{"parts":[{"text":123}]}]}`,
		`{"contents":[{"parts":[{"functionCall":{"args":[]}}]}]}`,
		`{"contents":[{"parts":[{"functionResponse":{"response":true}}]}]}`,
		`{"contents":[{"parts":[{"functionCall":null}]}]}`,
		`{"contents":[{"parts":[{"functionCall":{}}]}]}`,
		`{"contents":[{"parts":[{"functionCall":{"name":"search","args":null}}]}]}`,
		`{"contents":[{"parts":[{"functionResponse":{"name":"search","response":null}}]}]}`,
		`{"systemInstruction":{"parts":"hello"}}`, `{"tools":"hello"}`, `{"tools":[null]}`,
		`{"tools":[{"functionDeclarations":"hello"}]}`,
		`{"generateContentRequest":null}`, `{"generateContentRequest":[]}`,
		`{"generateContentRequest":{"contents":"hello"}}`,
	} {
		if got, err := EstimateGeminiRequestTokens([]byte(body)); err == nil {
			t.Errorf("请求 %q 未被拒绝，错误地返回 tokens = %d", body, got)
		}
	}
}

func TestEstimateGeminiRequestTokensCountsToolJSON(t *testing.T) {
	for _, function := range []struct {
		name    string
		payload string
	}{
		{"functionCall", "args"},
		{"functionResponse", "response"},
	} {
		for _, payload := range []string{
			`{"values":[` + strings.Repeat("12345,", 1023) + `12345]}`,
			`{"values":[` + strings.Repeat("true,", 1023) + `false]}`,
			`{"` + strings.Repeat("long_field_name", 256) + `":null}`,
		} {
			body := `{"contents":[{"parts":[{"` + function.name + `":{"name":"calculate","` + function.payload + `":` + payload + `}}]}]}`
			got, err := EstimateGeminiRequestTokens([]byte(body))
			if err != nil {
				t.Fatalf("工具 JSON 估算失败: %v", err)
			}
			if got < 1000 {
				t.Errorf("%s.%s 的数字、布尔值或字段名未完整计入: tokens = %d", function.name, function.payload, got)
			}
		}
	}
}

func TestEstimateGeminiRequestTokensPreservesToolSchema(t *testing.T) {
	// 现有 Gemini 类型会清洗 $defs；计数必须保留客户端实际提供的 Schema。
	body := `{"contents":[],"tools":[{"functionDeclarations":[{"name":"search","parameters":{"type":"object","$defs":{"detail":{"description":"` + strings.Repeat("schema detail ", 1024) + `"}}}}]}]}`
	got, err := EstimateGeminiRequestTokens([]byte(body))
	if err != nil {
		t.Fatalf("工具 Schema 估算失败: %v", err)
	}
	if got < 3000 {
		t.Fatalf("工具 Schema 被清洗或只计入固定下限: tokens = %d", got)
	}
}

func TestEstimateGeminiRequestTokensSkipsOpaqueMediaPayload(t *testing.T) {
	for _, media := range []struct {
		prefix string
		suffix string
		quota  int
	}{
		{`{"inlineData":{"mimeType":"image/png","data":"`, `"}}`, estimatedImageTokens},
		{`{"inlineData":{"mimeType":"application/pdf","data":"`, `"}}`, estimatedFileTokens},
		{`{"fileData":{"mimeType":"application/pdf","fileUri":"`, `"}}`, estimatedFileTokens},
		{`{"functionResponse":{"name":"look","response":{"ok":true},"parts":[{"inlineData":{"mimeType":"image/png","data":"`, `"}}]}}`, estimatedImageTokens},
	} {
		var previous int
		for _, size := range []int{1024, 1024 * 1024} {
			body := `{"contents":[{"parts":[` + media.prefix + strings.Repeat("A", size) + media.suffix + `]}]}`
			got, err := EstimateGeminiRequestTokens([]byte(body))
			if err != nil {
				t.Fatalf("媒体请求估算失败: %v", err)
			}
			if got < media.quota || got > media.quota+50 {
				t.Fatalf("媒体未按定额计数: tokens = %d, quota = %d", got, media.quota)
			}
			if previous != 0 && got != previous {
				t.Fatalf("媒体载荷增长改变了计数: small = %d, large = %d", previous, got)
			}
			previous = got
		}
	}
}

func TestEstimateRequestTokensSkipsOpaqueMediaPayload(t *testing.T) {
	pdfData := strings.Repeat("A", 1024*1024)
	imageData := strings.Repeat("B", 200000)
	prompt := "请阅读这个 PDF"

	tests := []struct {
		name        string
		request     map[string]interface{}
		maxExpected int
		minExpected int
		wantOpaque  bool
	}{
		{
			name: "messages_pdf_document",
			request: map[string]interface{}{
				"model": "claude-opus-4-6",
				"messages": []interface{}{
					map[string]interface{}{
						"role": "user",
						"content": []interface{}{
							map[string]interface{}{"type": "text", "text": prompt},
							map[string]interface{}{
								"type": "document",
								"source": map[string]interface{}{
									"type":       "base64",
									"media_type": "application/pdf",
									"data":       pdfData,
								},
							},
						},
					},
				},
			},
			minExpected: estimatedFileTokens,
			maxExpected: estimatedFileTokens + 200,
			wantOpaque:  true,
		},
		{
			name: "messages_image_block",
			request: map[string]interface{}{
				"model": "claude-opus-4-6",
				"messages": []interface{}{
					map[string]interface{}{
						"role": "user",
						"content": []interface{}{
							map[string]interface{}{"type": "text", "text": prompt},
							map[string]interface{}{
								"type": "image",
								"source": map[string]interface{}{
									"type":       "base64",
									"media_type": "image/png",
									"data":       imageData,
								},
							},
						},
					},
				},
			},
			minExpected: estimatedImageTokens,
			maxExpected: estimatedImageTokens + 200,
			wantOpaque:  true,
		},
		{
			name: "document_text_source_still_counts_text",
			request: map[string]interface{}{
				"model": "claude-opus-4-6",
				"messages": []interface{}{
					map[string]interface{}{
						"role": "user",
						"content": []interface{}{
							map[string]interface{}{
								"type": "document",
								"source": map[string]interface{}{
									"type": "text",
									"data": strings.Repeat("hello world ", 100),
								},
							},
						},
					},
				},
			},
			minExpected: 200,
			maxExpected: 800,
			wantOpaque:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bodyBytes, err := json.Marshal(tt.request)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			got := EstimateRequestTokens(bodyBytes)
			if got < tt.minExpected || got > tt.maxExpected {
				t.Fatalf("EstimateRequestTokens() = %d, want %d..%d", got, tt.minExpected, tt.maxExpected)
			}
			if RequestContainsOpaqueMedia(bodyBytes) != tt.wantOpaque {
				t.Fatalf("RequestContainsOpaqueMedia() = %v, want %v", RequestContainsOpaqueMedia(bodyBytes), tt.wantOpaque)
			}
		})
	}
}

func TestEstimateResponsesRequestTokensSkipsOpaqueMediaPayload(t *testing.T) {
	pdfData := strings.Repeat("A", 1024*1024)
	prompt := "summarize this file"

	tests := []struct {
		name        string
		request     map[string]interface{}
		maxExpected int
		minExpected int
		wantOpaque  bool
	}{
		{
			name: "responses_input_file",
			request: map[string]interface{}{
				"model": "gpt-5.4",
				"input": []interface{}{
					map[string]interface{}{
						"type": "message",
						"role": "user",
						"content": []interface{}{
							map[string]interface{}{"type": "input_text", "text": prompt},
							map[string]interface{}{
								"type":      "input_file",
								"filename":  "notes.pdf",
								"file_data": "data:application/pdf;base64," + pdfData,
							},
						},
					},
				},
			},
			minExpected: estimatedFileTokens,
			maxExpected: estimatedFileTokens + 200,
			wantOpaque:  true,
		},
		{
			name: "responses_unknown_item_with_file_data",
			request: map[string]interface{}{
				"model": "gpt-5.4",
				"input": []interface{}{
					map[string]interface{}{
						"type":      "document",
						"file_data": pdfData,
					},
				},
			},
			minExpected: estimatedFileTokens,
			maxExpected: estimatedFileTokens + 50,
			wantOpaque:  true,
		},
		{
			name: "responses_custom_tool_call_output_still_counts_text",
			request: map[string]interface{}{
				"model": "gpt-5.6-luna",
				"input": []interface{}{
					map[string]interface{}{
						"type":   "custom_tool_call_output",
						"output": strings.Repeat("tool result ", 3000),
					},
				},
			},
			minExpected: 8000,
			maxExpected: 20000,
			wantOpaque:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bodyBytes, err := json.Marshal(tt.request)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			got := EstimateResponsesRequestTokens(bodyBytes)
			if got < tt.minExpected || got > tt.maxExpected {
				t.Fatalf("EstimateResponsesRequestTokens() = %d, want %d..%d", got, tt.minExpected, tt.maxExpected)
			}
			if RequestContainsOpaqueMedia(bodyBytes) != tt.wantOpaque {
				t.Fatalf("RequestContainsOpaqueMedia() = %v, want %v", RequestContainsOpaqueMedia(bodyBytes), tt.wantOpaque)
			}
		})
	}
}
