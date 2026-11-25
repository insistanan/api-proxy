package proxycore

import "testing"

func TestExtractLatestUserPrompt(t *testing.T) {
	for _, test := range []struct {
		name, kind, body, want string
	}{
		{
			name: "Responses 超过三轮后取旁路输入",
			kind: "responses",
			body: `{"input":[{"role":"user","content":"共同起点"},{"role":"user","content":"第二轮"},{"role":"user","content":"第三轮"},{"role":"user","content":[{"type":"input_text","text":"检查旁路问题"}]},{"role":"assistant","content":[{"type":"output_text","text":"助手回答"}]},{"type":"function_call_output","output":"工具输出"}]}`,
			want: "检查旁路问题",
		},
		{
			name: "Messages 跳过用户角色内的工具结果",
			kind: "messages",
			body: `{"messages":[{"role":"user","content":"检查主线问题"},{"role":"assistant","content":"调用工具"},{"role":"user","content":[{"type":"tool_result","content":"工具结果不能成为摘要"}]}]}`,
			want: "检查主线问题",
		},
		{
			name: "Chat 跳过工具和助手消息",
			kind: "chat",
			body: `{"messages":[{"role":"user","content":"旧问题"},{"role":"user","content":"新问题"},{"role":"tool","content":"工具结果"},{"role":"assistant","content":"助手回答"}]}`,
			want: "新问题",
		},
		{
			name: "Gemini 默认用户角色与混合内容",
			kind: "gemini",
			body: `{"contents":[{"role":"user","parts":[{"text":"旧问题"}]},{"parts":[{"inlineData":{"data":"image"}},{"text":"推理","thought":true},{"text":"新问题"}]},{"role":"model","parts":[{"text":"回答"}]}]}`,
			want: "新问题",
		},
		{
			name: "Responses 字符串输入",
			kind: "responses",
			body: `{"input":"  单条输入  "}`,
			want: "单条输入",
		},
		{
			name: "Responses 默认用户消息",
			kind: "responses",
			body: `{"input":[{"type":"message","content":[{"type":"input_text","text":"默认用户输入"}]}]}`,
			want: "默认用户输入",
		},
		{
			name: "只有工具输出时没有用户摘要",
			kind: "responses",
			body: `{"input":[{"type":"function_call_output","content":"工具文本","output":"输出"}]}`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := extractLatestUserPrompt(test.kind, []byte(test.body))
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("输入摘要 = %q，期望 %q", got, test.want)
			}
		})
	}
}

func TestExtractLatestUserPromptRejectsInvalidJSON(t *testing.T) {
	if _, err := extractLatestUserPrompt("responses", []byte(`{"input":`)); err == nil {
		t.Fatal("格式错误必须返回明确错误")
	}
}
