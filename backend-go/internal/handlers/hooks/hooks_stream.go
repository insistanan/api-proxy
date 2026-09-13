package hooks

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/BenedictKing/api-proxy/internal/config"
	"github.com/BenedictKing/api-proxy/internal/sensitive"
	"github.com/gin-gonic/gin"
)

const maxStreamToolArgumentWindow = sensitive.MaxCommandBytes
const maxStreamToolArgumentBytes = 4 << 20
const maxStreamToolArguments = 128
const defaultStreamToolArgumentKey = "default"

type streamTextState struct {
	scanner  *sensitive.StreamCmdScanner
	finished bool
}

type streamToolArgumentState struct {
	body      strings.Builder
	name      string
	rawInput  bool
	started   bool
	inString  bool
	escaped   bool
	closed    bool
	scanned   bool
	stack     []byte
	digest    [sha256.Size]byte
}

func (s *streamToolArgumentState) append(fragment string) error {
	for index := 0; index < len(fragment); index++ {
		current := fragment[index]
		if !s.started {
			if current == ' ' || current == '\r' || current == '\n' || current == '\t' {
				continue
			}
			s.started = true
		}
		if s.rawInput {
			continue
		}
		if s.closed {
			if current != ' ' && current != '\r' && current != '\n' && current != '\t' {
				return fmt.Errorf("工具参数 JSON 结束后仍包含额外内容")
			}
			continue
		}
		if s.inString {
			if s.escaped {
				s.escaped = false
			} else if current == '\\' {
				s.escaped = true
			} else if current == '"' {
				s.inString = false
				if len(s.stack) == 0 {
					s.closed = true
				}
			}
			continue
		}
		switch current {
		case '"':
			s.inString = true
		case '{', '[':
			if len(s.stack) >= 128 {
				return fmt.Errorf("工具参数 JSON 嵌套超过 128 层上限")
			}
			s.stack = append(s.stack, current)
		case '}', ']':
			if len(s.stack) == 0 || current == '}' && s.stack[len(s.stack)-1] != '{' || current == ']' && s.stack[len(s.stack)-1] != '[' {
				return fmt.Errorf("工具参数 JSON 括号不匹配")
			}
			s.stack = s.stack[:len(s.stack)-1]
			s.closed = len(s.stack) == 0
		}
	}
	s.body.WriteString(fragment)
	return nil
}

// FeedAttachedStreamText 在代码块完整后审核，容量错误通过 Hook 错误显式上报。
func FeedAttachedStreamText(c requestContextGetter, text string) error {
	return FeedAttachedStreamTextForKey(c, "", text)
}

// FeedAttachedStreamTextForKey 分别保留各 choice / candidate 的代码块边界。
func FeedAttachedStreamTextForKey(c requestContextGetter, key, text string) error {
	if err := AttachedStreamVetoError(c); err != nil {
		return err
	}
	attached, err := attachedPipelineFromContext(c)
	if err != nil || attached == nil || text == "" {
		return wrapContentSafetyHookError(err)
	}
	attached.streamMu.Lock()
	defer attached.streamMu.Unlock()
	if err := ensureAttachedStreamScanner(attached); err != nil {
		return wrapContentSafetyHookError(err)
	}
	scanner := attached.stream
	if key != "" {
		state, err := attached.textStream(key)
		if err != nil {
			return wrapContentSafetyHookError(err)
		}
		if state.finished {
			return wrapContentSafetyHookError(fmt.Errorf("已结束的响应候选不能继续追加文本"))
		}
		scanner = state.scanner
	}
	previous := scanner.BufferedBytes()
	match, blocked := scanner.Feed(text)
	attached.textStreamBytes += scanner.BufferedBytes() - previous
	if attached.textStreamBytes > maxStreamToolArgumentBytes {
		return wrapContentSafetyHookError(fmt.Errorf("流式文本缓冲超过 4 MiB 检查上限"))
	}
	return attached.reviewStreamMatches(c, scanner, match, blocked)
}

func (a *attachedHookPipeline) textStream(key string) (*streamTextState, error) {
	if len(key) > 512 {
		return nil, fmt.Errorf("响应候选标识超过长度上限")
	}
	if a.textStreams == nil {
		a.textStreams = make(map[string]*streamTextState)
	}
	if state := a.textStreams[key]; state != nil {
		return state, nil
	}
	if len(a.textStreams) >= maxStreamToolArguments {
		return nil, fmt.Errorf("单次响应候选超过 %d 个上限", maxStreamToolArguments)
	}
	state := &streamTextState{scanner: sensitive.NewStreamCmdScanner(a.streamDetector)}
	a.textStreams[key] = state
	return state, nil
}

