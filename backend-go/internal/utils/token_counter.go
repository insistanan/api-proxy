package utils

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode"

	"github.com/BenedictKing/api-proxy/internal/types"
)

const (
	// estimatedImageTokens 是未知尺寸图片/音视频的保守视觉定额。
	// 对齐 Claude 对约 1024² 图的量级，避免把 base64 字符长度当成文本 token。
	estimatedImageTokens = 1600

	// estimatedFileTokens 是 PDF/文件附件的固定定额。
	// 真实页数与解析成本只有上游知道；本地只给一个不会触发 200K 压缩的占位值。
	estimatedFileTokens = 4096
)

// EstimateTokens 估算文本的 token 数量
// 使用字符估算法：
// - 中文/日文/韩文：约 1.5 字符/token
// - 英文及其他：约 3.5 字符/token
func EstimateTokens(text string) int {
	if text == "" {
		return 0
	}

	cjkCount := 0
	otherCount := 0

	for _, r := range text {
		if isCJK(r) {
			cjkCount++
		} else if !unicode.IsSpace(r) {
			otherCount++
		}
	}

	// CJK: ~1.5 字符/token, 其他: ~3.5 字符/token
	cjkTokens := float64(cjkCount) / 1.5
	otherTokens := float64(otherCount) / 3.5

	return int(cjkTokens + otherTokens + 0.5) // 四舍五入
}

// EstimateRequestTokens 从请求体估算输入 token。
// 只统计文本和工具定义；图片/PDF/文件走定额，不把 base64 当成长文本。
func EstimateRequestTokens(bodyBytes []byte) int {
	tokens, _ := analyzeMessagesRequest(bodyBytes)
	return tokens
}

// EstimateResponsesRequestTokens 从 Responses API 请求体估算输入 token。
// 支持 instructions、input (string 或 []item) 格式；媒体块同样走定额。
func EstimateResponsesRequestTokens(bodyBytes []byte) int {
	tokens, _ := analyzeResponsesRequest(bodyBytes)
	return tokens
}

// RequestContainsOpaqueMedia 判断请求是否含图片、PDF、文件等不透明媒体。
// 这类内容的真实 token 只能由上游计量，本地估算不得覆盖客户端 usage。
func RequestContainsOpaqueMedia(bodyBytes []byte) bool {
	if _, opaque := analyzeMessagesRequest(bodyBytes); opaque {
		return true
	}
	_, opaque := analyzeResponsesRequest(bodyBytes)
	return opaque
}

func analyzeMessagesRequest(bodyBytes []byte) (int, bool) {
	if len(bodyBytes) == 0 {
		return 0, false
	}

	var req map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &req); err != nil {
		tokens, opaque := estimateString(string(bodyBytes))
		return tokens, opaque
	}

	total := 0
	opaque := false

	if system, ok := req["system"]; ok {
		tokens, hasMedia := estimateValue(system)
		total += tokens
		opaque = opaque || hasMedia
	}

	if messages, ok := req["messages"]; ok {
		tokens, hasMedia := estimateMessagesTokens(messages)
		total += tokens
		opaque = opaque || hasMedia
	}

	if tools, ok := req["tools"].([]interface{}); ok {
		total += len(tools) * 150
	}

	return total, opaque
}

