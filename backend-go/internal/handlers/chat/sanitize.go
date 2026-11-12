package chat

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/insistanan/api-proxy/internal/protocol/types"
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

	sanitizeOpenAIChatModalities(payload)

	return nil
}

// sanitizeOpenAIChatModalities 归一化 modalities 字段：对象格式 → 数组格式。
// OpenAI Chat Completions API 规范要求 modalities 为数组（["text", "audio"]），
// 但部分客户端发送对象格式 { "text": true, "audio": true }，直接透传会导致上游 400。
func sanitizeOpenAIChatModalities(payload map[string]interface{}) {
	raw, exists := payload["modalities"]
	if !exists || raw == nil {
		return
	}
	if _, ok := raw.([]interface{}); ok {
		return
	}
	obj, ok := raw.(map[string]interface{})
	if !ok {
		return
	}
	out := make([]interface{}, 0, 2)
	if enabled, _ := obj["text"].(bool); enabled {
		out = append(out, "text")
	}
	if enabled, _ := obj["audio"].(bool); enabled {
		out = append(out, "audio")
	}
	if len(out) > 0 {
		payload["modalities"] = out
	} else {
		delete(payload, "modalities")
	}
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
		message["role"] = normalizeOpenAIChatRoleForUpstream(role)
	}
	return nil
}

func normalizeOpenAIChatRoleForUpstream(role string) string {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "developer":
		return "system"
	case "system", "user", "assistant", "tool":
		return strings.ToLower(strings.TrimSpace(role))
	default:
		return "user"
	}
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
		name, _ := tool["name"].(string)
		name = strings.TrimSpace(name)
		if name == "" {
			return nil, fmt.Errorf("Chat tool 第 %d 项缺少 function.name", index)
		}
		return map[string]interface{}{
			"type": "function",
			"function": map[string]interface{}{
				"name":        name,
				"description": customChatToolDescription(tool),
				"parameters":  customChatToolParameters(tool),
			},
		}, nil
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
	case "function", "custom":
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

func customChatToolParameters(tool map[string]interface{}) interface{} {
	if parameters, ok := tool["parameters"]; ok && parameters != nil {
		return parameters
	}
	if inputSchema, ok := tool["input_schema"]; ok && inputSchema != nil {
		return inputSchema
	}
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"input": map[string]interface{}{
				"type": "string",
			},
		},
		"required":             []string{"input"},
		"additionalProperties": false,
	}
}

func customChatToolDescription(tool map[string]interface{}) string {
	description, _ := tool["description"].(string)
	metadata, err := json.Marshal(tool)
	if err != nil {
		return description
	}
	metadataText := strings.TrimSpace(string(metadata))
	if metadataText == "" || metadataText == "{}" {
		return description
	}
	if strings.TrimSpace(description) == "" {
		return "Original tool definition:\n" + metadataText
	}
	return description + "\n\nOriginal tool definition:\n" + metadataText
}

func buildChatWebSearchTool(tool map[string]interface{}) map[string]interface{} {
	description, _ := tool["description"].(string)
	if strings.TrimSpace(description) == "" {
		description = "Search the web for relevant information."
	}

	parameters := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"query": map[string]interface{}{
				"type":        "string",
				"description": "Search query to run on the web.",
			},
		},
		"required": []string{"query"},
	}
	if rawParameters, ok := tool["parameters"].(map[string]interface{}); ok && rawParameters != nil {
		parameters = rawParameters
	}

	return map[string]interface{}{
		"type": "function",
		"function": map[string]interface{}{
			"name":        chatWebSearchProxyName,
			"description": description,
			"parameters":  parameters,
		},
	}
}

func emptyOpenAIChatToolParameters() map[string]interface{} {
	return map[string]interface{}{
		"type":       "object",
		"properties": map[string]interface{}{},
	}
}