func (a *attachedHookPipeline) reviewStreamMatches(c requestContextGetter, scanner *sensitive.StreamCmdScanner, match *sensitive.CommandMatch, blocked bool) error {
	if err := scanner.Err(); err != nil {
		return wrapContentSafetyHookError(err)
	}
	for ; blocked; match, blocked = scanner.Allow() {
		if safetyErr := gateReviewDangerousCommand(attachedRequestContext(c), c, a, a.currentMetadata(), *match); safetyErr != nil {
			return a.recordContentSafetyError(attachedRequestContext(c), a.currentMetadata(), safetyErr)
		}
	}
	return nil
}

func (a *attachedHookPipeline) flushStreamText(c requestContextGetter, scanner *sensitive.StreamCmdScanner) error {
	previous := scanner.BufferedBytes()
	match, blocked := scanner.Flush()
	a.textStreamBytes += scanner.BufferedBytes() - previous
	return a.reviewStreamMatches(c, scanner, match, blocked)
}

func (a *attachedHookPipeline) finishStreamBranch(c requestContextGetter, key string) error {
	state, err := a.textStream(key)
	if err != nil {
		return wrapContentSafetyHookError(err)
	}
	if !state.finished {
		if err := a.flushStreamText(c, state.scanner); err != nil {
			return err
		}
		state.finished = true
	}
	for toolKey, tool := range a.toolArgs {
		if !tool.scanned && strings.HasPrefix(toolKey, key) {
			if err := a.scanStreamToolArguments(c, toolKey); err != nil {
				return err
			}
		}
	}
	return nil
}

// FeedAttachedStreamToolArgumentsForKey 只拼接同一工具的完整参数，不截断 JSON 前缀。
func FeedAttachedStreamToolArgumentsForKey(c requestContextGetter, key, fragment string, toolNames ...string) error {
	return feedAttachedStreamToolArguments(c, key, fragment, false, false, toolNames...)
}

// FeedAttachedStreamToolArgumentsSnapshot 检查协议中的完整参数快照，并校验其与已审核增量一致。
func FeedAttachedStreamToolArgumentsSnapshot(c requestContextGetter, key, arguments string, toolNames ...string) error {
	return feedAttachedStreamToolArguments(c, key, arguments, true, false, toolNames...)
}

// FeedAttachedStreamToolInput 处理 Responses 自定义工具的原始输入，在完整快照或终止前审核。
func FeedAttachedStreamToolInput(c requestContextGetter, key, input string, snapshot bool, toolName string) error {
	return feedAttachedStreamToolArguments(c, key, input, snapshot, true, toolName)
}

func feedAttachedStreamToolArguments(c requestContextGetter, key, fragment string, snapshot, rawInput bool, toolNames ...string) error {
	if err := AttachedStreamVetoError(c); err != nil {
		return err
	}
	attached, err := attachedPipelineFromContext(c)
	if err != nil || attached == nil {
		return wrapContentSafetyHookError(err)
	}
	attached.streamMu.Lock()
	defer attached.streamMu.Unlock()
	if err := ensureAttachedStreamScanner(attached); err != nil {
		return wrapContentSafetyHookError(err)
	}
	if !attached.streamSettings.DangerousCmd.Enabled && !attached.streamSettings.Credential.Enabled {
		return nil
	}
	if len(toolNames) > 1 {
		return wrapContentSafetyHookError(fmt.Errorf("工具参数只能绑定一个工具名"))
	}
	key = strings.TrimSpace(key)
	if key == "" {
		key = defaultStreamToolArgumentKey
	}
	if len(key) > 512 {
		return wrapContentSafetyHookError(fmt.Errorf("工具调用标识超过长度上限"))
	}
	if attached.toolArgs == nil {
		attached.toolArgs = make(map[string]*streamToolArgumentState)
	}
	state := attached.toolArgs[key]
	if state == nil {
		if len(attached.toolArgs) >= maxStreamToolArguments {
			return wrapContentSafetyHookError(fmt.Errorf("单次响应工具调用超过 %d 个上限", maxStreamToolArguments))
		}
		state = &streamToolArgumentState{rawInput: rawInput}
		attached.toolArgs[key] = state
	} else if state.rawInput != rawInput {
		return wrapContentSafetyHookError(fmt.Errorf("同一工具调用的参数类型发生变化"))
	}
	if len(toolNames) == 1 && toolNames[0] != "" {
		if len(toolNames[0]) > 256 {
			return wrapContentSafetyHookError(fmt.Errorf("工具名超过 256 字节上限"))
		}
		if (state.name != "" || state.scanned) && state.name != toolNames[0] {
			return wrapContentSafetyHookError(fmt.Errorf("同一工具调用的工具名发生变化"))
		}
		state.name = toolNames[0]
	}
	if len(fragment) > maxStreamToolArgumentWindow {
		return wrapContentSafetyHookError(fmt.Errorf("工具参数超过 %d 字节检查上限", maxStreamToolArgumentWindow))
	}
	if snapshot && state.scanned {
		digest, err := streamToolArgumentDigest(fragment, state.rawInput)
		if err != nil {
			return wrapContentSafetyHookError(err)
		}
		if digest != state.digest {
			return wrapContentSafetyHookError(fmt.Errorf("工具参数最终快照与已审核内容不一致"))
		}
		return nil
	}
	if state.scanned {
		if strings.TrimSpace(fragment) != "" {
			return wrapContentSafetyHookError(fmt.Errorf("工具参数已完成，不能继续追加执行内容"))
		}
		return nil
	}
	if snapshot {
		if state.body.Len() > 0 && !strings.HasPrefix(fragment, state.body.String()) {
			return wrapContentSafetyHookError(fmt.Errorf("工具参数快照与先前增量不一致"))
		}
		fragment = fragment[state.body.Len():]
	}
	if len(fragment) > maxStreamToolArgumentWindow-state.body.Len() || len(fragment) > maxStreamToolArgumentBytes-attached.toolArgBytes {
		return wrapContentSafetyHookError(fmt.Errorf("工具参数缓冲超过检查容量上限"))
	}
	if err := state.append(fragment); err != nil {
		return wrapContentSafetyHookError(err)
	}
	attached.toolArgBytes += len(fragment)
	if state.closed || snapshot {
		return attached.scanStreamToolArguments(c, key)
	}
	return nil
}

