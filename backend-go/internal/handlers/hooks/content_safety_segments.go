package hooks

import (
	"encoding/json"
	"fmt"
	"strings"
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
	replace  func(string)
}

func extractSafetySegments(apiType string, root map[string]interface{}) ([]SafetySegment, error) {
	switch strings.ToLower(apiType) {
	case "messages":
		return extractMessagesSafetySegments(root), nil
	case "chat":
		return extractChatSafetySegments(root), nil
	case "responses":
		return extractResponsesSafetySegments(root), nil
	case "gemini":
		return extractGeminiSafetySegments(root), nil
	case "images":
		return extractImagesSafetySegments(root), nil
	default:
		return nil, fmt.Errorf("内容安全不支持协议 %q", apiType)
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
		toolName := stringValue(function["name"])
		if arguments, exists := function["arguments"]; exists {
			appendMutableJSONValue(segments, "chat", safetySourceToolArgument,
				fmt.Sprintf("%s.tool_calls[%d].function.arguments", path, callIndex), arguments, toolName, func(masked interface{}) { function["arguments"] = masked })
		}
	}
	if functionCall, ok := message["function_call"].(map[string]interface{}); ok {
		toolName := stringValue(functionCall["name"])
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
		blockType := strings.ToLower(stringValue(block["type"]))
		if blockType == "" || blockType == "text" || blockType == "input_text" {
			appendMapTextField(segments, protocol, source, fmt.Sprintf("%s[%d]", path, index), block)
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
	switch typed := value.(type) {
	case string:
		if replace == nil {
			if decoded, ok := decodeJSONValue([]byte(typed)); ok {
				switch decoded.(type) {
				case map[string]interface{}, []interface{}:
					appendMutableJSONValue(segments, protocol, source, path, decoded, toolName, nil)
					return
				}
			}
			appendSafetySegmentWithTool(segments, protocol, source, path, typed, toolName, nil)
			return
		}
		appendSafetySegmentWithTool(segments, protocol, source, path, typed, toolName, func(masked string) {
			replace(masked)
		})
	case map[string]interface{}:
		for _, key := range sortedMapKeys(typed) {
			field := key
			if replace == nil {
				appendMutableJSONValue(segments, protocol, source, path+"."+field, typed[field], toolName, nil)
				continue
			}
			appendMutableJSONValue(segments, protocol, source, path+"."+field, typed[field], toolName, func(masked interface{}) {
				typed[field] = masked
			})
		}
	case []interface{}:
		for index := range typed {
			itemIndex := index
			if replace == nil {
				appendMutableJSONValue(segments, protocol, source, fmt.Sprintf("%s[%d]", path, index), typed[index], toolName, nil)
				continue
			}
			appendMutableJSONValue(segments, protocol, source, fmt.Sprintf("%s[%d]", path, index), typed[index], toolName, func(masked interface{}) {
				typed[itemIndex] = masked
			})
		}
	default:
		if replace == nil {
			if body, err := json.Marshal(value); err == nil {
				appendSafetySegmentWithTool(segments, protocol, source, path, string(body), toolName, nil)
			}
		}
	}
}

func stringValue(value interface{}) string {
	text, _ := value.(string)
	return text
}

func equalString(value interface{}, expected string) bool {
	return strings.EqualFold(stringValue(value), expected)
}
