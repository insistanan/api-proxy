package modelfingerprint

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"insistanan/api-proxy/internal/protocol/codec"
)

// readStream 只提取原生文本；缺少协议结束事件时不能把已收到的数字当成完整样本。
func readStream(ctx context.Context, body io.Reader, protocol string) (text, model string, truncated bool, err error) {
	limited := &io.LimitedReader{R: body, N: maxSampleBytes + 1}
	scanner := bufio.NewScanner(limited)
	scanner.Buffer(make([]byte, 4096), maxSampleBytes)
	var data []string
	var output strings.Builder
	stopReason := ""
	completed := false
	consume := func(payload string) error {
		if payload == "[DONE]" {
			if protocol != "openai" || (stopReason != "stop" && stopReason != "length") {
				return fmt.Errorf("上游流缺少有效的结束原因")
			}
			completed = true
			return nil
		}
		var event struct {
			Type         string          `json:"type"`
			Model        string          `json:"model"`
			ModelVersion string          `json:"modelVersion"`
			Error        json.RawMessage `json:"error"`
			Delta        json.RawMessage `json:"delta"`
			Response     json.RawMessage `json:"response"`
			Message      struct {
				Model string `json:"model"`
			} `json:"message"`
			ContentBlock textPart `json:"content_block"`
			Choices      []struct {
				Index int `json:"index"`
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
				FinishReason string `json:"finish_reason"`
			} `json:"choices"`
			Candidates []struct {
				Index   int `json:"index"`
				Content struct {
					Parts []textPart `json:"parts"`
				} `json:"content"`
				FinishReason string `json:"finishReason"`
			} `json:"candidates"`
		}
		if err := json.Unmarshal([]byte(payload), &event); err != nil {
			return fmt.Errorf("上游流事件不是有效 JSON：%w", err)
		}
		if len(event.Error) > 0 && string(event.Error) != "null" {
			return fmt.Errorf("上游流报告错误：%s", event.Error)
		}
		switch protocol {
		case "responses":
			switch event.Type {
			case "response.completed", "response.incomplete":
				finalText, finalModel, cut, parseErr := parseReply(event.Response, protocol)
				if parseErr != nil {
					return parseErr
				}
				output.WriteString(finalText)
				model, truncated, completed = finalModel, cut, true
			case "response.failed", "error":
				return fmt.Errorf("上游 Responses 流失败：%s", payload)
			}
		case "claude":
			if event.Message.Model != "" {
				model = event.Message.Model
			}
			switch event.Type {
			case "content_block_start":
				if event.ContentBlock.Type == "text" {
					output.WriteString(event.ContentBlock.Text)
				}
			case "content_block_delta", "message_delta":
				var delta struct {
					Type       string `json:"type"`
					Text       string `json:"text"`
					StopReason string `json:"stop_reason"`
				}
				if err := json.Unmarshal(event.Delta, &delta); err != nil {
					return fmt.Errorf("无法解析 Messages 流增量：%w", err)
				}
				if delta.Type == "text_delta" {
					output.WriteString(delta.Text)
				}
				if delta.StopReason != "" {
					stopReason = delta.StopReason
					truncated = stopReason == "max_tokens"
				}
			case "message_stop":
				if stopReason != "end_turn" && !truncated {
					return fmt.Errorf("Messages 未正常结束：%s", stopReason)
				}
				completed = true
			case "error":
				return fmt.Errorf("上游 Messages 流失败：%s", payload)
			}
		case "openai":
			if event.Model != "" {
				model = event.Model
			}
			for _, choice := range event.Choices {
				if choice.Index != 0 {
					continue
				}
				output.WriteString(choice.Delta.Content)
				if choice.FinishReason != "" {
					stopReason = choice.FinishReason
					truncated = stopReason == "length"
				}
			}
		case "gemini":
			if event.ModelVersion != "" {
				model = event.ModelVersion
			}
			for _, candidate := range event.Candidates {
				if candidate.Index != 0 {
					continue
				}
				for _, part := range candidate.Content.Parts {
					if !part.Thought {
						output.WriteString(part.Text)
					}
				}
				if candidate.FinishReason != "" {
					truncated = candidate.FinishReason == "MAX_TOKENS"
					if candidate.FinishReason != "STOP" && !truncated {
						return fmt.Errorf("Gemini 未正常结束：%s", candidate.FinishReason)
					}
					completed = true
				}
			}
		default:
			return fmt.Errorf("不支持的流协议：%s", protocol)
		}
		return nil
	}
	flush := func() error {
		if len(data) == 0 {
			return nil
		}
		payload := strings.Join(data, "\n")
		data = nil
		return consume(payload)
	}
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if err = flush(); err != nil || completed {
				break
			}
		} else if payload, ok := codec.ParseSSEDataLine(line); ok {
			data = append(data, payload)
		}
	}
	if err == nil && !completed {
		err = flush()
	}
	text = output.String()
	if err != nil {
		return text, model, truncated, err
	}
	if limited.N <= 0 {
		return text, model, truncated, fmt.Errorf("上游流超过 2 MiB 限制")
	}
	if err = codec.CheckStreamCompletion(ctx, completed, scanner.Err()); err != nil {
		return text, model, truncated, err
	}
	if strings.TrimSpace(text) == "" {
		return text, model, truncated, fmt.Errorf("上游流没有可用于检测的文本回答")
	}
	return text, model, truncated, nil
}
