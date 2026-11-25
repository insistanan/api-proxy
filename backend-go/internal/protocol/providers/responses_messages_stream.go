// Responses → Messages 流式状态与事件分派，保留失败与容量错误契约。
package providers

import (
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/tidwall/gjson"
	"insistanan/api-proxy/internal/infra/upstreamreq"
	"insistanan/api-proxy/internal/protocol/codec"
	"insistanan/api-proxy/internal/protocol/contract"
	"insistanan/api-proxy/internal/protocol/types"
)

type responsesToClaudeStreamState struct {
	responseID          string
	upstreamResponseID  string
	messageStarted      bool
	nextBlockIndex      int
	textBlockIndex      int
	textBlockOpen       bool
	stopReason          string
	model               string
	toolCalls           map[string]*responsesStreamToolCall
	toolCallOrder       []string
	emittedToolCalls    map[string]bool
	reasoning           strings.Builder
	reasoningDeltaSeen  bool
	reasoningBlockIndex int
	reasoningBlockOpen  bool
	reasoningEmittedLen int
	assistantText       strings.Builder
	deliveredParts      map[responsesPartKey]string
	itemIndexes         map[string]int
	indexItems          map[int]string
	completedOutput     []types.ResponsesItem
	completed           bool
	messageStopped      bool
	// Upstream Responses usage fields captured from response.completed / usage events.
	// Without these, request logs always show cacheReadTokens=0 for Claude entry.
	inputTokens              int
	outputTokens             int
	cacheReadInputTokens     int
	cacheCreationInputTokens int
	hasUsage                 bool

	// 上游失败状态（response.failed / 顶层 error 事件）：
	// failed 阻止 finish() 伪造成功终止；capacityError 标记 model at capacity
	// 类容量错误（上层同渠道重试）；failedMessage 保存错误信封供上报。
	failed        bool
	capacityError bool
	failedMessage string
}

type responsesStreamToolCall struct {
	CallID    string
	Name      string
	Arguments string
}

func newResponsesToClaudeStreamState() *responsesToClaudeStreamState {
	return &responsesToClaudeStreamState{
		responseID:       fmt.Sprintf("msg_%s", uuid.New().String()),
		toolCalls:        make(map[string]*responsesStreamToolCall),
		emittedToolCalls: make(map[string]bool),
		deliveredParts:   make(map[responsesPartKey]string),
		itemIndexes:      make(map[string]int),
		indexItems:       make(map[int]string),
	}
}

