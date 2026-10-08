// 本文件是 Claude 协议流式响应的主链路：Context 等上下文类型的定义、
// HandleStreamResponse / ProcessStreamEvents 事件循环（读上游 -> 观测/修补 -> 写客户端）
// 与单事件处理 ProcessStreamEvent。
// 同包其余职责：事件判定与构造 stream_events.go，usage 检测/修补
// stream_usage_detect.go / stream_usage_patch.go，thinking 与工具调用累积
// stream_reasoning.go，结束收尾与日志 stream_log.go。
package streams

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/BenedictKing/api-proxy/internal/config"
	"github.com/BenedictKing/api-proxy/internal/handlers/hooks"
	"github.com/BenedictKing/api-proxy/internal/handlers/proxycore"
	"github.com/BenedictKing/api-proxy/internal/providers"
	"github.com/BenedictKing/api-proxy/internal/types"
	"github.com/BenedictKing/api-proxy/internal/utils"
	"github.com/gin-gonic/gin"
)

// Context 流处理上下文
type Context struct {
	LogBuffer        bytes.Buffer
	OutputTextBuffer bytes.Buffer
	ResponseText     bytes.Buffer
	Reasoning        bytes.Buffer
	Synthesizer      *utils.StreamSynthesizer
	LoggingEnabled   bool
	LogBufferFull    bool // LogBuffer 达到上限后标记，避免重复检查
	ClientGone       bool
	HasUsage         bool
	NeedTokenPatch   bool
	// 客户端出口使用的 token 统计，允许做安全补全和缓存字段整理。
	CollectedUsage CollectedUsageData
	// 上游原始 token 快照，仅用于指标与调用方统计，不接受客户端出口修补。
	UpstreamUsage CollectedUsageData
	// 用于日志的"续写前缀"（不参与真实转发，只影响 Stream-Synth 输出可读性）
	LogPrefillText string
	// SSE 事件调试追踪
	EventCount        int            // 事件总数
	ContentBlockCount int            // content block 计数
	ContentBlockTypes map[int]string // 每个 block 的类型
	// 低质量渠道处理
	RequestModel string // 请求中的 model（用于一致性检查）
	LowQuality   bool   // 是否为低质量渠道
	// 兼容旧的隐式缓存推断状态。标准 Anthropic usage 不再填入该字段，避免把
	// message_start 与 message_delta 的供应商私有差值冒充 cache_read。
	MessageStartInputTokens int
	// 兜底注入
	SeenMessageStart bool // 是否已收到 message_start 事件
	// 最终转换为 Claude SSE 后的工具调用；用于跨协议回放 reasoning_content。
	ToolCalls map[int]*StreamToolCall
}

type StreamToolCall struct {
	ID        string
	Name      string
	Arguments strings.Builder
}

// CollectedUsageData 从流事件中收集的 usage 数据
type CollectedUsageData struct {
	InputTokens              int
	OutputTokens             int
	CacheCreationInputTokens int
	CacheReadInputTokens     int
	// 缓存 TTL 细分
	CacheCreation5mInputTokens int
	CacheCreation1hInputTokens int
	CacheTTL                   string // "5m" | "1h" | "mixed"
}

// NewStreamContext 创建流处理上下文
func NewStreamContext(envCfg *config.EnvConfig) *Context {
	ctx := &Context{
		LoggingEnabled:    envCfg.EnableResponseLogs,
		ContentBlockTypes: make(map[int]string),
		ToolCalls:         make(map[int]*StreamToolCall),
	}
	if ctx.LoggingEnabled {
		ctx.Synthesizer = utils.NewStreamSynthesizer("claude")
	}
	return ctx
}

