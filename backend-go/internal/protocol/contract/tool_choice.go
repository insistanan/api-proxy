package contract

// ToolChoice 校验强制调用约束与本次有效声明一致，不能通过删除约束修补请求。
// 调用者先将 namespace 等协议专用名称转换为声明中的名称。
func ToolChoice(choice interface{}, names map[string]bool) error {
	kind, name := "", ""
	switch value := choice.(type) {
	case string:
		kind = value
	case map[string]interface{}:
		kind, _ = value["type"].(string)
		name, _ = value["name"].(string)
		if function, ok := value["function"].(map[string]interface{}); ok {
			name, _ = function["name"].(string)
		}
	}
	if (kind == "required" || kind == "any") && len(names) == 0 {
		return Invalid("强制 tool_choice 要求至少一个有效工具声明")
	}
	if kind == "tool" || kind == "function" || kind == "custom" {
		if name == "" || !names[name] {
			return Invalid("tool_choice 指定了未声明工具 %q", name)
		}
	}
	return nil
}
