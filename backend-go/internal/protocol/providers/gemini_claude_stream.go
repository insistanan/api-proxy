// Gemini SSE → Claude SSE 流式状态机（Messages 入口 → Gemini native 上游）。
//
// 从 gemini_stream.go 的 HandleStreamResponseCtx 巨型闭包重构为独立 struct。
// 行为契约（沿用既有实现 + 对照 cc-switch 的结构性修复）：
//   - 懒发送 message_start（首个带 model 的 chunk 时发出）。
//   - usage 抓取点必须在 candidates 守卫之前（第三方兼容网关会把终值放在只有
//     usageMetadata 的尾 chunk）；last-wins 覆盖（thinking 模型早期 chunk 只有
//     promptTokenCount/thoughtsTokenCount，first-wins 会把 output 锁在 0）。
//   - 定稿信号只认 candidate.finishReason；message_delta 延迟到流尾，只发一次，
//     保留 finishReason 之后到达的最终 usage。
//   - Gemini thought part 只登记 shadow / reasoning 缓存，绝不转换为客户端
//     可见的 thinking block（d70d8be 决策，与 Chat 上游同口径）。
//   - thoughtSignature shadow 收集逻辑保留（本项目特有：同 session 回放补签名）。
//   - 工具调用去重（兼容网关在多 chunk 重复发送同一完整 functionCall）。
package providers

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/google/uuid"
	"github.com/insistanan/api-proxy/internal/infra/utils"
	"github.com/insistanan/api-proxy/internal/protocol/types"
)

// parseGeminiSSEDataLine 提取一行 SSE 的 data JSON 载荷；非 data 行返回 false。
func parseGeminiSSEDataLine(line string) (string, bool) {
	return utils.SSEDataJSON(strings.TrimSpace(line))
}

// geminiToClaudeStreamState 聚合一次 Gemini 上游流的全部转换状态。
// 单 goroutine 顺序使用，不加锁。
type geminiToClaudeStreamState struct {
	// 块索引分配
	nextBlockIndex int

	// 文本块状态
	textBlockStarted bool
	textBlockIndex   int

	// message_start 状态
	messageStartEmitted bool
	streamModel         string

	// 工具调用 / shadow 收集
	hasToolCall         bool
	shadowParts         []interface{}
	shadowToolCalls     []GeminiShadowToolCall
	shadowTextParts     map[string]int
	seenStreamToolCalls map[string]struct{}
	reasoningText       strings.Builder
	assistantText       strings.Builder
	toolUseParts        []types.ClaudeContent

	// 上游真实 usage（抓取点在 candidates 守卫之前）
	usageInput        int
	usageOutput       int
	usageThoughts     int
	usageCacheRead    int
	usageSeen         bool
	pendingStopReason string
	messageStopped    bool
	streamError       error
}

func newGeminiToClaudeStreamState() *geminiToClaudeStreamState {
	return &geminiToClaudeStreamState{
		shadowTextParts:     make(map[string]int),
		seenStreamToolCalls: make(map[string]struct{}),
		textBlockIndex:      -1,
	}
}

func (s *geminiToClaudeStreamState) closeTextBlock() []string {
	if !s.textBlockStarted {
		return nil
	}
	stopEvent := map[string]interface{}{
		"type":  "content_block_stop",
		"index": s.textBlockIndex,
	}
	stopJSON, _ := json.Marshal(stopEvent)
	s.textBlockStarted = false
	s.textBlockIndex = -1
	return []string{fmt.Sprintf("event: content_block_stop\ndata: %s\n\n", stopJSON)}
}