// seedSynthesizerFromRequest 将请求里预置的 assistant 文本拼接进合成器（仅用于日志可读性）
//
// Claude Code 的部分内部调用会在 messages 里预置一条 assistant 内容（例如 "{"），让模型只输出“续写”部分。
// 这会导致我们仅基于 SSE delta 合成的日志缺失开头。这里用请求体做一次轻量补齐。
func seedSynthesizerFromRequest(ctx *Context, requestBody []byte) {
	if ctx == nil || ctx.Synthesizer == nil || len(requestBody) == 0 {
		return
	}

	var req struct {
		Messages []struct {
			Role    string `json:"role"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(requestBody, &req); err != nil {
		return
	}

	// 只取最后一条 assistant，避免把历史上下文都拼进日志
	for i := len(req.Messages) - 1; i >= 0; i-- {
		msg := req.Messages[i]
		if msg.Role != "assistant" {
			continue
		}
		var b strings.Builder
		for _, c := range msg.Content {
			if c.Type == "text" && c.Text != "" {
				b.WriteString(c.Text)
			}
		}
		prefill := b.String()
		// 防止把很长的预置内容刷进日志
		if len(prefill) > 0 && len(prefill) <= 256 {
			ctx.LogPrefillText = prefill
		}
		return
	}
}

// SetupStreamHeaders 设置流式响应头
func SetupStreamHeaders(c *gin.Context, resp *http.Response) {
	utils.ForwardResponseHeaders(resp.Header, c.Writer)
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")
	c.Status(200)
}

// HandleStreamResponse 处理流式响应（Messages API）
func HandleStreamResponse(
	c *gin.Context,
	resp *http.Response,
	provider providers.Provider,
	envCfg *config.EnvConfig,
	startTime time.Time,
	upstream *config.UpstreamConfig,
	requestBody []byte,
	requestModel string,
) (*types.Usage, error) {
	defer resp.Body.Close()
	stopVeto := hooks.BindAttachedStreamVeto(c, resp.Body)
	defer stopVeto()
	streamCtx, cancelStream := context.WithCancel(c.Request.Context())
	defer cancelStream()

	// 校验上游 Content-Type：如果不是 event-stream，说明上游返回了非流式错误内容。
	// 此时还未写响应头，返回 error 可触发 failover。
	if !IsEventStreamResponse(resp) {
		bodySnippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		snippet := strings.TrimSpace(string(bodySnippet))
		log.Printf("[Messages-Stream] 上游返回非 event-stream Content-Type: %s, body: %s",
			resp.Header.Get("Content-Type"), utils.RedactSensitivePlaceholdersForLog(snippet))
		return nil, fmt.Errorf("upstream returned non-stream response (Content-Type: %s): %s",
			resp.Header.Get("Content-Type"), snippet)
	}

	// 检查流式响应是否含有 Content-Encoding。由于已强制设置 Accept-Encoding: identity，
	// 正常情况下上游不应压缩 SSE；若上游违规压缩，尝试包装流式解码 Reader 以防分块解析失败。
	if encoding := utils.GetContentEncoding(resp.Header); encoding != "" {
		log.Printf("[Messages-Stream] 警告: 上游流式响应包含 Content-Encoding: %s，尝试流式解压", encoding)
		wrappedBody, wrapped, wrapErr := utils.WrapStreamReaderIfNeeded(resp)
		if wrapErr != nil {
			log.Printf("[Messages-Stream] 警告: 包装流式解压 Reader 失败 (%s): %v", encoding, wrapErr)
		} else if wrapped {
			resp.Body = wrappedBody
			utils.StripEntityHeadersForRebuiltBody(resp.Header)
		}
	}

	// 使用带 ctx 的流式方法：客户端断连时 ctx 取消，
	// provider goroutine 在向 eventChan 发送事件时 select ctx.Done() 立即退出，
	// 杜绝"缓冲写满后永久阻塞"的 goroutine/上游连接泄漏。
	eventChan, errChan, err := provider.HandleStreamResponseCtx(streamCtx, resp.Body)
	if err != nil {
		return nil, err
	}

	// Prime 阶段：在向客户端提交（Commit）响应头前，先预读第一个事件。
	// 若首包到达前上游报错、流异常中断或返回错误信封事件，保持 c.Writer.Written() 为 false，
	// 返回 error 让上层 proxycore 触发 Key/渠道级 Failover。
	var firstEvent string
	var primed bool

	for !primed {
		select {
		case <-c.Request.Context().Done():
			return nil, c.Request.Context().Err()
		case <-hooks.AttachedStreamVetoDone(c):
			return nil, hooks.HandleAttachedStreamVeto(c)

		case err, ok := <-errChan:
			if vetoErr := hooks.AttachedStreamVetoError(c); vetoErr != nil {
				return nil, vetoErr
			}
			if !ok {
				errChan = nil
				continue
			}
			if err != nil {
				log.Printf("[Messages-Stream] 预读首个流事件失败: %v", err)
				return nil, err
			}

		case event, ok := <-eventChan:
			if err := hooks.AttachedStreamVetoError(c); err != nil {
				return nil, err
			}
			if !ok {
				return nil, fmt.Errorf("upstream stream closed without sending any events")
			}
			firstEvent = event
			primed = true
		}
	}

	if IsStreamErrorEvent(firstEvent) {
		errorMsg := ExtractStreamErrorMessage(firstEvent)
		log.Printf("[Messages-Stream] 上游首包返回错误事件: %s", errorMsg)
		return nil, fmt.Errorf("upstream stream returned error event: %s", errorMsg)
	}

	// Commit 阶段：成功收到有效首包后，正式向客户端写入响应头与状态码
	SetupStreamHeaders(c, resp)

	w := c.Writer
	flusher, ok := w.(http.Flusher)
	if !ok {
		log.Printf("[Messages-Stream] 警告: ResponseWriter不支持Flush接口")
		return nil, fmt.Errorf("ResponseWriter不支持Flush接口")
	}
	flusher.Flush()

	ctx := NewStreamContext(envCfg)
	ctx.RequestModel = requestModel
	ctx.LowQuality = upstream.LowQuality
	seedSynthesizerFromRequest(ctx, requestBody)

	// 处理并写出预读的首个事件
	if err := ProcessStreamEvent(c, w, flusher, firstEvent, ctx, envCfg, requestBody); err != nil {
		// 内容安全错误由上层统一按入口协议编码，避免先写通用
		// stream_error、随后再写 content_safety_error 的重复事件。
		if !ctx.ClientGone && hooks.ContentSafetyErrorFrom(err) == nil && hooks.ContentSafetyHookErrorFrom(err) == nil {
			w.Write([]byte(BuildStreamErrorEvent(err)))
			flusher.Flush()
		}
		return nil, err
	}

	usage, processErr := ProcessStreamEvents(c, w, flusher, eventChan, errChan, ctx, envCfg, startTime, requestBody)
	if processErr == nil {
		ctx.cacheClaudeReasoning()
	}
	return usage, processErr
}

// ProcessStreamEvents 处理流事件循环
// 返回值: error 表示流处理过程中是否发生错误（用于调用方决定是否记录失败指标）
func ProcessStreamEvents(
	c *gin.Context,
	w gin.ResponseWriter,
	flusher http.Flusher,
	eventChan <-chan string,
	errChan <-chan error,
	ctx *Context,
	envCfg *config.EnvConfig,
	startTime time.Time,
	requestBody []byte,
) (*types.Usage, error) {
	for {
		select {
		case <-c.Request.Context().Done():
			ctx.ClientGone = true
			if envCfg.ShouldLog("info") {
				log.Printf("[Messages-Stream] 客户端上下文已取消: %v", c.Request.Context().Err())
			}
			logPartialResponse(c, ctx, envCfg)
			return nil, c.Request.Context().Err()

		case <-hooks.AttachedStreamVetoDone(c):
			// 危险命令 AI 闸门的事后否决：用户在否决窗口内加黑名单，
			// 终止当前流，错误 SSE 和终止序列由协议上层统一写出。
			logPartialResponse(c, ctx, envCfg)
			return nil, hooks.HandleAttachedStreamVeto(c)

		case event, ok := <-eventChan:
			if err := hooks.AttachedStreamVetoError(c); err != nil {
				logPartialResponse(c, ctx, envCfg)
				return nil, err
			}
			if !ok {
				remaining, err := hooks.DrainAttachedStreamRestoration(c)
				if err != nil {
					return nil, err
				}
				for _, pendingEvent := range remaining {
					if _, err := w.Write([]byte(pendingEvent)); err != nil {
						return nil, err
					}
					flusher.Flush()
				}
				if err := hooks.FlushAttachedStreamHooks(c); err != nil {
					return nil, err
				}
				usage := logStreamCompletion(c, ctx, envCfg, startTime)
				return usage, nil
			}
			if err := ProcessStreamEvent(c, w, flusher, event, ctx, envCfg, requestBody); err != nil {
				// 内容安全错误由上层统一按入口协议编码，避免先写通用
				// stream_error、随后再写 content_safety_error 的重复事件。
				if !ctx.ClientGone && hooks.ContentSafetyErrorFrom(err) == nil && hooks.ContentSafetyHookErrorFrom(err) == nil {
					w.Write([]byte(BuildStreamErrorEvent(err)))
					flusher.Flush()
				}
				return nil, err
			}

		case err, ok := <-errChan:
			if vetoErr := hooks.AttachedStreamVetoError(c); vetoErr != nil {
				logPartialResponse(c, ctx, envCfg)
				return nil, vetoErr
			}
			if !ok {
				errChan = nil
				continue
			}
			if err != nil {
				log.Printf("[Messages-Stream] 错误: 流式传输错误: %v", err)
				logPartialResponse(c, ctx, envCfg)
				if !ctx.ClientGone && hooks.ContentSafetyErrorFrom(err) == nil && hooks.ContentSafetyHookErrorFrom(err) == nil {
					w.Write([]byte(BuildStreamErrorEvent(err)))
					flusher.Flush()
				}

				return nil, err
			}
		}
	}
}

// ProcessStreamEvent 处理单个流事件
func ProcessStreamEvent(
	c *gin.Context,
	w gin.ResponseWriter,
	flusher http.Flusher,
	event string,
	ctx *Context,
	envCfg *config.EnvConfig,
	requestBody []byte,
) error {
	if err := hooks.AttachedStreamVetoError(c); err != nil {
		return err
	}
	// SSE 事件调试日志
	ctx.EventCount++
	if envCfg.SSEDebugLevel == "full" || envCfg.SSEDebugLevel == "summary" {
		eventType, blockIndex, blockType := extractSSEEventInfo(event)
		if eventType == "content_block_start" {
			ctx.ContentBlockCount++
			if blockType != "" {
				ctx.ContentBlockTypes[blockIndex] = blockType
			}
		}
		if envCfg.SSEDebugLevel == "full" {
			log.Printf("[Messages-Stream-Event] #%d 类型=%s 长度=%d block_index=%d block_type=%s",
				ctx.EventCount, eventType, len(event), blockIndex, blockType)
			// 对于 content_block 相关事件，记录详细内容
			if strings.Contains(event, "content_block") {
				log.Printf("[Messages-Stream-Event] 详情: %s", truncateForLog(event, 500))
			}
		}
	}

	eventData, hasEventData := ParseSSEEventData(event)
	if hasEventData {
		ctx.captureReasoningContext(eventData)
		text, toolArguments := streamSafetyFragments(eventData)
		if err := hooks.FeedAttachedStreamText(c, text); err != nil {
			return err
		}
		for _, toolArgument := range toolArguments {
			var err error
			if toolArgument.snapshot {
				err = hooks.FeedAttachedStreamToolArgumentsSnapshot(c, toolArgument.key, toolArgument.fragment, toolArgument.name)
			} else {
				err = hooks.FeedAttachedStreamToolArgumentsForKey(c, toolArgument.key, toolArgument.fragment, toolArgument.name)
			}
			if err != nil {
				return err
			}
		}
	}

	// 提取文本用于估算 token
	if hasEventData {
		ExtractTextFromEventData(eventData, &ctx.OutputTextBuffer)
	} else {
		ExtractTextFromEvent(event, &ctx.OutputTextBuffer)
	}

	// 检测并收集 usage
	var hasUsage, needInputPatch, needOutputPatch bool
	var usageData CollectedUsageData
	if hasEventData {
		hasUsage, needInputPatch, needOutputPatch, usageData = CheckEventUsageStatusFromData(eventData, envCfg.EnableResponseLogs && envCfg.ShouldLog("debug"))
	} else {
		hasUsage, needInputPatch, needOutputPatch, usageData = CheckEventUsageStatus(event, envCfg.EnableResponseLogs && envCfg.ShouldLog("debug"))
	}
	needPatch := needInputPatch || needOutputPatch
	if hasUsage {
		if !ctx.HasUsage {
			ctx.HasUsage = true
			ctx.NeedTokenPatch = needPatch || ctx.LowQuality
			if envCfg.EnableResponseLogs && envCfg.ShouldLog("debug") && needPatch && !IsMessageDeltaEvent(event) {
				log.Printf("[Messages-Stream-Token] 检测到虚假值, 延迟到流结束修补")
			}
		}
		// Anthropic 标准流通常只在 message_start.message.usage 中报告输入量。
		// 必须保留该值；后续 message_delta 可能只有 output_tokens。
		// 保留一份未经过客户端补全的上游快照。
		updateCollectedUsage(&ctx.UpstreamUsage, usageData)
		// 累积收集 usage 数据
		updateCollectedUsage(&ctx.CollectedUsage, usageData)
	}

	// 日志缓存（带内存上限保护）
	if ctx.LoggingEnabled {
		if !ctx.LogBufferFull {
			const maxLogBufferSize = 2 * 1024 * 1024 // 2MB 上限
			if ctx.LogBuffer.Len()+len(event) > maxLogBufferSize {
				ctx.LogBufferFull = true
				if envCfg.ShouldLog("debug") {
					log.Printf("[Messages-Stream] 日志缓冲区已达上限 (2MB)，后续流事件不再缓存")
				}
			} else {
				ctx.LogBuffer.WriteString(event)
			}
		}
		if ctx.Synthesizer != nil {
			for _, line := range strings.Split(event, "\n") {
				ctx.Synthesizer.ProcessLine(line)
			}
		}
	}

	// 在 message_stop 前注入 usage（上游完全没有 usage 的情况）
	if !ctx.HasUsage && !ctx.ClientGone && IsMessageStopEvent(event) {
		usageEvent := BuildUsageEvent(requestBody, ctx.OutputTextBuffer.String())
		if envCfg.EnableResponseLogs && envCfg.ShouldLog("debug") {
			log.Printf("[Messages-Stream-Token] 上游无usage, 注入本地估算事件")
		}
		proxycore.MarkRequestLogFirstToken(c)
		w.Write([]byte(usageEvent))
		flusher.Flush()
		ctx.HasUsage = true
	}

	// 修补 token
	eventToSend := event

	// 兜底注入：如果第一个事件不是 message_start，自动注入
	if !ctx.SeenMessageStart && !IsMessageStartEvent(event) {
		if IsContentBlockStartEvent(event) || IsMessageDeltaEvent(event) || IsMessageStopEvent(event) {
			syntheticStart := BuildMessageStartEvent(ctx.RequestModel)
			if envCfg.EnableResponseLogs && envCfg.ShouldLog("debug") {
				log.Printf("[Messages-Stream-Patch] 上游缺少 message_start，自动注入")
			}
			proxycore.MarkRequestLogFirstToken(c)
			w.Write([]byte(syntheticStart))
			flusher.Flush()
			ctx.SeenMessageStart = true
		}
	}
	if IsMessageStartEvent(event) {
		ctx.SeenMessageStart = true
	}

	// 处理 message_start 事件：补全空 id 和检查 model 一致性（可选）
	if IsMessageStartEvent(event) && ctx.RequestModel != "" {
		eventToSend = PatchMessageStartEvent(eventToSend, ctx.RequestModel, envCfg.RewriteResponseModel, envCfg.EnableResponseLogs && envCfg.ShouldLog("debug"))
	}

	eventHasUsage := hasUsage
	if !hasEventData {
		eventHasUsage = HasEventWithUsage(event)
	}
	if ctx.NeedTokenPatch && eventHasUsage {
		if IsMessageDeltaEvent(event) || IsMessageStopEvent(event) {
			inputTokens := ctx.CollectedUsage.InputTokens
			// Never EstimateRequestTokens(full requestBody) into client usage.
			outputTokens := ctx.CollectedUsage.OutputTokens
			estimatedOutputTokens := utils.EstimateTokens(ctx.OutputTextBuffer.String())
			if outputTokens <= 1 && estimatedOutputTokens > outputTokens {
				outputTokens = estimatedOutputTokens
			}

			if outputTokens > ctx.CollectedUsage.OutputTokens {
				ctx.CollectedUsage.OutputTokens = outputTokens
			}

			// 修补事件，包括推断的 cache_read_input_tokens
			if hasEventData {
				PatchTokensInEventDataWithCache(eventData, inputTokens, outputTokens, 0 /* no cache to client */, false, envCfg.EnableResponseLogs && envCfg.ShouldLog("debug"), ctx.LowQuality)
				eventToSend = ReplaceSSEData(eventToSend, eventData)
			} else {
				eventToSend = PatchTokensInEventWithCache(eventToSend, inputTokens, outputTokens, 0 /* no cache to client */, false, envCfg.EnableResponseLogs && envCfg.ShouldLog("debug"), ctx.LowQuality)
			}
			ctx.NeedTokenPatch = false
		}
	}

	// 上游 usage 合理性校验：中转渠道会双向错报（实测 448KB 请求报 26032、压缩后 24KB 请求
	// 报 209736），而 messages+claude 上游是全代理唯一完全不校验、原样透传的路径。客户端
	// （Cursor/Codex/OpenCode）全拿这个数字决定何时压缩上下文，假值直接表现为
	// 「压缩后立刻又要压缩」或「撞满上限也不压缩」。与估算相差 2 倍且量级 ≥20000 时，
	// 用估算总量重建客户端 usage。正常缓存字段完整保留；缓存量本身不可信时，客户端副本
	// 会同时清理缓存字段，避免留下 input=0、cache_read=20万这种仍然虚假的总量。
	// 只改客户端事件，ctx.CollectedUsage 保持上游原值用于指标/计费。
	//
	// 历史约定变更：stream_usage_patch.go 曾约定「绝不用更大的估算覆盖真实正数」，那是防
	// 「全量估算塞给客户端」的无条件覆盖；本校正只在数量级级错报（≥2 倍）时介入，
	// 两种风险同时挡住。实测数据与外部佐证见 utils/usage_sanity.go。
	if eventHasUsage {
		if corrected, ok := sanityCheckMessageStreamInputTokens(eventToSend, requestBody, envCfg.EnableResponseLogs); ok {
			eventToSend = corrected
		}
	}
	eventToSend, err := hooks.RestoreAttachedSSEEvent(c, eventToSend)
	if err != nil {
		return err
	}

	// 转发给客户端
	// 未做 usage / message_start 修补的事件按原文字节转发。未知事件和字段
	// （含 safeguard_results）会留下，这条 Claude 透传路径也不改写 tool_use.id。
	// 需要补 usage 时按 map 重编码，未知字段仍留在 map 里。
	if !ctx.ClientGone {
		proxycore.MarkRequestLogFirstToken(c)
		if _, err := w.Write([]byte(eventToSend)); err != nil {
			ctx.ClientGone = true
			if !IsClientDisconnectError(err) {
				log.Printf("[Messages-Stream] 警告: 写入错误: %v", err)
			} else if envCfg.ShouldLog("info") {
				log.Printf("[Messages-Stream] 客户端中断连接 (正常行为)，继续接收上游数据...")
			}
		} else {
			flusher.Flush()
		}
	}
	return nil
}

type streamToolArgumentFragment struct {
	key      string
	fragment string
	name     string
	snapshot bool
}

func streamSafetyFragments(data map[string]interface{}) (string, []streamToolArgumentFragment) {
	if data == nil {
		return "", nil
	}
	key := "messages.content_block.default"
	if index, exists := data["index"]; exists {
		key = "messages.content_block." + strings.TrimSpace(fmt.Sprint(index))
	}
	var text string
	var toolArguments []streamToolArgumentFragment
	if block, ok := data["content_block"].(map[string]interface{}); ok {
		text, _ = block["text"].(string)
		if block["type"] == "tool_use" {
			name, _ := block["name"].(string)
			fragment := streamToolArgumentFragment{key: key, name: name}
			if input, ok := block["input"].(map[string]interface{}); ok && len(input) > 0 {
				body, _ := json.Marshal(input) // 来自 JSON 解码器，仅包含 JSON 值。
				fragment.fragment = string(body)
				fragment.snapshot = true
			}
			toolArguments = append(toolArguments, fragment)
		}
	}
	if delta, ok := data["delta"].(map[string]interface{}); ok {
		if deltaText, _ := delta["text"].(string); deltaText != "" {
			text += deltaText
		}
		if fragment, _ := delta["partial_json"].(string); fragment != "" {
			toolArguments = append(toolArguments, streamToolArgumentFragment{key: key, fragment: fragment})
		}
	}
	return text, toolArguments
}

// IsClientDisconnectError 判断是否为客户端断开连接错误
func IsClientDisconnectError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "broken pipe") || strings.Contains(msg, "connection reset")
}
