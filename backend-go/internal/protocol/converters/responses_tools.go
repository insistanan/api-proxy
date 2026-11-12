// Responses 工具契约：工具类型、上下文收集与输入输出规范化。
package converters

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/insistanan/api-proxy/internal/infra/utils"
	"github.com/insistanan/api-proxy/internal/protocol/types"
)

type responseToolKind string

const (
	responseToolKindFunction   responseToolKind = "function"
	responseToolKindCustom     responseToolKind = "custom"
	responseToolKindToolSearch responseToolKind = "tool_search"
)

type responseToolSpec struct {
	Kind         responseToolKind
	OriginalName string
	Namespace    string
	Execution    string
}

type customToolSpec = responseToolSpec

func (s responseToolSpec) OriginalNameOr(fallback string) string {
	if strings.TrimSpace(s.OriginalName) != "" {
		return strings.TrimSpace(s.OriginalName)
	}
	return strings.TrimSpace(fallback)
}

func (s responseToolSpec) ExecutionOrDefault() string {
	if strings.TrimSpace(s.Execution) != "" {
		return strings.TrimSpace(s.Execution)
	}
	return "client"
}

func buildResponseToolContextFromRawRequest(raw []byte) map[string]responseToolSpec {
	if len(raw) == 0 {
		return nil
	}

	var request map[string]interface{}
	if err := json.Unmarshal(raw, &request); err != nil {
		return nil
	}
	return buildResponseToolContext(request["tools"], request["input"])
}

func buildCustomToolContextFromResponsesTools(raw interface{}) map[string]customToolSpec {
	return buildResponseToolContext(raw, nil)
}

func buildResponseToolContext(toolsRaw interface{}, inputRaw interface{}) map[string]responseToolSpec {
	context := map[string]responseToolSpec{}
	collectResponseToolContextFromTools(context, toolsRaw)
	collectResponseToolContextFromToolSearchOutputs(context, inputRaw)
	if len(context) == 0 {
		return nil
	}
	return context
}

func collectResponseToolContextFromTools(context map[string]responseToolSpec, raw interface{}) {
	tools, err := normalizeMapSlice(raw)
	if err != nil || len(tools) == 0 {
		return
	}

	for _, tool := range tools {
		spec := normalizeResponsesToolDefinition(tool)
		switch spec.ToolType {
		case "", "function":
			if spec.Name == "" {
				continue
			}
			context[spec.Name] = responseToolSpec{
				Kind:         responseToolKindFunction,
				OriginalName: spec.Name,
			}
		case "custom":
			name, _ := tool["name"].(string)
			name = strings.TrimSpace(name)
			if name == "" {
				name = spec.Name
			}
			if name == "" {
				continue
			}
			context[name] = responseToolSpec{
				Kind:         responseToolKindCustom,
				OriginalName: name,
			}
		case "tool_search":
			execution, _ := tool["execution"].(string)
			context[openAIChatToolSearchName] = responseToolSpec{
				Kind:         responseToolKindToolSearch,
				OriginalName: openAIChatToolSearchName,
				Execution:    execution,
			}
		case "namespace":
			namespace, _ := tool["name"].(string)
			namespace = strings.TrimSpace(namespace)
			if namespace == "" {
				continue
			}
			rawChildren := tool["tools"]
			if rawChildren == nil {
				rawChildren = tool["children"]
			}
			children, err := normalizeMapSlice(rawChildren)
			if err != nil {
				continue
			}
			for _, child := range children {
				childSpec := normalizeResponsesToolDefinition(child)
				if childSpec.ToolType != "" && childSpec.ToolType != "function" {
					continue
				}
				if childSpec.Name == "" {
					continue
				}
				context[flattenNamespaceToolName(namespace, childSpec.Name)] = responseToolSpec{
					Kind:         responseToolKindFunction,
					OriginalName: childSpec.Name,
					Namespace:    namespace,
				}
			}
		}
	}
}

func collectResponseToolContextFromToolSearchOutputs(context map[string]responseToolSpec, raw interface{}) {
	switch value := raw.(type) {
	case []interface{}:
		for _, item := range value {
			collectResponseToolContextFromToolSearchOutputs(context, item)
		}
	case map[string]interface{}:
		itemType, _ := value["type"].(string)
		if itemType == "tool_search_output" {
			collectResponseToolContextFromTools(context, value["tools"])
		}
		for _, nested := range value {
			collectResponseToolContextFromToolSearchOutputs(context, nested)
		}
	case []types.ResponsesItem:
		for _, item := range value {
			collectResponseToolContextFromToolSearchOutputs(context, item)
		}
	case types.ResponsesItem:
		if value.Type == "tool_search_output" {
			collectResponseToolContextFromTools(context, value.Tools)
		}
	}
}

func parseToolSearchArguments(arguments string) interface{} {
	arguments = strings.TrimSpace(arguments)
	if arguments == "" {
		return map[string]interface{}{}
	}

	var parsed interface{}
	if err := json.Unmarshal([]byte(arguments), &parsed); err == nil {
		return parsed
	}
	return map[string]interface{}{"query": arguments}
}