// EstimateGeminiRequestTokens 从 Gemini 原生请求体估算输入 token。
// 支持直接输入和 generateContentRequest 包装；请求格式错误时显式返回错误。
// inlineData、fileData 走定额，工具参数和返回值按 JSON 内容计数。
func EstimateGeminiRequestTokens(bodyBytes []byte) (int, error) {
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(bodyBytes, &envelope); err != nil {
		return 0, fmt.Errorf("解析 Gemini 计数请求失败: %w", err)
	}
	if envelope == nil {
		return 0, fmt.Errorf("Gemini 计数请求必须是 JSON 对象")
	}
	// 与原生接口一致：包装请求存在时使用其中的输入，不重复统计顶层 contents。
	if wrapped, ok := envelope["generateContentRequest"]; ok {
		bodyBytes = wrapped
	}

	// 用现有协议类型验证已知字段，但估算保留原始 JSON，避免工具 Schema 清洗
	// 或协议类型未声明的新字段改变计数内容。
	var validated *types.GeminiRequest
	if err := json.Unmarshal(bodyBytes, &validated); err != nil {
		return 0, fmt.Errorf("Gemini 计数请求字段格式错误: %w", err)
	}
	if validated == nil {
		return 0, fmt.Errorf("Gemini 计数输入必须是 JSON 对象")
	}
	if validated.Contents == nil && validated.SystemInstruction == nil && validated.Tools == nil {
		return 0, fmt.Errorf("Gemini 计数请求必须包含 contents、systemInstruction 或 tools")
	}

	var req map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &req); err != nil {
		return 0, fmt.Errorf("解析 Gemini 计数输入失败: %w", err)
	}

	total := 0

	if system, ok := req["systemInstruction"]; ok && system != nil {
		tokens, err := estimateGeminiContentTokens(system)
		if err != nil {
			return 0, fmt.Errorf("systemInstruction 格式错误: %w", err)
		}
		total += tokens
	}

	if contents, ok := req["contents"].([]interface{}); ok {
		for i, content := range contents {
			tokens, err := estimateGeminiContentTokens(content)
			if err != nil {
				return 0, fmt.Errorf("contents[%d] 格式错误: %w", i, err)
			}
			total += 4
			total += tokens
		}
	}

	if tools, ok := req["tools"].([]interface{}); ok {
		for i, tool := range tools {
			if obj, ok := tool.(map[string]interface{}); !ok || len(obj) == 0 {
				return 0, fmt.Errorf("tools[%d] 必须是非空 JSON 对象", i)
			}
		}
		tokens, err := estimateGeminiToolsTokens(tools)
		if err != nil {
			return 0, err
		}
		total += tokens
	}

	return total, nil
}

// estimateGeminiContentTokens 保留媒体定额，仅将函数参数和返回值按完整 JSON 计数。
// 通用 estimateValue 会忽略数字、布尔值及对象字段名，不适合这些工具数据。
func estimateGeminiContentTokens(value interface{}) (int, error) {
	content, ok := value.(map[string]interface{})
	if !ok || content == nil {
		return 0, fmt.Errorf("内容必须是 JSON 对象")
	}
	parts, ok := content["parts"].([]interface{})
	if !ok || len(parts) == 0 {
		return 0, fmt.Errorf("parts 必须是非空数组")
	}
	total, _ := estimateObjectIgnoring(content, "parts")
	for i, value := range parts {
		part, ok := value.(map[string]interface{})
		if !ok || len(part) == 0 {
			return 0, fmt.Errorf("parts[%d] 必须是非空 JSON 对象", i)
		}
		// 保留 part 包装，让通用估算识别 inlineData/fileData 的媒体定额。
		// 函数数据在后续按完整 JSON 单独计数，先从这一分支排除，避免重复。
		nonFunctionFields := make(map[string]interface{}, len(part))
		for key, value := range part {
			if key != "functionCall" && key != "functionResponse" {
				nonFunctionFields[key] = value
			}
		}
		tokens, _ := estimateObject(nonFunctionFields)
		total += tokens
		for _, field := range []struct {
			name    string
			payload string
		}{
			{"functionCall", "args"},
			{"functionResponse", "response"},
		} {
			function, exists := part[field.name]
			if !exists {
				continue
			}
			obj, ok := function.(map[string]interface{})
			if !ok || obj == nil {
				return 0, fmt.Errorf("parts[%d].%s 必须是 JSON 对象", i, field.name)
			}
			if name, ok := obj["name"].(string); !ok || strings.TrimSpace(name) == "" {
				return 0, fmt.Errorf("parts[%d].%s.name 必须是非空字符串", i, field.name)
			}
			tokens, _ := estimateObjectIgnoring(obj, field.payload)
			total += tokens
			if payload, exists := obj[field.payload]; exists {
				if obj, ok := payload.(map[string]interface{}); !ok || obj == nil {
					return 0, fmt.Errorf("parts[%d].%s.%s 必须是 JSON 对象", i, field.name, field.payload)
				}
				data, err := json.Marshal(payload)
				if err != nil {
					return 0, fmt.Errorf("序列化 parts[%d].%s.%s 失败: %w", i, field.name, field.payload, err)
				}
				total += EstimateTokens(string(data))
			}
		}
	}
	return total, nil
}

