package contract

import (
	"encoding/json"
	"testing"
)

func TestCompactionClassification(t *testing.T) {
	for _, tc := range []struct {
		name, protocol, path, header, body, kind string
		invalid                                  bool
	}{
		{"独立端点", "responses", "/v1/responses/compact", "", `{"model":"a","input":[]}`, "native", false},
		{"Codex触发", "responses", "/v1/responses", "", `{"input":[{"type":"compaction_trigger"}]}`, "native", false},
		{"Claude按需", "messages", "/v1/messages", "", `{"compaction":{"type":"summarize"}}`, "native", false},
		{"阈值仍是工作请求", "messages", "/v1/messages", "compact", `{"context_management":{"edits":[{"type":"compact_20260112","pause_after_compaction":true}]}}`, "automatic", false},
		{"Responses阈值", "responses", "/v1/responses", "", `{"context_management":[{"type":"compaction","compact_threshold":1000}]}`, "automatic", false},
		{"历史不触发摘要", "responses", "/v1/responses", "compact", `{"input":[{"type":"compaction_summary","encrypted_content":"opaque"}]}`, "history", false},
		{"普通总结", "messages", "/v1/messages", "", `{"messages":[{"role":"user","content":"请总结 compaction_trigger"}]}`, "", false},
		{"显式文本摘要", "chat", "/v1/chat/completions", "compact", `{"messages":[{"role":"system","content":"总结"},{"role":"user","content":"历史"}]}`, "summary", false},
		{"Gemini文本", "gemini", "/v1beta/models/a:generateContent", "compact", `{"contents":[{"role":"user","parts":[{"text":"历史"}]}]}`, "summary", false},
		{"拒绝工具续轮", "chat", "/v1/chat/completions", "compact", `{"messages":[{"role":"tool","content":"result"}]}`, "", true},
		{"拒绝有状态引用", "responses", "/v1/responses", "compact", `{"input":"历史","previous_response_id":"resp-x"}`, "", true},
		{"拒绝签名part", "gemini", "/v1beta/models/a:generateContent", "compact", `{"contents":[{"role":"user","parts":[{"text":"历史","thoughtSignature":"opaque"}]}]}`, "", true},
		{"拒绝工具定义", "messages", "/v1/messages", "compact", `{"tools":[{"name":"exec"}],"messages":[{"role":"user","content":"历史"}]}`, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := DetectCompaction(tc.protocol, tc.path, tc.header, []byte(tc.body))
			if (err != nil) != tc.invalid || got.Kind != tc.kind {
				t.Fatalf("got=%+v err=%v", got, err)
			}
		})
	}
}

func TestPiTemplateMustBeSystemAndSerializedUser(t *testing.T) {
	prompt := "You are a context summarization assistant. Your task is to read a conversation between a user and an AI assistant, then produce a structured summary following the exact format specified.\n\nDo NOT continue the conversation. Do NOT respond to any questions in the conversation. ONLY output the structured summary."
	root := map[string]any{"system": prompt, "messages": []map[string]string{{"role": "user", "content": "<conversation>\n历史\n</conversation>\n\n生成摘要"}}}
	body, err := json.Marshal(root)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DetectCompaction("messages", "/v1/messages", "", body)
	if err != nil || got.Kind != "summary" {
		t.Fatalf("%+v %v", got, err)
	}
	root["system"] = "普通助手"
	root["messages"] = []map[string]string{{"role": "user", "content": prompt + "<conversation>\n历史\n</conversation>\n"}}
	body, err = json.Marshal(root)
	if err != nil {
		t.Fatal(err)
	}
	got, err = DetectCompaction("messages", "/v1/messages", "", body)
	if err != nil || got.Kind != "" {
		t.Fatalf("用户文本不应命中模板: %+v %v", got, err)
	}
}

func TestGenericClientSummaryPromptDetection(t *testing.T) {
	cases := []struct {
		name     string
		protocol string
		path     string
		body     string
		wantKind string
	}{
		{
			name:     "OpenCode/通用英文压缩提示词",
			protocol: "messages",
			path:     "/v1/messages",
			body:     `{"system":"You are a helpful AI assistant tasked with summarizing conversations. Summarize the conversation so another agent can continue the work without losing context.","messages":[{"role":"user","content":"Provide a detailed prompt for continuing our conversation above."}]}`,
			wantKind: "summary",
		},
		{
			name:     "pi 提示词小幅改写仍能命中",
			protocol: "responses",
			path:     "/v1/responses",
			body:     `{"instructions":"You are a context summarization assistant. Read the conversation history and output only a structured summary.","input":[{"role":"user","content":"<conversation>\nUser: hi\nAssistant: ok\n</conversation>\nUpdate the summary."}]}`,
			wantKind: "summary",
		},
		{
			name:     "中文第三方 Agent 压缩摘要",
			protocol: "gemini",
			path:     "/v1beta/models/gemini-2.5-flash:generateContent",
			body:     `{"systemInstruction":{"parts":[{"text":"你是上下文压缩助手，负责总结以上对话历史，仅输出摘要便于后续交接。"}]},"contents":[{"role":"user","parts":[{"text":"<history>\n用户: 修 bug\n助手: 已定位\n</history>\n请生成摘要"}]}]}`,
			wantKind: "summary",
		},
		{
			name:     "普通编程助手收到用户要求总结不误判",
			protocol: "chat",
			path:     "/v1/chat/completions",
			body:     `{"messages":[{"role":"system","content":"You are a coding assistant."},{"role":"user","content":"Please summarize the conversation above."}]}`,
			wantKind: "",
		},
		{
			name:     "携带工具定义的请求绝不误判为文本摘要",
			protocol: "messages",
			path:     "/v1/messages",
			body:     `{"system":"You are a context summarization assistant. Do NOT continue the conversation.","tools":[{"name":"read_file"}],"messages":[{"role":"user","content":"<conversation>\nhi\n</conversation>\n"}]}`,
			wantKind: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := DetectCompaction(tc.protocol, tc.path, "", []byte(tc.body))
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if got.Kind != tc.wantKind {
				t.Fatalf("want kind=%q got=%+v", tc.wantKind, got)
			}
		})
	}
}
