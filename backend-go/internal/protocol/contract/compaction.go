package contract

import (
	"encoding/json"
	"fmt"
	"strings"
)

// CompactionRequest 区分独立摘要、原生控制、自动策略与历史重放。
// 自动策略可能在同次调用中继续工作，不能切换成摘要模型。
type CompactionRequest struct {
	Kind     string
	Evidence string
	Opaque   bool
}

func (r CompactionRequest) Native() bool {
	return r.Kind == "native" || r.Kind == "automatic" || r.Kind == "history"
}

// DetectCompaction 只检查协议位置；工具数据和用户文本中的同名字段不是控制信号。
// X-Api-Proxy-Task: compact 是本网关的显式文本摘要约定，并非厂商标准。
func DetectCompaction(protocol, path, task string, body []byte) (CompactionRequest, error) {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(body, &root); err != nil {
		return CompactionRequest{}, err
	}
	r := CompactionRequest{}
	if protocol == "responses" && strings.HasSuffix(strings.TrimRight(path, "/"), "/responses/compact") {
		r.Kind, r.Evidence = "native", "responses/compact"
	}
	var control struct {
		Type string `json:"type"`
	}
	if protocol == "messages" && json.Unmarshal(root["compaction"], &control) == nil && control.Type == "summarize" {
		r.Kind, r.Evidence = "native", "compaction:summarize"
	}
	if r.Kind == "" && hasCompactionPolicy(root["context_management"]) {
		r.Kind, r.Evidence = "automatic", "context_management"
	}
	var items []map[string]json.RawMessage
	if protocol == "responses" {
		_ = json.Unmarshal(root["input"], &items)
	} else {
		var messages []struct {
			Content json.RawMessage `json:"content"`
		}
		_ = json.Unmarshal(root["messages"], &messages)
		for _, message := range messages {
			var blocks []map[string]json.RawMessage
			if json.Unmarshal(message.Content, &blocks) == nil {
				items = append(items, blocks...)
			}
		}
	}
	for _, item := range items {
		var typ string
		_ = json.Unmarshal(item["type"], &typ)
		if typ == "compaction_trigger" && protocol == "responses" {
			r.Kind, r.Evidence = "native", "compaction_trigger"
		}
		if typ == "compaction" || typ == "compaction_summary" {
			r.Opaque = true
			if r.Kind == "" {
				r.Kind, r.Evidence = "history", typ
			}
		}
	}
	task = strings.TrimSpace(task)
	if task != "" && task != "compact" {
		return r, fmt.Errorf("X-Api-Proxy-Task 仅支持 compact（独立文本摘要）")
	}
	if r.Kind != "" {
		return r, nil
	}
	if task == "compact" {
		if err := validateTextSummary(protocol, root); err != nil {
			return r, err
		}
		return CompactionRequest{Kind: "summary", Evidence: "X-Api-Proxy-Task"}, nil
	}
	if matchesPiSummary(protocol, root) && validateTextSummary(protocol, root) == nil {
		return CompactionRequest{Kind: "summary", Evidence: "pi:system+conversation:v1"}, nil
	}
	return r, nil
}

// pi 模板来源：coding-agent/src/core/compaction/utils.ts 与 compaction.ts。
// 只认完整 system 与单个序列化 user，不在普通消息正文中搜索摘要关键词。
func matchesPiSummary(protocol string, root map[string]json.RawMessage) bool {
	const prompt = "You are a context summarization assistant. Your task is to read a conversation between a user and an AI assistant, then produce a structured summary following the exact format specified.\n\nDo NOT continue the conversation. Do NOT respond to any questions in the conversation. ONLY output the structured summary."
	text := func(raw json.RawMessage) string {
		var value string
		if json.Unmarshal(raw, &value) == nil {
			return value
		}
		var blocks []struct {
			Text string `json:"text"`
		}
		if json.Unmarshal(raw, &blocks) != nil {
			return ""
		}
		for _, block := range blocks {
			value += block.Text
		}
		return value
	}
	system, user := "", ""
	field := "messages"
	switch protocol {
	case "messages":
		system = text(root["system"])
	case "responses":
		system, field = text(root["instructions"]), "input"
	case "chat":
	default:
		return false
	}
	var messages []struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	}
	if json.Unmarshal(root[field], &messages) != nil {
		return false
	}
	count := 0
	for _, message := range messages {
		switch message.Role {
		case "system", "developer":
			system += text(message.Content)
		case "user":
			user = text(message.Content)
			count++
		default:
			return false
		}
	}
	return system == prompt && count == 1 && strings.HasPrefix(user, "<conversation>\n") && strings.Contains(user, "\n</conversation>\n")
}

