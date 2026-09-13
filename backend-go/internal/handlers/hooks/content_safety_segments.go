package hooks

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/BenedictKing/api-proxy/internal/config"
	"github.com/BenedictKing/api-proxy/internal/sensitive"
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

func applyContentSafetySegments(
	ctx context.Context,
	metadata HookContext,
	segments []SafetySegment,
	snapshot *contentSafetySnapshot,
	recorder BlockedLogRecorder,
	prompts *[]string,
) (bool, error) {
	vault := metadata.redactionVault
	if vault == nil {
		vault = sensitive.NewVaultWithContext(ctx)
	}
	for index := range segments {
		vault.ReserveText(segments[index].Text)
	}
	if err := vault.Err(); err != nil {
		return false, err
	}
	changed := false
	for index := range segments {
		segment := &segments[index]
		// 白名单放行：命中工具名的 tool_result / tool_argument 跳过全部检测维度，
		// 并记录一条 whitelist 审计事件，让拦截记录页可见"已放行"的条目。
		if isWhitelistedSegment(snapshot, segment) {
			if err := recordWhitelistAllowed(ctx, metadata, recorder, *segment); err != nil {
				return changed, err
			}
			if segment.Mutable && segment.Text != "" {
				segment.replace(segment.Text)
			}
			continue
		}
		// 放行字面量（整段相等）：segment 文本与某条目完全相等时跳过全部检测维度，
		// 写一条 allowlist 审计事件。覆盖"整个字段就是 localhost 这类值"的最常见场景。
		if allowlistSkipsWholeSegment(snapshot.settings.Allowlist, segment.Text) {
			if err := recordAllowlistAllowed(ctx, metadata, recorder, *segment); err != nil {
				return changed, err
			}
			if segment.Mutable && segment.Text != "" {
				segment.replace(segment.Text)
			}
			continue
		}
		if segment.Source == safetySourceUser || segment.Source == safetySourceSystem {
			if match, ok := snapshot.words.FindFirst(segment.Text); ok {
				return changed, &ContentSafetyError{
					BlockType: sensitive.BlockTypeSensitiveWord,
					RuleName:  string(match.Category),
					Snippet: safetyHitSnippet("block", *segment, segment.Text, safetyHit{
						rule:   string(match.Category),
						start:  match.Start,
						end:    match.End,
						reveal: match.Word,
					}),
				}
			}
		}

		credentialMode := snapshot.settings.SensitiveData.Mode

		// credentialSource 是凭据检测时的原文；掩码会改写 segment.Text，
		// 命中偏移只对原文有效，必须先留存。
		credentialSource := segment.Text
		credentialMatches := snapshot.credential.FindAll(segment.Text)
		credentialMatches = filterAllowlistedCredentialMatches(credentialMatches, credentialSource, snapshot.settings.Allowlist)
		if len(credentialMatches) > 0 {
			if credentialMode == config.ContentSafetyModeBlock || credentialMode == config.ContentSafetyModeMask && !segment.Mutable {
				first := credentialMatches[0]
				return changed, &ContentSafetyError{
					BlockType: sensitive.BlockTypeCredential,
					RuleName:  first.Rule,
					Snippet: safetyHitSnippet("block", *segment, credentialSource, safetyHit{
						rule:   first.Rule,
						start:  first.Start,
						end:    first.End,
						reveal: "[凭据已隐藏:" + first.Rule + "]",
					}),
				}
			}
			if credentialMode == config.ContentSafetyModeMask {
				masked, maskErr := vault.Mask(segment.Text, credentialRedactionMatches(credentialMatches))
				if maskErr != nil {
					return changed, maskErr
				}
				segment.Text = masked
				changed = true
			}
			if err := recordSafetyMatches(ctx, metadata, recorder, sensitive.BlockTypeCredential, credentialMode, *segment, credentialSource, credentialHits(credentialMatches)); err != nil {
				return changed, err
			}
		}

		if segment.Source == safetySourceUser || segment.Source == safetySourceSystem || segment.Source == safetySourceAssistant ||
			segment.Source == safetySourceToolResult || segment.Source == safetySourceToolArgument {
			// 敏感信息检测在凭据掩码之后执行，命中偏移相对当前文本；
			// 以此为窗口基准，改写前再留存一份。
			infoSource := segment.Text
			infoMatches := snapshot.info.FindAll(segment.Text)
			infoMatches = filterAllowlistedInfoMatches(infoMatches, infoSource, snapshot.settings.Allowlist)
			if len(infoMatches) > 0 {
				mode := snapshot.settings.SensitiveData.Mode
				if mode == config.ContentSafetyModeBlock || mode == config.ContentSafetyModeMask && !segment.Mutable {
					first := infoMatches[0]
					return changed, &ContentSafetyError{
						BlockType: sensitive.BlockTypeSensitiveInfo,
						RuleName:  first.Rule,
						Snippet: safetyHitSnippet("block", *segment, infoSource, safetyHit{
							rule:   first.Rule,
							start:  first.Start,
							end:    first.End,
							reveal: first.Original,
						}),
					}
				}
				if mode == config.ContentSafetyModeMask {
					masked, maskErr := vault.Mask(segment.Text, infoRedactionMatches(infoMatches))
					if maskErr != nil {
						return changed, maskErr
					}
					segment.Text = masked
					changed = true
				}
				if err := recordSafetyMatches(ctx, metadata, recorder, sensitive.BlockTypeSensitiveInfo, mode, *segment, infoSource, infoHits(infoMatches)); err != nil {
					return changed, err
				}
			}
		}

		// 自定义规则（可逆脱敏，最低优先级）：仅 mask 模式参与，且在凭据/个人信息
		// 之后执行——内置规则先占住各自区间，自定义规则只处理剩余文本；且后续没有
		// 其他掩码步骤，宽正则不会把已生成的占位符再包一层（复检只查 credential/info）。
		// 放行字面量过滤同样生效（命中原文与条目相等则丢弃）。
		if snapshot.settings.SensitiveData.Mode == config.ContentSafetyModeMask && snapshot.custom != nil && snapshot.custom.Enabled() {
			customSource := segment.Text
			customMatches := snapshot.custom.FindAll(segment.Text)
			customMatches = filterAllowlistedCustomMatches(customMatches, snapshot.settings.Allowlist)
			if len(customMatches) > 0 {
				masked, maskErr := vault.Mask(segment.Text, customRedactionMatches(customMatches))
				if maskErr != nil {
					return changed, maskErr
				}
				segment.Text = masked
				changed = true
				if err := recordSafetyMatches(ctx, metadata, recorder, sensitive.BlockTypeCustomRule,
					config.ContentSafetyModeMask, *segment, customSource, customHits(customMatches)); err != nil {
					return changed, err
				}
			}
		}

		if segment.Mutable && segment.Text != "" {
			segment.replace(segment.Text)
		}
		if snapshot.settings.SensitiveData.Mode == config.ContentSafetyModeMask {
			// 复检同样过放行字面量过滤：被放行的值（如 localhost）本来就应留在
			// 文本里，不能被复检误报为"脱敏不干净"。
			if matches := snapshot.credential.FindAll(segment.Text); len(matches) > 0 {
				matches = filterAllowlistedCredentialMatches(matches, segment.Text, snapshot.settings.Allowlist)
				if len(matches) > 0 {
					return changed, fmt.Errorf("凭据脱敏后仍命中规则 %q", matches[0].Rule)
				}
			}
			if matches := snapshot.info.FindAll(segment.Text); len(matches) > 0 {
				matches = filterAllowlistedInfoMatches(matches, segment.Text, snapshot.settings.Allowlist)
				if len(matches) > 0 {
					return changed, fmt.Errorf("敏感信息脱敏后仍命中规则 %q", matches[0].Rule)
				}
			}
		}
		if (segment.Source == safetySourceUser || segment.Source == safetySourceSystem) && strings.TrimSpace(segment.Text) != "" {
			*prompts = append(*prompts, segment.Text)
		}
	}
	return changed, nil
}

