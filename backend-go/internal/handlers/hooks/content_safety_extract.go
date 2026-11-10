// 五协议内容安全片段提取，保留文本来源、工具身份和可改写路径。
package hooks

import (
	"fmt"
	"strings"
)

func extractMessagesSafetySegments(root map[string]interface{}) []SafetySegment {
	segments := make([]SafetySegment, 0, 8)
	appendRootTextSegments(&segments, "messages", safetySourceSystem, "system", root, "system")
	// 先扫描全部 assistant tool_use，建立 tool_use_id -> tool_name 映射，
	// 供后续 user tool_result 块按 tool_use_id 反查工具名。
	toolNameByID := make(map[string]string)
	messages, _ := root["messages"].([]interface{})
	for _, rawMessage := range messages {
		message, ok := rawMessage.(map[string]interface{})
		if !ok {
			continue
		}
		if strings.ToLower(stringValue(message["role"])) != "assistant" {
			continue
		}
		blocks, _ := message["content"].([]interface{})
		for _, rawBlock := range blocks {
			block, ok := rawBlock.(map[string]interface{})
			if !ok || !equalString(block["type"], "tool_use") {
				continue
			}
			id := stringValue(block["id"])
			name := stringValue(block["name"])
			if id != "" && name != "" {
				toolNameByID[id] = name
			}
		}
	}
	for messageIndex, rawMessage := range messages {
		message, ok := rawMessage.(map[string]interface{})
		if !ok {
			continue
		}
		role := strings.ToLower(stringValue(message["role"]))
		content, exists := message["content"]
		if !exists {
			continue
		}
		path := fmt.Sprintf("messages[%d].content", messageIndex)
		if role == "assistant" {
			blocks, _ := content.([]interface{})
			for blockIndex, rawBlock := range blocks {
				block, ok := rawBlock.(map[string]interface{})
				if !ok || !equalString(block["type"], "tool_use") {
					continue
				}
				toolName := stringValue(block["name"])
				if input, exists := block["input"]; exists {
					appendMutableJSONValue(&segments, "messages", safetySourceToolArgument,
						fmt.Sprintf("%s[%d].input", path, blockIndex), input, toolName, func(masked interface{}) { block["input"] = masked })
				}
			}
			appendKnownTextContent(&segments, "messages", safetySourceAssistant, path, content, func(value string) {
				message["content"] = value
			})
			continue
		}
		if role != "user" {
			continue
		}
		if text, ok := content.(string); ok {
			appendMapStringSegment(&segments, "messages", safetySourceUser, path, message, "content", text)
			continue
		}
		blocks, _ := content.([]interface{})
		for blockIndex, rawBlock := range blocks {
			block, ok := rawBlock.(map[string]interface{})
			if !ok {
				continue
			}
			blockPath := fmt.Sprintf("%s[%d]", path, blockIndex)
			switch stringValue(block["type"]) {
			case "tool_result":
				toolName := toolNameByID[stringValue(block["tool_use_id"])]
				if value, exists := block["content"]; exists {
					appendMutableJSONValue(&segments, "messages", safetySourceToolResult, blockPath+".content", value, toolName, func(masked interface{}) { block["content"] = masked })
				}
			case "", "text", "input_text":
				appendMapTextField(&segments, "messages", safetySourceUser, blockPath, block)
			}
		}
	}
	return segments
}

func extractChatSafetySegments(root map[string]interface{}) []SafetySegment {
	segments := make([]SafetySegment, 0, 8)
	messages, _ := root["messages"].([]interface{})
	toolNameByID := make(map[string]string)
	for _, rawMessage := range messages {
		message, _ := rawMessage.(map[string]interface{})
		if !equalString(message["role"], "assistant") {
			continue
		}
		calls, _ := message["tool_calls"].([]interface{})
		for _, rawCall := range calls {
			call, _ := rawCall.(map[string]interface{})
			function, _ := call["function"].(map[string]interface{})
			if id := stringValue(call["id"]); id != "" {
				toolNameByID[id] = stringValue(function["name"])
			}
		}
	}
	for messageIndex, rawMessage := range messages {
		message, ok := rawMessage.(map[string]interface{})
		if !ok {
			continue
		}
		role := strings.ToLower(stringValue(message["role"]))
		source := ""
		switch role {
		case "user":
			source = safetySourceUser
		case "system", "developer":
			source = safetySourceSystem
		case "tool", "function":
			source = safetySourceToolResult
		case "assistant":
			appendChatMessageToolArguments(&segments, "messages", messageIndex, message)
			source = safetySourceAssistant
		default:
			continue
		}
		content, exists := message["content"]
		if !exists {
			continue
		}
		path := fmt.Sprintf("messages[%d].content", messageIndex)
		if source == safetySourceToolResult {
			toolName := stringValue(message["name"])
			if toolName == "" {
				toolName = toolNameByID[stringValue(message["tool_call_id"])]
			}
			appendMutableJSONValue(&segments, "chat", source, path, content, toolName, func(masked interface{}) { message["content"] = masked })
			continue
		}
		appendKnownTextContent(&segments, "chat", source, path, content, func(value string) {
			message["content"] = value
		})
	}
	return segments
}

