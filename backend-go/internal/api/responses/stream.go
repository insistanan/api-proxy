package responses

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"insistanan/api-proxy/internal/api/common"
	"insistanan/api-proxy/internal/dialogue/session"
	"insistanan/api-proxy/internal/infra/config"
	"insistanan/api-proxy/internal/infra/upstreamreq"
	"insistanan/api-proxy/internal/orchestration/scheduler"
	"insistanan/api-proxy/internal/protocol/codec"
	"insistanan/api-proxy/internal/protocol/converters"
	"insistanan/api-proxy/internal/protocol/items"
	"insistanan/api-proxy/internal/protocol/providers"
	"insistanan/api-proxy/internal/protocol/tokenusage"
	"insistanan/api-proxy/internal/protocol/types"
	"insistanan/api-proxy/internal/proxycore"
	"insistanan/api-proxy/internal/safety/pipeline"
	"insistanan/api-proxy/internal/storage/logbody"
	"insistanan/api-proxy/internal/storage/logger"
)

// hasResponsesContent 判断 SSE 事件是否包含实际 output。兼容只在
// response.completed 中返回完整 output、没有逐段 delta 的上游。
func hasResponsesContent(event string) bool {
	for _, line := range strings.Split(event, "\n") {
		jsonStr, isData := codec.SSEDataJSON(line)
		if !isData {
			continue
		}
		var data map[string]interface{}
		if err := json.Unmarshal([]byte(jsonStr), &data); err != nil {
			continue
		}
		eventType, _ := data["type"].(string)
		if eventType == "response.completed" || eventType == "response.incomplete" {
			if response, ok := data["response"].(map[string]interface{}); ok {
				if output, ok := response["output"].([]interface{}); ok && len(output) > 0 {
					return true
				}
			}
		}
		switch eventType {
		case "response.image_generation_call.partial_image",
			"response.output_text.delta",
			"response.function_call_arguments.delta",
			"response.custom_tool_call_input.delta",
			"response.reasoning_summary_text.delta",
			"response.output_json.delta",
			"response.content_part.delta",
			"response.audio.delta",
			"response.audio_transcript.delta",
			"response.output_item.added",
			"response.output_text.done",
			"response.reasoning_summary_text.done",
			"response.function_call_arguments.done",
			"response.custom_tool_call_input.done",
			"response.output_item.done":
			return true
		}
	}
	return false
}

func responsesStreamEventError(event string) error {
	for _, line := range strings.Split(event, "\n") {
		payload, isData := codec.SSEDataJSON(line)
		if !isData {
			continue
		}
		var data map[string]interface{}
		if err := json.Unmarshal([]byte(payload), &data); err != nil {
			return fmt.Errorf("解析 Responses SSE 数据失败: %w", err)
		}
		if data == nil {
			return fmt.Errorf("上游 Responses SSE 数据不是 JSON 对象")
		}
		eventType, _ := data["type"].(string)
		failed := eventType == "response.failed" || eventType == "error" || data["error"] != nil
		if response, ok := data["response"].(map[string]interface{}); ok && response["status"] == "failed" {
			failed = true
		}
		if !failed {
			continue
		}
		if proxycore.IsUpstreamModelCapacityError([]byte(payload)) {
			return proxycore.NewRetrySameCandidateError(ErrEmptyStreamResponse)
		}
		return fmt.Errorf("upstream stream failed: %s", payload)
	}
	return nil
}