// captureUsage last-wins 覆盖，不能改成"首个非空生效"：thinking 模型的早期
// chunk 会给出只含 promptTokenCount/thoughtsTokenCount 的 usageMetadata（没有
// candidatesTokenCount），first-wins 会把 output_tokens 永久锁在 0。
func (s *geminiToClaudeStreamState) captureUsage(chunk map[string]interface{}) {
	meta, ok := chunk["usageMetadata"].(map[string]interface{})
	if !ok {
		return
	}
	s.usageSeen = true
	if v, ok := meta["cachedContentTokenCount"].(float64); ok {
		s.usageCacheRead = int(v)
	}
	if v, ok := meta["promptTokenCount"].(float64); ok {
		// promptTokenCount 含 cachedContentTokenCount，扣除后才是新增输入。
		// 与非流式 ConvertResponse、handlers/gemini 两条路径、converters
		// responses_stream 同一口径，含 <0 钳制防上游异常回包污染指标。
		s.usageInput = int(v) - s.usageCacheRead
		if s.usageInput < 0 {
			s.usageInput = 0
		}
	}
	if v, ok := meta["thoughtsTokenCount"].(float64); ok {
		s.usageThoughts = int(v)
	}
	// candidatesTokenCount 不含 thoughtsTokenCount，而 Anthropic 的 output_tokens
	// 含 thinking，必须相加；不加就把 thinking 模型的输出系统性低报（reasoning 重的
	// 场景缺口可达 86%）。三家 output 侧语义与官方证据见 types.ClaudeOutputTokensDetails。
	//
	// thoughts 必须先于 candidates 赋值：同一个 meta 内相加要用本轮的 thoughts。
	// 只报 thoughts 不报 candidates 的早期 chunk，output 就等于 thoughts。
	if v, ok := meta["candidatesTokenCount"].(float64); ok {
		s.usageOutput = int(v) + s.usageThoughts
	} else if s.usageThoughts > 0 {
		s.usageOutput = s.usageThoughts
	}
}

// buildMessageDeltaUsage 构造 message_delta 的 usage 负载。
// 上游没给 usageMetadata 时保持 0：不猜测、不用请求体估算（客户端每轮全量重发，
// 估算值会离真实输入差一个数量级），由流结束后的日志留痕。
func (s *geminiToClaudeStreamState) buildMessageDeltaUsage() map[string]interface{} {
	if !s.usageSeen {
		return map[string]interface{}{"output_tokens": 0}
	}
	usage := map[string]interface{}{
		"input_tokens":  s.usageInput,
		"output_tokens": s.usageOutput,
	}
	if s.usageCacheRead > 0 {
		usage["cache_read_input_tokens"] = s.usageCacheRead
	}
	// 已计入 output_tokens，这里只作拆分展示，客户端不得再求和。
	if s.usageThoughts > 0 {
		usage["output_tokens_details"] = map[string]interface{}{
			"thinking_tokens": s.usageThoughts,
		}
	}
	return usage
}

// emitMessageDelta 定稿事件（stop_reason + usage），只发一次。
func (s *geminiToClaudeStreamState) emitMessageDelta(stopReason string) string {
	deltaEvent := map[string]interface{}{
		"type": "message_delta",
		"delta": map[string]interface{}{
			"stop_reason":   stopReason,
			"stop_sequence": nil,
		},
		"usage": s.buildMessageDeltaUsage(),
	}
	deltaJSON, _ := json.Marshal(deltaEvent)
	return fmt.Sprintf("event: message_delta\ndata: %s\n\n", deltaJSON)
}

// geminiStopReasonFromFinish 只认 finishReason；length 语义按子串匹配。
func geminiStopReasonFromFinish(finishReason string, hasToolCall bool) string {
	stopReason := "end_turn"
	if hasToolCall {
		stopReason = "tool_use"
	} else if strings.Contains(strings.ToLower(finishReason), "length") {
		stopReason = "max_tokens"
	}
	return stopReason
}

