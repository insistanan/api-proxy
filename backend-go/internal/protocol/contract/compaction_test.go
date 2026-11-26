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
