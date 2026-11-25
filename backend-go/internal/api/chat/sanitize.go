package chat

import (
	"fmt"
	"strings"

	"insistanan/api-proxy/internal/protocol/contract"
	"insistanan/api-proxy/internal/protocol/types"
)

func sanitizeOpenAIChatPayloadForUpstream(payload map[string]interface{}) error {
	if err := sanitizeOpenAIChatMessagesForUpstream(payload); err != nil {
		return err
	}

	toolsRaw, hasToolsField := payload["tools"]
	tools, err := sanitizeOpenAIChatTools(toolsRaw)
	if err != nil {
		return err
	}
	names := make(map[string]bool, len(tools))
	for _, tool := range tools {
		function, _ := tool["function"].(map[string]interface{})
		name, _ := function["name"].(string)
		names[name] = true
	}
	if err := contract.ToolChoice(payload["tool_choice"], names); err != nil {
		return err
	}
	if len(tools) > 0 {
		payload["tools"] = tools
	} else {
		delete(payload, "tools")
	}

	toolChoice, ok, err := sanitizeOpenAIChatToolChoice(payload["tool_choice"])
	if err != nil {
		return err
	}
	if len(tools) > 0 && ok {
		payload["tool_choice"] = toolChoice
	} else {
		delete(payload, "tool_choice")
	}

	if len(tools) == 0 {
		delete(payload, "parallel_tool_calls")
	} else if hasToolsField {
		if _, exists := payload["parallel_tool_calls"]; exists {
			if b, ok := payload["parallel_tool_calls"].(bool); ok {
				payload["parallel_tool_calls"] = b
			}
		}
	}

	return sanitizeOpenAIChatModalities(payload)
}

// sanitizeOpenAIChatModalities 只校验协议形状，原生模态值由上游判断支持范围。
func sanitizeOpenAIChatModalities(payload map[string]interface{}) error {
	raw, exists := payload["modalities"]
	if !exists {
		return nil
	}
	values, ok := raw.([]interface{})
	if !ok {
		return fmt.Errorf("Chat modalities 必须是字符串数组")
	}
	for _, value := range values {
		if text, ok := value.(string); !ok || text == "" {
			return fmt.Errorf("Chat modalities 必须是非空字符串数组")
		}
	}
	return nil
}

func sanitizeOpenAIChatMessagesForUpstream(payload map[string]interface{}) error {
	rawMessages, exists := payload["messages"]
	if !exists || rawMessages == nil {
		return nil
	}

	items, ok := rawMessages.([]interface{})
	if !ok {
		return fmt.Errorf("Chat messages 必须是数组")
	}

	for i, item := range items {
		message, ok := item.(map[string]interface{})
		if !ok {
			return fmt.Errorf("Chat messages 第 %d 项必须是对象", i)
		}
		role, _ := message["role"].(string)
		switch role {
		case "system", "developer", "user", "assistant", "tool":
		default:
			return fmt.Errorf("Chat messages 第 %d 项 role 无效: %q", i, role)
		}
	}
	return nil
}

func sanitizeOpenAIChatTools(raw interface{}) ([]map[string]interface{}, error) {
	if raw == nil {
		return nil, nil
	}
	items, ok := raw.([]interface{})
	if !ok {
		return nil, fmt.Errorf("Chat tools 必须是数组")
	}

	out := make([]map[string]interface{}, 0, len(items))
	for i, item := range items {
		tool, ok := item.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("Chat tools 第 %d 项必须是对象", i)
		}
		normalized, err := sanitizeOpenAIChatTool(tool, i)
		if err != nil {
			return nil, err
		}
		out = append(out, normalized)
	}
	return out, nil
}

