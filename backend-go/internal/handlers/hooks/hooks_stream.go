package hooks

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/BenedictKing/api-proxy/internal/config"
	"github.com/BenedictKing/api-proxy/internal/sensitive"
	"github.com/gin-gonic/gin"
)

const maxStreamToolArgumentWindow = 16 * 1024
const defaultStreamToolArgumentKey = "default"

// FeedAttachedStreamText 将客户端即将收到的文本增量送入每请求流式扫描器。
func FeedAttachedStreamText(c requestContextGetter, text string) error {
	attached, err := attachedPipelineFromContext(c)
	if err != nil || attached == nil || text == "" {
		return wrapContentSafetyHookError(err)
	}
	attached.streamMu.Lock()
	defer attached.streamMu.Unlock()
	if err := ensureAttachedStreamScanner(attached); err != nil {
		return wrapContentSafetyHookError(err)
	}
	if match, blocked := attached.stream.Feed(text); blocked {
		safetyErr := dangerousCommandError(*match)
		return attached.recordContentSafetyError(attachedRequestContext(c), attached.currentMetadata(), safetyErr)
	}
	return nil
}

// FeedAttachedStreamToolArgumentsForKey 按工具调用分别拼接参数，避免并行工具调用
// 的 JSON 片段互相串接后产生误报。
func FeedAttachedStreamToolArgumentsForKey(c requestContextGetter, key, fragment string) error {
	attached, err := attachedPipelineFromContext(c)
	if err != nil || attached == nil || fragment == "" {
		return wrapContentSafetyHookError(err)
	}
	key = strings.TrimSpace(key)
	if key == "" {
		key = defaultStreamToolArgumentKey
	}
	attached.streamMu.Lock()
	defer attached.streamMu.Unlock()
	if err := ensureAttachedStreamScanner(attached); err != nil {
		return wrapContentSafetyHookError(err)
	}
	if attached.toolArgs == nil {
		attached.toolArgs = make(map[string]string)
	}
	toolArgs := attached.toolArgs[key] + fragment
	if len(toolArgs) > maxStreamToolArgumentWindow {
		toolArgs = toolArgs[len(toolArgs)-maxStreamToolArgumentWindow:]
	}
	attached.toolArgs[key] = toolArgs
	detector := attachedStreamDetector(attached)
	completeJSON := false
	if decoded, ok := decodeJSONValue([]byte(toolArgs)); ok {
		switch decoded.(type) {
		case map[string]interface{}, []interface{}:
			completeJSON = true
			if matches := detector.FindInToolArguments(decoded); len(matches) > 0 {
				safetyErr := dangerousCommandError(matches[0])
				return attached.recordContentSafetyError(attachedRequestContext(c), attached.currentMetadata(), safetyErr)
			}
		}
	}
	if matches := detector.FindInToolArguments(toolArgs); len(matches) > 0 {
		safetyErr := dangerousCommandError(matches[0])
		return attached.recordContentSafetyError(attachedRequestContext(c), attached.currentMetadata(), safetyErr)
	}
	if err := scanAttachedStreamCredentials(c, attached, key, toolArgs); err != nil {
		return wrapContentSafetyHookError(err)
	}
	if completeJSON {
		delete(attached.toolArgs, key)
	}
	return nil
}

