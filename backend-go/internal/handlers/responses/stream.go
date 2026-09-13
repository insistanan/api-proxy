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

	"github.com/BenedictKing/api-proxy/internal/config"
	"github.com/BenedictKing/api-proxy/internal/converters"
	"github.com/BenedictKing/api-proxy/internal/handlers/hooks"
	"github.com/BenedictKing/api-proxy/internal/handlers/proxycore"
	"github.com/BenedictKing/api-proxy/internal/logger"
	"github.com/BenedictKing/api-proxy/internal/scheduler"
	"github.com/BenedictKing/api-proxy/internal/session"
	"github.com/BenedictKing/api-proxy/internal/types"
	"github.com/BenedictKing/api-proxy/internal/utils"
	"github.com/gin-gonic/gin"
)

// hasResponsesContent 判断 SSE 事件是否包含实际 output。兼容只在
// response.completed 中返回完整 output、没有逐段 delta 的上游。
func hasResponsesContent(event string) bool {
	for _, line := range strings.Split(event, "\n") {
		jsonStr, isData := utils.SSEDataJSON(line)
		if !isData {
			continue
		}
		var data map[string]interface{}
		if err := json.Unmarshal([]byte(jsonStr), &data); err != nil {
			continue
		}
		eventType, _ := data["type"].(string)
		if eventType == "response.completed" {
			if response, ok := data["response"].(map[string]interface{}); ok {
				if output, ok := response["output"].([]interface{}); ok && len(output) > 0 {
					return true
				}
			}
		}
		switch eventType {
		case "response.output_text.delta",
			"response.function_call_arguments.delta",
			"response.reasoning_summary_text.delta",
			"response.output_json.delta",
			"response.content_part.delta",
			"response.audio.delta",
			"response.audio_transcript.delta",
			"response.output_item.added",
			"response.output_text.done",
			"response.reasoning_summary_text.done",
			"response.function_call_arguments.done",
			"response.output_item.done":
			return true
		}
	}
	return false
}

func responsesStreamEventError(event string) error {
	for _, line := range strings.Split(event, "\n") {
		payload, isData := utils.SSEDataJSON(line)
		if !isData {
			continue
		}
		var data map[string]interface{}
		if err := json.Unmarshal([]byte(payload), &data); err != nil {
			continue
		}
		eventType, _ := data["type"].(string)
		if eventType != "response.failed" && eventType != "error" {
			continue
		}
		if proxycore.IsUpstreamModelCapacityError([]byte(payload)) {
			return proxycore.NewRetrySameCandidateError(ErrEmptyStreamResponse)
		}
		return fmt.Errorf("upstream stream failed: %s", payload)
	}
	return nil
}

// hasDeliveredResponsesToolCall 判断事件是否已完整交付一个客户端可执行的工具调用。
// Codex 收到 output_item.done 后会立即进入工具执行阶段，并可能主动结束当前 SSE 连接；
// 这种终止代表本轮代理目标已经完成，不应记为普通客户端取消。
func hasDeliveredResponsesToolCall(event string) bool {
	for _, line := range strings.Split(event, "\n") {
		payload, isData := utils.SSEDataJSON(line)
		if !isData {
			continue
		}
		var data map[string]interface{}
		if err := json.Unmarshal([]byte(payload), &data); err != nil {
			continue
		}
		if eventType, _ := data["type"].(string); eventType != "response.output_item.done" {
			continue
		}
		item, _ := data["item"].(map[string]interface{})
		itemType, _ := item["type"].(string)
		if itemType == "function_call" || itemType == "custom_tool_call" {
			return true
		}
	}
	return false
}