// handleStreamSuccess 处理流式响应。
// 未写入客户端的空响应可重试；中途截断、转换失败和写入失败显式上报。
// 在缓冲阶段（尚未收到内容 delta 前），所有 SSE 事件只缓冲不 flush；
// 若最终 response.completed 时 output 为空，则返回 ErrEmptyStreamResponse。
func handleStreamSuccess(
	c *gin.Context,
	resp *http.Response,
	upstreamType string,
	envCfg *config.EnvConfig,
	sessionManager *session.SessionManager,
	startTime time.Time,
	originalReq *types.ResponsesRequest,
	originalRequestJSON []byte,
	usageRequestJSON []byte,
	channelScheduler *scheduler.ChannelScheduler,
	conversationID string,
) (usage *types.Usage, resultErr error) {
	defer func() { resultErr = common.ReportStreamFailure(c, "responses", resultErr) }()
	envCfg = proxycore.RequestLogEnv(c, envCfg)
	if envCfg.Current().EnableResponseLogs {
		responseTime := time.Since(startTime).Milliseconds()
		log.Printf("[Responses-Stream] Responses 流式响应开始: %dms, 状态: %d", responseTime, resp.StatusCode)
	}

	var synthesizer *codec.StreamSynthesizer
	logBuffer := logbody.Buffer{Limit: logger.StreamLogLimit(c.Request.Context(), envCfg.Current().EnableResponseLogs)}
	streamLoggingEnabled := logBuffer.Limit > 0

	if streamLoggingEnabled {
		synthesizer = codec.NewStreamSynthesizer(upstreamType, logBuffer.Limit)
	}

	var converterState any

	flusher, _ := c.Writer.(http.Flusher)
	streamStarted := false
	startStream := func() {
		if streamStarted {
			return
		}
		upstreamreq.ForwardResponseHeaders(resp.Header, c.Writer)
		c.Header("Content-Type", "text/event-stream")
		c.Header("Cache-Control", "no-cache")
		c.Header("Connection", "keep-alive")
		c.Header("X-Accel-Buffering", "no")
		c.Status(resp.StatusCode)
		streamStarted = true
	}

	stopVeto := pipeline.BindAttachedStreamVeto(c, resp.Body)
	defer stopVeto()
	defer codec.BindStreamBody(c.Request.Context(), resp.Body)()
	// 检查流式响应是否含有 Content-Encoding。由于已强制设置 Accept-Encoding: identity，
	// 正常情况下上游不应压缩 SSE；若上游违规压缩，尝试包装流式解码 Reader 以防分块解析失败。
	if encoding := codec.GetContentEncoding(resp.Header); encoding != "" {
		log.Printf("[Responses-Stream] 警告: 上游流式响应包含 Content-Encoding: %s，尝试流式解压", encoding)
		wrappedBody, wrapped, wrapErr := codec.WrapStreamReaderIfNeeded(resp)
		if wrapErr != nil {
			if err := c.Request.Context().Err(); err != nil {
				return nil, err
			}
			return nil, fmt.Errorf("包装 Responses 流式解压 Reader 失败 (%s): %w", encoding, wrapErr)
		} else if wrapped {
			resp.Body = wrappedBody
			defer wrappedBody.Close()
			upstreamreq.StripEntityHeadersForRebuiltBody(resp.Header)
		}
	}
	reader := bufio.NewReaderSize(resp.Body, 64*1024)

	// Token 统计状态
	sessionCollector := newResponsesStreamSessionCollector()
	var collectedUsage responsesStreamUsage
	var upstreamUsage responsesStreamUsage
	var openAIUsage *types.Usage
	readOpenAIUsage := upstreamType == converters.ResponsesUpstreamOpenAI && tokenusage.RecorderFrom(c.Request.Context()) != nil
	hasUsage := false
	defer func() {
		if resultErr != nil && hasUsage {
			usage = upstreamUsage.snapshot(false)
		}
		if openAIUsage != nil {
			if usage == nil || !hasUsage {
				usage = openAIUsage
			} else {
				usage.UnattributedCacheInput = openAIUsage.UnattributedCacheInput
			}
		}
	}()
	needTokenPatch := false
	clientGone := false
	toolCallDelivered := false
	streamCompleted := false
	var streamWriteErr error
	var streamResponseID string

	committed := false
	commitStream := func(delivered bool) error {
		if originalReq == nil || committed {
			return nil
		}
		if err := sessionCollector.Err(); err != nil {
			return &sessionCommitError{Err: err}
		}
		sessionTokens := upstreamUsage.TotalTokens
		if sessionTokens <= 0 {
			sessionTokens = upstreamUsage.InputTokens + upstreamUsage.OutputTokens
		}
		if !hasUsage {
			sessionTokens = collectedUsage.TotalTokens
		}
		outputItems := sessionCollector.Items()
		if err := sessionCollector.Err(); err != nil {
			return &sessionCommitError{Err: err}
		}
		if delivered {
			hasTool := false
			for _, item := range outputItems {
				if item.Type == "function_call" || item.Type == "custom_tool_call" {
					hasTool = true
				}
			}
			if !hasTool {
				return &sessionCommitError{Err: fmt.Errorf("已交付工具前仍有未完成条目，无法提交完整历史前缀")}
			}
		}
		if err := commitResponsesTurn(c, sessionManager, originalReq, originalRequestJSON, outputItems, sessionTokens, streamResponseID, responseUpstreamOwner(resp), false, delivered); err != nil {
			return err
		}
		committed = true
		return nil
	}

	// ---- 空响应检测：缓冲阶段 ----
	// 在收到第一个包含实际内容的事件前，所有事件暂存到 bufferedEvents，
	// 不调用 c.Writer.Write / flusher.Flush，确保 c.Writer.Written() == false。
	// 这样若最终发现是空响应（如 model at capacity），上层可安全 failover。
	bufferedEvents := make([]string, 0, 64)
	bufferedBytes := 0
	const maxBufferedEventBytes = 256 * 1024
	buffering := true
	writeEvent := func(event string) bool {
		if clientGone {
			return false
		}
		if err := c.Request.Context().Err(); err != nil {
			clientGone = true
			streamWriteErr = err
			return false
		}
		startStream()
		proxycore.MarkRequestLogFirstToken(c)
		if err := common.WriteStream(c.Writer, flusher, []byte(event)); err != nil {
			clientGone = true
			streamWriteErr = err
			if !isClientDisconnectError(err) {
				log.Printf("[Responses-Stream] 警告: 流式响应传输错误: %v", err)
			}
			return false
		}
		if !isResponsesTerminalEvent(event) {
			sessionCollector.consumeEvent(event)
		}
		return true
	}
	flushBufferedEvents := func() {
		for _, buffered := range bufferedEvents {
			if !writeEvent(buffered) {
				break
			}
		}
		bufferedEvents = nil
		bufferedBytes = 0
	}

	var streamReadErr error
	for {
		// 危险命令 AI 闸门的事后否决：非阻塞检查，用户在否决窗口内加黑名单即终止当前流。
		select {
		case <-pipeline.AttachedStreamVetoDone(c):
			return nil, pipeline.HandleAttachedStreamVeto(c)
		default:
		}
		var rawLine []byte
		readErr := streamReadErr
		if readErr == nil {
			var event codec.SSEEvent
			event, readErr = codec.ReadSSEEvent(c.Request.Context(), reader)
			line, err := event.DataLine()
			if err != nil {
				return nil, err
			}
			if event.Raw != "" {
				rawLine = []byte(line)
			}
			if event.Raw != "" && line == "" && readErr == nil {
				continue
			}
		}
		if err := pipeline.AttachedStreamVetoError(c); err != nil {
			return nil, err
		}
		line := strings.TrimSuffix(strings.TrimSuffix(string(rawLine), "\n"), "\r")
		if readOpenAIUsage {
			if payload, ok := codec.ParseSSEDataLineBytes(rawLine); ok && bytes.Contains(payload, []byte(`"usage"`)) {
				if snapshot := tokenusage.ExtractJSONUsage(payload); snapshot != nil {
					openAIUsage = snapshot
				}
			}
		}
		finalizing := len(rawLine) == 0 && readErr != nil
		if payload, isData := codec.ParseSSEDataLine(line); isData && payload == codec.SSEDoneMarker && upstreamType == converters.ResponsesUpstreamGemini {
			finalizing = true
		}
		if finalizing {
			streamReadErr = readErr
			// 工具调用交付后的取消由后面的成功例外处理，不能继续补发完成事件。
			if c.Request.Context().Err() != nil {
				break
			}
		}

		if streamLoggingEnabled {
			logBuffer.Write(rawLine)
			if synthesizer != nil {
				synthesizer.ProcessLine(line)
			}
		}

		// 正常读取与流尾定稿共用下面的事件处理链，避免两套 usage/安全/会话逻辑。
		var events []string
		var err error
		if finalizing {
			events, err = converters.FinishUpstreamStreamToResponses(upstreamType, originalRequestJSON, &converterState)
		} else {
			events, err = converters.ConvertUpstreamStreamLineToResponses(
				c.Request.Context(), upstreamType, originalReq.Model, originalRequestJSON, []byte(line), &converterState,
				providers.ResponseToolsForRequest(c),
			)
		}
		if err != nil {
			log.Printf("[Responses-Stream] 流式响应转换失败: %v", err)
			return nil, fmt.Errorf("转换上游流式响应失败: %w", err)
		}
		for _, event := range events {
			// 检测并收集 usage
			detected, needPatch, usageData := checkResponsesEventUsage(event, envCfg.Current().EnableResponseLogs && envCfg.ShouldLog("debug"))
			if detected {
				if !hasUsage {
					hasUsage = true
					needTokenPatch = needPatch
					if envCfg.Current().EnableResponseLogs && envCfg.ShouldLog("debug") && needPatch {
						log.Printf("[Responses-Stream-Token] 检测到缺失统计，延迟到流结束补全")
					}
				}
				updateResponsesStreamUsage(&collectedUsage, usageData)
				updateResponsesStreamUsage(&upstreamUsage, usageData)
			}
			if eventErr := responsesStreamEventError(event); eventErr != nil {
				return nil, eventErr
			}

			// 在 response.completed 事件前注入/修补 usage
			eventToSend := event
			if upstreamType == converters.ResponsesUpstreamResponses && isResponsesTerminalEvent(event) {
				// 原生终止行提前收尾时也必须形成完整 SSE 事件。
				eventToSend = strings.TrimRight(eventToSend, "\n") + "\n\n"
			}
			if isResponsesTerminalEvent(event) {
				// 仅按 Responses output item 的语义字段估算 output_tokens。
				// 不能把 reasoning/function-call/audio 的 SSE delta 与完整
				// response.completed JSON envelope 混成普通文本，否则无 usage
				// 的上游会把 envelope 字段也算进输出，且大响应会额外占用内存。
				snapshot, err := sessionCollector.output.Snapshot()
				if err != nil {
					return nil, err
				}
				estimatedOutputTokens := estimateResponsesOutputFromItems(snapshot)
				eventToSend = finalizeResponsesCompletedUsage(
					eventToSend, usageRequestJSON, estimatedOutputTokens, &collectedUsage,
					hasUsage, needTokenPatch, upstreamType == converters.ResponsesUpstreamResponses, envCfg,
				)
			}
			if streamResponseID == "" {
				streamResponseID = extractResponsesStreamResponseID(eventToSend)
			}
			if isResponsesCompletedEvent(eventToSend) && originalReq != nil &&
				(respitems.ResponsesItemsContainCompactionFromInput(originalReq.Input) ||
					c.GetBool(upstreamreq.ContextKeyResponsesPreviousIDDropped)) {
				eventToSend = clearResponsesPreviousIDsFromCompletedEvent(eventToSend)
			}
			text, toolArguments := extractResponsesStreamSafetyFragments(eventToSend)
			if err := pipeline.FeedAttachedStreamText(c, text); err != nil {
				return nil, err
			}
			for _, toolArgument := range toolArguments {
				var err error
				if toolArgument.rawInput {
					err = pipeline.FeedAttachedStreamToolInput(c, toolArgument.key, toolArgument.fragment, toolArgument.snapshot, toolArgument.name)
				} else if toolArgument.snapshot {
					err = pipeline.FeedAttachedStreamToolArgumentsSnapshot(c, toolArgument.key, toolArgument.fragment, toolArgument.name)
				} else {
					err = pipeline.FeedAttachedStreamToolArgumentsForKey(c, toolArgument.key, toolArgument.fragment, toolArgument.name)
				}
				if err != nil {
					return nil, err
				}
			}
			eventToSend, err = pipeline.RestoreAttachedSSEEvent(c, eventToSend)
			if err != nil {
				return nil, err
			}
			if eventToSend == "" {
				continue
			}
			// 会话持久化保存客户端可见的已还原内容；下一轮即使只携带
			// previous_response_id，也会重新经过上行脱敏而不会遗留旧占位符。

			// ---- 缓冲阶段逻辑 ----
			if buffering {
				if hasResponsesContent(eventToSend) {
					// 收到第一个内容 delta，退出缓冲阶段
					// 先 flush 所有已缓冲事件（包括 response.created, response.in_progress 等）
					flushBufferedEvents()
					buffering = false
				} else {
					// 仍在缓冲阶段，暂存事件
					bufferedEvents = append(bufferedEvents, eventToSend)
					bufferedBytes += len(eventToSend)

					// 如果是 response.completed 且仍在缓冲阶段，说明是空响应
					// （没有收到任何内容 delta 就完成了）
					if isResponsesTerminalEvent(event) {
						log.Printf("[Responses-Stream] 检测到空响应 (model at capacity)，buffered=%d, written=%v",
							len(bufferedEvents), c.Writer.Written())
						// 不写入任何数据，返回 error 让上层 failover
						if envCfg.Current().EnableResponseLogs {
							logBuffer.WriteString(eventToSend)
						}
						// 跳出循环，返回 ErrEmptyStreamResponse
						// 直接返回，不 flush 任何缓冲事件
						return nil, proxycore.NewRetrySameCandidateError(ErrEmptyStreamResponse)
					}
					if bufferedBytes > maxBufferedEventBytes {
						log.Printf("[Responses-Stream] 首个内容事件前的元数据超过 %d 字节，停止缓冲", maxBufferedEventBytes)
						flushBufferedEvents()
						buffering = false
					}
					continue // 缓冲阶段不执行下方 write 逻辑
				}
			}

			if isResponsesTerminalEvent(eventToSend) {
				sessionCollector.consumeEvent(eventToSend)
				if err := commitStream(false); err != nil {
					return upstreamUsage.snapshot(false), err
				}
			}
			// 正常（已退出缓冲阶段）转发给客户端
			if writeEvent(eventToSend) {
				if sessionCollector.output.HasCompletedTool() {
					toolCallDelivered = true
				}
				if isResponsesTerminalEvent(event) {
					streamCompleted = true
				}
			}
		}
		// 转换器只有在显式终止或读取尾部 usage 后才发完成事件，不再等待连接关闭。
		if clientGone || streamCompleted || finalizing {
			break
		}

		if readErr != nil {
			streamReadErr = readErr
		}
	}

	contextErr := c.Request.Context().Err()
	toolCallCompletedCancellation := toolCallDelivered && errors.Is(contextErr, context.Canceled)
	// 已成功写出终止事件后，客户端关闭 SSE 属于正常收尾；仍未交付的
	// 流以及写出失败继续按取消或传输错误处理，不能仅凭 HTTP 200 判成功。
	if contextErr != nil && !streamCompleted && !toolCallCompletedCancellation {
		return nil, contextErr
	}
	if streamWriteErr != nil && !toolCallCompletedCancellation {
		return nil, streamWriteErr
	}
	if toolCallCompletedCancellation && envCfg.ShouldLog("info") {
		log.Printf("[Responses-Stream] 工具调用已完整交付，客户端结束当前流，按成功回合记录")
	}

	// 流正常结束但没有内容时才按瞬时空响应重试；不要覆盖转换或读取错误。
	if buffering && !c.Writer.Written() && (streamReadErr == nil || errors.Is(streamReadErr, io.EOF)) {
		log.Printf("[Responses-Stream] 流结束仍无内容，判定为空响应 (buffered=%d)", len(bufferedEvents))
		return nil, proxycore.NewRetrySameCandidateError(ErrEmptyStreamResponse)
	}
	if !streamCompleted && !toolCallCompletedCancellation {
		if err := codec.CheckStreamCompletion(c.Request.Context(), streamCompleted, streamReadErr); err != nil {
			return nil, err
		}
	}
	remaining, err := pipeline.DrainAttachedStreamRestoration(c)
	if err != nil {
		return nil, err
	}
	for _, event := range remaining {
		if !writeEvent(event) && !toolCallCompletedCancellation {
			return nil, streamWriteErr
		}
	}
	if err := pipeline.FlushAttachedStreamHooks(c); err != nil {
		return nil, err
	}

	if envCfg.Current().EnableResponseLogs {
		responseTime := time.Since(startTime).Milliseconds()
		log.Printf("[Responses-Stream] Responses 流式响应完成: %dms", responseTime)

		// 输出 Token 统计
		loggedUsage := upstreamUsage
		if !hasUsage {
			loggedUsage = collectedUsage
		}
		if hasUsage || loggedUsage.InputTokens > 0 || loggedUsage.OutputTokens > 0 {
			log.Printf("[Responses-Stream-Token] InputTokens=%d, OutputTokens=%d, CacheCreation=%d, CacheRead=%d, CacheCreation5m=%d, CacheCreation1h=%d, CacheTTL=%s",
				loggedUsage.InputTokens, loggedUsage.OutputTokens,
				loggedUsage.CacheCreationInputTokens, loggedUsage.CacheReadInputTokens,
				loggedUsage.CacheCreation5mInputTokens, loggedUsage.CacheCreation1hInputTokens,
				loggedUsage.CacheTTL)
		}

		if synthesizer != nil {
			synthesizedContent := synthesizer.GetSynthesizedContent()
			parseFailed := synthesizer.IsParseFailed()
			if synthesizedContent != "" && !parseFailed {
				synth := strings.TrimSpace(synthesizedContent)
				if envCfg.ShouldLog("debug") {
					log.Printf("[Responses-Stream] requestId=%s attemptId=%s 合成正文=%d 字节；正文见结构化日志", logger.RequestIDFromContext(c.Request.Context()), logger.AttemptIDFromContext(c.Request.Context()), len(synth))
				}
				logger.RecordStreamSynth(c.Request.Context(), "Responses", synth, synthesizer.Truncated())
			} else if logBuffer.Len() > 0 {
				raw := logBuffer.String()
				if envCfg.ShouldLog("debug") {
					log.Printf("[Responses-Stream] requestId=%s attemptId=%s 原始流=%d 字节；内容见结构化日志", logger.RequestIDFromContext(c.Request.Context()), logger.AttemptIDFromContext(c.Request.Context()), len(raw))
				}
				logger.RecordStreamSynth(c.Request.Context(), "Responses", raw, logBuffer.Truncated)
			}
		} else if logBuffer.Len() > 0 {
			raw := logBuffer.String()
			if envCfg.ShouldLog("debug") {
				log.Printf("[Responses-Stream] requestId=%s attemptId=%s 原始流=%d 字节；内容见结构化日志", logger.RequestIDFromContext(c.Request.Context()), logger.AttemptIDFromContext(c.Request.Context()), len(raw))
			}
			logger.RecordStreamSynth(c.Request.Context(), "Responses", raw, logBuffer.Truncated)
		}
	}

	if !committed && toolCallCompletedCancellation {
		if err := commitStream(true); err != nil {
			return nil, err
		}
	}

	// 返回上游 usage 快照；仅在上游完全没有 usage 时返回受限的本地补全值。
	if hasUsage {
		return upstreamUsage.snapshot(false), nil
	}
	return collectedUsage.snapshot(true), nil
}