func scanAttachedStreamCredentials(c requestContextGetter, attached *attachedHookPipeline, key, toolArgs string) error {
	if attached == nil || attached.pipeline == nil {
		return nil
	}
	var hook *contentSafetyPostResponseHook
	for _, entry := range attached.pipeline.snapshot(HookStagePostResponse) {
		if candidate, ok := entry.hook.(*contentSafetyPostResponseHook); ok {
			hook = candidate
			break
		}
	}
	if hook == nil {
		return nil
	}
	snapshot, err := hook.currentSnapshot()
	if err != nil {
		return wrapContentSafetyHookError(err)
	}
	matches := snapshot.credential.FindAll(toolArgs)
	if len(matches) == 0 {
		return nil
	}
	segment := SafetySegment{
		Protocol: attached.currentMetadata().APIType,
		Source:   safetySourceToolArgument,
		Path:     fmt.Sprintf("stream.tool_arguments[%q]", key),
		Text:     toolArgs,
		Mutable:  false,
	}
	mode := snapshot.settings.Credential.ToolArgumentMode
	if mode == config.ContentSafetyModeBlock {
		first := matches[0]
		safetyErr := &ContentSafetyError{
			BlockType: sensitive.BlockTypeCredential,
			RuleName:  first.Rule,
			Snippet: safetyHitSnippet("block", segment, toolArgs, safetyHit{
				rule:   first.Rule,
				start:  first.Start,
				end:    first.End,
				reveal: "[凭据已隐藏:" + first.Rule + "]",
			}),
		}
		return attached.recordContentSafetyError(attachedRequestContext(c), attached.currentMetadata(), safetyErr)
	}
	if attached.credentialHits == nil {
		attached.credentialHits = make(map[string]struct{})
	}
	newRules := make([]string, 0, len(matches))
	hits := make([]safetyHit, 0, len(matches))
	for _, match := range matches {
		hitKey := key + "\x00" + match.Rule
		if _, exists := attached.credentialHits[hitKey]; exists {
			continue
		}
		attached.credentialHits[hitKey] = struct{}{}
		newRules = append(newRules, match.Rule)
		hits = append(hits, safetyHit{
			rule:   match.Rule,
			start:  match.Start,
			end:    match.End,
			reveal: "[凭据已隐藏:" + match.Rule + "]",
		})
	}
	if len(newRules) == 0 {
		return nil
	}
	return wrapContentSafetyHookError(recordSafetyMatches(attachedRequestContext(c), attached.currentMetadata(), hook.recorder, sensitive.BlockTypeCredential, mode, segment, toolArgs, hits))
}

// FlushAttachedStreamHooks 在流结束时确认未闭合代码围栏中的延迟匹配。
func FlushAttachedStreamHooks(c requestContextGetter) error {
	attached, err := attachedPipelineFromContext(c)
	if err != nil || attached == nil {
		return wrapContentSafetyHookError(err)
	}
	attached.streamMu.Lock()
	defer attached.streamMu.Unlock()
	if err := ensureAttachedStreamScanner(attached); err != nil {
		return wrapContentSafetyHookError(err)
	}
	if match, blocked := attached.stream.Flush(); blocked {
		safetyErr := dangerousCommandError(*match)
		return attached.recordContentSafetyError(attachedRequestContext(c), attached.currentMetadata(), safetyErr)
	}
	return nil
}

func attachedRequestContext(c requestContextGetter) context.Context {
	if ginContext, ok := c.(*gin.Context); ok && ginContext != nil && ginContext.Request != nil {
		return ginContext.Request.Context()
	}
	return context.Background()
}

// WriteAttachedContentSafetyError 将内容安全错误编码为当前协议的非流式 JSON 响应。
func WriteAttachedContentSafetyError(c *gin.Context, err error) error {
	apiType, metadata, safetyErr, lookupErr := attachedSafetyError(c, err)
	if lookupErr != nil {
		return lookupErr
	}
	payload, payloadErr := contentSafetyJSONPayload(apiType, metadata, safetyErr)
	if payloadErr != nil {
		return payloadErr
	}
	c.JSON(http.StatusForbidden, payload)
	return nil
}

// WriteAttachedStreamError 将内容安全流错误编码为当前协议的 SSE 事件。
// 写完 error 事件后补发该协议的流终止序列，否则客户端只收到 error 事件而缺少
// 结束信号，会一直等待导致 agent 卡死。
func WriteAttachedStreamError(c *gin.Context, err error) error {
	apiType, metadata, safetyErr, lookupErr := attachedSafetyError(c, err)
	if lookupErr != nil {
		return lookupErr
	}
	payload, eventName, payloadErr := contentSafetyStreamPayload(apiType, metadata, safetyErr)
	if payloadErr != nil {
		return payloadErr
	}
	if err := writeAttachedSSEPayload(c, payload, eventName); err != nil {
		return err
	}
	return writeAttachedSSEStreamTerminator(c, apiType, safetyErr.Code(), safetyErr.Error())
}

// WriteAttachedStreamHookError 将内容安全基础设施错误编码为当前协议的 SSE 错误。
// 同样补发流终止序列，避免客户端等待结束信号。
func WriteAttachedStreamHookError(c *gin.Context, _ error) error {
	attached, lookupErr := attachedPipelineFromContext(c)
	if lookupErr != nil {
		return lookupErr
	}
	if attached == nil {
		return fmt.Errorf("内容安全错误缺少已绑定的协议上下文")
	}
	metadata := attached.currentMetadata()
	payload, eventName, payloadErr := contentSafetyHookStreamPayload(metadata.APIType, metadata)
	if payloadErr != nil {
		return payloadErr
	}
	if err := writeAttachedSSEPayload(c, payload, eventName); err != nil {
		return err
	}
	return writeAttachedSSEStreamTerminator(c, metadata.APIType, "CONTENT_SAFETY_HOOK_ERROR", "内容安全检查失败")
}