func credentialRedactionMatches(matches []sensitive.CredentialMatch) []sensitive.RedactionMatch {
	result := make([]sensitive.RedactionMatch, 0, len(matches))
	for _, match := range matches {
		result = append(result, sensitive.RedactionMatch{
			Type: credentialPlaceholderType(match.Rule), Rule: match.Rule, Original: match.Original,
			Start: match.Start, End: match.End,
		})
	}
	return result
}

func infoRedactionMatches(matches []sensitive.InfoMatch) []sensitive.RedactionMatch {
	result := make([]sensitive.RedactionMatch, 0, len(matches))
	for _, match := range matches {
		kind := map[string]string{
			config.SensitiveInfoRulePhone: "PHONE", config.SensitiveInfoRuleIDCard: "ID_CARD",
			config.SensitiveInfoRuleEmail: "EMAIL", config.SensitiveInfoRuleIPAddress: "PRIVATE_IP",
			config.SensitiveInfoRuleBankCard: "BANK_CARD",
		}[match.Rule]
		result = append(result, sensitive.RedactionMatch{
			Type: kind, Rule: match.Rule, Original: match.Original, Start: match.Start, End: match.End,
		})
	}
	return result
}

func credentialPlaceholderType(rule string) string {
	switch rule {
	case config.CredentialRuleConnectionString:
		return "CONNECTION_STRING"
	case config.CredentialRulePrivateKey:
		return "PRIVATE_KEY"
	case config.CredentialRuleAPIKey:
		return "API_TOKEN"
	case config.CredentialRuleNamedSecret:
		return "SECRET"
	case config.CredentialRuleHighEntropy:
		return "TOKEN"
	default:
		return "CREDENTIAL"
	}
}

