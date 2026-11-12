package converters

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/insistanan/api-proxy/internal/protocol/types"
)

func ResponsesToolsToClaudeTools(raw interface{}) (_ []types.ClaudeTool, err error) {
	defer func() {
		if err != nil && !types.IsUnsupportedProtocol(err) {
			err = &types.InvalidRequestError{Err: err}
		}
	}()
	items, err := interfaceSlice(raw)
	if err != nil || len(items) == 0 {
		return nil, err
	}

	tools := make([]types.ClaudeTool, 0, len(items))
	for index, item := range items {
		m, ok := item.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("Responses tools 项必须是对象")
		}
		converted, err := responsesToolToClaudeTools(m, index)
		if err != nil {
			return nil, err
		}
		tools = append(tools, converted...)
	}
	return tools, nil
}

func ResponsesToolChoiceToClaude(raw interface{}) (_ interface{}, err error) {
	defer func() {
		if err != nil && !types.IsUnsupportedProtocol(err) {
			err = &types.InvalidRequestError{Err: err}
		}
	}()
	if raw == nil {
		return nil, nil
	}
	switch v := raw.(type) {
	case string:
		switch v {
		case "auto":
			return map[string]interface{}{"type": "auto"}, nil
		case "required":
			return map[string]interface{}{"type": "any"}, nil
		case "none":
			return map[string]interface{}{"type": "none"}, nil
		default:
			return nil, fmt.Errorf("不支持的 Responses tool_choice: %s", v)
		}
	case map[string]interface{}:
		typ, _ := v["type"].(string)
		switch strings.TrimSpace(typ) {
		case "auto", "":
			return map[string]interface{}{"type": "auto"}, nil
		case "required", "any":
			return map[string]interface{}{"type": "any"}, nil
		case "none":
			return map[string]interface{}{"type": "none"}, nil
		case "function", "custom", "tool", "namespace":
			name := extractNamedToolChoice(v)
			if name == "" {
				return nil, fmt.Errorf("function tool_choice 缺少 name")
			}
			if namespace, _ := v["namespace"].(string); strings.TrimSpace(namespace) != "" {
				name = flattenNamespaceToolName(namespace, name)
			}
			return map[string]interface{}{"type": "tool", "name": name}, nil
		case "tool_search":
			return map[string]interface{}{"type": "tool", "name": "tool_search"}, nil
		case "web_search", "web_search_preview":
			return nil, &types.UnsupportedProtocolError{Err: fmt.Errorf("Claude 转换不支持 Responses 内置 web_search 的服务器执行与回程，请使用原生 Responses 渠道")}
		default:
			return nil, &types.UnsupportedProtocolError{Err: fmt.Errorf("Claude 上游暂不支持 Responses tool_choice type %q", typ)}
		}
	default:
		return nil, fmt.Errorf("不支持的 tool_choice 类型: %T", raw)
	}
}

func responsesToolToClaudeTools(tool map[string]interface{}, index int) ([]types.ClaudeTool, error) {
	spec := normalizeResponsesToolDefinition(tool)
	switch spec.ToolType {
	case "", "function", "custom":
		if spec.Name == "" {
			return nil, fmt.Errorf("Responses function tool 缺少 name")
		}
		return []types.ClaudeTool{buildClaudeTool(spec.Name, spec.Description, spec.Parameters)}, nil
	case "namespace":
		return buildClaudeNamespaceTools(tool, index)
	case "tool_search":
		return []types.ClaudeTool{buildClaudeToolSearchTool()}, nil
	case "web_search", "web_search_preview":
		return nil, &types.UnsupportedProtocolError{Err: fmt.Errorf("Claude 转换不支持 Responses 内置 web_search 的服务器执行与回程，请使用原生 Responses 渠道")}
	default:
		return nil, &types.UnsupportedProtocolError{Err: fmt.Errorf("Claude 上游暂不支持 Responses tool type %q", spec.ToolType)}
	}
}