// ProcessChunk 处理一个上游 SSE data JSON，返回要发给客户端的 Claude SSE 事件。
func (s *geminiToClaudeStreamState) ProcessChunk(jsonStr string) []string {
	if s.streamError != nil || s.messageStopped {
		return nil
	}
	var chunk map[string]interface{}
	if err := json.Unmarshal([]byte(jsonStr), &chunk); err != nil {
		s.streamError = fmt.Errorf("解析 Gemini SSE 数据失败: %w", err)
		return nil
	}
	if chunk == nil {
		s.streamError = fmt.Errorf("上游 Gemini SSE 数据不是 JSON 对象")
		return nil
	}
	if upstreamError, exists := chunk["error"]; exists && upstreamError != nil {
		s.streamError = NewUpstreamStreamFailedError(utils.IsUpstreamModelCapacityErrorBody([]byte(jsonStr)), jsonStr)
		return nil
	}
	// 定稿后仍可接收纯 usage 尾包，不再接收正文或工具调用。
	if s.pendingStopReason != "" {
		s.captureUsage(chunk)
		return nil
	}

	var out []string

	// 首次收到有效 chunk 时，提取 model 并发送 message_start
	if !s.messageStartEmitted {
		if m, ok := chunk["model"].(string); ok {
			s.streamModel = m
		}
		msgStart := map[string]interface{}{
			"type": "message_start",
			"message": map[string]interface{}{
				"id":            fmt.Sprintf("msg_%s", uuid.New().String()),
				"type":          "message",
				"role":          "assistant",
				"content":       []interface{}{},
				"model":         s.streamModel,
				"stop_reason":   nil,
				"stop_sequence": nil,
				"usage":         map[string]interface{}{"input_tokens": 0, "output_tokens": 1},
			},
		}
		startJSON, _ := json.Marshal(msgStart)
		s.messageStartEmitted = true
		out = append(out, fmt.Sprintf("event: message_start\ndata: %s\n\n", startJSON))
	}

	// usage 抓取必须在 candidates 守卫之前（见 struct 注释）。
	s.captureUsage(chunk)

	candidates, ok := chunk["candidates"].([]interface{})
	if !ok || len(candidates) == 0 {
		return out
	}

	candidate, ok := candidates[0].(map[string]interface{})
	if !ok {
		return out
	}

	content, _ := candidate["content"].(map[string]interface{})
	parts, _ := content["parts"].([]interface{})

	for _, p := range parts {
		part, ok := p.(map[string]interface{})
		if !ok {
			continue
		}

		// 处理 thinking/thought 内容（Gemini 2.5 thinking 模型）。
		// 思考只登记到代理内部缓存，绝不转换为客户端可重放的
		// Claude thinking block。
		if isGeminiThoughtPart(part) {
			thought, _ := part["text"].(string)
			if thought != "" {
				s.reasoningText.WriteString(thought)
			}
			continue
		}

		// 处理文本
		if text, ok := part["text"].(string); ok {
			if !s.textBlockStarted {
				s.textBlockIndex = s.nextBlockIndex
				s.nextBlockIndex++
				startEvent := map[string]interface{}{
					"type":  "content_block_start",
					"index": s.textBlockIndex,
					"content_block": map[string]string{
						"type": "text",
						"text": "",
					},
				}
				startJSON, _ := json.Marshal(startEvent)
				out = append(out, fmt.Sprintf("event: content_block_start\ndata: %s\n\n", startJSON))
				s.textBlockStarted = true
			}
			if text != "" {
				s.assistantText.WriteString(text)
				key := fmt.Sprintf("text:%d", s.textBlockIndex)
				if idx, exists := s.shadowTextParts[key]; exists {
					if existing, ok := s.shadowParts[idx].(map[string]interface{}); ok {
						existingText, _ := existing["text"].(string)
						existing["text"] = existingText + text
					}
				} else {
					s.shadowTextParts[key] = len(s.shadowParts)
					s.shadowParts = append(s.shadowParts, map[string]interface{}{"text": text})
				}
			}

			deltaEvent := map[string]interface{}{
				"type":  "content_block_delta",
				"index": s.textBlockIndex,
				"delta": map[string]string{
					"type": "text_delta",
					"text": text,
				},
			}
			deltaJSON, _ := json.Marshal(deltaEvent)
			out = append(out, fmt.Sprintf("event: content_block_delta\ndata: %s\n\n", deltaJSON))
		}

		// 处理函数调用
		if fc, ok := part["functionCall"].(map[string]interface{}); ok {
			name, _ := fc["name"].(string)
			args := fc["args"]
			streamToolCallKey := geminiFunctionCallKey(fc, name, args)
			if _, exists := s.seenStreamToolCalls[streamToolCallKey]; exists {
				// Gemini 兼容网关可能在多个 chunk 重复发送同一个完整
				// functionCall。它不是参数 delta，不能重复转成多个
				// Claude tool_use 或 shadow tool call。
				continue
			}
			s.seenStreamToolCalls[streamToolCallKey] = struct{}{}
			out = append(out, s.closeTextBlock()...)

			toolUseBlockIndex := s.nextBlockIndex
			s.nextBlockIndex++
			id, _ := fc["id"].(string)
			if id == "" {
				id = synthesizeGeminiToolCallID()
				fc["id"] = id
			}
			s.hasToolCall = true
			shadowPart := cloneMapStringInterface(part)
			if shadowFC, ok := shadowPart["functionCall"].(map[string]interface{}); ok {
				shadowFC["id"] = id
			}
			s.shadowParts = append(s.shadowParts, shadowPart)
			s.shadowToolCalls = append(s.shadowToolCalls, GeminiShadowToolCall{
				ID:               id,
				Name:             name,
				Args:             args,
				ThoughtSignature: extractGeminiThoughtSignature(part),
			})
			s.toolUseParts = append(s.toolUseParts, types.ClaudeContent{
				Type:  "tool_use",
				ID:    id,
				Name:  name,
				Input: args,
			})

			out = append(out, processToolUsePart(id, name, args, toolUseBlockIndex)...)
		}
	}

	// 处理结束原因
	if finishReason, ok := candidate["finishReason"].(string); ok && finishReason != "" {
		out = append(out, s.closeTextBlock()...)
		s.pendingStopReason = geminiStopReasonFromFinish(finishReason, s.hasToolCall)
	}

	return out
}