func (u responsesStreamUsage) snapshot(estimated bool) *types.Usage {
	return &types.Usage{
		Estimated:                  estimated,
		UnattributedCacheInput:     u.UnattributedCacheInput,
		InputTokens:                u.InputTokens,
		OutputTokens:               u.OutputTokens,
		CacheCreationInputTokens:   u.CacheCreationInputTokens,
		CacheReadInputTokens:       u.CacheReadInputTokens,
		CacheCreation5mInputTokens: u.CacheCreation5mInputTokens,
		CacheCreation1hInputTokens: u.CacheCreation1hInputTokens,
		CacheTTL:                   u.CacheTTL,
	}
}

// responsesStreamUsage 流式响应 usage 收集结构
type responsesStreamUsage struct {
	InputTokens                int
	OutputTokens               int
	TotalTokens                int // 用于检测 total_tokens 是否需要补全
	CacheCreationInputTokens   int
	CacheReadInputTokens       int
	UnattributedCacheInput     *int
	CacheCreation5mInputTokens int
	CacheCreation1hInputTokens int
	CacheTTL                   string
	HasClaudeCache             bool // 是否检测到 Claude 原生缓存字段（区别于 OpenAI cached_tokens）
}

type responsesStreamToolArgumentFragment struct {
	key      string
	fragment string
	name     string
	snapshot bool
	rawInput bool
}

