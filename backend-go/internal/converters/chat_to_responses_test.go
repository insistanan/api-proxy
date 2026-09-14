package converters

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/tidwall/gjson"
)

func TestConvertResponsesToOpenAIChatRequest(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		model    string
		stream   bool
		validate func(t *testing.T, result []byte)
	}{
		{
			name: "基本文本输入",
			input: `{
				"model": "gpt-4",
				"input": "Hello, world!",
				"instructions": "You are a helpful assistant."
			}`,
			model:  "gpt-4o",
			stream: false,
			validate: func(t *testing.T, result []byte) {
				root := gjson.ParseBytes(result)
				if root.Get("model").String() != "gpt-4o" {
					t.Errorf("model should be gpt-4o, got %s", root.Get("model").String())
				}
				if root.Get("stream").Bool() != false {
					t.Error("stream should be false")
				}
				messages := root.Get("messages").Array()
				if len(messages) != 2 {
					t.Errorf("should have 2 messages (system + user), got %d", len(messages))
				}
				if messages[0].Get("role").String() != "system" {
					t.Error("first message should be system")
				}
				if messages[1].Get("role").String() != "user" {
					t.Error("second message should be user")
				}
			},
		},
		{
			name: "带 tools 的请求",
			input: `{
				"model": "gpt-4",
				"input": [{"type": "message", "role": "user", "content": [{"type": "input_text", "text": "What's the weather?"}]}],
				"tools": [
					{
						"name": "get_weather",
						"description": "Get weather info",
						"parameters": {"type": "object", "properties": {"location": {"type": "string"}}}
					}
				]
			}`,
			model:  "gpt-4o",
			stream: true,
			validate: func(t *testing.T, result []byte) {
				root := gjson.ParseBytes(result)
				if root.Get("stream").Bool() != true {
					t.Error("stream should be true")
				}
				tools := root.Get("tools").Array()
				if len(tools) != 1 {
					t.Errorf("should have 1 tool, got %d", len(tools))
				}
				if tools[0].Get("function.name").String() != "get_weather" {
					t.Error("tool name should be get_weather")
				}
			},
		},
		{
			name: "custom tool 降级为 function",
			input: `{
				"model": "gpt-4",
				"input": "Run the tool",
				"tools": [
					{
						"type": "custom",
						"name": "code_exec",
						"description": "Execute raw code",
						"format": {"type": "text"}
					}
				],
				"tool_choice": {
					"type": "custom",
					"name": "code_exec"
				}
			}`,
			model:  "gpt-4o",
			stream: false,
			validate: func(t *testing.T, result []byte) {
				root := gjson.ParseBytes(result)
				if root.Get("tools.0.type").String() != "function" {
					t.Errorf("tool type should be function, got %s", root.Get("tools.0.type").String())
				}
				if root.Get("tools.0.function.name").String() != "code_exec" {
					t.Errorf("tool name should be code_exec, got %s", root.Get("tools.0.function.name").String())
				}
				if root.Get("tools.0.function.parameters.properties.input.type").String() != "string" {
					t.Errorf("custom tool input should downgrade to string schema, got %s", root.Get("tools.0.function.parameters.properties.input.type").String())
				}
				if root.Get("tool_choice.type").String() != "function" {
					t.Errorf("tool_choice type should be function, got %s", root.Get("tool_choice.type").String())
				}
				if root.Get("tool_choice.function.name").String() != "code_exec" {
					t.Errorf("tool_choice function name should be code_exec, got %s", root.Get("tool_choice.function.name").String())
				}
			},
		},
		{
			name: "function_call 和 function_call_output",
			input: `{
				"model": "gpt-4",
				"input": [
					{"type": "message", "role": "user", "content": [{"type": "input_text", "text": "What's the weather in NYC?"}]},
					{"type": "function_call", "call_id": "call_123", "name": "get_weather", "arguments": "{\"location\": \"NYC\"}"},
					{"type": "function_call_output", "call_id": "call_123", "output": "Sunny, 72°F"}
				]
			}`,
			model:  "gpt-4o",
			stream: false,
			validate: func(t *testing.T, result []byte) {
				root := gjson.ParseBytes(result)
				messages := root.Get("messages").Array()
				if len(messages) != 3 {
					t.Errorf("should have 3 messages, got %d", len(messages))
				}
				// 第二条消息应该是 assistant with tool_calls
				if messages[1].Get("role").String() != "assistant" {
					t.Error("second message should be assistant")
				}
				if !messages[1].Get("tool_calls").Exists() {
					t.Error("assistant message should have tool_calls")
				}
				// 第三条消息应该是 tool
				if messages[2].Get("role").String() != "tool" {
					t.Error("third message should be tool")
				}
			},
		},
		{
			name: "reasoning effort 转换",
			input: `{
				"model": "o1-mini",
				"input": "Think about this",
				"reasoning": {"effort": "high"}
			}`,
			model:  "o1-mini",
			stream: false,
			validate: func(t *testing.T, result []byte) {
				root := gjson.ParseBytes(result)
				if root.Get("reasoning_effort").String() != "high" {
					t.Errorf("reasoning_effort should be high, got %s", root.Get("reasoning_effort").String())
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ConvertResponsesToOpenAIChatRequest(tt.model, []byte(tt.input), tt.stream)
			tt.validate(t, result)
		})
	}
}