func (s *responsesToClaudeStreamState) processLine(line string) []string {
	if s.completed || s.failed {
		return nil
	}
	if strings.HasPrefix(line, "event:") {
		return nil
	}
	data, isData := codec.SSEDataJSON(line)
	if !isData {
		return nil
	}
	root := gjson.Parse(data)
	if !gjson.Valid(data) || !root.IsObject() {
		s.failed = true
		s.failedMessage = "上游 Responses SSE 数据不是有效 JSON 对象"
		return nil
	}
	if err := contract.ResponseContent("responses", []byte(data)); err != nil {
		s.failed, s.failedMessage = true, err.Error()
		return nil
	}

	eventType := root.Get("type").String()

	// response.failed 的错误信封在 response.error 下（与顶层 error 事件不同形态）。
	// 不处理会让流尾 finish() 伪造 message_stop，客户端把失败当成功终止。
	// model at capacity 类容量错误转 RetrySameCandidateError 由上层同渠道重试。
	if eventType == "response.failed" || eventType == "error" ||
		(root.Get("error").Exists() && root.Get("error").Type != gjson.Null) || root.Get("response.status").String() == "failed" {
		s.failed = true
		failedPayload := root.Get("response.error").Raw
		if failedPayload == "" {
			failedPayload = root.Get("error").Raw
		}
		if failedPayload == "" {
			failedPayload = data
		}
		if proxycoreIsModelCapacity(failedPayload) {
			s.capacityError = true
		} else {
			s.failedMessage = failedPayload
		}
		return nil
	}

	s.captureUpstreamResponseID(root)
	s.captureResponsesUsage(root)
	s.rememberItemIndex(root)
	if s.failed {
		return nil
	}
	out := s.ensureMessageStart(root)
	switch {
	case eventType == "response.refusal.delta":
		s.stopReason = "refusal"
		out = append(out, s.deliverPart(s.eventPartKey(root, "text"), root.Get("delta").String(), false, false)...)
	case strings.Contains(eventType, "output_text.delta"):
		out = append(out, s.deliverPart(s.eventPartKey(root, "text"), root.Get("delta").String(), false, false)...)
	case strings.Contains(eventType, "reasoning") && strings.Contains(eventType, "delta"):
		if delta := root.Get("delta").String(); delta != "" {
			s.reasoningDeltaSeen = true
			out = append(out, s.deliverPart(s.eventPartKey(root, "reasoning"), delta, true, false)...)
		}
	case strings.Contains(eventType, "reasoning") && strings.Contains(eventType, "done"):
		if value := root.Get("text"); value.Exists() {
			out = append(out, s.deliverPart(s.eventPartKey(root, "reasoning"), value.String(), true, true)...)
		}
		out = append(out, s.closeReasoningBlock()...)
	case strings.Contains(eventType, "function_call_arguments.delta") || strings.Contains(eventType, "custom_tool_call_input.delta"):
		s.captureToolArgumentDelta(root)
	case strings.Contains(eventType, "response.output_item.added"):
		s.captureToolCall(root)
	case strings.Contains(eventType, "response.output_item.done"):
		s.captureToolCall(root)
		itemType := root.Get("item.type").String()
		if itemType == "function_call" || itemType == "custom_tool_call" {
			out = append(out, s.emitToolUse(root)...)
		}
	case strings.Contains(eventType, "function_call_arguments.done"):
		s.captureToolCall(root)
	case eventType == "response.completed" || eventType == "response.incomplete":
		completion, err := contract.ResponsesCompletion(strings.TrimPrefix(eventType, "response."), root.Get("response.incomplete_details.reason").String())
		if err != nil {
			s.failed, s.failedMessage = true, err.Error()
			return nil
		}
		s.completed = true
		out = append(out, s.emitCompletedOutput(root)...)
		if s.failed {
			return nil
		}
		if s.stopReason != "refusal" {
			s.stopReason = completion.ClaudeStopReason(len(s.emittedToolCalls) > 0)
		}
		// Final usage is usually on response.completed; re-capture then emit message_delta with real tokens.
		s.captureResponsesUsage(root)
		out = append(out, s.closeReasoningBlock()...)
		out = append(out, s.closeTextBlock()...)
		out = append(out, s.emitMessageDelta()...)
	default:
		if text := root.Get("delta").String(); text != "" && strings.Contains(eventType, "text") {
			out = append(out, s.emitTextDelta(text)...)
		}
	}
	return out
}

func (s *responsesToClaudeStreamState) finish() []string {
	if s.failed || !s.completed || s.messageStopped {
		// 错误流：不伪造成功终止（message_delta/message_stop 由错误路径统一处理）。
		return nil
	}
	s.messageStopped = true
	out := []string{}
	out = append(out, s.closeReasoningBlock()...)
	out = append(out, s.closeTextBlock()...)
	if s.messageStarted {
		if s.stopReason == "" {
			s.stopReason = "end_turn"
			out = append(out, s.emitMessageDelta()...)
		}
		out = append(out, buildClaudeSSE("message_stop", map[string]interface{}{"type": "message_stop"}))
	}
	return out
}

// proxycoreIsModelCapacity 委托 upstreamreq.IsUpstreamModelCapacityMessage：
// 容量错误判据的唯一出处（R4-a 收敛），消除 providers 与 proxycore 的
// 重复实现（providers 不能 import proxycore：proxycore→visionlayer→providers
// 会成环；utils 是双方共同依赖）。
func proxycoreIsModelCapacity(payload string) bool {
	return upstreamreq.IsUpstreamModelCapacityMessage(payload)
}

// UpstreamStreamFailedError 表示上游流内失败，包括错误事件和正常结束却仅有思考的无效回复。
// Capacity=true 时是 model at capacity
// 类容量错误，调用方（proxycore）可按同候选重试处理；否则是真实上游失败。
type UpstreamStreamFailedError struct {
	Capacity bool
	Payload  string
}

func (e *UpstreamStreamFailedError) Error() string {
	if e.Capacity {
		return "upstream stream failed: model at capacity"
	}
	if e.Payload != "" {
		return "upstream stream failed: " + e.Payload
	}
	return "upstream stream failed"
}

// NewUpstreamStreamFailedError 构造流内失败错误。
func NewUpstreamStreamFailedError(capacity bool, payload string) error {
	return &UpstreamStreamFailedError{Capacity: capacity, Payload: payload}
}

// IsUpstreamStreamFailed 判断 err 是否为流内失败错误（供上层分类）。
func IsUpstreamStreamFailed(err error) (*UpstreamStreamFailedError, bool) {
	var target *UpstreamStreamFailedError
	if errors.As(err, &target) {
		return target, true
	}
	return nil, false
}