// estimateGeminiToolsTokens 估算 Gemini tools（形如 [{functionDeclarations:[...]}]）。
// 工具声明是可读的 JSON Schema，按序列化长度估算；每个声明再兜底 150 token，
// 与 Messages 的 tools 下限口径一致。
func estimateGeminiToolsTokens(tools []interface{}) (int, error) {
	declarations := 0
	for _, tool := range tools {
		if m, ok := tool.(map[string]interface{}); ok {
			if fds, ok := m["functionDeclarations"].([]interface{}); ok {
				declarations += len(fds)
			}
		}
	}
	if declarations == 0 {
		declarations = len(tools)
	}

	data, err := json.Marshal(tools)
	if err != nil {
		return 0, fmt.Errorf("序列化 Gemini 工具声明失败: %w", err)
	}
	estimated := EstimateTokens(string(data))
	if minimum := declarations * 150; estimated < minimum {
		return minimum, nil
	}
	return estimated, nil
}

func analyzeResponsesRequest(bodyBytes []byte) (int, bool) {
	if len(bodyBytes) == 0 {
		return 0, false
	}

	var req map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &req); err != nil {
		tokens, opaque := estimateString(string(bodyBytes))
		return tokens, opaque
	}

	total := 0
	opaque := false

	if instructions, ok := req["instructions"].(string); ok {
		tokens, hasMedia := estimateString(instructions)
		total += tokens
		opaque = opaque || hasMedia
	}

	if input := req["input"]; input != nil {
		tokens, hasMedia := estimateResponsesInputTokens(input)
		total += tokens
		opaque = opaque || hasMedia
	}

	if tools, ok := req["tools"].([]interface{}); ok {
		estimatedTools := estimateJSONTokens(tools)
		minimumTools := len(tools) * 150
		if estimatedTools < minimumTools {
			estimatedTools = minimumTools
		}
		total += estimatedTools
	}

	return total, opaque
}

func estimateJSONTokens(value interface{}) int {
	if value == nil {
		return 0
	}
	data, err := json.Marshal(value)
	if err != nil {
		return 0
	}
	return EstimateTokens(string(data))
}

func estimateMessagesTokens(messages interface{}) (int, bool) {
	if messages == nil {
		return 0, false
	}

	arr, ok := messages.([]interface{})
	if !ok {
		return estimateValue(messages)
	}

	total := 0
	opaque := false
	for _, item := range arr {
		total += 4
		tokens, hasMedia := estimateValue(item)
		total += tokens
		opaque = opaque || hasMedia
	}
	return total, opaque
}

// EstimateResponseTokens 从响应内容估算输出 token
func EstimateResponseTokens(content interface{}) int {
	if content == nil {
		return 0
	}

	// 字符串内容
	if str, ok := content.(string); ok {
		return EstimateTokens(str)
	}

	// 内容数组
	if arr, ok := content.([]interface{}); ok {
		total := 0
		for _, item := range arr {
			if m, ok := item.(map[string]interface{}); ok {
				if text, ok := m["text"].(string); ok {
					total += EstimateTokens(text)
				}
				// tool_use 的 input 也计入
				if input, ok := m["input"]; ok {
					data, _ := json.Marshal(input)
					total += EstimateTokens(string(data))
				}
			}
		}
		return total
	}

	// 其他情况序列化后估算
	data, err := json.Marshal(content)
	if err != nil {
		return 0
	}
	return EstimateTokens(string(data))
}

// isCJK 判断是否为中日韩字符
func isCJK(r rune) bool {
	return unicode.Is(unicode.Han, r) ||
		unicode.Is(unicode.Hiragana, r) ||
		unicode.Is(unicode.Katakana, r) ||
		unicode.Is(unicode.Hangul, r)
}

func estimateResponsesInputTokens(input interface{}) (int, bool) {
	switch v := input.(type) {
	case string:
		return estimateString(v)
	case []interface{}:
		total := 0
		opaque := false
		for _, item := range v {
			if m, ok := item.(map[string]interface{}); ok {
				total += 4
				tokens, hasMedia := estimateResponsesInputItemTokens(m)
				total += tokens
				opaque = opaque || hasMedia
				continue
			}
			tokens, hasMedia := estimateValue(item)
			total += tokens
			opaque = opaque || hasMedia
		}
		return total, opaque
	default:
		return estimateValue(input)
	}
}