// handleStreamSuccess 处理流式响应。
// 返回 error 仅在"空响应未写入客户端"场景下非 nil（ErrEmptyStreamResponse），
// 使上层 UpstreamAttempt 的 Key/BaseURL 轮转可以安全 failover。
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
) (*types.Usage, error) {
	if envCfg.EnableResponseLogs {
		responseTime := time.Since(startTime).Milliseconds()
		log.Printf("[Responses-Stream] Responses 流式响应开始: %dms, 状态: %d", responseTime, resp.StatusCode)
	}

	var synthesizer *utils.StreamSynthesizer
	var logBuffer bytes.Buffer
	streamLoggingEnabled := envCfg.EnableResponseLogs

	if streamLoggingEnabled {
		synthesizer = utils.NewStreamSynthesizer(upstreamType)
	}

	var converterState any

	flusher, _ := c.Writer.(http.Flusher)
	streamStarted := false
	startStream := func() {
		if streamStarted {
			return
		}
		utils.ForwardResponseHeaders(resp.Header, c.Writer)
		c.Header("Content-Type", "text/event-stream")
		c.Header("Cache-Control", "no-cache")
		c.Header("Connection", "keep-alive")
		c.Header("X-Accel-Buffering", "no")
		c.Status(resp.StatusCode)
		streamStarted = true
	}

	stopVeto := hooks.BindAttachedStreamVeto(c, resp.Body)
	defer stopVeto()
	// 检查流式响应是否含有 Content-Encoding。由于已强制设置 Accept-Encoding: identity，
	// 正常情况下上游不应压缩 SSE；若上游违规压缩，尝试包装流式解码 Reader 以防分块解析失败。
	if encoding := utils.GetContentEncoding(resp.Header); encoding != "" {
		log.Printf("[Responses-Stream] 警告: 上游流式响应包含 Content-Encoding: %s，尝试流式解压", encoding)
		wrappedBody, wrapped, wrapErr := utils.WrapStreamReaderIfNeeded(resp)
		if wrapErr != nil {
			log.Printf("[Responses-Stream] 警告: 包装流式解压 Reader 失败 (%s): %v", encoding, wrapErr)
		} else if wrapped {
			resp.Body = wrappedBody
			utils.StripEntityHeadersForRebuiltBody(resp.Header)
		}
	}
	reader := bufio.NewReaderSize(resp.Body, 64*1024)

	// Token 统计状态
	sessionCollector := newResponsesStreamSessionCollector()
	var collectedUsage responsesStreamUsage
	var upstreamUsage responsesStreamUsage
	hasUsage := false
	needTokenPatch := false
	clientGone := false
	toolCallDelivered := false
	var streamResponseID string

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
		startStream()
		proxycore.MarkRequestLogFirstToken(c)
		if _, err := c.Writer.Write([]byte(event)); err != nil {
			clientGone = true
			if !isClientDisconnectError(err) {
				log.Printf("[Responses-Stream] 警告: 流式响应传输错误: %v", err)
			}
			return false
		}
		if flusher != nil {
			flusher.Flush()
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
		case <-hooks.AttachedStreamVetoDone(c):
			return nil, hooks.HandleAttachedStreamVeto(c)
		default:
		}
		rawLine, readErr := reader.ReadBytes('\n')
		if err := hooks.AttachedStreamVetoError(c); err != nil {
			return nil, err
		}
		if len(rawLine) == 0 && readErr != nil {
			if !errors.Is(readErr, io.EOF) {
				streamReadErr = readErr
			}
			break
		}

		line := strings.TrimSuffix(strings.TrimSuffix(string(rawLine), "\n"), "\r")

		if streamLoggingEnabled {
			logBuffer.Write(rawLine)
			if synthesizer != nil {
				synthesizer.ProcessLine(line)
			}
		}

		// 处理转换后的事件
		var eventsToProcess []string

		events, err := converters.ConvertUpstreamStreamLineToResponses(
			c.Request.Context(),
			upstreamType,
			originalReq.Model,
			originalRequestJSON,
			[]byte(line),
			&converterState,
		)
		if err != nil {
			log.Printf("[Responses-Stream] 流式响应转换失败: %v", err)
			streamReadErr = fmt.Errorf("转换上游流式响应失败: %w", err)
			break
		}
		eventsToProcess = events

		for _, event := range eventsToProcess {
			if eventErr := responsesStreamEventError(event); eventErr != nil {
				return nil, eventErr
			}
			// 检测并收集 usage
			detected, needPatch, usageData := checkResponsesEventUsage(event, envCfg.EnableResponseLogs && envCfg.ShouldLog("debug"))
			if detected {
				if !hasUsage {
					hasUsage = true
					needTokenPatch = needPatch
					if envCfg.EnableResponseLogs && envCfg.ShouldLog("debug") && needPatch {
						log.Printf("[Responses-Stream-Token] 检测到虚假值, 延迟到流结束修补")
					}
				}
				updateResponsesStreamUsage(&collectedUsage, usageData)
				updateResponsesStreamUsage(&upstreamUsage, usageData)
			}

			// 在 response.completed 事件前注入/修补 usage
			eventToSend := event
			if isResponsesCompletedEvent(event) {
				// 仅按 Responses output item 的语义字段估算 output_tokens。
				// 不能把 reasoning/function-call/audio 的 SSE delta 与完整
				// response.completed JSON envelope 混成普通文本，否则无 usage
				// 的上游会把 envelope 字段也算进输出，且大响应会额外占用内存。
				estimatedOutputTokens := estimateResponsesOutputFromItems(sessionCollector.Items())
				if !hasUsage {
					// 上游完全没有 usage，注入本地估算
					var injectedInput, injectedOutput int
					eventToSend, injectedInput, injectedOutput = injectResponsesUsageToCompletedEventWithTokens(event, usageRequestJSON, estimatedOutputTokens, envCfg)
					// 更新 collectedUsage 以便最终日志输出
					collectedUsage.InputTokens = injectedInput
					collectedUsage.OutputTokens = injectedOutput
					collectedUsage.TotalTokens = injectedInput + injectedOutput
					if envCfg.EnableResponseLogs && envCfg.ShouldLog("debug") {
						log.Printf("[Responses-Stream-Token] 上游无usage, 注入本地估算: input=%d, output=%d", injectedInput, injectedOutput)
					}
				} else if needTokenPatch {
					// 需要修补虚假值
					eventToSend = patchResponsesCompletedEventUsageWithTokens(event, usageRequestJSON, estimatedOutputTokens, &collectedUsage, envCfg)
				}
				// 透传分支（上游本身就是 responses 协议）剥离累积式缓存统计：
				// grok-4.6 等 OpenAI 兼容上游的 input_tokens_details.cached_tokens 是跨请求
				// 单调递增的累积命中量，原样下发会让 Cursor 误判上下文一直满、反复触发压缩。
				// 真正 Claude 上游的 cache_read_input_tokens 与携带 previous_response_id 的
				// 链式增量请求不受影响（函数内按字段语义判断保留）。
				if upstreamType == converters.ResponsesUpstreamResponses {
					eventToSend = stripAccumulatedCacheFromCompletedEvent(eventToSend, usageRequestJSON)
					// 剥离缓存字段后 input_tokens 成了客户端判断上下文占用的唯一依据，
					// 校正上游对长上下文的错报值，否则 Cursor 会误判上下文为空、永不触发压缩。
					// 只有仍处于真实链式增量模式时才跳过；压缩边界已经使用不带旧 ID 的
					// usageRequestJSON，因此必须按新的完整历史重新校正。
					eventToSend = correctUnderreportedInputTokensInCompletedEvent(eventToSend, usageRequestJSON, envCfg)
				}
			}
			if streamResponseID == "" {
				streamResponseID = extractResponsesStreamResponseID(eventToSend)
			}
			if isResponsesCompletedEvent(eventToSend) && originalReq != nil &&
				(utils.ResponsesItemsContainCompactionFromInput(originalReq.Input) ||
					c.GetBool(utils.ContextKeyResponsesPreviousIDDropped)) {
				eventToSend = clearResponsesPreviousIDsFromCompletedEvent(eventToSend)
			}
			text, toolArguments := extractResponsesStreamSafetyFragments(eventToSend)
			if err := hooks.FeedAttachedStreamText(c, text); err != nil {
				return nil, err
			}
			for _, toolArgument := range toolArguments {
				var err error
				if toolArgument.rawInput {
					err = hooks.FeedAttachedStreamToolInput(c, toolArgument.key, toolArgument.fragment, toolArgument.snapshot, toolArgument.name)
				} else if toolArgument.snapshot {
					err = hooks.FeedAttachedStreamToolArgumentsSnapshot(c, toolArgument.key, toolArgument.fragment, toolArgument.name)
				} else {
					err = hooks.FeedAttachedStreamToolArgumentsForKey(c, toolArgument.key, toolArgument.fragment, toolArgument.name)
				}
				if err != nil {
					return nil, err
				}
			}
			eventToSend, err = hooks.RestoreAttachedSSEEvent(c, eventToSend)
			if err != nil {
				return nil, err
			}
			// 会话持久化保存客户端可见的已还原内容；下一轮即使只携带
			// previous_response_id，也会重新经过上行脱敏而不会遗留旧占位符。
			sessionCollector.consumeEvent(eventToSend)

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
					if isResponsesCompletedEvent(eventToSend) {
						log.Printf("[Responses-Stream] 检测到空响应 (model at capacity)，buffered=%d, written=%v",
							len(bufferedEvents), c.Writer.Written())
						// 不写入任何数据，返回 error 让上层 failover
						if envCfg.EnableResponseLogs {
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

			// 正常（已退出缓冲阶段）转发给客户端
			if writeEvent(eventToSend) && hasDeliveredResponsesToolCall(eventToSend) {
				toolCallDelivered = true
			}
		}

		if readErr != nil {
			if !errors.Is(readErr, io.EOF) {
				streamReadErr = readErr
			}
			break
		}
	}

	contextErr := c.Request.Context().Err()
	toolCallCompletedCancellation := toolCallDelivered && errors.Is(contextErr, context.Canceled)
	if contextErr != nil && !toolCallCompletedCancellation {
		return nil, contextErr
	}
	if streamReadErr != nil && !toolCallCompletedCancellation {
		return nil, streamReadErr
	}
	if toolCallCompletedCancellation && envCfg.ShouldLog("info") {
		log.Printf("[Responses-Stream] 工具调用已完整交付，客户端结束当前流，按成功回合记录")
	}

	// 流正常结束但没有内容时才按瞬时空响应重试；不要覆盖转换或读取错误。
	if buffering && !c.Writer.Written() {
		log.Printf("[Responses-Stream] 流结束仍无内容，判定为空响应 (buffered=%d)", len(bufferedEvents))
		return nil, proxycore.NewRetrySameCandidateError(ErrEmptyStreamResponse)
	}
	remaining, err := hooks.DrainAttachedStreamRestoration(c)
	if err != nil {
		return nil, err
	}
	for _, event := range remaining {
		writeEvent(event)
	}
	if err := hooks.FlushAttachedStreamHooks(c); err != nil {
		return nil, err
	}

	if envCfg.EnableResponseLogs {
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
				log.Printf("[Responses-Stream] 上游流式响应合成内容:\n%s", utils.RedactSensitivePlaceholdersForLog(synth))
				logger.RecordStreamSynth(c.Request.Context(), "Responses", synth)
			} else if logBuffer.Len() > 0 {
				raw := logBuffer.String()
				log.Printf("[Responses-Stream] 上游流式响应原始内容:\n%s", utils.RedactSensitivePlaceholdersForLog(raw))
				logger.RecordStreamSynth(c.Request.Context(), "Responses", raw)
			}
		} else if logBuffer.Len() > 0 {
			raw := logBuffer.String()
			log.Printf("[Responses-Stream] 上游流式响应原始内容:\n%s", utils.RedactSensitivePlaceholdersForLog(raw))
			logger.RecordStreamSynth(c.Request.Context(), "Responses", raw)
		}
	}

	// 即使客户端向上游声明 store=false，本代理仍需保存会话链，保证重启后可继续。
	if originalReq != nil {
		sessionLookupPreviousResponseID := originalReq.PreviousResponseID
		if c.GetBool(utils.ContextKeyResponsesHistoryBoundary) {
			// 当前 input 已成为新的历史根，按 conversation 找到旧 session
			// 后替换它；不能再用旧 response ID 恢复旧边界。
			sessionLookupPreviousResponseID = ""
		}
		if sess, err := sessionManager.GetOrCreateSessionForConversation(sessionLookupPreviousResponseID, conversationID); err == nil {
			if collectorErr := sessionCollector.Err(); collectorErr != nil {
				log.Printf("[Session] Responses 流式 output 不完整，跳过本轮会话持久化: %v", collectorErr)
			} else if !sessionCollector.Completed() &&
				!(toolCallDelivered && sessionCollector.CompletedToolCall()) {
				// 正常文本流必须以 response.completed 收尾。客户端在收到
				// response.output_item.done 后为执行工具而主动断开是唯一
				// 可接受的例外；其余 EOF/取消都可能只是半截响应，不能写入
				// session，否则下一轮会把不完整正文或参数当成真实历史。
				log.Printf("[Session] Responses 流未收到完整完成事件，跳过本轮会话持久化")
			} else {
				inputItems, parseErr := parseInputToItems(originalReq.Input)
				if parseErr != nil {
					log.Printf("[Session] 保存 Responses 流式输入历史失败: %v", parseErr)
				} else {
					outputItems := sessionCollector.Items()
					turnItems := make([]types.ResponsesItem, 0, len(inputItems)+len(outputItems))
					turnItems = append(turnItems, inputItems...)
					turnItems = append(turnItems, outputItems...)
					sessionTokens := upstreamUsage.TotalTokens
					if sessionTokens <= 0 {
						if hasUsage {
							sessionTokens = upstreamUsage.InputTokens + upstreamUsage.OutputTokens
						} else {
							sessionTokens = collectedUsage.TotalTokens
						}
					}
					var commitErr error
					if utils.ResponsesItemsContainCompactionFromInput(originalReq.Input) {
						// compaction 必须优先走 CommitTurn 的根截断逻辑；即使
						// 上游请求同时移除了 previous_response_id，也不能把压缩前
						// 的完整 input 通过 ReplaceSessionAfterBoundary 原样保存。
						commitErr = sessionManager.CommitTurn(sess.ID, turnItems, sessionTokens,
							utils.DetectImageContent(originalRequestJSON), streamResponseID)
					} else if c.GetBool(utils.ContextKeyResponsesHistoryBoundary) {
						commitErr = sessionManager.ReplaceSessionAfterBoundary(sess.ID, turnItems,
							utils.DetectImageContent(originalRequestJSON), streamResponseID)
					} else {
						commitErr = sessionManager.CommitTurn(sess.ID, turnItems, sessionTokens,
							utils.DetectImageContent(originalRequestJSON), streamResponseID)
					}
					if err := commitErr; err != nil {
						log.Printf("[Session] 持久化 Responses 流式会话轮次失败: %v", err)
					} else if utils.ResponsesItemsContainCompactionFromInput(originalReq.Input) ||
						c.GetBool(utils.ContextKeyResponsesHistoryBoundary) {
						// 压缩或客户端历史替换已建立新的本地历史根，清理旧的
						// Messages→Responses 链，避免下一轮复用旧服务端上下文。
						session.DefaultResponseChainManager().Clear(conversationID)
					}
				}
			}
		} else {
			log.Printf("[Session] 保存 Responses 流式会话失败: %v", err)
		}
	}
	proxycore.AssociateConversationExternalID(channelScheduler, conversationID, scheduler.ChannelKindResponses, streamResponseID)

	// 返回上游 usage 快照；仅在上游完全没有 usage 时返回受限的本地补全值。
	metricsUsage := upstreamUsage
	if !hasUsage {
		metricsUsage = collectedUsage
	}
	// 返回收集到的 usage 数据
	return &types.Usage{
		InputTokens:                metricsUsage.InputTokens,
		OutputTokens:               metricsUsage.OutputTokens,
		CacheCreationInputTokens:   metricsUsage.CacheCreationInputTokens,
		CacheReadInputTokens:       metricsUsage.CacheReadInputTokens,
		CacheCreation5mInputTokens: metricsUsage.CacheCreation5mInputTokens,
		CacheCreation1hInputTokens: metricsUsage.CacheCreation1hInputTokens,
		CacheTTL:                   metricsUsage.CacheTTL,
	}, nil
}