func writeAttachedSSEPayload(c *gin.Context, payload any, eventName string) error {
	body, marshalErr := json.Marshal(payload)
	if marshalErr != nil {
		return marshalErr
	}
	if c == nil || c.Writer == nil {
		return fmt.Errorf("流错误写出上下文不支持写入")
	}
	c.Header("Content-Type", "text/event-stream")
	event := make([]byte, 0, len(body)+32)
	if eventName != "" {
		event = append(event, "event: "...)
		event = append(event, eventName...)
		event = append(event, '\n')
	}
	event = append(event, "data: "...)
	event = append(event, body...)
	event = append(event, '\n', '\n')
	if _, writeErr := c.Writer.Write(event); writeErr != nil {
		return writeErr
	}
	if flusher, ok := c.Writer.(interface{ Flush() }); ok {
		flusher.Flush()
	}
	return nil
}

// writeAttachedSSEStreamTerminator 在内容安全 error 事件之后补发该协议的流终止序列。
// 客户端（尤其 Claude Code / Codex 等 agent）在收到 error 事件后不会自动结束 SSE 读取
// 循环，它们等的是 message_stop 等终止标记 + 连接关闭。
// 缺少终止序列会导致 agent 一直等待，表现为"卡死、无法中断对话"。
//
// 各协议终止序列：
//   - messages：event: message_stop + data: {"type":"message_stop"}
//   - responses：event: response.failed + data / images：无统一终止标记，靠连接关闭
func writeAttachedSSEStreamTerminator(c *gin.Context, apiType, code, message string) error {
	if c == nil || c.Writer == nil {
		return nil
	}
	terminator := streamTerminatorForAPI(apiType, code, message)
	if terminator == "" {
		return nil
	}
	if _, writeErr := c.Writer.Write([]byte(terminator)); writeErr != nil {
		return writeErr
	}
	if flusher, ok := c.Writer.(interface{ Flush() }); ok {
		flusher.Flush()
	}
	return nil
}

func streamTerminatorForAPI(apiType, code, message string) string {
	switch strings.ToLower(apiType) {
	case "messages":
		return "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
	case "responses":
		if code == "" {
			code = "stream_failed"
		}
		if message == "" {
			message = "Responses 流已失败"
		}
		payload, err := json.Marshal(map[string]any{
			"type": "response.failed",
			"response": map[string]any{
				"id":     "",
				"object": "response",
				"status": "failed",
				"error": map[string]any{
					"code":    code,
					"message": message,
				},
			},
		})
		if err != nil {
			return ""
		}
		return "event: response.failed\ndata: " + string(payload) + "\n\n"
	case "chat":
		return "data: [DONE]\n\n"
	case "gemini", "images":
		return ""
	default:
		return ""
	}
}

func attachedSafetyError(c *gin.Context, err error) (string, HookContext, *ContentSafetyError, error) {
	attached, lookupErr := attachedPipelineFromContext(c)
	if lookupErr != nil {
		return "", HookContext{}, nil, lookupErr
	}
	if attached == nil {
		return "", HookContext{}, nil, fmt.Errorf("内容安全错误缺少已绑定的协议上下文")
	}
	safetyErr := ContentSafetyErrorFrom(err)
	if safetyErr == nil {
		return "", HookContext{}, nil, fmt.Errorf("错误不是内容安全拦截错误: %w", err)
	}
	metadata := attached.currentMetadata()
	return metadata.APIType, metadata, safetyErr, nil
}

func contentSafetyJSONPayload(apiType string, metadata HookContext, safetyErr *ContentSafetyError) (any, error) {
	message := safetyErr.Error()
	code := safetyErr.Code()
	switch apiType {
	case "messages":
		payload := map[string]any{
			"type":  "error",
			"error": map[string]any{"type": "content_safety_error", "message": message, "code": code},
		}
		if metadata.RequestID != "" {
			payload["request_id"] = metadata.RequestID
		}
		return payload, nil
	case "responses", "chat", "images":
		return map[string]any{
			"error": map[string]any{"message": message, "type": "content_safety_error", "param": nil, "code": code},
		}, nil
	case "gemini":
		return geminiContentSafetyPayload(message, code), nil
	default:
		return nil, fmt.Errorf("不支持的内容安全错误协议 %q", apiType)
	}
}