func buildClaudeNamespaceTools(tool map[string]interface{}, index int) ([]types.ClaudeTool, error) {
	namespace, _ := tool["name"].(string)
	namespace = strings.TrimSpace(namespace)
	if namespace == "" {
		return nil, fmt.Errorf("Claude namespace tool 第 %d 项缺少 name", index)
	}

	rawChildren := tool["tools"]
	if rawChildren == nil {
		rawChildren = tool["children"]
	}
	children, err := normalizeMapSlice(rawChildren)
	if err != nil {
		return nil, fmt.Errorf("Claude namespace tool 第 %d 项 children 非法: %w", index, err)
	}

	out := make([]types.ClaudeTool, 0, len(children))
	for _, child := range children {
		childSpec := normalizeResponsesToolDefinition(child)
		if childSpec.ToolType != "" && childSpec.ToolType != "function" && childSpec.ToolType != "custom" {
			continue
		}
		if childSpec.Name == "" {
			continue
		}
		out = append(out, buildClaudeTool(
			flattenNamespaceToolName(namespace, childSpec.Name),
			childSpec.Description,
			childSpec.Parameters,
		))
	}
	return out, nil
}

func buildClaudeTool(name, description string, inputSchema interface{}) types.ClaudeTool {
	if inputSchema == nil {
		inputSchema = emptyOpenAIChatToolParameters()
	}
	return types.ClaudeTool{
		Name:        name,
		Description: description,
		InputSchema: inputSchema,
	}
}

func buildClaudeToolSearchTool() types.ClaudeTool {
	return types.ClaudeTool{
		Name:        "tool_search",
		Description: "Search and load Codex tools, plugins, connectors, and MCP namespaces for the current task.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"query": map[string]interface{}{
					"type":        "string",
					"description": "Search query for tools or connectors to load.",
				},
				"limit": map[string]interface{}{
					"type":        "integer",
					"description": "Maximum number of tool groups to return.",
				},
			},
			"required": []string{"query"},
		},
	}
}

func buildClaudeWebSearchTool(tool map[string]interface{}) types.ClaudeTool {
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

	return types.ClaudeTool{
		Name:        "web_search",
		Description: description,
		InputSchema: parameters,
	}
}

func validateClaudeThinkingToolChoice(raw interface{}) error {
	if raw == nil {
		return nil
	}
	switch v := raw.(type) {
	case string:
		switch v {
		case "", "auto", "none":
			return nil
		case "required":
			return fmt.Errorf("Claude extended thinking 仅支持 tool_choice auto/none，不支持 required")
		default:
			return fmt.Errorf("不支持的 Responses tool_choice: %s", v)
		}
	case map[string]interface{}:
		typ, _ := v["type"].(string)
		switch typ {
		case "", "auto", "none":
			return nil
		case "function", "tool", "any", "required":
			return fmt.Errorf("Claude extended thinking 仅支持 tool_choice auto/none，不支持指定或强制工具")
		default:
			return fmt.Errorf("Claude extended thinking 不支持 tool_choice type %q", typ)
		}
	default:
		return fmt.Errorf("不支持的 tool_choice 类型: %T", raw)
	}
}

func ResponsesReasoningToClaudeThinking(raw interface{}, maxOutputTokens int) (interface{}, error) {
	if raw == nil {
		return nil, nil
	}
	m, ok := raw.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("reasoning 必须是对象")
	}
	effort, _ := m["effort"].(string)
	effort, err := NormalizeReasoningEffortForConstrainedUpstream(effort)
	if err != nil {
		return nil, fmt.Errorf("Claude extended thinking %w", err)
	}
	if effort == "" || effort == "none" {
		return nil, nil
	}
	if maxOutputTokens > 0 && maxOutputTokens <= 1024 {
		return nil, fmt.Errorf("Claude extended thinking 需要 max_output_tokens 大于 1024")
	}
	budget := ReasoningBudgetTokens(effort, maxOutputTokens)
	if budget <= 0 {
		return nil, fmt.Errorf("Claude extended thinking 无法转换 reasoning.effort=%q", effort)
	}
	return map[string]interface{}{"type": "enabled", "budget_tokens": budget}, nil
}

