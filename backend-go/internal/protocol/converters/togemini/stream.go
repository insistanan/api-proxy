package togemini

import (
	"encoding/json"
	"fmt"

	"github.com/tidwall/gjson"
	"insistanan/api-proxy/internal/protocol/codec"
	"insistanan/api-proxy/internal/protocol/contract"
	"insistanan/api-proxy/internal/protocol/tokenusage"
	"insistanan/api-proxy/internal/protocol/types"
)

// Stream 只处理协议事件，不持有 HTTP、取消或安全管线状态。
// Feed 返回内容和用量，Final 在调用方完成安全交付后提供唯一终态。
type Stream struct {
	protocol    string
	tools       StreamTools
	usageFields map[string]json.RawMessage
	usage       *types.Usage
	stopReason  string
	messageSeen bool
	terminated  bool
}

func NewStream(protocol string) (*Stream, error) {
	if protocol != "claude" && protocol != "openai" {
		return nil, fmt.Errorf("Gemini 流式转换不支持上游 %q", protocol)
	}
	return &Stream{protocol: protocol}, nil
}

func (s *Stream) Feed(payload []byte) ([]types.GeminiStreamChunk, error) {
	if s.terminated {
		return nil, fmt.Errorf("上游流已结束，不能继续转换事件")
	}
	if string(payload) == codec.SSEDoneMarker {
		if s.protocol != "openai" {
			return nil, fmt.Errorf("Claude 流必须以 message_stop 结束")
		}
		s.terminated = true
		return nil, nil
	}
	object, err := contract.Object(payload, "上游 SSE 数据")
	if err != nil {
		return nil, err
	}
	if object["error"] != nil || object["type"] == "error" {
		return nil, fmt.Errorf("上游流返回错误: %s", payload)
	}
	root := gjson.ParseBytes(payload)
	if s.protocol == "claude" {
		return s.feedClaude(root, payload)
	}
	return s.feedChat(root, payload)
}

func (s *Stream) Completed() bool {
	if s.protocol == "claude" {
		return s.terminated
	}
	return s.stopReason != "" || (s.terminated && s.messageSeen)
}

// Terminated 标记无需再读的事件；Chat finish_reason 之后仍要读取 usage 尾包。
func (s *Stream) Terminated() bool    { return s.terminated }
func (s *Stream) Usage() *types.Usage { return s.usage }

func (s *Stream) Final() (types.GeminiStreamChunk, error) {
	if !s.Completed() {
		return types.GeminiStreamChunk{}, fmt.Errorf("上游流缺少完成事件")
	}
	if err := s.tools.CheckComplete(); err != nil {
		return types.GeminiStreamChunk{}, err
	}
	reason := s.stopReason
	if reason == "" {
		// message_stop / 有效消息后的 [DONE] 已确认完成。
		reason = "STOP"
	}
	return types.GeminiStreamChunk{Candidates: []types.GeminiCandidate{{FinishReason: reason}}}, nil
}

func contentChunk(parts []types.GeminiPart) types.GeminiStreamChunk {
	return types.GeminiStreamChunk{Candidates: []types.GeminiCandidate{{Content: &types.GeminiContent{Role: "model", Parts: parts}}}}
}

func (s *Stream) updateUsage(raw gjson.Result) (*types.GeminiUsageMetadata, error) {
	if !raw.Exists() || raw.Type == gjson.Null {
		return nil, nil
	}
	if !raw.IsObject() {
		return nil, fmt.Errorf("上游 usage 必须是对象")
	}
	if s.usageFields == nil {
		s.usageFields = make(map[string]json.RawMessage)
	}
	// Claude message_delta 只携带变更的用量字段，保留 message_start 的输入和缓存量。
	for key, value := range raw.Map() {
		s.usageFields[key] = json.RawMessage(value.Raw)
	}
	encoded, err := json.Marshal(s.usageFields)
	if err != nil {
		return nil, err
	}
	s.usage, err = tokenusage.ParseJSONUsage(encoded)
	if err != nil {
		return nil, err
	}
	return geminiUsage(s.usage), nil
}
