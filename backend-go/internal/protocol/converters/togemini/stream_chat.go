package togemini

import (
	"fmt"

	"github.com/tidwall/gjson"
	"insistanan/api-proxy/internal/protocol/types"
)

func (s *Stream) feedChat(root gjson.Result, payload []byte) ([]types.GeminiStreamChunk, error) {
	var chunks []types.GeminiStreamChunk
	metadata, err := s.updateUsage(root.Get("usage"))
	if err != nil {
		return nil, err
	}
	if metadata != nil {
		chunks = append(chunks, types.GeminiStreamChunk{UsageMetadata: metadata})
	}
	// 完成后的包只更新用量，不再交付重复内容或工具。
	if s.stopReason != "" {
		return chunks, nil
	}
	parts, err := s.tools.Feed("openai", payload)
	if err != nil {
		return nil, err
	}
	for _, choice := range root.Get("choices").Array() {
		if !choice.IsObject() {
			return nil, fmt.Errorf("Chat 流式 choice 必须是对象")
		}
		if choice.Get("delta").IsObject() {
			s.messageSeen = true
		}
		if content := choice.Get("delta.content").String(); content != "" {
			parts = append(parts, types.GeminiPart{Text: content})
		}
		if reason := choice.Get("finish_reason"); reason.Exists() && reason.Type != gjson.Null && reason.String() != "" {
			s.stopReason, err = FinishReason("openai", reason.String())
			if err != nil {
				return nil, err
			}
		}
	}
	if len(parts) > 0 {
		chunks = append(chunks, contentChunk(parts))
	}
	return chunks, nil
}