// estimateResponsesInputItemTokens 按 Responses item 的实际字段估算 token。
// 同一语义在不同兼容实现中可能叫 content/input/output；不能把这些别名
// 无条件相加，否则一个同时带 input 与 content 的 custom_tool_call，或同时带
// output 与 content 的 function_call_output，会把同一份工具数据重复计入。
// 未知类型走媒体感知递归，不再把整个 item（含 base64）序列化后当文本。
func estimateResponsesInputItemTokens(item map[string]interface{}) (int, bool) {
	if item == nil {
		return 0, false
	}

	itemType, _ := item["type"].(string)
	switch itemType {
	case "", "message", "text":
		return estimateValue(item["content"])
	case "function_call":
		return estimateResponsesCallFields(item["name"], item["arguments"])
	case "custom_tool_call":
		value, exists := item["input"]
		if !exists || value == nil {
			value = item["content"]
		}
		return estimateResponsesCallFields(item["name"], value)
	case "tool_call":
		if toolUse, ok := item["tool_use"].(map[string]interface{}); ok {
			return estimateResponsesCallFields(toolUse["name"], toolUse["input"])
		}
		return estimateResponsesCallFields(item["name"], item["arguments"])
	case "tool_search_call":
		return estimateResponsesCallFields(item["name"], item["arguments"])
	case "function_call_output", "custom_tool_call_output", "tool_result":
		return estimateResponsesOutputFieldTokens(item)
	case "tool_search_output":
		total, opaque := estimateResponsesOutputFieldTokens(item)
		tokens, hasMedia := estimateValue(item["tools"])
		return total + tokens, opaque || hasMedia
	case "reasoning":
		// summary 与 encrypted/content 是不同字段：summary 是可读摘要，
		// encrypted_content 是上游要求回传的 opaque 上下文，不能因为不可读而丢弃。
		summaryTokens, summaryOpaque := estimateValue(item["summary"])
		contentTokens, contentOpaque := estimateValue(item["content"])
		encryptedTokens, encryptedOpaque := estimateValue(item["encrypted_content"])
		return summaryTokens + contentTokens + encryptedTokens,
			summaryOpaque || contentOpaque || encryptedOpaque
	default:
		return estimateObject(item)
	}
}

func estimateResponsesCallFields(name, arguments interface{}) (int, bool) {
	total, opaque := estimateValue(name)
	tokens, hasMedia := estimateValue(arguments)
	return total + tokens, opaque || hasMedia
}

func estimateResponsesOutputFieldTokens(item map[string]interface{}) (int, bool) {
	if output, exists := item["output"]; exists && output != nil {
		return estimateValue(output)
	}
	return estimateValue(item["content"])
}

// estimateContentTokens 估算 content 字段的 token
func estimateContentTokens(content interface{}) int {
	tokens, _ := estimateValue(content)
	return tokens
}

func estimateValue(content interface{}) (int, bool) {
	switch v := content.(type) {
	case nil:
		return 0, false
	case string:
		return estimateString(v)
	case []interface{}:
		total := 0
		opaque := false
		for _, item := range v {
			tokens, hasMedia := estimateValue(item)
			total += tokens
			opaque = opaque || hasMedia
		}
		return total, opaque
	case map[string]interface{}:
		return estimateObject(v)
	default:
		return 0, false
	}
}

func estimateObject(obj map[string]interface{}) (int, bool) {
	if obj == nil {
		return 0, false
	}

	if inline := firstMap(obj, "inlineData", "inline_data"); inline != nil {
		tokens, opaque := estimateInlineMedia(inline)
		rest, restOpaque := estimateObjectIgnoring(obj, "inlineData", "inline_data")
		return tokens + rest, opaque || restOpaque
	}
	if fileData := firstMap(obj, "fileData", "file_data"); fileData != nil && looksLikeMediaFileData(fileData) {
		tokens, opaque := estimateFileData(fileData)
		rest, restOpaque := estimateObjectIgnoring(obj, "fileData", "file_data")
		return tokens + rest, opaque || restOpaque
	}

	itemType := strings.ToLower(strings.TrimSpace(asString(obj["type"])))
	switch itemType {
	case "image", "image_url", "input_image", "input_audio", "audio", "video":
		return estimatedImageTokens + estimateMediaSideText(obj), true
	case "document":
		return estimateDocument(obj)
	case "input_file", "file":
		return estimatedFileTokens + estimateMediaSideText(obj), true
	}

	return estimateObjectIgnoring(obj)
}

