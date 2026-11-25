package togemini

import (
	"github.com/tidwall/gjson"
	"insistanan/api-proxy/internal/protocol/types"
)

func (s *Stream) feedClaude(root gjson.Result, payload []byte) ([]types.GeminiStreamChunk, error) {
	parts, err := s.tools.Feed("claude", payload)
	if err != nil {
		return nil, err
	}
	var chunks []types.GeminiStreamChunk
	usage := root.Get("usage")
	switch root.Get("type").String() {
	case "message_start":
		usage = root.Get("message.usage")
	case "content_block_start":
		block := root.Get("content_block")
		switch block.Get("type").String() {
		case "text":
			parts = append(parts, types.GeminiPart{Text: block.Get("text").String()})
		case "thinking":
			parts = append(parts, types.GeminiPart{Text: block.Get("thinking").String(), Thought: true})
		}
	case "content_block_delta":
		delta := root.Get("delta")
		switch delta.Get("type").String() {
		case "text_delta":
			parts = append(parts, types.GeminiPart{Text: delta.Get("text").String()})
		case "thinking_delta":
			parts = append(parts, types.GeminiPart{Text: delta.Get("thinking").String(), Thought: true})
		}
	case "message_delta":
		if reason := root.Get("delta.stop_reason"); reason.Exists() && reason.Type != gjson.Null {
			s.stopReason, err = FinishReason("claude", reason.String())
			if err != nil {
				return nil, err
			}
		}
	case "message_stop":
		s.terminated = true
	}
	if len(parts) > 0 {
		chunks = append(chunks, contentChunk(parts))
	}
	metadata, err := s.updateUsage(usage)
	if err != nil {
		return nil, err
	}
	if metadata != nil {
		chunks = append(chunks, types.GeminiStreamChunk{UsageMetadata: metadata})
	}
	return chunks, nil
}
