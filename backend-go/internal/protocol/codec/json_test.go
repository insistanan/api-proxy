package codec

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSimplifyToolsArray(t *testing.T) {
	tests := []struct {
		name     string
		input    interface{}
		expected string
	}{
		{
			name: "Claude格式工具",
			input: map[string]interface{}{
				"tools": []interface{}{
					map[string]interface{}{
						"name":        "get_weather",
						"description": "Get weather info",
					},
					map[string]interface{}{
						"name":        "search",
						"description": "Search the web",
					},
				},
			},
			expected: `["get_weather","search"]`,
		},
		{
			name: "OpenAI格式工具",
			input: map[string]interface{}{
				"tools": []interface{}{
					map[string]interface{}{
						"type": "function",
						"function": map[string]interface{}{
							"name":        "calculator",
							"description": "Calculate math",
						},
					},
				},
			},
			expected: `["calculator"]`,
		},
		{
			name: "非工具字段不受影响",
			input: map[string]interface{}{
				"model":    "claude-3",
				"messages": []interface{}{"hello"},
			},
			expected: `{"messages":["hello"],"model":"claude-3"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := SimplifyToolsArray(tt.input)

			// 提取tools字段检查
			if resultMap, ok := result.(map[string]interface{}); ok {
				if tools, exists := resultMap["tools"]; exists {
					toolsJSON, _ := json.Marshal(tools)
					if !strings.Contains(string(toolsJSON), tt.expected) {
						t.Errorf("Expected tools to contain %s, got %s", tt.expected, string(toolsJSON))
					}
				}
			}
		})
	}
}

func TestMaskAPIKey(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "空字符串",
			input:    "",
			expected: "",
		},
		{
			name:     "短密钥(5字符)",
			input:    "abc12",
			expected: "***", // 长度<=5时返回***
		},
		{
			name:     "短密钥(6字符)",
			input:    "abc123",
			expected: "abc***23",
		},
		{
			name:     "长密钥",
			input:    "sk-1234567890abcdef",
			expected: "sk-12345***bcdef",
		},
		{
			name:     "超短密钥",
			input:    "key",
			expected: "***",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := MaskAPIKey(tt.input)
			if result != tt.expected {
				t.Errorf("Expected %s, got %s", tt.expected, result)
			}
		})
	}
}