func sanitizeOpenAIChatTool(tool map[string]interface{}, index int) (map[string]interface{}, error) {
	toolType, _ := tool["type"].(string)
	toolType = strings.TrimSpace(toolType)
	switch toolType {
	case "", "function":
		function := map[string]interface{}{}
		if nested, ok := tool["function"].(map[string]interface{}); ok {
			for k, v := range nested {
				function[k] = v
			}
		} else {
			copyToolField(function, tool, "name")
			copyToolField(function, tool, "description")
			copyToolField(function, tool, "parameters")
			copyToolField(function, tool, "strict")
		}
		name, _ := function["name"].(string)
		name = strings.TrimSpace(name)
		if name == "" {
			return nil, fmt.Errorf("Chat tool 第 %d 项缺少 function.name", index)
		}
		if _, ok := function["parameters"]; !ok || function["parameters"] == nil {
			function["parameters"] = emptyOpenAIChatToolParameters()
		}
		return map[string]interface{}{
			"type":     "function",
			"function": function,
		}, nil
	case "custom":
		return nil, &types.UnsupportedProtocolError{Err: fmt.Errorf("Chat 原生路径未实现 custom 工具的对称适配，请使用 Responses custom 工具")}
	case "web_search", "web_search_preview":
		return nil, &types.UnsupportedProtocolError{Err: fmt.Errorf("Chat 渠道无法等价执行内置 web_search，请使用原生 Responses 搜索；普通 function 工具仍由客户端执行")}
	default:
		return nil, fmt.Errorf("OpenAI Chat 不支持第 %d 个 tool type %q", index, toolType)
	}
}

func sanitizeOpenAIChatToolChoice(raw interface{}) (interface{}, bool, error) {
	if raw == nil {
		return nil, false, nil
	}
	if choice, ok := raw.(string); ok {
		switch choice {
		case "auto", "none", "required":
			return choice, true, nil
		default:
			return nil, false, fmt.Errorf("OpenAI Chat 不支持 tool_choice=%q", choice)
		}
	}

	obj, ok := raw.(map[string]interface{})
	if !ok {
		return nil, false, fmt.Errorf("Chat tool_choice 必须是字符串或对象")
	}

	choiceType, _ := obj["type"].(string)
	choiceType = strings.TrimSpace(choiceType)
	if choiceType == "" {
		choiceType = "function"
	}
	switch choiceType {
	case "custom":
		return nil, false, &types.UnsupportedProtocolError{Err: fmt.Errorf("Chat custom tool_choice 尚不支持对称适配")}
	case "function":
		name := extractNamedToolChoice(obj)
		if name == "" {
			return nil, false, fmt.Errorf("OpenAI Chat tool_choice 对象缺少 function.name 或 name")
		}
		return map[string]interface{}{
			"type": "function",
			"function": map[string]interface{}{
				"name": name,
			},
		}, true, nil
	case "tool_search":
		return map[string]interface{}{
			"type": "function",
			"function": map[string]interface{}{
				"name": chatToolSearchProxyName,
			},
		}, true, nil
	case "web_search", "web_search_preview":
		return nil, false, &types.UnsupportedProtocolError{Err: fmt.Errorf("Chat 渠道无法等价执行内置 web_search tool_choice，请使用原生 Responses 搜索")}
	case "auto", "none", "required":
		return choiceType, true, nil
	default:
		return nil, false, fmt.Errorf("OpenAI Chat 不支持 tool_choice.type=%q", choiceType)
	}
}

func copyToolField(dst, src map[string]interface{}, key string) {
	if value, ok := src[key]; ok {
		dst[key] = value
	}
}

func extractNamedToolChoice(raw map[string]interface{}) string {
	name, _ := raw["name"].(string)
	if strings.TrimSpace(name) != "" {
		return strings.TrimSpace(name)
	}
	if fn, ok := raw["function"].(map[string]interface{}); ok {
		if nested, _ := fn["name"].(string); strings.TrimSpace(nested) != "" {
			return strings.TrimSpace(nested)
		}
	}
	if tool, ok := raw["tool"].(map[string]interface{}); ok {
		if nested, _ := tool["name"].(string); strings.TrimSpace(nested) != "" {
			return strings.TrimSpace(nested)
		}
	}
	return ""
}

func emptyOpenAIChatToolParameters() map[string]interface{} {
	return map[string]interface{}{
		"type":       "object",
		"properties": map[string]interface{}{},
	}
}