func streamToolArgumentDigest(arguments string, rawInput bool) ([sha256.Size]byte, error) {
	if rawInput {
		return sha256.Sum256([]byte(arguments)), nil
	}
	if strings.TrimSpace(arguments) == "" {
		arguments = "{}"
	}
	if decoded, ok := decodeJSONValue([]byte(arguments)); ok {
		body, err := json.Marshal(decoded)
		if err != nil {
			return [sha256.Size]byte{}, fmt.Errorf("序列化工具参数失败: %w", err)
		}
		return sha256.Sum256(body), nil
	}
	return [sha256.Size]byte{}, fmt.Errorf("工具参数不是完整有效的 JSON")
}

// scanStreamToolArguments 只审核完整值，JSON 解码失败时不能退回未解码文本放行。
func (a *attachedHookPipeline) scanStreamToolArguments(c requestContextGetter, key string) error {
	state := a.toolArgs[key]
	if state == nil || state.scanned {
		return nil
	}
	if !state.started && !state.rawInput {
		if err := state.append("{}"); err != nil {
			return wrapContentSafetyHookError(err)
		}
		a.toolArgBytes += 2
	}
	toolArgs := state.body.String()
	var value any = toolArgs
	var matches []sensitive.CommandMatch
	if state.rawInput {
		matches = attachedStreamDetector(a).FindInRawToolInput(state.name, toolArgs)
	} else {
		decoded, ok := decodeJSONValue([]byte(toolArgs))
		if !ok {
			return wrapContentSafetyHookError(fmt.Errorf("工具参数不是完整有效的 JSON"))
		}
		value = decoded
		matches = attachedStreamDetector(a).FindInNamedToolArguments(state.name, toolArgs)
	}
	for _, match := range matches {
		if safetyErr := gateReviewDangerousCommand(attachedRequestContext(c), c, a, a.currentMetadata(), match); safetyErr != nil {
			return a.recordContentSafetyError(attachedRequestContext(c), a.currentMetadata(), safetyErr)
		}
	}
	segments := make([]SafetySegment, 0)
	appendImmutableJSONSegmentWithTool(&segments, a.currentMetadata().APIType, safetySourceToolArgument, fmt.Sprintf("stream.tool_arguments[%q]", key), value, state.name)
	for _, segment := range segments {
		if err := scanAttachedStreamCredentials(c, a, key, segment); err != nil {
			return wrapContentSafetyHookError(err)
		}
	}
	digest, err := streamToolArgumentDigest(toolArgs, state.rawInput)
	if err != nil {
		return wrapContentSafetyHookError(err)
	}
	state.digest = digest
	state.scanned = true
	a.toolArgBytes -= state.body.Len()
	state.body.Reset()
	return nil
}