func extractResponsesStreamSafetyFragments(event string) (string, []responsesStreamToolArgumentFragment) {
	var text strings.Builder
	toolArguments := make([]responsesStreamToolArgumentFragment, 0)
	for lineIndex, line := range strings.Split(event, "\n") {
		jsonStr, isData := codec.SSEDataJSON(line)
		if !isData {
			continue
		}
		var data map[string]interface{}
		if err := json.Unmarshal([]byte(jsonStr), &data); err != nil {
			continue
		}
		eventType, _ := data["type"].(string)
		delta, _ := data["delta"].(string)
		switch eventType {
		case "response.output_text.delta", "response.reasoning_summary_text.delta", "response.output_json.delta", "response.content_part.delta":
			if delta != "" {
				text.WriteString(delta)
			} else if value, _ := data["text"].(string); value != "" {
				text.WriteString(value)
			}
		case "response.function_call_arguments.delta", "response.custom_tool_call_input.delta":
			if delta != "" {
				toolArguments = append(toolArguments, responsesStreamToolArgumentFragment{
					key:      responsesStreamToolArgumentKey(data, lineIndex),
					fragment: delta,
					rawInput: eventType == "response.custom_tool_call_input.delta",
				})
			}
		case "response.function_call_arguments.done", "response.custom_tool_call_input.done":
			arguments, _ := data["arguments"].(string)
			if eventType == "response.custom_tool_call_input.done" {
				arguments, _ = data["input"].(string)
			}
			toolArguments = append(toolArguments, responsesStreamToolArgumentFragment{
				key: responsesStreamToolArgumentKey(data, lineIndex), fragment: arguments, snapshot: true,
				rawInput: eventType == "response.custom_tool_call_input.done",
			})
		case "response.output_item.added", "response.output_item.done":
			item, _ := data["item"].(map[string]interface{})
			itemType, _ := item["type"].(string)
			if itemType != "function_call" && itemType != "custom_tool_call" {
				continue
			}
			name, _ := item["name"].(string)
			arguments, _ := item["arguments"].(string)
			if itemType == "custom_tool_call" {
				arguments, _ = item["input"].(string)
			}
			toolArguments = append(toolArguments, responsesStreamToolArgumentFragment{
				key: responsesStreamToolArgumentKey(data, lineIndex), name: name, fragment: arguments,
				snapshot: eventType == "response.output_item.done" || arguments != "",
				rawInput: itemType == "custom_tool_call",
			})
		case "response.completed":
			response, _ := data["response"].(map[string]interface{})
			outputs, _ := response["output"].([]interface{})
			for index, rawItem := range outputs {
				item, _ := rawItem.(map[string]interface{})
				itemType, _ := item["type"].(string)
				if itemType != "function_call" && itemType != "custom_tool_call" {
					continue
				}
				name, _ := item["name"].(string)
				arguments, _ := item["arguments"].(string)
				if itemType == "custom_tool_call" {
					arguments, _ = item["input"].(string)
				}
				toolArguments = append(toolArguments, responsesStreamToolArgumentFragment{
					key: fmt.Sprintf("responses.tool.%d", index), name: name, fragment: arguments, snapshot: true,
					rawInput: itemType == "custom_tool_call",
				})
			}
		}
	}
	return text.String(), toolArguments
}