// recordSafetyMatches 写入 mask/audit 模式的内容安全事件。sourceText 是命中
// 时的原始片段文本（掩码改写之前），hits 携带每个命中规则的原始字节偏移；
// 凭据命中只落规则名，绝不把明文写入拦截记录。
func recordSafetyMatches(ctx context.Context, metadata HookContext, recorder BlockedLogRecorder, blockType, mode string, segment SafetySegment, sourceText string, hits []safetyHit) error {
	if len(hits) == 0 || mode == "" || mode == config.ContentSafetyModeBlock {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, gateStoreTimeout)
	defer cancel()
	if recorder == nil {
		if mode == config.ContentSafetyModeAudit {
			return fmt.Errorf("内容安全审计模式未配置记录器")
		}
		return nil
	}
	seen := make(map[string]struct{}, len(hits))
	contentHash := sha256.Sum256([]byte(sourceText))
	safeContext := sensitive.RedactAuditContext(sourceText)
	for _, hit := range hits {
		if _, exists := seen[hit.rule]; exists {
			continue
		}
		seen[hit.rule] = struct{}{}
		eventKey := fmt.Sprintf("%s\x00%s\x00%s\x00%s\x00%s\x00%x", blockType, mode, segment.Source, segment.Path, hit.rule, contentHash)
		if metadata.eventDeduper.contains(eventKey) {
			continue
		}
		if _, err := recorder.Record(ctx, sensitive.BlockedLog{
			APIType:        metadata.APIType,
			BlockType:      blockType,
			RuleName:       hit.rule,
			PromptSnippet:  fmt.Sprintf("mode=%s source=%s path=%s\n%s", mode, segment.Source, segment.Path, safetySnippetWindowRedacted(safeContext, hit)),
			ChannelName:    metadata.ChannelName,
			Model:          metadata.Model,
			RequestID:      metadata.RequestID,
			ConversationID: metadata.ConversationID,
		}); err != nil {
			return fmt.Errorf("写入内容安全%s记录失败: %w", mode, err)
		}
		if err := metadata.eventDeduper.mark(eventKey, metadata.ConversationID == ""); err != nil {
			return err
		}
	}
	return nil
}

// credentialHits 把凭据命中转换为 safetyHit；reveal 只落规则名，明文不落库。
func credentialHits(matches []sensitive.CredentialMatch) []safetyHit {
	hits := make([]safetyHit, 0, len(matches))
	for _, match := range matches {
		hits = append(hits, safetyHit{
			rule:   match.Rule,
			start:  match.Start,
			end:    match.End,
			reveal: "[凭据已隐藏:" + match.Rule + "]",
		})
	}
	return hits
}

// infoHits 把敏感信息命中转换为 safetyHit；reveal 显示命中原文（管理员
// 已确认 PII 明文落库，用于拦截记录页审计定位）。
func infoHits(matches []sensitive.InfoMatch) []safetyHit {
	hits := make([]safetyHit, 0, len(matches))
	for _, match := range matches {
		hits = append(hits, safetyHit{
			rule:   match.Rule,
			start:  match.Start,
			end:    match.End,
			reveal: match.Original,
		})
	}
	return hits
}

// isWhitelistedSegment 判断片段是否命中白名单：仅 tool_result / tool_argument
// 来源、且携带的工具名出现在白名单 ToolNames 中时放行。白名单未启用或工具名
// 为空时一律不放行。
func isWhitelistedSegment(snapshot *contentSafetySnapshot, segment *SafetySegment) bool {
	if snapshot == nil || segment == nil {
		return false
	}
	whitelist := snapshot.settings.Whitelist
	if !whitelist.Enabled || len(whitelist.ToolNames) == 0 {
		return false
	}
	if segment.Source != safetySourceToolResult && segment.Source != safetySourceToolArgument {
		return false
	}
	toolName := strings.TrimSpace(segment.ToolName)
	if toolName == "" {
		return false
	}
	for _, allowed := range whitelist.ToolNames {
		if strings.TrimSpace(allowed) == toolName {
			return true
		}
	}
	return false
}