// responsesStreamUsage 流式响应 usage 收集结构
type responsesStreamUsage struct {
	InputTokens                int
	OutputTokens               int
	TotalTokens                int // 用于检测 total_tokens 是否需要补全
	CacheCreationInputTokens   int
	CacheReadInputTokens       int
	CacheCreation5mInputTokens int
	CacheCreation1hInputTokens int
	CacheTTL                   string
	HasClaudeCache             bool // 是否检测到 Claude 原生缓存字段（区别于 OpenAI cached_tokens）
}

// extractResponsesTextFromEvent 从 Responses SSE 事件中提取文本内容
func extractResponsesTextFromEvent(event string, buf *bytes.Buffer) {
	for _, line := range strings.Split(event, "\n") {
		jsonStr, isData := utils.SSEDataJSON(line)
		if !isData {
			continue
		}
		var data map[string]interface{}
		if err := json.Unmarshal([]byte(jsonStr), &data); err != nil {
			continue
		}

		eventType, _ := data["type"].(string)

		// 处理各种 delta 类型
		switch eventType {
		case "response.output_text.delta":
			if delta, ok := data["delta"].(string); ok {
				buf.WriteString(delta)
			}
		case "response.function_call_arguments.delta", "response.custom_tool_call_input.delta":
			if delta, ok := data["delta"].(string); ok {
				buf.WriteString(delta)
			}
		case "response.reasoning_summary_text.delta":
			if delta, ok := data["delta"].(string); ok {
				buf.WriteString(delta)
			} else if text, ok := data["text"].(string); ok {
				buf.WriteString(text)
			}
		case "response.output_json.delta":
			// JSON 输出增量
			if delta, ok := data["delta"].(string); ok {
				buf.WriteString(delta)
			}
		case "response.content_part.delta":
			// 内容块增量（通用）
			if delta, ok := data["delta"].(string); ok {
				buf.WriteString(delta)
			} else if text, ok := data["text"].(string); ok {
				buf.WriteString(text)
			}
		case "response.audio.delta", "response.audio_transcript.delta":
			// 音频转录增量
			if delta, ok := data["delta"].(string); ok {
				buf.WriteString(delta)
			}
		}
	}
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
		jsonStr, isData := utils.SSEDataJSON(line)
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
	return strings.Contains(event, `"type":"response.completed"`) ||
		strings.Contains(event, `"type": "response.completed"`)
}

func extractResponsesCompletedID(event string) string {
	return extractResponsesStreamResponseIDForTypes(event, map[string]bool{"response.completed": true})
}

func extractResponsesStreamResponseID(event string) string {
	return extractResponsesStreamResponseIDForTypes(event, map[string]bool{
		"response.created":     true,
		"response.in_progress": true,
		"response.completed":   true,
	})
}

func extractResponsesStreamResponseIDForTypes(event string, allowedTypes map[string]bool) string {
	for _, line := range strings.Split(event, "\n") {
		jsonStr, isData := utils.SSEDataJSON(line)
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
	rewritten, _ := utils.RewriteSSEDataLines(event, func(payload string) (string, bool) {
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