func TestValidateResponsesToOpenAIChatRequest_CustomToolCompatible(t *testing.T) {
	input := `{
		"model":"gpt-4o",
		"input":"hi",
		"tools":[{"type":"custom","name":"code_exec","description":"Execute raw code"}],
		"tool_choice":{"type":"custom","name":"code_exec"}
	}`

	if err := ValidateResponsesToOpenAIChatRequest([]byte(input)); err != nil {
		t.Fatalf("custom tool 应该兼容降级到 OpenAI Chat，实际报错: %v", err)
	}
}

func TestValidateResponsesToOpenAIChatRequest_NamespaceToolCompatible(t *testing.T) {
	input := `{
		"model":"gpt-4o",
		"tools":[
			{
				"type":"namespace",
				"name":"functions",
				"tools":[
					{"type":"function","name":"grep","description":"Search","parameters":{"type":"object"}}
				]
			}
		],
		"tool_choice":{"type":"function","namespace":"functions","name":"grep"}
	}`

	if err := ValidateResponsesToOpenAIChatRequest([]byte(input)); err != nil {
		t.Fatalf("namespace tool 应该兼容降级到 OpenAI Chat，实际报错: %v", err)
	}
}

func TestValidateResponsesToOpenAIChatRequest_InvalidFields(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{
			name:  "非 function tool",
			input: `{"model":"gpt-4o","input":"hi","tools":[{"type":"browser_use","name":"search"}]}`,
		},
		{
			name:  "tool 缺少 name",
			input: `{"model":"gpt-4o","input":"hi","tools":[{"type":"function","parameters":{"type":"object"}}]}`,
		},
		{
			name:  "非法 tool_choice",
			input: `{"model":"gpt-4o","input":"hi","tool_choice":"always"}`,
		},
		{
			name:  "非法 reasoning effort",
			input: `{"model":"gpt-4o","input":"hi","reasoning":{"effort":"future"}}`,
		},
		{
			name:  "非法 function_call arguments",
			input: `{"model":"gpt-4o","input":[{"type":"function_call","call_id":"call_bad","name":"lookup","arguments":"{bad-json"}]}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := ValidateResponsesToOpenAIChatRequest([]byte(tt.input)); err == nil {
				t.Fatal("应该返回协议兼容错误")
			}
		})
	}
}

func TestConvertOpenAIChatToResponses_Stream(t *testing.T) {
	ctx := context.Background()

	// 模拟 OpenAI Chat Completions SSE 流
	sseLines := []string{
		`data: {"id":"chatcmpl-123","object":"chat.completion.chunk","created":1234567890,"model":"gpt-4o","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl-123","object":"chat.completion.chunk","created":1234567890,"model":"gpt-4o","choices":[{"index":0,"delta":{"content":"Hello"},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl-123","object":"chat.completion.chunk","created":1234567890,"model":"gpt-4o","choices":[{"index":0,"delta":{"content":" world!"},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl-123","object":"chat.completion.chunk","created":1234567890,"model":"gpt-4o","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`,
		`data: [DONE]`,
	}

	originalReq := []byte(`{"model":"gpt-4o","input":"Hi"}`)

	var state any
	var allEvents []string

	for _, line := range sseLines {
		events := ConvertOpenAIChatToResponses(ctx, "gpt-4o", originalReq, nil, []byte(line), &state)
		allEvents = append(allEvents, events...)
	}

	// 验证事件序列
	if len(allEvents) == 0 {
		t.Fatal("should produce events")
	}

	// 检查是否有 response.created 事件
	hasCreated := false
	hasInProgress := false
	hasCompleted := false
	hasTextDelta := false

	for _, ev := range allEvents {
		if strings.Contains(ev, "response.created") {
			hasCreated = true
		}
		if strings.Contains(ev, "response.in_progress") {
			hasInProgress = true
		}
		if strings.Contains(ev, "response.completed") {
			hasCompleted = true
		}
		if strings.Contains(ev, "response.output_text.delta") {
			hasTextDelta = true
		}
	}

	if !hasCreated {
		t.Error("should have response.created event")
	}
	if !hasInProgress {
		t.Error("should have response.in_progress event")
	}
	if !hasCompleted {
		t.Error("should have response.completed event")
	}
	if !hasTextDelta {
		t.Error("should have response.output_text.delta event")
	}
}

func TestConvertOpenAIChatToResponses_ToolCall(t *testing.T) {
	ctx := context.Background()

	// 模拟带 tool_call 的 SSE 流
	sseLines := []string{
		`data: {"id":"chatcmpl-456","object":"chat.completion.chunk","created":1234567890,"model":"gpt-4o","choices":[{"index":0,"delta":{"role":"assistant","content":null,"tool_calls":[{"index":0,"id":"call_abc","type":"function","function":{"name":"get_weather","arguments":""}}]},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl-456","object":"chat.completion.chunk","created":1234567890,"model":"gpt-4o","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"loc"}}]},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl-456","object":"chat.completion.chunk","created":1234567890,"model":"gpt-4o","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"ation\": \"NYC\"}"}}]},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl-456","object":"chat.completion.chunk","created":1234567890,"model":"gpt-4o","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`,
	}

	originalReq := []byte(`{"model":"gpt-4o","input":"What's the weather?","tools":[{"name":"get_weather"}]}`)

	var state any
	var allEvents []string

	for _, line := range sseLines {
		events := ConvertOpenAIChatToResponses(ctx, "gpt-4o", originalReq, nil, []byte(line), &state)
		allEvents = append(allEvents, events...)
	}

	// 验证是否有 function_call 相关事件
	hasFuncAdded := false
	hasFuncDelta := false
	hasFuncDone := false

	for _, ev := range allEvents {
		if strings.Contains(ev, "response.output_item.added") && strings.Contains(ev, "function_call") {
			hasFuncAdded = true
			// 验证 output_item.added 携带 name 字段（Fix #1）
			payload := extractSSEPayload(ev)
			root := gjson.Parse(payload)
			itemName := root.Get("item.name").String()
			if itemName != "get_weather" {
				t.Errorf("output_item.added 的 item.name 应为 get_weather，实际: %q", itemName)
			}
		}
		if strings.Contains(ev, "response.function_call_arguments.delta") {
			hasFuncDelta = true
		}
		if strings.Contains(ev, "response.function_call_arguments.done") {
			hasFuncDone = true
		}
	}

	if !hasFuncAdded {
		t.Error("should have function_call output_item.added event")
	}
	if !hasFuncDelta {
		t.Error("should have function_call_arguments.delta event")
	}
	if !hasFuncDone {
		t.Error("should have function_call_arguments.done event")
	}
}

func TestConvertOpenAIChatToResponses_ParallelToolCalls(t *testing.T) {
	ctx := context.Background()

	// 两个并行 tool call，首 chunk 各携带 id+name
	sseLines := []string{
		`data: {"id":"chatcmpl-para","object":"chat.completion.chunk","created":1234567890,"model":"gpt-4o","choices":[{"index":0,"delta":{"role":"assistant","content":null,"tool_calls":[{"index":0,"id":"call_a1","type":"function","function":{"name":"get_weather","arguments":""}},{"index":1,"id":"call_a2","type":"function","function":{"name":"get_time","arguments":""}}]},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl-para","object":"chat.completion.chunk","created":1234567890,"model":"gpt-4o","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"city\":\"NYC\"}"}},{"index":1,"function":{"arguments":"{\"tz\":\"EST\"}"}}]},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl-para","object":"chat.completion.chunk","created":1234567890,"model":"gpt-4o","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`,
	}

	originalReq := []byte(`{"model":"gpt-4o","input":"Weather and time?","tools":[{"name":"get_weather"},{"name":"get_time"}]}`)

	var state any
	var allEvents []string
	for _, line := range sseLines {
		events := ConvertOpenAIChatToResponses(ctx, "gpt-4o", originalReq, nil, []byte(line), &state)
		allEvents = append(allEvents, events...)
	}

	// 收集所有 output_item.added 事件的 name
	addedNames := make(map[int]string)
	doneCount := 0
	for _, ev := range allEvents {
		if strings.Contains(ev, "response.output_item.added") {
			payload := extractSSEPayload(ev)
			root := gjson.Parse(payload)
			oi := int(root.Get("output_index").Int())
			addedNames[oi] = root.Get("item.name").String()
		}
		if strings.Contains(ev, "response.output_item.done") {
			doneCount++
		}
	}

	if len(addedNames) != 2 {
		t.Errorf("应有 2 个 output_item.added 事件，实际: %d", len(addedNames))
	}
	if addedNames[0] != "get_weather" {
		t.Errorf("output_index 0 的 name 应为 get_weather，实际: %q", addedNames[0])
	}
	if addedNames[1] != "get_time" {
		t.Errorf("output_index 1 的 name 应为 get_time，实际: %q", addedNames[1])
	}
	if doneCount != 2 {
		t.Errorf("应有 2 个 output_item.done 事件，实际: %d", doneCount)
	}
}

func TestConvertOpenAIChatToResponses_ReasoningTextToolCallMixedOutputIndex(t *testing.T) {
	ctx := context.Background()

	// reasoning → text → tool_call 的混合输出，验证 output_index 连续递增
	sseLines := []string{
		// reasoning content
		`data: {"id":"chatcmpl-mix","object":"chat.completion.chunk","created":1234567890,"model":"o1","choices":[{"index":0,"delta":{"role":"assistant","reasoning_content":"Let me think..."},"finish_reason":null}]}`,
		// text content
		`data: {"id":"chatcmpl-mix","object":"chat.completion.chunk","created":1234567890,"model":"o1","choices":[{"index":0,"delta":{"content":"I'll check the weather."},"finish_reason":null}]}`,
		// tool call
		`data: {"id":"chatcmpl-mix","object":"chat.completion.chunk","created":1234567890,"model":"o1","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_mix1","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"NYC\"}"}}]},"finish_reason":null}]}`,
		// finish
		`data: {"id":"chatcmpl-mix","object":"chat.completion.chunk","created":1234567890,"model":"o1","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
	}

	originalReq := []byte(`{"model":"o1","input":"Weather in NYC?","tools":[{"name":"get_weather"}]}`)

	var state any
	var allEvents []string
	for _, line := range sseLines {
		events := ConvertOpenAIChatToResponses(ctx, "o1", originalReq, nil, []byte(line), &state)
		allEvents = append(allEvents, events...)
	}

	// reasoning → output_index 0
	// text → output_index 1
	// tool_call (idx=0) → output_index 2 (0 + reasoning(1) + text(1))
	reasoningIdx := -1
	textIdx := -1
	funcAddedIdx := -1
	funcDoneIdx := -1

	for _, ev := range allEvents {
		payload := extractSSEPayload(ev)
		root := gjson.Parse(payload)
		oi := int(root.Get("output_index").Int())

		if strings.Contains(ev, "response.reasoning_summary_part.added") {
			reasoningIdx = oi
		}
		if strings.Contains(ev, "response.output_text.delta") {
			textIdx = oi
		}
		if strings.Contains(ev, "response.output_item.added") && strings.Contains(ev, "function_call") {
			funcAddedIdx = oi
		}
		if strings.Contains(ev, "response.function_call_arguments.done") {
			funcDoneIdx = oi
		}
	}

	if reasoningIdx != 0 {
		t.Errorf("reasoning output_index 应为 0，实际: %d", reasoningIdx)
	}
	if textIdx != 1 {
		t.Errorf("text output_index 应为 1，实际: %d", textIdx)
	}
	if funcAddedIdx != 2 {
		t.Errorf("function_call output_item.added output_index 应为 2，实际: %d", funcAddedIdx)
	}
	if funcDoneIdx != 2 {
		t.Errorf("function_call_arguments.done output_index 应为 2，实际: %d", funcDoneIdx)
	}
}

func extractSSEPayload(ev string) string {
	for _, line := range strings.Split(ev, "\n") {
		if strings.HasPrefix(line, "data: ") {
			return strings.TrimPrefix(line, "data: ")
		}
	}
	return ""
}

func TestConvertOpenAIChatToResponses_CustomToolCall(t *testing.T) {
	ctx := context.Background()

	sseLines := []string{
		`data: {"id":"chatcmpl-custom","object":"chat.completion.chunk","created":1234567890,"model":"gpt-4o","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_exec","type":"function","function":{"name":"code_exec","arguments":""}}]},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl-custom","object":"chat.completion.chunk","created":1234567890,"model":"gpt-4o","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"input\":\"print('hi')\"}"}}]},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl-custom","object":"chat.completion.chunk","created":1234567890,"model":"gpt-4o","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`,
	}

	originalReq := []byte(`{"model":"gpt-4o","input":"Run code","tools":[{"type":"custom","name":"code_exec","description":"Execute code"}]}`)

	var state any
	var allEvents []string
	for _, line := range sseLines {
		events := ConvertOpenAIChatToResponses(ctx, "gpt-4o", originalReq, nil, []byte(line), &state)
		allEvents = append(allEvents, events...)
	}

	hasCustomDone := false
	for _, ev := range allEvents {
		if !strings.Contains(ev, "response.output_item.done") || !strings.Contains(ev, "\"custom_tool_call\"") {
			continue
		}
		hasCustomDone = true
		if !strings.Contains(ev, "\"input\":\"print('hi')\"") {
			t.Fatalf("custom tool 完成事件应携带 input，事件内容: %s", ev)
		}
	}

	if !hasCustomDone {
		t.Fatal("should have custom_tool_call output_item.done event")
	}
}

func TestConvertOpenAIChatToResponses_ReasoningDeltaUsesDeltaField(t *testing.T) {
	ctx := context.Background()
	sseLines := []string{
		`data: {"id":"chatcmpl-reasoning","object":"chat.completion.chunk","created":1234567890,"model":"o1-mini","choices":[{"index":0,"delta":{"role":"assistant","reasoning_content":"Thinking"},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl-reasoning","object":"chat.completion.chunk","created":1234567890,"model":"o1-mini","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		`data: [DONE]`,
	}

	originalReq := []byte(`{"model":"o1-mini","input":"Think"}`)
	var state any
	var allEvents []string
	for _, line := range sseLines {
		events := ConvertOpenAIChatToResponses(ctx, "o1-mini", originalReq, nil, []byte(line), &state)
		allEvents = append(allEvents, events...)
	}

	found := false
	for _, ev := range allEvents {
		if !strings.Contains(ev, "response.reasoning_summary_text.delta") {
			continue
		}
		found = true
		payload := ""
		for _, line := range strings.Split(ev, "\n") {
			if strings.HasPrefix(line, "data: ") {
				payload = strings.TrimPrefix(line, "data: ")
				break
			}
		}
		root := gjson.Parse(payload)
		if root.Get("delta").String() != "Thinking" {
			t.Errorf("reasoning delta 应使用 delta 字段")
		}
		if root.Get("text").Exists() {
			t.Errorf("reasoning delta 不应使用 text 字段")
		}
	}

	if !found {
		t.Fatal("should have reasoning_summary_text.delta event")
	}
}

func TestConvertOpenAIChatToResponsesNonStream(t *testing.T) {
	ctx := context.Background()

	// 模拟 OpenAI Chat Completions 非流式响应
	chatResponse := `{
		"id": "chatcmpl-789",
		"object": "chat.completion",
		"created": 1234567890,
		"model": "gpt-4o",
		"choices": [{
			"index": 0,
			"message": {
				"role": "assistant",
				"content": "Hello! How can I help you today?"
			},
			"finish_reason": "stop"
		}],
		"usage": {
			"prompt_tokens": 10,
			"completion_tokens": 8,
			"total_tokens": 18
		}
	}`

	originalReq := []byte(`{"model":"gpt-4o","input":"Hi","instructions":"Be helpful"}`)

	result := ConvertOpenAIChatToResponsesNonStream(ctx, "gpt-4o", originalReq, nil, []byte(chatResponse), nil)

	// 解析结果
	var resp map[string]interface{}
	if err := json.Unmarshal([]byte(result), &resp); err != nil {
		t.Fatalf("failed to parse result: %v", err)
	}

	// 验证基本字段
	if resp["object"] != "response" {
		t.Errorf("object should be response, got %v", resp["object"])
	}
	if resp["status"] != "completed" {
		t.Errorf("status should be completed, got %v", resp["status"])
	}

	// 验证 output
	output, ok := resp["output"].([]interface{})
	if !ok || len(output) == 0 {
		t.Fatal("output should have items")
	}

	msgItem := output[0].(map[string]interface{})
	if msgItem["type"] != "message" {
		t.Errorf("first output item should be message, got %v", msgItem["type"])
	}

	// 验证 usage
	usage, ok := resp["usage"].(map[string]interface{})
	if !ok {
		t.Fatal("usage should exist")
	}
	if usage["input_tokens"].(float64) != 10 {
		t.Errorf("input_tokens should be 10, got %v", usage["input_tokens"])
	}
	if usage["output_tokens"].(float64) != 8 {
		t.Errorf("output_tokens should be 8, got %v", usage["output_tokens"])
	}
}

func TestConvertOpenAIChatToResponsesNonStream_ToolCalls(t *testing.T) {
	ctx := context.Background()

	// 模拟带 tool_calls 的响应
	chatResponse := `{
		"id": "chatcmpl-tool",
		"object": "chat.completion",
		"created": 1234567890,
		"model": "gpt-4o",
		"choices": [{
			"index": 0,
			"message": {
				"role": "assistant",
				"content": null,
				"tool_calls": [
					{
						"id": "call_xyz",
						"type": "function",
						"function": {
							"name": "search",
							"arguments": "{\"query\": \"test\"}"
						}
					}
				]
			},
			"finish_reason": "tool_calls"
		}],
		"usage": {"prompt_tokens": 5, "completion_tokens": 10, "total_tokens": 15}
	}`

	originalReq := []byte(`{"model":"gpt-4o","input":"Search for test"}`)

	result := ConvertOpenAIChatToResponsesNonStream(ctx, "gpt-4o", originalReq, nil, []byte(chatResponse), nil)

	var resp map[string]interface{}
	if err := json.Unmarshal([]byte(result), &resp); err != nil {
		t.Fatalf("failed to parse result: %v", err)
	}

	output, ok := resp["output"].([]interface{})
	if !ok || len(output) == 0 {
		t.Fatal("output should have items")
	}

	// 查找 function_call item
	var funcItem map[string]interface{}
	for _, item := range output {
		itemMap := item.(map[string]interface{})
		if itemMap["type"] == "function_call" {
			funcItem = itemMap
			break
		}
	}

	if funcItem == nil {
		t.Fatal("should have function_call item")
	}

	if funcItem["name"] != "search" {
		t.Errorf("function name should be search, got %v", funcItem["name"])
	}
	if funcItem["call_id"] != "call_xyz" {
		t.Errorf("call_id should be call_xyz, got %v", funcItem["call_id"])
	}
}

func TestConvertOpenAIChatToResponsesNonStream_CustomToolCalls(t *testing.T) {
	ctx := context.Background()

	chatResponse := `{
		"id": "chatcmpl-custom-tool",
		"object": "chat.completion",
		"created": 1234567890,
		"model": "gpt-4o",
		"choices": [{
			"index": 0,
			"message": {
				"role": "assistant",
				"tool_calls": [
					{
						"id": "call_exec",
						"type": "function",
						"function": {
							"name": "code_exec",
							"arguments": "{\"input\":\"print('hi')\"}"
						}
					}
				]
			},
			"finish_reason": "tool_calls"
		}],
		"usage": {"prompt_tokens": 5, "completion_tokens": 10, "total_tokens": 15}
	}`

	originalReq := []byte(`{"model":"gpt-4o","input":"Run code","tools":[{"type":"custom","name":"code_exec","description":"Execute code"}]}`)

	result := ConvertOpenAIChatToResponsesNonStream(ctx, "gpt-4o", originalReq, nil, []byte(chatResponse), nil)

	root := gjson.Parse(result)
	if root.Get("output.0.type").String() != "custom_tool_call" {
		t.Fatalf("应恢复为 custom_tool_call，实际为 %s", root.Get("output.0.type").String())
	}
	if root.Get("output.0.input").String() != "print('hi')" {
		t.Fatalf("custom tool input 不匹配，实际为 %s", root.Get("output.0.input").String())
	}
}

func TestConvertResponsesToOpenAIChatRequest_PrunesToolControlWithoutTools(t *testing.T) {
	input := `{
		"model":"gpt-4o",
		"input":"hi",
		"tool_choice":{"type":"function","name":"lookup"},
		"parallel_tool_calls":true
	}`

	result := ConvertResponsesToOpenAIChatRequest("gpt-4o", []byte(input), false)
	root := gjson.ParseBytes(result)
	if root.Get("tool_choice").Exists() {
		t.Fatal("没有 tools 时应移除 tool_choice")
	}
	if root.Get("parallel_tool_calls").Exists() {
		t.Fatal("没有 tools 时应移除 parallel_tool_calls")
	}
}

func TestConvertResponsesToOpenAIChatRequest_FlattensNamespaceTool(t *testing.T) {
	input := `{
		"model":"gpt-4o",
		"tools":[
			{
				"type":"namespace",
				"name":"functions",
				"tools":[
					{"type":"function","name":"grep","description":"Search","parameters":{"type":"object"}}
				]
			}
		],
		"tool_choice":{"type":"function","namespace":"functions","name":"grep"}
	}`

	result := ConvertResponsesToOpenAIChatRequest("gpt-4o", []byte(input), false)
	root := gjson.ParseBytes(result)

	if root.Get("tools.0.function.name").String() != "functions__grep" {
		t.Fatalf("namespace tool 应拍平为 functions__grep，实际为 %s", root.Get("tools.0.function.name").String())
	}
	if root.Get("tool_choice.function.name").String() != "functions__grep" {
		t.Fatalf("namespace tool_choice 应拍平，实际为 %s", root.Get("tool_choice.function.name").String())
	}
}

func TestConvertResponsesToOpenAIChatRequest_MapsWebSearchTool(t *testing.T) {
	input := `{
		"model":"gpt-4o",
		"tools":[{"type":"web_search","description":"Search the web"}],
		"tool_choice":{"type":"web_search"}
	}`

	result := ConvertResponsesToOpenAIChatRequest("gpt-4o", []byte(input), false)
	root := gjson.ParseBytes(result)

	if root.Get("tools.0.type").String() != "function" {
		t.Fatalf("web_search 应降级为 function，实际为 %s", root.Get("tools.0.type").String())
	}
	if root.Get("tools.0.function.name").String() != "web_search" {
		t.Fatalf("web_search function.name 不匹配，实际为 %s", root.Get("tools.0.function.name").String())
	}
	if root.Get("tool_choice.function.name").String() != "web_search" {
		t.Fatalf("web_search tool_choice 不匹配，实际为 %s", root.Get("tool_choice.function.name").String())
	}
}

func TestConvertResponsesToOpenAIChatRequest_StringifiesToolOutputContent(t *testing.T) {
	input := `{
		"model":"gpt-4o",
		"input":[
			{"type":"custom_tool_call_output","call_id":"call_exec","content":{"ok":true,"files":["a.go"]}}
		]
	}`

	result := ConvertResponsesToOpenAIChatRequest("gpt-4o", []byte(input), false)
	root := gjson.ParseBytes(result)

	if root.Get("messages.0.role").String() != "tool" {
		t.Fatalf("应转换为 tool message，实际为 %s", root.Get("messages.0.role").String())
	}
	if root.Get("messages.0.content").Type != gjson.String {
		t.Fatalf("tool message content 应为字符串，实际为 %s", root.Get("messages.0.content").Type.String())
	}
	if root.Get("messages.0.content").String() != `{"ok":true,"files":["a.go"]}` {
		t.Fatalf("tool message content 不匹配，实际为 %s", root.Get("messages.0.content").String())
	}
}

// chatUsageToResponsesInput 跑一遍非流式转换，返回 usage.input_tokens 与 cached_tokens。
func chatUsageToResponsesInput(t *testing.T, usageJSON string) (int64, int64) {
	t.Helper()
	chatResponse := `{"id":"chatcmpl-usage","object":"chat.completion","created":1,"model":"gpt-4o",` +
		`"choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],` +
		`"usage":` + usageJSON + `}`
	result := ConvertOpenAIChatToResponsesNonStream(
		context.Background(), "gpt-4o",
		[]byte(`{"model":"gpt-4o","input":"Hi"}`), nil, []byte(chatResponse), nil,
	)
	root := gjson.Parse(result)
	return root.Get("usage.input_tokens").Int(), root.Get("usage.input_tokens_details.cached_tokens").Int()
}

// OpenAI 语义 cached_tokens ⊆ prompt_tokens：Responses 出口的 input_tokens 必须是扣除后余量。
func TestConvertOpenAIChatToResponsesNonStream_SubsetCachedSubtracted(t *testing.T) {
	input, cached := chatUsageToResponsesInput(t,
		`{"prompt_tokens":1000,"completion_tokens":20,"total_tokens":1020,"prompt_tokens_details":{"cached_tokens":900}}`)
	if input != 100 {
		t.Errorf("input_tokens 应为 1000-900=100，实际 %d", input)
	}
	if cached != 900 {
		t.Errorf("cached_tokens 应为 900，实际 %d", cached)
	}
}

// 少数兼容网关两套字段一起发：prompt_tokens/cached_tokens 之外还给一个同为总量的
// input_tokens。此时不能让 Claude 分支把已减好的值覆盖回总量，否则客户端求和又双计。
func TestConvertOpenAIChatToResponsesNonStream_MixedStyleKeepsSubtractedInput(t *testing.T) {
	input, _ := chatUsageToResponsesInput(t,
		`{"prompt_tokens":1000,"completion_tokens":20,"input_tokens":1000,"prompt_tokens_details":{"cached_tokens":900}}`)
	if input != 100 {
		t.Errorf("混合形态下 input_tokens 应保持已减值 100，实际 %d（被总量覆盖回去了）", input)
	}
}

// 纯 Anthropic 形态：input_tokens 本就是 uncached 余量，必须原样采用，不得再减。
func TestConvertOpenAIChatToResponsesNonStream_AnthropicStyleNotSubtracted(t *testing.T) {
	input, _ := chatUsageToResponsesInput(t,
		`{"input_tokens":100,"output_tokens":20,"cache_read_input_tokens":900}`)
	if input != 100 {
		t.Errorf("input_tokens 应保持 100（不得双减），实际 %d", input)
	}
}

func TestConvertOpenAIChatToResponses_CustomToolCallEventConsistency(t *testing.T) {
	// 回归：自定义工具经 chat 上游流式转换时，同一调用曾出现 added/delta 为
	// function_call 事件、done/completed 为 custom_tool_call 事件的混发，内容安全
	// 流式检查按"同一工具调用的参数类型发生变化"中断整个响应。
	// 修复后全链路必须对同一调用只发一种类型的事件。
	ctx := context.Background()

	sseLines := []string{
		`data: {"id":"chatcmpl-custom2","object":"chat.completion.chunk","created":1234567890,"model":"gpt-4o","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_exec2","type":"function","function":{"name":"code_exec","arguments":""}}]},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl-custom2","object":"chat.completion.chunk","created":1234567890,"model":"gpt-4o","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"input\":\"ls -la\"}"}}]},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl-custom2","object":"chat.completion.chunk","created":1234567890,"model":"gpt-4o","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`,
	}

	originalReq := []byte(`{"model":"gpt-4o","input":"List files","tools":[{"type":"custom","name":"code_exec","description":"Execute code"}]}`)

	var state any
	var allEvents []string
	for _, line := range sseLines {
		events := ConvertOpenAIChatToResponses(ctx, "gpt-4o", originalReq, nil, []byte(line), &state)
		allEvents = append(allEvents, events...)
	}

	hasCustomAdded := false
	hasCustomInputDone := false
	hasFunctionArgumentsEvent := false
	hasCompletedCustomItem := false

	for _, ev := range allEvents {
		if strings.Contains(ev, "response.output_item.added") && strings.Contains(ev, "\"custom_tool_call\"") {
			hasCustomAdded = true
		}
		if strings.Contains(ev, "response.output_item.added") && strings.Contains(ev, "\"function_call\"") {
			t.Errorf("自定义工具的 output_item.added 不应为 function_call，事件: %s", ev)
		}
		if strings.Contains(ev, "response.function_call_arguments.delta") || strings.Contains(ev, "response.function_call_arguments.done") {
			hasFunctionArgumentsEvent = true
		}
		if strings.Contains(ev, "response.custom_tool_call_input.done") {
			hasCustomInputDone = true
			if !strings.Contains(ev, "\"input\":\"ls -la\"") {
				t.Errorf("custom_tool_call_input.done 应携带还原后的自由文本 input，事件: %s", ev)
			}
		}
		if strings.Contains(ev, "response.completed") && strings.Contains(ev, "\"custom_tool_call\"") {
			hasCompletedCustomItem = true
		}
	}

	if !hasCustomAdded {
		t.Error("自定义工具的 output_item.added 应为 custom_tool_call 事件")
	}
	if hasFunctionArgumentsEvent {
		t.Error("自定义工具不应发出 function_call_arguments.delta/done 事件（与 custom_tool_call 事件类型冲突）")
	}
	if !hasCustomInputDone {
		t.Error("自定义工具应发出 response.custom_tool_call_input.done 事件")
	}
	if !hasCompletedCustomItem {
		t.Error("response.completed 的 output 应包含 custom_tool_call 条目")
	}
}

func TestConvertOpenAIChatToResponses_LateToolNameLocksFunctionType(t *testing.T) {
	// 退化场景：首个 tool_calls 增量未携带工具名（锁定为 function_call），
	// 名称在后续增量才到且实为自定义工具——全链路必须保持 function_call，
	// 不能在收尾阶段混发 custom_tool_call 事件。
	ctx := context.Background()

	sseLines := []string{
		`data: {"id":"chatcmpl-late","object":"chat.completion.chunk","created":1234567890,"model":"gpt-4o","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_late","type":"function","function":{"name":"","arguments":""}}]},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl-late","object":"chat.completion.chunk","created":1234567890,"model":"gpt-4o","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"name":"code_exec","arguments":"{\"input\":\"hi\"}"}}]},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl-late","object":"chat.completion.chunk","created":1234567890,"model":"gpt-4o","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`,
	}

	originalReq := []byte(`{"model":"gpt-4o","input":"Run","tools":[{"type":"custom","name":"code_exec","description":"Execute code"}]}`)

	var state any
	var allEvents []string
	for _, line := range sseLines {
		events := ConvertOpenAIChatToResponses(ctx, "gpt-4o", originalReq, nil, []byte(line), &state)
		allEvents = append(allEvents, events...)
	}

	for _, ev := range allEvents {
		if strings.Contains(ev, "custom_tool_call") {
			t.Errorf("锁定 function_call 后不应再出现 custom_tool_call 事件，事件: %s", ev)
		}
	}
	if !hasEvent(allEvents, "response.function_call_arguments.done") {
		t.Error("锁定 function_call 后应以 function_call_arguments.done 收尾")
	}
}

func hasEvent(events []string, needle string) bool {
	for _, ev := range events {
		if strings.Contains(ev, needle) {
			return true
		}
	}
	return false
}