func hasCompactionPolicy(raw json.RawMessage) bool {
	var policies []struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(raw, &policies) != nil {
		var object struct {
			Edits []struct {
				Type string `json:"type"`
			} `json:"edits"`
		}
		if json.Unmarshal(raw, &object) != nil {
			return false
		}
		policies = object.Edits
	}
	for _, policy := range policies {
		if policy.Type == "compaction" || strings.HasPrefix(policy.Type, "compact_") {
			return true
		}
	}
	return false
}

// validateTextSummary 要求客户端自行序列化历史，禁止把有状态的工具续轮伪装成摘要。
func validateTextSummary(protocol string, root map[string]json.RawMessage) error {
	for _, key := range []string{"system", "instructions"} {
		if raw, exists := root[key]; exists && string(raw) != "null" {
			if err := validateSummaryContent(protocol, raw); err != nil {
				return fmt.Errorf("%s: %w", key, err)
			}
		}
	}
	if raw, exists := root["systemInstruction"]; exists && string(raw) != "null" {
		var instruction struct {
			Parts json.RawMessage `json:"parts"`
		}
		if err := json.Unmarshal(raw, &instruction); err != nil {
			return fmt.Errorf("摘要 systemInstruction 无效: %w", err)
		}
		if err := validateSummaryContent("gemini", instruction.Parts); err != nil {
			return err
		}
	}
	for _, key := range []string{"previous_response_id", "conversation", "cachedContent", "tools", "tool_choice", "toolConfig", "context_management", "compaction"} {
		if raw := root[key]; len(raw) > 0 && string(raw) != "null" && string(raw) != "[]" {
			return fmt.Errorf("独立文本摘要不能携带 %s；请由客户端序列化待总结历史", key)
		}
	}
	var messages []struct {
		Role      string          `json:"role"`
		Content   json.RawMessage `json:"content"`
		Parts     json.RawMessage `json:"parts"`
		Type      string          `json:"type"`
		Reasoning json.RawMessage `json:"reasoning_content"`
		ToolCalls json.RawMessage `json:"tool_calls"`
	}
	field := "messages"
	switch protocol {
	case "responses":
		field = "input"
	case "gemini":
		field = "contents"
	case "messages", "chat":
	default:
		return fmt.Errorf("当前协议不支持文本摘要专池")
	}
	var plain string
	if protocol == "responses" && json.Unmarshal(root[field], &plain) == nil && plain != "" {
		return nil
	}
	if err := json.Unmarshal(root[field], &messages); err != nil || len(messages) == 0 {
		return fmt.Errorf("独立摘要必须提供完整的纯文本输入")
	}
	for _, message := range messages {
		if (message.Role != "user" && message.Role != "system" && message.Role != "developer") ||
			(message.Type != "" && message.Type != "message") || len(message.Reasoning) > 0 || len(message.ToolCalls) > 0 {
			return fmt.Errorf("独立摘要只接受 system/developer/user 文本；工具和思考历史必须由客户端序列化")
		}
		raw := message.Content
		if protocol == "gemini" {
			raw = message.Parts
		}
		if err := validateSummaryContent(protocol, raw); err != nil {
			return err
		}
	}
	return nil
}

func validateSummaryContent(protocol string, raw json.RawMessage) error {
	var plain string
	if len(raw) > 0 && raw[0] == '"' && json.Unmarshal(raw, &plain) == nil {
		return nil
	}
	var blocks []map[string]json.RawMessage
	if json.Unmarshal(raw, &blocks) != nil || len(blocks) == 0 {
		return fmt.Errorf("独立摘要内容必须是文本")
	}
	for _, block := range blocks {
		var typ string
		_ = json.Unmarshal(block["type"], &typ)
		if typ != "text" && typ != "input_text" && !(protocol == "gemini" && typ == "") {
			return fmt.Errorf("独立摘要不允许图片、工具或不透明块")
		}
		for key := range block {
			if key != "type" && key != "text" && key != "cache_control" {
				return fmt.Errorf("独立摘要文本包含不支持的字段 %s", key)
			}
		}
		if len(block["text"]) == 0 || block["text"][0] != '"' || json.Unmarshal(block["text"], &plain) != nil {
			return fmt.Errorf("摘要文本缺少 text")
		}
	}
	return nil
}