func contentSafetyStreamPayload(apiType string, metadata HookContext, safetyErr *ContentSafetyError) (any, string, error) {
	message := safetyErr.Error()
	code := safetyErr.Code()
	switch apiType {
	case "messages":
		payload := map[string]any{
			"type":  "error",
			"error": map[string]any{"type": "content_safety_error", "message": message, "code": code},
		}
		if metadata.RequestID != "" {
			payload["request_id"] = metadata.RequestID
		}
		return payload, "error", nil
	case "responses":
		return map[string]any{"type": "error", "code": code, "message": message, "param": nil}, "error", nil
	case "chat":
		return map[string]any{
			"error": map[string]any{"message": message, "type": "content_safety_error", "param": nil, "code": code},
		}, "", nil
	case "gemini":
		return geminiContentSafetyPayload(message, code), "", nil
	case "images":
		return map[string]any{
			"error": map[string]any{"message": message, "type": "content_safety_error", "param": nil, "code": code},
		}, "", nil
	default:
		return nil, "", fmt.Errorf("不支持的内容安全流错误协议 %q", apiType)
	}
}

func contentSafetyHookStreamPayload(apiType string, metadata HookContext) (any, string, error) {
	const message = "内容安全检查失败"
	const code = "CONTENT_SAFETY_HOOK_ERROR"
	switch apiType {
	case "messages":
		payload := map[string]any{
			"type":  "error",
			"error": map[string]any{"type": "api_error", "message": message, "code": code},
		}
		if metadata.RequestID != "" {
			payload["request_id"] = metadata.RequestID
		}
		return payload, "error", nil
	case "responses":
		return map[string]any{"type": "error", "code": code, "message": message, "param": nil}, "error", nil
	case "chat", "images":
		return map[string]any{
			"error": map[string]any{"message": message, "type": "api_error", "param": nil, "code": code},
		}, "", nil
	case "gemini":
		return map[string]any{
			"error": map[string]any{
				"code": 500, "message": message, "status": "INTERNAL",
				"details": []any{map[string]any{
					"@type":  "type.googleapis.com/google.rpc.ErrorInfo",
					"reason": code,
					"domain": "api-proxy",
				}},
			},
		}, "", nil
	default:
		return nil, "", fmt.Errorf("不支持的内容安全错误协议 %q", apiType)
	}
}

func geminiContentSafetyPayload(message, code string) map[string]any {
	return map[string]any{
		"error": map[string]any{
			"code": 403, "message": message, "status": "PERMISSION_DENIED",
			"details": []any{map[string]any{
				"@type":  "type.googleapis.com/google.rpc.ErrorInfo",
				"reason": code,
				"domain": "api-proxy",
			}},
		},
	}
}

func attachedPipelineFromContext(c requestContextGetter) (*attachedHookPipeline, error) {
	if c == nil {
		return nil, nil
	}
	value, exists := c.Get(contentSafetyPipelineKey)
	if !exists || value == nil {
		return nil, nil
	}
	attached, ok := value.(*attachedHookPipeline)
	if !ok || attached == nil || attached.pipeline == nil {
		return nil, fmt.Errorf("内容安全管道绑定数据无效")
	}
	return attached, nil
}

func ensureAttachedStreamScanner(attached *attachedHookPipeline) error {
	if attached.stream != nil {
		return nil
	}
	for _, entry := range attached.pipeline.snapshot(HookStagePostResponse) {
		hook, ok := entry.hook.(*contentSafetyPostResponseHook)
		if !ok {
			continue
		}
		snapshot, err := hook.currentSnapshot()
		if err != nil {
			return err
		}
		attached.stream = sensitive.NewStreamCmdScanner(snapshot.detector)
		return nil
	}
	return fmt.Errorf("响应安全管道缺少流式危险命令 Hook")
}

func attachedStreamDetector(attached *attachedHookPipeline) *sensitive.CmdDetector {
	for _, entry := range attached.pipeline.snapshot(HookStagePostResponse) {
		if hook, ok := entry.hook.(*contentSafetyPostResponseHook); ok {
			if snapshot := hook.snapshot.Load(); snapshot != nil {
				return snapshot.detector
			}
		}
	}
	return nil
}