// Finish 流结束时的统一收尾：关块、shadow 登记、reasoning 缓存、usage 留痕。
func (s *geminiToClaudeStreamState) Finish(shadowProviderID, shadowSessionID string) []string {
	if s.streamError != nil || s.pendingStopReason == "" || s.messageStopped {
		return nil
	}
	s.messageStopped = true
	// 确保流结束时关闭任何未关闭的文本块
	out := s.closeTextBlock()
	out = append(out, s.emitMessageDelta(s.pendingStopReason))

	// usage 失配留痕：两种情况都会让客户端与指标看到 0，必须能从日志查出来，
	// 不允许静默当成"这一轮真的是 0 token"。
	switch {
	case !s.usageSeen:
		log.Printf("[Gemini-Stream-Token] 警告: 上游整条流未提供 usageMetadata, usage 记为 0")
	}

	if len(s.shadowParts) > 0 {
		defaultGeminiShadowStore.Record(shadowProviderID, shadowSessionID, GeminiShadowTurn{
			AssistantContent: map[string]interface{}{"parts": cloneInterface(s.shadowParts)},
			ToolCalls:        s.shadowToolCalls,
		})
	}

	if s.reasoningText.Len() > 0 {
		// 流式响应只有正常读完后才登记 reasoning，避免把断流产生的
		// 不完整思考绑定到下一轮 assistant 消息。
		cacheContent := make([]types.ClaudeContent, 0, len(s.toolUseParts)+2)
		cacheContent = append(cacheContent, types.ClaudeContent{
			Type:     "thinking",
			Thinking: s.reasoningText.String(),
		})
		if s.assistantText.Len() > 0 {
			cacheContent = append(cacheContent, types.ClaudeContent{
				Type: "text",
				Text: s.assistantText.String(),
			})
		}
		cacheContent = append(cacheContent, s.toolUseParts...)
		CacheClaudeResponseReasoning(&types.ClaudeResponse{
			Role:    "assistant",
			Content: cacheContent,
		})
	}

	out = append(out, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
	return out
}