func estimateDocument(obj map[string]interface{}) (int, bool) {
	extra := estimateMediaSideText(obj)
	source, _ := obj["source"].(map[string]interface{})
	if source == nil {
		return estimatedFileTokens + extra, true
	}

	sourceType := strings.ToLower(strings.TrimSpace(asString(source["type"])))
	switch sourceType {
	case "text":
		text := asString(source["data"])
		if text == "" {
			text = asString(source["text"])
		}
		return extra + EstimateTokens(text), false
	case "content":
		tokens, opaque := estimateValue(source["content"])
		return extra + tokens, opaque
	default:
		return estimatedFileTokens + extra, true
	}
}

func estimateInlineMedia(inline map[string]interface{}) (int, bool) {
	mime := strings.ToLower(strings.TrimSpace(firstString(inline, "mimeType", "mime_type", "media_type")))
	if mime == "" {
		return estimatedImageTokens, true
	}
	return quotaForMIME(mime), true
}

func estimateFileData(fileData map[string]interface{}) (int, bool) {
	mime := strings.ToLower(strings.TrimSpace(firstString(fileData, "mimeType", "mime_type", "media_type")))
	return quotaForMIME(mime), true
}

func looksLikeMediaFileData(fileData map[string]interface{}) bool {
	mime := strings.ToLower(strings.TrimSpace(firstString(fileData, "mimeType", "mime_type", "media_type")))
	if mime == "" {
		return false
	}
	return strings.HasPrefix(mime, "image/") ||
		strings.HasPrefix(mime, "audio/") ||
		strings.HasPrefix(mime, "video/") ||
		strings.Contains(mime, "pdf")
}

func quotaForMIME(mime string) int {
	if strings.HasPrefix(mime, "image/") || strings.HasPrefix(mime, "audio/") || strings.HasPrefix(mime, "video/") {
		return estimatedImageTokens
	}
	return estimatedFileTokens
}

func estimateMediaSideText(obj map[string]interface{}) int {
	total := 0
	for _, key := range []string{"title", "context", "filename", "name", "text"} {
		if text, ok := obj[key].(string); ok {
			total += EstimateTokens(text)
		}
	}
	return total
}

func estimateObjectIgnoring(obj map[string]interface{}, skipKeys ...string) (int, bool) {
	skip := make(map[string]struct{}, len(skipKeys))
	for _, key := range skipKeys {
		skip[key] = struct{}{}
	}

	total := 0
	opaque := false
	for key, value := range obj {
		if _, exists := skip[key]; exists {
			continue
		}
		if isBinaryPayloadKey(key) {
			tokens, hasMedia := estimateBinaryPayload(value)
			total += tokens
			opaque = opaque || hasMedia
			continue
		}
		tokens, hasMedia := estimateValue(value)
		total += tokens
		opaque = opaque || hasMedia
	}
	return total, opaque
}

func estimateBinaryPayload(value interface{}) (int, bool) {
	switch v := value.(type) {
	case string:
		if isDataURL(v) {
			return dataURLQuota(v), true
		}
		if isOpaqueBase64(v) {
			return 0, true
		}
		return EstimateTokens(v), false
	case map[string]interface{}:
		if url := firstString(v, "url", "image_url"); url != "" && isDataURL(url) {
			return dataURLQuota(url), true
		}
		return estimateValue(v)
	default:
		return estimateValue(value)
	}
}

func estimateString(s string) (int, bool) {
	if isDataURL(s) {
		return dataURLQuota(s), true
	}
	return EstimateTokens(s), false
}

func isBinaryPayloadKey(key string) bool {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "data", "file_data", "filedata", "b64_json", "image_url":
		return true
	default:
		return false
	}
}

func isDataURL(s string) bool {
	trimmed := strings.TrimSpace(s)
	if !strings.HasPrefix(strings.ToLower(trimmed), "data:") {
		return false
	}
	_, payload, found := strings.Cut(trimmed, ",")
	return found && payload != ""
}

func dataURLQuota(s string) int {
	mime := dataURLMediaType(s)
	if strings.HasPrefix(mime, "image/") || strings.HasPrefix(mime, "audio/") || strings.HasPrefix(mime, "video/") {
		return estimatedImageTokens
	}
	return estimatedFileTokens
}

func dataURLMediaType(s string) string {
	trimmed := strings.TrimSpace(s)
	if !strings.HasPrefix(strings.ToLower(trimmed), "data:") {
		return ""
	}
	header, _, found := strings.Cut(trimmed[5:], ",")
	if !found {
		return ""
	}
	header = strings.ToLower(header)
	header, _, _ = strings.Cut(header, ";")
	return strings.TrimSpace(header)
}

