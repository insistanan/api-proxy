package types

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// ResponsesItem Responses API 消息项
type ResponsesItem struct {
	// Extra 保留尚未理解的原生字段，落盘和重放不裁剪扩展业务内容。
	Extra     map[string]json.RawMessage `json:"-"`
	ID        string                     `json:"id,omitempty"`
	Type      string                     `json:"type"` // message, text, function_call, tool_call, tool_result
	Status    string                     `json:"status,omitempty"`
	Role      string                     `json:"role,omitempty"` // user, assistant (用于 type=message)
	Content   interface{}                `json:"content,omitempty"`
	Summary   interface{}                `json:"summary,omitempty"`
	ToolUse   *ToolUse                   `json:"tool_use,omitempty"`
	CallID    string                     `json:"call_id,omitempty"`
	Name      string                     `json:"name,omitempty"`
	Arguments string                     `json:"arguments,omitempty"`
	Tools     interface{}                `json:"tools,omitempty"`
	Namespace string                     `json:"namespace,omitempty"`
	Execution string                     `json:"execution,omitempty"`
	// Responses reasoning/tool item 可能携带不透明的完整性材料，必须在会话持久化
	// 和客户端重放匹配过程中保留。
	EncryptedContent interface{} `json:"encrypted_content,omitempty"`
	Signature        string      `json:"signature,omitempty"`
	// 原生 web_search_call 的搜索、打开页面或页内查找动作须完整保留。
	Action interface{} `json:"action,omitempty"`
}

// UnmarshalJSON 保留 Responses 扩展 item 的真实字段形态。
// 尤其是 custom_tool_call 的 input 与 function_call_output 的 output，不能
// 只依赖 content；否则响应转 Claude/Chat 及 previous_response 指纹都会把
// 工具参数或工具结果变成空值。
func (i *ResponsesItem) UnmarshalJSON(data []byte) error {
	if i == nil {
		return fmt.Errorf("ResponsesItem 不能为空")
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if raw == nil {
		return fmt.Errorf("Responses item 必须是对象")
	}
	*i = ResponsesItem{}
	for _, field := range []struct {
		name   string
		target *string
	}{
		{"id", &i.ID}, {"type", &i.Type}, {"status", &i.Status}, {"role", &i.Role},
		{"call_id", &i.CallID}, {"name", &i.Name}, {"namespace", &i.Namespace},
		{"execution", &i.Execution}, {"signature", &i.Signature},
	} {
		if value, ok := raw[field.name]; ok {
			if err := json.Unmarshal(value, field.target); err != nil {
				return fmt.Errorf("Responses item %s 必须是字符串: %w", field.name, err)
			}
			delete(raw, field.name)
		}
	}
	if i.Type == "" && i.Role != "" {
		i.Type = "message"
	}
	for _, field := range []struct {
		name   string
		target *interface{}
	}{
		{"content", &i.Content}, {"summary", &i.Summary}, {"tools", &i.Tools},
		{"encrypted_content", &i.EncryptedContent}, {"action", &i.Action},
	} {
		if value, ok := raw[field.name]; ok {
			if err := json.Unmarshal(value, field.target); err != nil {
				return err
			}
			delete(raw, field.name)
		}
	}
	// 只解释已知工具的字段，未知原生工具的 input/output/arguments 原样留在 Extra。
	contentKey := ""
	if i.Type == "custom_tool_call" {
		contentKey = "input"
	}
	if responsesItemUsesOutputField(i.Type) {
		contentKey = "output"
	}
	if value, ok := raw[contentKey]; ok {
		if err := json.Unmarshal(value, &i.Content); err != nil {
			return err
		}
		delete(raw, contentKey)
	}
	if value, ok := raw["arguments"]; ok {
		switch i.Type {
		case "function_call", "tool_call":
			if err := json.Unmarshal(value, &i.Arguments); err != nil {
				return fmt.Errorf("Responses function arguments 必须是字符串: %w", err)
			}
			delete(raw, "arguments")
		case "tool_search_call":
			// 对象是原生 wire 格式；内部字符串由同一编码器在输出时还原。
			if len(bytes.TrimSpace(value)) == 0 || bytes.TrimSpace(value)[0] != '{' {
				return fmt.Errorf("Responses tool_search_call arguments 必须是对象")
			}
			i.Arguments = string(value)
			delete(raw, "arguments")
		}
	}
	if value, ok := raw["tool_use"]; ok {
		if err := json.Unmarshal(value, &i.ToolUse); err != nil {
			return err
		}
		delete(raw, "tool_use")
	}
	if len(raw) > 0 {
		i.Extra = raw
	}
	return nil
}

// ParseResponsesItem 供普通响应、请求历史和流式快照共用同一解码契约。
func ParseResponsesItem(value interface{}) (ResponsesItem, error) {
	var item ResponsesItem
	data, err := json.Marshal(value)
	if err != nil {
		return item, fmt.Errorf("编码 Responses item 失败: %w", err)
	}
	if err := json.Unmarshal(data, &item); err != nil {
		return item, err
	}
	if item.Type == "" {
		return item, fmt.Errorf("Responses item 缺少 type")
	}
	return item, nil
}

func (i ResponsesItem) MarshalJSON() ([]byte, error) {
	out := make(map[string]interface{}, len(i.Extra)+12)
	for key, value := range i.Extra {
		out[key] = value
	}
	out["type"] = i.Type
	if i.ID != "" {
		out["id"] = i.ID
	}
	if i.Status != "" {
		out["status"] = i.Status
	}
	if i.Role != "" {
		out["role"] = i.Role
	}
	if i.Content != nil && i.Type != "custom_tool_call" && !responsesItemUsesOutputField(i.Type) {
		out["content"] = i.Content
	}
	if i.Summary != nil {
		out["summary"] = i.Summary
	}
	if i.ToolUse != nil {
		out["tool_use"] = i.ToolUse
	}
	if i.CallID != "" {
		out["call_id"] = i.CallID
	}
	if i.Name != "" && i.Type != "tool_search_call" {
		out["name"] = i.Name
	}
	if i.Tools != nil {
		out["tools"] = i.Tools
	}
	if i.Namespace != "" {
		out["namespace"] = i.Namespace
	}
	if i.Execution != "" {
		out["execution"] = i.Execution
	}
	if i.EncryptedContent != nil {
		out["encrypted_content"] = i.EncryptedContent
	}
	if i.Signature != "" {
		out["signature"] = i.Signature
	}
	if i.Action != nil {
		out["action"] = i.Action
	}

	switch i.Type {
	case "custom_tool_call":
		if i.Content != nil {
			out["input"] = i.Content
		}
	case "function_call_output", "custom_tool_call_output", "tool_search_output", "tool_result":
		if i.Content != nil {
			out["output"] = i.Content
		}
	case "tool_search_call":
		if i.Arguments != "" {
			var parsed map[string]interface{}
			if err := json.Unmarshal([]byte(i.Arguments), &parsed); err != nil || parsed == nil {
				return nil, fmt.Errorf("Responses tool_search_call arguments 必须是 JSON 对象")
			}
			out["arguments"] = json.RawMessage(i.Arguments)
		}
	default:
		if i.Arguments != "" {
			out["arguments"] = i.Arguments
		}
	}

	return json.Marshal(out)
}

func responsesItemUsesOutputField(itemType string) bool {
	switch itemType {
	case "function_call_output", "custom_tool_call_output", "tool_search_output", "tool_result":
		return true
	default:
		return false
	}
}