func extractResponsesSafetySegments(root map[string]interface{}) []SafetySegment {
	segments := make([]SafetySegment, 0, 8)
	appendRootTextSegments(&segments, "responses", safetySourceSystem, "instructions", root, "instructions")
	input, exists := root["input"]
	if !exists {
		return segments
	}
	if text, ok := input.(string); ok {
		appendMapStringSegment(&segments, "responses", safetySourceUser, "input", root, "input", text)
		return segments
	}
	items, _ := input.([]interface{})
	// 先扫描所有 function_call / custom_tool_call / tool_search_call，建立
	// call_id -> tool_name 映射，供后续 output 项按 call_id 反查工具名。
	toolNameByCallID := make(map[string]string)
	for _, rawItem := range items {
		item, ok := rawItem.(map[string]interface{})
		if !ok {
			continue
		}
		itemType := strings.ToLower(stringValue(item["type"]))
		if itemType != "function_call" && itemType != "custom_tool_call" && itemType != "tool_search_call" {
			continue
		}
		callID := stringValue(item["call_id"])
		toolName := stringValue(item["name"])
		if callID != "" && toolName != "" {
			toolNameByCallID[callID] = toolName
		}
	}
	for itemIndex, rawItem := range items {
		item, ok := rawItem.(map[string]interface{})
		if !ok {
			continue
		}
		itemType := strings.ToLower(stringValue(item["type"]))
		path := fmt.Sprintf("input[%d]", itemIndex)
		switch itemType {
		case "function_call_output", "custom_tool_call_output", "tool_search_output":
			toolName := toolNameByCallID[stringValue(item["call_id"])]
			if output, exists := item["output"]; exists {
				appendMutableJSONValue(&segments, "responses", safetySourceToolResult, path+".output", output, toolName, func(masked interface{}) { item["output"] = masked })
			}
			continue
		case "function_call", "custom_tool_call", "tool_search_call":
			toolName := stringValue(item["name"])
			if arguments, exists := item["arguments"]; exists {
				appendMutableJSONValue(&segments, "responses", safetySourceToolArgument, path+".arguments", arguments, toolName, func(masked interface{}) { item["arguments"] = masked })
			} else if input, exists := item["input"]; exists {
				appendMutableJSONValue(&segments, "responses", safetySourceToolArgument, path+".input", input, toolName, func(masked interface{}) { item["input"] = masked })
			}
			continue
		case "input_text":
			appendMapTextField(&segments, "responses", safetySourceUser, path, item)
			continue
		}
		role := strings.ToLower(stringValue(item["role"]))
		source := ""
		switch role {
		case "user":
			source = safetySourceUser
		case "system", "developer":
			source = safetySourceSystem
		case "assistant":
			source = safetySourceAssistant
		default:
			continue
		}
		if content, exists := item["content"]; exists {
			appendKnownTextContent(&segments, "responses", source, path+".content", content, func(value string) {
				item["content"] = value
			})
		}
	}
	return segments
}

func extractGeminiSafetySegments(root map[string]interface{}) []SafetySegment {
	segments := make([]SafetySegment, 0, 8)
	if instruction, ok := root["systemInstruction"].(map[string]interface{}); ok {
		appendGeminiTextParts(&segments, safetySourceSystem, "systemInstruction.parts", instruction["parts"])
	}
	contents, _ := root["contents"].([]interface{})
	for contentIndex, rawContent := range contents {
		content, ok := rawContent.(map[string]interface{})
		if !ok {
			continue
		}
		role := strings.ToLower(stringValue(content["role"]))
		parts, _ := content["parts"].([]interface{})
		for partIndex, rawPart := range parts {
			part, ok := rawPart.(map[string]interface{})
			if !ok {
				continue
			}
			path := fmt.Sprintf("contents[%d].parts[%d]", contentIndex, partIndex)
			if functionResponse, exists := part["functionResponse"]; exists {
				toolName := ""
				if response, ok := functionResponse.(map[string]interface{}); ok {
					toolName = stringValue(response["name"])
					if value, exists := response["response"]; exists {
						appendMutableJSONValue(&segments, "gemini", safetySourceToolResult, path+".functionResponse.response", value, toolName, func(masked interface{}) { response["response"] = masked })
					}
				}
				continue
			}
			if functionCall, exists := part["functionCall"]; exists {
				toolName := ""
				if call, ok := functionCall.(map[string]interface{}); ok {
					toolName = stringValue(call["name"])
					if args, exists := call["args"]; exists {
						appendMutableJSONValue(&segments, "gemini", safetySourceToolArgument, path+".functionCall.args", args, toolName, func(masked interface{}) { call["args"] = masked })
					}
				}
				continue
			}
			if role == "" || role == "user" {
				appendMapTextField(&segments, "gemini", safetySourceUser, path, part)
			} else if role == "model" || role == "assistant" {
				appendMapTextField(&segments, "gemini", safetySourceAssistant, path, part)
			}
		}
	}
	return segments
}

