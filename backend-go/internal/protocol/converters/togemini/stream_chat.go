package togemini

import (
	"fmt"

	"github.com/tidwall/gjson"
	"insistanan/api-proxy/internal/protocol/contract"
	"insistanan/api-proxy/internal/protocol/types"
)

func (s *Stream) feedChat(root gjson.Result, payload []byte) ([]types.GeminiStreamChunk, error) {
	var chunks []types.GeminiStreamChunk
	choices := root.Get("choices").Array()
	if len(choices) > 1 {
		return nil, fmt.Errorf("Chat 转 Gemini 不支持多个 choice")
	}
	if len(choices) == 1 && choices[0].Get("index").Exists() && choices[0].Get("index").Raw != "0" {
		return nil, fmt.Errorf("Chat 转 Gemini 只支持 index 为 0 的 choice")
	}
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
	for _, choice := range choices {
		if !choice.IsObject() {
			return nil, fmt.Errorf("Chat 流式 choice 必须是对象")
		}
		if choice.Get("delta").IsObject() {
			s.messageSeen = true
		}
		thought, err := chatReasoning(choice.Get("delta"))
		if err != nil {
			return nil, err
		}
		if thought != "" {
			parts = append(parts, types.GeminiPart{Text: thought, Thought: true})
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

func chatReasoning(message gjson.Result) (string, error) {
	var thought string
	seen := false
	for _, field := range []string{"reasoning_content", "reasoning", "thinking"} {
		value := message.Get(field)
		if !value.Exists() || value.Type == gjson.Null {
			continue
		}
		if value.Type != gjson.String || seen {
			return "", contract.Unsupported("Chat 推理字段无法转换为 Gemini thought")
		}
		thought, seen = value.String(), true
	}
	return thought, nil
}