func responsesStreamToolArgumentKey(data map[string]interface{}, fallback int) string {
	for _, field := range []string{"output_index", "item_id", "call_id"} {
		if value, exists := data[field]; exists {
			if key := strings.TrimSpace(fmt.Sprint(value)); key != "" {
				return "responses.tool." + key
			}
		}
	}
	return fmt.Sprintf("responses.tool.default.%d", fallback)
}

// isResponsesCompletedEvent 检测是否为 response.completed 事件
func isResponsesCompletedEvent(event string) bool {
	return responsesEventType(event) == "response.completed"
}

func isResponsesTerminalEvent(event string) bool {
	eventType := responsesEventType(event)
	return eventType == "response.completed" || eventType == "response.incomplete"
}

func responsesEventType(event string) string {
	for _, line := range strings.Split(event, "\n") {
		payload, isData := codec.SSEDataJSON(line)
		if !isData {
			continue
		}
		var data struct {
			Type string `json:"type"`
		}
		if json.Unmarshal([]byte(payload), &data) == nil {
			return data.Type
		}
	}
	return ""
}

func extractResponsesCompletedID(event string) string {
	return extractResponsesStreamResponseIDForTypes(event, map[string]bool{"response.completed": true})
}

func extractResponsesStreamResponseID(event string) string {
	return extractResponsesStreamResponseIDForTypes(event, map[string]bool{
		"response.created":     true,
		"response.in_progress": true,
		"response.incomplete":  true,
		"response.completed":   true,
	})
}