func scanAttachedStreamCredentials(c requestContextGetter, attached *attachedHookPipeline, key string, segment SafetySegment) error {
	if attached == nil || attached.pipeline == nil || attached.streamCredential == nil {
		return nil
	}
	metadata := attached.currentMetadata()
	settings := attached.streamSettings
	snapshot := &contentSafetySnapshot{settings: settings}
	if isWhitelistedSegment(snapshot, &segment) {
		return recordWhitelistAllowed(attachedRequestContext(c), metadata, attached.pipeline.blockedLogRecorder, segment)
	}
	if allowlistSkipsWholeSegment(settings.Allowlist, segment.Text) {
		return recordAllowlistAllowed(attachedRequestContext(c), metadata, attached.pipeline.blockedLogRecorder, segment)
	}
	matches := attached.streamCredential.FindAll(segment.Text)
	matches = filterAllowlistedCredentialMatches(matches, segment.Text, settings.Allowlist)
	if len(matches) == 0 {
		return nil
	}
	if attached.streamMaskMode == config.ContentSafetyModeBlock {
		first := matches[0]
		safetyErr := &ContentSafetyError{
			BlockType: sensitive.BlockTypeCredential,
			RuleName:  first.Rule,
			Snippet: safetyHitSnippet("block", segment, segment.Text, safetyHit{
				rule: first.Rule, start: first.Start, end: first.End, reveal: "[凭据已隐藏:" + first.Rule + "]",
			}),
		}
		return attached.recordContentSafetyError(attachedRequestContext(c), metadata, safetyErr)
	}
	return recordSafetyMatches(attachedRequestContext(c), metadata, attached.pipeline.blockedLogRecorder,
		sensitive.BlockTypeCredential, attached.streamMaskMode, segment, segment.Text, credentialHits(matches))
}

// FlushAttachedStreamHooks 必须在协议终止事件写出前调用。
func FlushAttachedStreamHooks(c requestContextGetter) error {
	if err := AttachedStreamVetoError(c); err != nil {
		return err
	}
	attached, err := attachedPipelineFromContext(c)
	if err != nil || attached == nil {
		return wrapContentSafetyHookError(err)
	}
	attached.streamMu.Lock()
	defer attached.streamMu.Unlock()
	if err := ensureAttachedStreamScanner(attached); err != nil {
		return wrapContentSafetyHookError(err)
	}
	if err := attached.flushStreamText(c, attached.stream); err != nil {
		return err
	}
	for _, state := range attached.textStreams {
		if !state.finished {
			if err := attached.flushStreamText(c, state.scanner); err != nil {
				return err
			}
			state.finished = true
		}
	}
	for key, state := range attached.toolArgs {
		if !state.scanned {
			if err := attached.scanStreamToolArguments(c, key); err != nil {
				return err
			}
		}
	}
	return nil
}

// FinishAttachedStreamSafetyEvent 覆盖先收到工具结束、后收到整流结束的协议。
func FinishAttachedStreamSafetyEvent(c requestContextGetter, event string) error {
	return finishAttachedStreamSafetyEvent(c, streamSafetyBoundaryForEvent(event))
}

func finishAttachedStreamSafetyEvent(c requestContextGetter, boundary streamSafetyBoundary) error {
	if err := AttachedStreamVetoError(c); err != nil {
		return err
	}
	if boundary.terminal {
		return FlushAttachedStreamHooks(c)
	}
	if len(boundary.branches) == 0 && len(boundary.stoppedTools) == 0 {
		return nil
	}
	attached, err := attachedPipelineFromContext(c)
	if err != nil || attached == nil {
		return wrapContentSafetyHookError(err)
	}
	attached.streamMu.Lock()
	defer attached.streamMu.Unlock()
	if err := ensureAttachedStreamScanner(attached); err != nil {
		return wrapContentSafetyHookError(err)
	}
	for _, key := range boundary.branches {
		if err := attached.finishStreamBranch(c, key); err != nil {
			return err
		}
	}
	for _, key := range boundary.stoppedTools {
		if err := attached.scanStreamToolArguments(c, key); err != nil {
			return err
		}
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
		// 凭据检测器与处置模式在扫描器初始化时一次性固化：
		// 流式生命周期内配置热重载不生效（与扫描器同一约定），
		// 换取消除逐 chunk 的配置深拷贝 + DeepEqual。
		attached.streamCredential = snapshot.credential
		attached.streamMaskMode = snapshot.settings.SensitiveData.Mode
		attached.streamSettings = snapshot.settings
		attached.streamDetector = snapshot.detector
		return nil
	}
	return fmt.Errorf("响应安全管道缺少流式危险命令 Hook")
}

func attachedStreamDetector(attached *attachedHookPipeline) *sensitive.CmdDetector {
	return attached.streamDetector
}
