package pipeline

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"insistanan/api-proxy/internal/protocol/codec"
	"insistanan/api-proxy/internal/safety/sensitive"
)

const (
	safetySourceUser         = "user"
	safetySourceSystem       = "system"
	safetySourceAssistant    = "assistant"
	safetySourceToolResult   = "tool_result"
	safetySourceToolArgument = "tool_argument"
)

// SafetySegment 是协议请求中一个经过来源分类的文本片段。Mutable 为 false
// 的片段只允许审计或阻断，不会被内容安全逻辑改写。ToolName 标记片段来源的
// 工具名（仅 tool_result / tool_argument 来源可能携带），用于白名单放行判定。
type SafetySegment struct {
	Protocol string
	Source   string
	Path     string
	Text     string
	Mutable  bool
	ToolName string
	Field    string
	// Signed 文本不能改写；命中必须阻断，避免破坏供应商签名。
	Signed  bool
	replace func(string)
	commits []func() error
}

func extractSafetySegments(apiType string, root map[string]interface{}) ([]SafetySegment, error) {
	var segments []SafetySegment
	switch strings.ToLower(apiType) {
	case "messages":
		segments = extractMessagesSafetySegments(root)
	case "chat":
		segments = extractChatSafetySegments(root)
	case "responses":
		segments = extractResponsesSafetySegments(root)
	case "gemini":
		segments = extractGeminiSafetySegments(root)
	case "images":
		segments = extractImagesSafetySegments(root)
	default:
		return nil, fmt.Errorf("内容安全不支持协议 %q", apiType)
	}
	appendToolDefinitionSegments(&segments, apiType, "tools", root["tools"])
	return segments, nil
}

// 工具说明可脱敏；schema 的默认值、示例与枚举只能阻断，避免改变工具契约。
func appendToolDefinitionSegments(segments *[]SafetySegment, protocol, path string, value interface{}) {
	switch typed := value.(type) {
	case []interface{}:
		for i, child := range typed {
			appendToolDefinitionSegments(segments, protocol, fmt.Sprintf("%s[%d]", path, i), child)
		}
	case map[string]interface{}:
		for _, key := range sortedMapKeys(typed) {
			switch key {
			case "description", "title":
				if text, ok := typed[key].(string); ok {
					appendMapStringSegment(segments, protocol, safetySourceSystem, path+"."+key, typed, key, text)
				} else {
					appendToolDefinitionSegments(segments, protocol, path+"."+key, typed[key])
				}
			case "enum", "const", "default", "examples":
				appendImmutableJSONSegment(segments, protocol, safetySourceSystem, path+"."+key, typed[key])
			default:
				appendToolDefinitionSegments(segments, protocol, path+"."+key, typed[key])
			}
		}
	}
}

func appendGeminiTextParts(segments *[]SafetySegment, source, path string, rawParts interface{}) {
	parts, _ := rawParts.([]interface{})
	for index, rawPart := range parts {
		part, ok := rawPart.(map[string]interface{})
		if !ok {
			continue
		}
		appendMapTextField(segments, "gemini", source, fmt.Sprintf("%s[%d]", path, index), part)
	}
}

func appendChatMessageToolArguments(segments *[]SafetySegment, pathPrefix string, messageIndex int, message map[string]interface{}) {
	path := fmt.Sprintf("%s[%d]", pathPrefix, messageIndex)
	toolCalls, _ := message["tool_calls"].([]interface{})
	for callIndex, rawCall := range toolCalls {
		call, _ := rawCall.(map[string]interface{})
		function, _ := call["function"].(map[string]interface{})
		if function == nil {
			continue
		}
		toolName := codec.StringValue(function["name"])
		if arguments, exists := function["arguments"]; exists {
			appendMutableJSONValue(segments, "chat", safetySourceToolArgument,
				fmt.Sprintf("%s.tool_calls[%d].function.arguments", path, callIndex), arguments, toolName, func(masked interface{}) { function["arguments"] = masked })
		}
	}
	if functionCall, ok := message["function_call"].(map[string]interface{}); ok {
		toolName := codec.StringValue(functionCall["name"])
		if arguments, exists := functionCall["arguments"]; exists {
			appendMutableJSONValue(segments, "chat", safetySourceToolArgument,
				path+".function_call.arguments", arguments, toolName, func(masked interface{}) { functionCall["arguments"] = masked })
		}
	}
}

func appendRootTextSegments(segments *[]SafetySegment, protocol, source, path string, root map[string]interface{}, key string) {
	value, exists := root[key]
	if !exists {
		return
	}
	appendKnownTextContent(segments, protocol, source, path, value, func(text string) {
		root[key] = text
	})
}

func appendKnownTextContent(segments *[]SafetySegment, protocol, source, path string, value interface{}, replaceString func(string)) {
	if text, ok := value.(string); ok {
		appendSafetySegment(segments, protocol, source, path, text, replaceString)
		return
	}
	blocks, _ := value.([]interface{})
	for index, rawBlock := range blocks {
		if text, ok := rawBlock.(string); ok {
			blockIndex := index
			appendSafetySegment(segments, protocol, source, fmt.Sprintf("%s[%d]", path, index), text, func(masked string) {
				blocks[blockIndex] = masked
			})
			continue
		}
		block, ok := rawBlock.(map[string]interface{})
		if !ok {
			continue
		}
		blockType := strings.ToLower(codec.StringValue(block["type"]))
		if blockType == "" || blockType == "text" || blockType == "input_text" || blockType == "output_text" || blockType == "summary_text" {
			appendMapTextField(segments, protocol, source, fmt.Sprintf("%s[%d]", path, index), block)
		}
		if blockType == "thinking" {
			if text, ok := block["thinking"].(string); ok {
				appendMapStringSegment(segments, protocol, source, fmt.Sprintf("%s[%d].thinking", path, index), block, "thinking", text)
				if text != "" {
					(*segments)[len(*segments)-1].Signed = block["signature"] != nil
				}
			}
		}
	}
}