// extractTextFromContent 从 content 中提取文本内容
// 支持三种格式：
// 1. string - 直接返回
// 2. []ContentBlock - 提取 input_text/output_text 类型的 text 字段
// 3. []interface{} - 动态解析为 ContentBlock
func extractTextFromContent(content interface{}) string {
	// 1. 如果是 string，直接返回
	if str, ok := content.(string); ok {
		return str
	}

	// 2. 如果是 []ContentBlock（已解析类型）
	if blocks, ok := content.([]types.ContentBlock); ok {
		texts := []string{}
		for _, block := range blocks {
			if block.Type == "input_text" || block.Type == "output_text" {
				texts = append(texts, block.Text)
			}
		}
		return strings.Join(texts, "\n")
	}

	// 3. 如果是 []interface{}（未解析类型）
	if arr, ok := content.([]interface{}); ok {
		texts := []string{}
		for _, c := range arr {
			if block, ok := c.(map[string]interface{}); ok {
				blockType, _ := block["type"].(string)
				if blockType == "input_text" || blockType == "output_text" {
					if text, ok := block["text"].(string); ok {
						texts = append(texts, text)
					}
				}
			}
		}
		return strings.Join(texts, "\n")
	}

	return ""
}

type normalizedResponsesToolDefinition struct {
	ToolType    string
	Name        string
	Description string
	Parameters  interface{}
	Strict      interface{}
	Function    map[string]interface{}
}

func copyToolField(dst, src map[string]interface{}, key string) {
	if value, ok := src[key]; ok {
		dst[key] = value
	}
}

func normalizeResponsesToolDefinition(tool map[string]interface{}) normalizedResponsesToolDefinition {
	spec := normalizedResponsesToolDefinition{
		Function: map[string]interface{}{},
	}
	spec.ToolType, _ = tool["type"].(string)
	spec.ToolType = strings.TrimSpace(spec.ToolType)

	if nested, ok := tool["function"].(map[string]interface{}); ok {
		for k, v := range nested {
			spec.Function[k] = v
		}
	} else {
		copyToolField(spec.Function, tool, "name")
		copyToolField(spec.Function, tool, "description")
		copyToolField(spec.Function, tool, "parameters")
		copyToolField(spec.Function, tool, "strict")
	}

	if spec.ToolType == "custom" {
		spec.Function = map[string]interface{}{
			"name":        extractNamedToolChoice(tool),
			"description": customResponsesToolDescription(tool),
			"parameters":  customResponsesToolParameters(tool),
		}
	}

	if _, ok := spec.Function["parameters"]; !ok || spec.Function["parameters"] == nil {
		spec.Function["parameters"] = emptyOpenAIChatToolParameters()
	}

	spec.Name, _ = spec.Function["name"].(string)
	spec.Name = strings.TrimSpace(spec.Name)
	spec.Description, _ = spec.Function["description"].(string)
	spec.Parameters = spec.Function["parameters"]
	spec.Strict = spec.Function["strict"]
	return spec
}

func extractNamedToolChoice(raw map[string]interface{}) string {
	name, _ := raw["name"].(string)
	if name != "" {
		return strings.TrimSpace(name)
	}
	if fn, ok := raw["function"].(map[string]interface{}); ok {
		if name, _ := fn["name"].(string); name != "" {
			return strings.TrimSpace(name)
		}
	}
	if tool, ok := raw["tool"].(map[string]interface{}); ok {
		if name, _ := tool["name"].(string); name != "" {
			return strings.TrimSpace(name)
		}
	}
	return ""
}

func isResponsesToolOutputType(itemType string) bool {
	return itemType == "function_call_output" || itemType == "custom_tool_call_output" || itemType == "tool_search_output"
}

func stringifyResponsesToolOutput(content interface{}) string {
	if content == nil {
		return ""
	}
	if text, ok := content.(string); ok {
		return text
	}
	if blocks := utils.NormalizeContentBlocks(content); len(blocks) > 0 {
		texts := make([]string, 0, len(blocks))
		for _, block := range blocks {
			if text, ok := utils.ExtractTextFromBlock(block); ok {
				texts = append(texts, text)
			}
		}
		if len(texts) > 0 {
			return strings.Join(texts, "\n")
		}
	}
	contentJSON, err := utils.MarshalJSONNoEscape(content)
	if err != nil {
		return fmt.Sprint(content)
	}
	return string(contentJSON)
}

func extractCustomToolInput(arguments string) string {
	arguments = strings.TrimSpace(arguments)
	if arguments == "" {
		return ""
	}

	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(arguments), &payload); err != nil {
		return arguments
	}
	input, _ := payload["input"].(string)
	if input == "" {
		return arguments
	}
	return input
}

func customToolArgumentsJSON(item types.ResponsesItem) string {
	var input interface{}
	if text := extractTextFromContent(item.Content); text != "" {
		input = text
	} else if item.Content != nil {
		// custom_tool_call 的 input 也可能是结构化值；不能只取文本，
		// 否则回灌到 Chat/Claude 时会把整个工具输入变成空字符串。
		input = item.Content
	} else {
		input = strings.TrimSpace(item.Arguments)
	}
	payload, err := json.Marshal(map[string]interface{}{"input": input})
	if err != nil {
		return "{}"
	}
	return string(payload)
}
