package contract

import "testing"

func TestCompleteResponseRequiresExplicitFinish(t *testing.T) {
	for _, tc := range []struct{ protocol, body string }{
		{"openai", `{"choices":[{"message":{"content":"partial"}}]}`},
		{"openai", `{"choices":[{"message":{},"finish_reason":null}]}`},
		{"openai", `{"choices":[{"message":{},"finish_reason":"unknown"}]}`},
		{"claude", `{"content":[]}`},
		{"claude", `{"content":[],"stop_reason":null}`},
		{"claude", `{"content":[],"stop_reason":"unknown"}`},
		{"gemini", `{"candidates":[null]}`},
		{"gemini", `{"candidates":[{}]}`},
		{"gemini", `{"candidates":[{"finishReason":null}]}`},
		{"gemini", `{"candidates":[{"finishReason":"unknown"}]}`},
	} {
		t.Run(tc.protocol+"/"+tc.body, func(t *testing.T) {
			if err := ValidateResponse(tc.protocol, []byte(tc.body)); err == nil {
				t.Fatal("缺少有效结构或终态的普通响应不能成功")
			}
		})
	}
}

func TestCompleteResponseAllowsEmptyContentAndTools(t *testing.T) {
	for _, tc := range []struct{ protocol, body string }{
		{"openai", `{"choices":[{"message":{"content":null},"finish_reason":"content_filter"}]}`},
		{"openai", `{"choices":[{"message":{"tool_calls":[{"id":"a","function":{"name":"run","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`},
		{"claude", `{"content":[],"stop_reason":"refusal"}`},
		{"claude", `{"content":[{"type":"tool_use","id":"a","name":"run","input":{}}],"stop_reason":"tool_use"}`},
		{"gemini", `{"candidates":[{"finishReason":"SAFETY"}]}`},
		{"gemini", `{"candidates":[{"content":{"parts":[{"functionCall":{"name":"run","args":{}}}]},"finishReason":"STOP"}]}`},
	} {
		if err := ValidateResponse(tc.protocol, []byte(tc.body)); err != nil {
			t.Errorf("%s: %v", tc.body, err)
		}
	}
}
