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
	if evidence := matchClientSummaryPrompt(protocol, root); evidence != "" && validateTextSummary(protocol, root) == nil {
		return CompactionRequest{Kind: "summary", Evidence: evidence}, nil
	}
	return r, nil
}

// matchClientSummaryPrompt 通用识别主流 Coding Agent 发起的“纯文本上下文压缩/摘要”请求。
// 约束：
//  1. 必须满足 validateTextSummary（无工具定义、无工具续轮、无服务端会话链、纯文本）；
//  2. 只检查 system/instructions 中的摘要任务指令，且最后一条 user 消息必须是摘要指令或序列化历史；
//     普通对话里用户说“帮我总结一下”（system 是普通编码助手而非摘要专用）不会命中。
func matchClientSummaryPrompt(protocol string, root map[string]json.RawMessage) string {
	system, users, ok := extractSummaryTextTurns(protocol, root)
	if !ok || len(users) == 0 {
		return ""
	}
	sys := strings.TrimSpace(system)
	lastUser := strings.TrimSpace(users[len(users)-1])

	// 1) pi-agent 历史兼容特征：固定摘要 system + <conversation>...</conversation>
	const piPrompt = "You are a context summarization assistant. Your task is to read a conversation between a user and an AI assistant, then produce a structured summary following the exact format specified.\n\nDo NOT continue the conversation. Do NOT respond to any questions in the conversation. ONLY output the structured summary."
	if sys == piPrompt && len(users) == 1 &&
		strings.HasPrefix(lastUser, "<conversation>\n") && strings.Contains(lastUser, "\n</conversation>\n") {
		return "client:pi:v1"
	}

	// 2) 通用 Agent 压缩识别（OpenCode / Claude Code / Codex / pi 变体 / 第三方 Agent）：
	// system 必须明确把自己限定为“对话摘要/上下文压缩”角色，且不能只是普通对话助手。
	if !isDedicatedSummarySystem(sys) {
		return ""
	}
	if !looksLikeSerializedSummaryInput(lastUser, len(users)) {
		return ""
	}
	return "client:summary-prompt"
}

func extractSummaryTextTurns(protocol string, root map[string]json.RawMessage) (string, []string, bool) {
	extractContent := func(raw json.RawMessage) (string, bool) {
		var plain string
		if json.Unmarshal(raw, &plain) == nil {
			return plain, true
		}
		var blocks []struct {
			Text string `json:"text"`
		}
		if json.Unmarshal(raw, &blocks) != nil {
			return "", false
		}
		var b strings.Builder
		for _, block := range blocks {
			b.WriteString(block.Text)
		}
		return b.String(), true
	}

	var sysBuilder strings.Builder
	appendSystem := func(s string) {
		s = strings.TrimSpace(s)
		if s == "" {
			return
		}
		if sysBuilder.Len() > 0 {
			sysBuilder.WriteByte('\n')
		}
		sysBuilder.WriteString(s)
	}

	var field string
	switch protocol {
	case "messages":
		if s, ok := extractContent(root["system"]); ok {
			appendSystem(s)
		}
		field = "messages"
	case "responses":
		if s, ok := extractContent(root["instructions"]); ok {
			appendSystem(s)
		}
		field = "input"
	case "chat":
		field = "messages"
	case "gemini":
		if raw := root["systemInstruction"]; len(raw) > 0 && string(raw) != "null" {
			var inst struct {
				Parts json.RawMessage `json:"parts"`
			}
			if json.Unmarshal(raw, &inst) == nil {
				if s, ok := extractContent(inst.Parts); ok {
					appendSystem(s)
				}
			}
		}
		field = "contents"
	default:
		return "", nil, false
	}

	if protocol == "responses" {
		var plain string
		if json.Unmarshal(root[field], &plain) == nil && strings.TrimSpace(plain) != "" {
			return sysBuilder.String(), []string{plain}, true
		}
	}

	var messages []struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
		Parts   json.RawMessage `json:"parts"`
	}
	if json.Unmarshal(root[field], &messages) != nil {
		return "", nil, false
	}
	users := make([]string, 0, len(messages))
	for _, msg := range messages {
		raw := msg.Content
		if protocol == "gemini" {
			raw = msg.Parts
		}
		text, ok := extractContent(raw)
		if !ok {
			return "", nil, false
		}
		switch msg.Role {
		case "system", "developer":
			appendSystem(text)
		case "user", "":
			users = append(users, text)
		default:
			return "", nil, false
		}
	}
	return sysBuilder.String(), users, true
}

// isDedicatedSummarySystem 识别专门用于“压缩/总结历史对话”的 system 提示词，
// 要求同时包含角色/任务信号与摘要目标对象信号，避免普通对话助手误命中。
func isDedicatedSummarySystem(system string) bool {
	if system == "" {
		return false
	}
	lower := strings.ToLower(system)

	hasTask := containsAny(lower,
		"summarization assistant",
		"summarize the conversation",
		"summarize this conversation",
		"summarize a conversation",
		"conversation summarization",
		"context summarization",
		"context compaction",
		"compact the conversation",
		"compress the conversation",
		"compaction assistant",
		"detailed summary of the conversation",
		"structured summary",
		"handover summary",
		"handoff summary",
		"对话摘要",
		"上下文压缩",
		"压缩上下文",
		"压缩对话",
		"总结这段对话",
		"总结以上对话",
		"摘要助手",
	)
	if !hasTask {
		return false
	}

	hasGuardOrTarget := containsAny(lower,
		"do not continue",
		"don't continue",
		"do not respond to",
		"only output",
		"output only",
		"previous conversation",
		"conversation history",
		"conversation between",
		"continue the work",
		"resume the",
		"another agent",
		"next session",
		"context window",
		"不要继续对话",
		"仅输出摘要",
		"只输出摘要",
		"对话历史",
		"上下文窗口",
		"交接",
		"接续",
	)
	return hasGuardOrTarget
}

// looksLikeSerializedSummaryInput 检查 user 负载是否是待压缩的历史或摘要触发指令。
func looksLikeSerializedSummaryInput(lastUser string, userTurns int) bool {
	if lastUser == "" {
		return false
	}
	lower := strings.ToLower(lastUser)
	// 显式序列化包裹标签（pi / 常见 agent 序列化格式）
	if containsAny(lower, "<conversation>", "</conversation>", "<transcript>", "</transcript>", "<history>", "</history>", "<summary>") {
		return true
	}
	// 明确要求生成交接/压缩摘要的指令（如 OpenCode / Claude Code / 第三方 Agent）
	if containsAny(lower,
		"provide a detailed prompt for continuing",
		"provide a detailed summary",
		"summarize the conversation",
		"summarize what we",
		"compact the conversation",
		"create a summary",
		"update the summary",
		"write a summary",
		"生成摘要",
		"更新摘要",
		"总结对话",
		"压缩上下文",
	) {
		return true
	}
	// 单轮且包含多段角色回放的序列化文本
	if userTurns == 1 && (strings.Contains(lastUser, "\nUser:") || strings.Contains(lastUser, "\nAssistant:") || strings.Contains(lastUser, "\n[User]") || strings.Contains(lastUser, "\n[Assistant]")) {
		return true
	}
	return false
}

func containsAny(text string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(text, needle) {
			return true
		}
	}
	return false
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
