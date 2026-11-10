package converters

import (
	"errors"
	"testing"

	"github.com/insistanan/api-proxy/internal/types"
)

func TestResponsesChatValidationUnsupportedTools(t *testing.T) {
	cases := map[string]string{
		"hosted_search":      `{"tools":[{"type":"web_search"}]}`,
		"preview_search":     `{"tools":[{"type":"web_search_preview"}]}`,
		"search_choice":      `{"tool_choice":{"type":"web_search"}}`,
		"preview_choice":     `{"tool_choice":{"type":"web_search_preview"}}`,
		"file_search":        `{"tools":[{"type":"file_search"}]}`,
		"file_search_choice": `{"tool_choice":{"type":"file_search"}}`,
		"dynamic_search":     `{"input":[{"type":"tool_search_output","tools":[{"type":"web_search"}]}]}`,
		"namespace_search":   `{"tools":[{"type":"namespace","name":"tools","tools":[{"type":"web_search"}]}]}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			err := ValidateResponsesToOpenAIChatRequest([]byte(body))
			if !types.IsUnsupportedProtocol(err) {
				t.Fatalf("无法表达的工具应标记候选协议不支持，实际为 %v", err)
			}
		})
	}
}

func TestResponsesChatValidationMalformedTools(t *testing.T) {
	cases := map[string]string{
		"invalid_json":           `not-json`,
		"request_array":          `[]`,
		"tools_object":           `{"tools":{}}`,
		"tool_scalar":            `{"tools":[42]}`,
		"tool_type_number":       `{"tools":[{"type":42}]}`,
		"missing_name":           `{"tools":[{"type":"function"}]}`,
		"invalid_name":           `{"tools":[{"type":"function","name":42}]}`,
		"invalid_nested_name":    `{"tools":[{"type":"function","function":{"name":42}}]}`,
		"invalid_namespace_name": `{"tools":[{"type":"namespace","name":42}]}`,
		"namespace_tools_object": `{"tools":[{"type":"namespace","name":"tools","tools":{}}]}`,
		"choice_scalar":          `{"tool_choice":42}`,
		"choice_type_number":     `{"tool_choice":{"type":42}}`,
		"choice_name_number":     `{"tool_choice":{"type":"function","name":42}}`,
		"invalid_call_arguments": `{"input":[{"type":"function_call","call_id":"call_1","name":"search","arguments":"invalid json"}]}`,
		"invalid_dynamic_tools":  `{"input":[{"type":"tool_search_output","tools":[42]}]}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			err := ValidateResponsesToOpenAIChatRequest([]byte(body))
			if err == nil || types.IsUnsupportedProtocol(err) {
				t.Fatalf("非法请求应保留校验错误，实际为 %v", err)
			}
			var clientErr interface{ ClientRequestError() bool }
			if !errors.As(err, &clientErr) || !clientErr.ClientRequestError() {
				t.Fatalf("非法参数应明确返回客户端错误，不重试 Key: %v", err)
			}
		})
	}
}

func TestResponsesChatValidationClientWebSearchFunction(t *testing.T) {
	for _, body := range []string{
		`{"tools":[{"type":"function","name":"web_search"}],"tool_choice":{"type":"function","name":"web_search"}}`,
		`{"tools":[{"type":"function","function":{"name":"web_search"}}],"tool_choice":{"type":"function","function":{"name":"web_search"}}}`,
		`{"tools":[{"type":"tool_search"}],"tool_choice":{"type":"tool_search"},"input":[{"type":"tool_search_output","tools":[{"type":"function","name":"web_search"}]}]}`,
	} {
		if err := ValidateResponsesToOpenAIChatRequest([]byte(body)); err != nil {
			t.Fatalf("普通客户端函数和现有动态工具搜索应保持兼容: %v", err)
		}
	}
}