func claudeContentBlockToResponsesItem(block map[string]interface{}) (types.ResponsesItem, bool, error) {
	blockType, _ := block["type"].(string)
	switch blockType {
	case "text":
		text, _ := block["text"].(string)
		return types.ResponsesItem{
			Type:    "message",
			Status:  "completed",
			Role:    "assistant",
			Content: []interface{}{map[string]interface{}{"type": "output_text", "text": text}},
		}, true, nil
	case "thinking":
		text, _ := block["thinking"].(string)
		if text == "" {
			text, _ = block["text"].(string)
		}
		return types.ResponsesItem{
			Type:   "reasoning",
			Status: "completed",
			Summary: []interface{}{map[string]interface{}{
				"type": "summary_text",
				"text": text,
			}},
		}, true, nil
	case "tool_use":
		id, _ := block["id"].(string)
		name, _ := block["name"].(string)
		args := "{}"
		if input, ok := block["input"]; ok {
			data, err := json.Marshal(input)
			if err != nil {
				return types.ResponsesItem{}, false, fmt.Errorf("序列化 Claude tool_use input 失败: %w", err)
			}
			args = string(data)
		}
		return types.ResponsesItem{
			ID:        fmt.Sprintf("fc_%s", id),
			Type:      "function_call",
			Status:    "completed",
			CallID:    id,
			Name:      name,
			Arguments: args,
		}, true, nil
	default:
		return types.ResponsesItem{}, false, nil
	}
}

func responsesItemToClaudeToolMessage(item types.ResponsesItem) (*types.ClaudeMessage, error) {
	switch item.Type {
	case "function_call", "custom_tool_call", "tool_search_call":
		callID := item.CallID
		if callID == "" {
			callID = strings.TrimPrefix(item.ID, "fc_")
			if callID == item.ID {
				callID = strings.TrimPrefix(item.ID, "ctc_")
			}
			if callID == item.ID {
				callID = strings.TrimPrefix(item.ID, "tsc_")
			}
		}
		var input interface{} = map[string]interface{}{}
		if item.Type == "custom_tool_call" {
			if text := extractTextFromContent(item.Content); text != "" {
				input = map[string]interface{}{"input": text}
			} else if item.Content != nil {
				input = map[string]interface{}{"input": item.Content}
			} else {
				input = map[string]interface{}{"input": strings.TrimSpace(item.Arguments)}
			}
		} else if item.Type == "tool_search_call" {
			name := item.Name
			if strings.TrimSpace(name) == "" {
				name = "tool_search"
			}
			if strings.TrimSpace(item.Arguments) != "" {
				var parsed interface{}
				if err := json.Unmarshal([]byte(item.Arguments), &parsed); err == nil {
					input = parsed
				} else {
					return nil, fmt.Errorf("tool_search arguments 不是合法 JSON: %w", err)
				}
			}
			return &types.ClaudeMessage{Role: "assistant", Content: []map[string]interface{}{{"type": "tool_use", "id": callID, "name": name, "input": input}}}, nil
		} else if item.Arguments != "" {
			var parsed interface{}
			if err := json.Unmarshal([]byte(item.Arguments), &parsed); err == nil {
				input = parsed
			} else {
				return nil, fmt.Errorf("function_call arguments 不是合法 JSON: %w", err)
			}
		}
		return &types.ClaudeMessage{Role: "assistant", Content: []map[string]interface{}{{"type": "tool_use", "id": callID, "name": item.Name, "input": input}}}, nil
	case "tool_call":
		if item.ToolUse == nil {
			return nil, nil
		}
		return &types.ClaudeMessage{Role: "assistant", Content: []map[string]interface{}{{"type": "tool_use", "id": item.ToolUse.ID, "name": item.ToolUse.Name, "input": item.ToolUse.Input}}}, nil
	case "tool_result":
		return &types.ClaudeMessage{Role: "user", Content: []map[string]interface{}{{"type": "tool_result", "tool_use_id": item.CallID, "content": item.Content}}}, nil
	default:
		if isResponsesToolOutputType(item.Type) {
			content := item.Content
			if content == nil && item.Type == "tool_search_output" {
				content = item.Tools
			}
			return &types.ClaudeMessage{Role: "user", Content: []map[string]interface{}{{"type": "tool_result", "tool_use_id": item.CallID, "content": content}}}, nil
		}
		return nil, nil
	}
}

func interfaceSlice(raw interface{}) ([]interface{}, error) {
	if raw == nil {
		return nil, nil
	}
	switch v := raw.(type) {
	case []interface{}:
		return v, nil
	case []map[string]interface{}:
		out := make([]interface{}, len(v))
		for i := range v {
			out[i] = v[i]
		}
		return out, nil
	default:
		return nil, fmt.Errorf("期望数组，实际为 %T", raw)
	}
}