func isOpaqueBase64(s string) bool {
	if len(s) < 512 {
		return false
	}
	checked := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '\n', '\r':
			continue
		}
		if !isBase64Char(c) {
			return false
		}
		checked++
		if checked >= 512 {
			return true
		}
	}
	return checked >= 512
}

func isBase64Char(c byte) bool {
	return (c >= 'A' && c <= 'Z') ||
		(c >= 'a' && c <= 'z') ||
		(c >= '0' && c <= '9') ||
		c == '+' || c == '/' || c == '='
}

func firstMap(obj map[string]interface{}, keys ...string) map[string]interface{} {
	for _, key := range keys {
		if nested, ok := obj[key].(map[string]interface{}); ok {
			return nested
		}
	}
	return nil
}

func firstString(obj map[string]interface{}, keys ...string) string {
	for _, key := range keys {
		if value, ok := obj[key].(string); ok && value != "" {
			return value
		}
	}
	return ""
}

func asString(v interface{}) string {
	s, _ := v.(string)
	return s
}

// EstimateResponsesOutputTokens 从 Responses API 响应估算输出 token
// 支持 []ResponsesItem 格式
func EstimateResponsesOutputTokens(output interface{}) int {
	if output == nil {
		return 0
	}

	// 处理 []types.ResponsesItem 类型
	if items, ok := output.([]types.ResponsesItem); ok {
		total := 0
		for _, item := range items {
			total += estimateResponsesItemTokens(item)
		}
		return total
	}

	// 处理 []interface{} 类型
	if arr, ok := output.([]interface{}); ok {
		total := 0
		for _, item := range arr {
			if m, ok := item.(map[string]interface{}); ok {
				// 处理 content 字段
				if content := m["content"]; content != nil {
					total += estimateContentTokens(content)
				}

				// 处理 tool_use
				if toolUse, ok := m["tool_use"].(map[string]interface{}); ok {
					data, _ := json.Marshal(toolUse)
					total += EstimateTokens(string(data))
				}

				// 处理 function_call 类型
				if m["type"] == "function_call" {
					if args, ok := m["arguments"].(string); ok {
						total += EstimateTokens(args)
					}
					if name, ok := m["name"].(string); ok {
						total += EstimateTokens(name) + 2 // 函数名 + 开销
					}
				}

				// 处理 reasoning 类型
				if m["type"] == "reasoning" {
					if summary, ok := m["summary"].([]interface{}); ok {
						for _, s := range summary {
							if sm, ok := s.(map[string]interface{}); ok {
								if text, ok := sm["text"].(string); ok {
									total += EstimateTokens(text)
								}
							}
						}
					}
				}
			}
		}
		return total
	}

	// 其他情况序列化后估算
	data, err := json.Marshal(output)
	if err != nil {
		return 0
	}
	return EstimateTokens(string(data))
}

// estimateResponsesItemTokens 估算单个 ResponsesItem 的 token 数
func estimateResponsesItemTokens(item types.ResponsesItem) int {
	total := 0

	// 处理 content 字段
	if item.Content != nil {
		total += estimateContentTokens(item.Content)
	}

	// 处理 tool_use
	if item.ToolUse != nil {
		data, _ := json.Marshal(item.ToolUse)
		total += EstimateTokens(string(data))
	}

	// 处理 Responses 原生 function_call 字段
	if item.Type == "function_call" {
		if item.Arguments != "" {
			total += EstimateTokens(item.Arguments)
		}
		if item.Name != "" {
			total += EstimateTokens(item.Name) + 2
		}
	}

	// 处理 reasoning summary 字段
	if item.Summary != nil {
		data, _ := json.Marshal(item.Summary)
		total += EstimateTokens(string(data))
	}
	if item.EncryptedContent != nil {
		data, _ := json.Marshal(item.EncryptedContent)
		total += EstimateTokens(string(data))
	}
	if item.Signature != "" {
		total += EstimateTokens(item.Signature)
	}

	// 如果是特殊类型且 content/tool_use 都为空，序列化整个结构估算
	// 这处理 function_call、reasoning 等类型，其数据可能在其他字段中
	if total == 0 && item.Type != "" && item.Type != "message" && item.Type != "text" {
		data, _ := json.Marshal(item)
		total = EstimateTokens(string(data))
	}

	return total
}