func extractResponsesStreamResponseIDForTypes(event string, allowedTypes map[string]bool) string {
	for _, line := range strings.Split(event, "\n") {
		jsonStr, isData := codec.SSEDataJSON(line)
		if !isData {
			continue
		}

		var data map[string]interface{}
		if err := json.Unmarshal([]byte(jsonStr), &data); err != nil {
			continue
		}

		eventType, _ := data["type"].(string)
		if !allowedTypes[eventType] {
			continue
		}

		response, ok := data["response"].(map[string]interface{})
		if ok {
			if responseID, _ := response["id"].(string); strings.TrimSpace(responseID) != "" {
				return strings.TrimSpace(responseID)
			}
		}
		// 少数兼容上游把 response id 放在生命周期事件顶层。只对
		// response.created/in_progress/completed 读取，绝不把 output item
		// 的 id 误当成 response id。
		if responseID, _ := data["id"].(string); strings.TrimSpace(responseID) != "" {
			return strings.TrimSpace(responseID)
		}
	}

	return ""
}

func clearResponsesPreviousIDsFromCompletedEvent(event string) string {
	rewritten, _ := codec.RewriteSSEDataLines(event, func(payload string) (string, bool) {
		var data map[string]interface{}
		if err := json.Unmarshal([]byte(payload), &data); err != nil {
			return "", false
		}
		if data["type"] != "response.completed" {
			return "", false
		}
		changed := false
		for _, key := range []string{"previous_id", "previous_response_id"} {
			if _, exists := data[key]; exists {
				delete(data, key)
				changed = true
			}
		}
		response, ok := data["response"].(map[string]interface{})
		if ok {
			for _, key := range []string{"previous_id", "previous_response_id"} {
				if _, exists := response[key]; exists {
					delete(response, key)
					changed = true
				}
			}
		}
		if !changed {
			return "", false
		}
		patched, err := json.Marshal(data)
		if err != nil {
			return "", false
		}
		return string(patched), true
	})
	return rewritten
}

// isClientDisconnectError 判断是否为客户端断开连接错误
func isClientDisconnectError(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "broken pipe") || strings.Contains(msg, "connection reset")
}