// recordWhitelistAllowed 写入一条 whitelist 审计事件，让拦截记录页能看见"已放行"
// 的条目。同请求内同工具名 + 同路径去重，避免刷屏。片段展示工具输出的段首窗口，
// 该内容是管理员通过白名单认可的工具输出，直接展示原文。
func recordWhitelistAllowed(ctx context.Context, metadata HookContext, recorder BlockedLogRecorder, segment SafetySegment) error {
	if recorder == nil || metadata.eventDeduper == nil {
		return nil
	}
	toolName := strings.TrimSpace(segment.ToolName)
	if toolName == "" {
		return nil
	}
	eventKey := fmt.Sprintf("%s\x00%s\x00%s\x00%s", sensitive.BlockTypeWhitelist, segment.Source, segment.Path, toolName)
	if metadata.eventDeduper.contains(eventKey) {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, gateStoreTimeout)
	defer cancel()
	if _, err := recorder.Record(ctx, sensitive.BlockedLog{
		APIType:        metadata.APIType,
		BlockType:      sensitive.BlockTypeWhitelist,
		RuleName:       toolName,
		PromptSnippet:  whitelistSnippet(segment),
		ChannelName:    metadata.ChannelName,
		Model:          metadata.Model,
		RequestID:      metadata.RequestID,
		ConversationID: metadata.ConversationID,
	}); err != nil {
		return fmt.Errorf("写入白名单放行记录失败: %w", err)
	}
	return metadata.eventDeduper.mark(eventKey, metadata.ConversationID == "")
}

// safetyHit 表示检测器返回的单个命中。Start/End 是原始片段文本
// （sourceText，掩码改写之前）的字节偏移，End 不包含在命中范围内。
// reveal 是写入拦截记录时该命中位置展示的内容：敏感词与个人信息按
// 管理员决策显示明文，凭据只显示规则名，明文绝不落库。
type safetyHit struct {
	rule   string
	start  int
	end    int
	reveal string
}

// safetySnippetWindowRunes 是命中上下文窗口在命中位置前后各取的字符数。
const safetySnippetWindowRunes = 60

// safetySnippetWindow 从 sourceText 截取以命中位置为中心的上下文窗口，
// 窗口内命中区间替换为 hit.reveal。偏移按 rune 对齐，避免截断多字节字符。
func safetySnippetWindow(sourceText string, hit safetyHit) string {
	return safetySnippetWindowRedacted(sensitive.RedactAuditContext(sourceText), hit)
}

func safetySnippetWindowRedacted(sourceText string, hit safetyHit) string {
	reveal := sensitive.RedactAuditText(hit.reveal)
	if hit.start < 0 || hit.end > len(sourceText) || hit.start >= hit.end {
		return reveal
	}
	windowStart, windowEnd := hit.start, hit.end
	for count := 0; count < safetySnippetWindowRunes && windowStart > 0; count++ {
		_, size := utf8.DecodeLastRuneInString(sourceText[:windowStart])
		windowStart -= size
	}
	for count := 0; count < safetySnippetWindowRunes && windowEnd < len(sourceText); count++ {
		_, size := utf8.DecodeRuneInString(sourceText[windowEnd:])
		windowEnd += size
	}
	var builder strings.Builder
	if windowStart > 0 {
		builder.WriteString("...")
	}
	builder.WriteString(sourceText[windowStart:hit.start])
	builder.WriteString(reveal)
	builder.WriteString(sourceText[hit.end:windowEnd])
	if windowEnd < len(sourceText) {
		builder.WriteString("...")
	}
	return builder.String()
}

// safetyHitSnippet 生成拦截记录的命中片段：第一行保留既有元数据
// （mode/source/path），第二行是命中上下文窗口，便于在拦截记录页
// 直接看到具体命中的内容。
func safetyHitSnippet(mode string, segment SafetySegment, sourceText string, hit safetyHit) string {
	return fmt.Sprintf("mode=%s source=%s path=%s\n%s",
		mode, segment.Source, segment.Path, safetySnippetWindow(sourceText, hit))
}

// whitelistSnippetWindowRunes 是白名单放行事件展示工具输出段首的字符数。
const whitelistSnippetWindowRunes = 120

// whitelistSnippet 生成白名单放行事件片段：元数据行 + 工具输出段首窗口。
func whitelistSnippet(segment SafetySegment) string {
	text := sensitive.RedactAuditText(segment.Text)
	window := text
	count := 0
	for index := range text {
		if count == whitelistSnippetWindowRunes {
			window = text[:index]
			break
		}
		count++
	}
	return fmt.Sprintf("mode=allow source=%s path=%s\n%s", segment.Source, segment.Path, window)
}

func stringValue(value interface{}) string {
	text, _ := value.(string)
	return text
}

func equalString(value interface{}, expected string) bool {
	return strings.EqualFold(stringValue(value), expected)
}