// extractImagesSafetySegments 提取 Images API（/v1/images/generations|edits|variations）
// 请求体里的用户文本。Images 载荷只有 prompt 一个自由文本字段：model / n / size /
// quality / style / response_format 等都是枚举或数值，user 是终端用户标识而非正文，
// 因此这里不做投机式的字段清单扫描，只覆盖 prompt（含被拆成字符串数组的写法）。
//
// 注意本函数只覆盖 JSON 载荷；multipart/form-data 形态（/v1/images/edits 与
// variations 的常用写法）走请求前 Hook 的 multipart 分支，见 content_safety_multipart.go。
func extractImagesSafetySegments(root map[string]interface{}) []SafetySegment {
	segments := make([]SafetySegment, 0, 1)
	appendRootTextSegments(&segments, "images", safetySourceUser, "prompt", root, "prompt")
	return segments
}

func extractResponseToolArgumentSegments(apiType string, root map[string]interface{}) []SafetySegment {
	segments := make([]SafetySegment, 0, 4)
	switch strings.ToLower(apiType) {
	case "messages":
		blocks, _ := root["content"].([]interface{})
		for index, rawBlock := range blocks {
			block, ok := rawBlock.(map[string]interface{})
			if ok && equalString(block["type"], "tool_use") {
				appendImmutableJSONSegmentWithTool(&segments, apiType, safetySourceToolArgument, fmt.Sprintf("content[%d].input", index), block["input"], stringValue(block["name"]))
			}
		}
	case "chat":
		choices, _ := root["choices"].([]interface{})
		for choiceIndex, rawChoice := range choices {
			choice, _ := rawChoice.(map[string]interface{})
			message, _ := choice["message"].(map[string]interface{})
			calls, _ := message["tool_calls"].([]interface{})
			for callIndex, rawCall := range calls {
				call, _ := rawCall.(map[string]interface{})
				function, _ := call["function"].(map[string]interface{})
				if arguments, exists := function["arguments"]; exists {
					appendImmutableJSONSegmentWithTool(&segments, apiType, safetySourceToolArgument, fmt.Sprintf("choices[%d].message.tool_calls[%d].function.arguments", choiceIndex, callIndex), arguments, stringValue(function["name"]))
				}
			}
			if functionCall, ok := message["function_call"].(map[string]interface{}); ok {
				if arguments, exists := functionCall["arguments"]; exists {
					appendImmutableJSONSegmentWithTool(&segments, apiType, safetySourceToolArgument, fmt.Sprintf("choices[%d].message.function_call.arguments", choiceIndex), arguments, stringValue(functionCall["name"]))
				}
			}
		}
	case "responses":
		outputs, _ := root["output"].([]interface{})
		for index, rawOutput := range outputs {
			output, ok := rawOutput.(map[string]interface{})
			if !ok {
				continue
			}
			switch strings.ToLower(stringValue(output["type"])) {
			case "function_call", "custom_tool_call", "tool_search_call":
				if arguments, exists := output["arguments"]; exists {
					appendImmutableJSONSegmentWithTool(&segments, apiType, safetySourceToolArgument, fmt.Sprintf("output[%d].arguments", index), arguments, stringValue(output["name"]))
				} else if input, exists := output["input"]; exists {
					appendImmutableJSONSegmentWithTool(&segments, apiType, safetySourceToolArgument, fmt.Sprintf("output[%d].input", index), input, stringValue(output["name"]))
				}
			}
		}
	case "gemini":
		candidates, _ := root["candidates"].([]interface{})
		for candidateIndex, rawCandidate := range candidates {
			candidate, _ := rawCandidate.(map[string]interface{})
			content, _ := candidate["content"].(map[string]interface{})
			parts, _ := content["parts"].([]interface{})
			for partIndex, rawPart := range parts {
				part, _ := rawPart.(map[string]interface{})
				if call, ok := part["functionCall"].(map[string]interface{}); ok {
					appendImmutableJSONSegmentWithTool(&segments, apiType, safetySourceToolArgument, fmt.Sprintf("candidates[%d].content.parts[%d].functionCall.args", candidateIndex, partIndex), call["args"], stringValue(call["name"]))
				}
			}
		}
	}
	return segments
}