func appendMapTextField(segments *[]SafetySegment, protocol, source, path string, value map[string]interface{}) {
	for _, key := range []string{"text", "input_text"} {
		if text, ok := value[key].(string); ok {
			appendMapStringSegment(segments, protocol, source, path+"."+key, value, key, text)
			return
		}
	}
}

func appendMapStringSegment(segments *[]SafetySegment, protocol, source, path string, value map[string]interface{}, key, text string) {
	appendSafetySegment(segments, protocol, source, path, text, func(masked string) {
		value[key] = masked
	})
}

func appendSafetySegment(segments *[]SafetySegment, protocol, source, path, value string, replace func(string)) {
	appendSafetySegmentWithTool(segments, protocol, source, path, value, "", replace)
}

func appendSafetySegmentWithTool(segments *[]SafetySegment, protocol, source, path, value, toolName string, replace func(string)) {
	if value == "" {
		return
	}
	*segments = append(*segments, SafetySegment{
		Protocol: protocol,
		Source:   source,
		Path:     path,
		Text:     value,
		Mutable:  replace != nil,
		ToolName: toolName,
		replace:  replace,
	})
}

func appendImmutableJSONSegment(segments *[]SafetySegment, protocol, source, path string, value interface{}) {
	appendImmutableJSONSegmentWithTool(segments, protocol, source, path, value, "")
}

func appendImmutableJSONSegmentWithTool(segments *[]SafetySegment, protocol, source, path string, value interface{}, toolName string) {
	appendMutableJSONValue(segments, protocol, source, path, value, toolName, nil)
}

func appendMutableJSONValue(
	segments *[]SafetySegment,
	protocol, source, path string,
	value interface{},
	toolName string,
	replace func(interface{}),
) {
	appendJSONSafetyValue(segments, protocol, source, path, "", value, toolName, replace)
}

func appendJSONSafetyValue(segments *[]SafetySegment, protocol, source, path, field string, value interface{}, toolName string, replace func(interface{})) {
	switch typed := value.(type) {
	case string:
		if decoded, ok := decodeSafetyContainer(typed, field); ok {
			switch decoded.(type) {
			case map[string]interface{}, []interface{}:
				start := len(*segments)
				var setDecoded func(interface{})
				if replace != nil {
					setDecoded = func(updated interface{}) { decoded = updated }
				}
				appendJSONSafetyValue(segments, protocol, source, path, field, decoded, toolName, setDecoded)
				if replace != nil && len(*segments) > start {
					last := &(*segments)[len(*segments)-1]
					last.commits = append(last.commits, func() error {
						body, err := codec.MarshalJSONNoEscape(decoded)
						if err != nil {
							return fmt.Errorf("编码脱敏后的嵌套 JSON 失败: %w", err)
						}
						replace(string(body))
						return nil
					})
				}
				return
			}
		}
		if replace == nil {
			appendSafetySegmentWithTool(segments, protocol, source, path, typed, toolName, nil)
		} else {
			appendSafetySegmentWithTool(segments, protocol, source, path, typed, toolName, func(masked string) { replace(masked) })
		}
		if typed != "" {
			(*segments)[len(*segments)-1].Field = field
		}
	case map[string]interface{}:
		for _, key := range sortedMapKeys(typed) {
			field := key
			// 键本身也可能是邮箱或其他已知原值，不能改名破坏工具 schema。
			appendSafetySegmentWithTool(segments, protocol, source, path+".[key]", key, toolName, nil)
			if replace == nil {
				appendJSONSafetyValue(segments, protocol, source, path+"."+field, field, typed[field], toolName, nil)
				continue
			}
			appendJSONSafetyValue(segments, protocol, source, path+"."+field, field, typed[field], toolName, func(masked interface{}) {
				typed[field] = masked
			})
		}
	case []interface{}:
		for index := range typed {
			itemIndex := index
			if replace == nil {
				appendJSONSafetyValue(segments, protocol, source, fmt.Sprintf("%s[%d]", path, index), field, typed[index], toolName, nil)
				continue
			}
			appendJSONSafetyValue(segments, protocol, source, fmt.Sprintf("%s[%d]", path, index), field, typed[index], toolName, func(masked interface{}) {
				typed[itemIndex] = masked
			})
		}
	case json.Number:
		// 用字符串占位符替换数字会改变工具 schema；命中时显式阻断。
		appendSafetySegmentWithTool(segments, protocol, source, path, typed.String(), toolName, nil)
		(*segments)[len(*segments)-1].Field = field
	case float64:
		appendSafetySegmentWithTool(segments, protocol, source, path, strconv.FormatFloat(typed, 'f', -1, 64), toolName, nil)
		(*segments)[len(*segments)-1].Field = field
	}
}

func decodeSafetyContainer(text, field string) (interface{}, bool) {
	trimmed := strings.TrimSpace(text)
	if len(trimmed) < 2 || trimmed[0] != '{' && trimmed[0] != '[' || sensitive.IsCredentialFieldName(field) {
		return nil, false
	}
	return decodeJSONValue([]byte(trimmed))
}

func equalString(value interface{}, expected string) bool {
	return strings.EqualFold(codec.StringValue(value), expected)
}
