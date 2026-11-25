package cachekeepalive

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
	"insistanan/api-proxy/internal/protocol/codec"
	"insistanan/api-proxy/internal/protocol/tokenusage"
	"insistanan/api-proxy/internal/protocol/types"
)

func tokenField(root gjson.Result, path string, required bool) (int, error) {
	v := root.Get(path)
	if !v.Exists() && !required {
		return 0, nil
	}
	if v.Type != gjson.Number || v.Num < 0 || v.Num > 1e9 || math.Trunc(v.Num) != v.Num {
		return 0, fmt.Errorf("上游缺少有效 Token 字段 %s", path)
	}
	return int(v.Num), nil
}

func parseProtocolResponse(protocol string, body []byte, status int) (*types.Usage, error) {
	if protocol == "claude" {
		return parseResponse(body, status)
	}
	if !gjson.ValidBytes(body) {
		return nil, errors.New("保活上游响应不是完整 JSON")
	}
	root := gjson.ParseBytes(body)
	var responseErr error
	if status < 200 || status >= 300 || root.Get("error").Exists() && root.Get("error").Type != gjson.Null {
		responseErr = fmt.Errorf("保活上游拒绝请求（HTTP %d），不重试或切换目标", status)
	}
	if protocol == "gemini" {
		u := root.Get("usageMetadata")
		input, e1 := tokenField(u, "promptTokenCount", true)
		cached, e2 := tokenField(u, "cachedContentTokenCount", true)
		output, e3 := tokenField(u, "candidatesTokenCount", false)
		thinking, e4 := tokenField(u, "thoughtsTokenCount", false)
		if err := errors.Join(e1, e2, e3, e4); err != nil {
			return nil, errors.Join(responseErr, err)
		}
		if !root.Get("candidates").IsArray() || root.Get("candidates.#").Int() == 0 {
			responseErr = errors.Join(responseErr, errors.New("Gemini 未返回 candidates"))
		}
		for _, candidate := range root.Get("candidates").Array() {
			if reason := candidate.Get("finishReason").String(); reason != "STOP" && reason != "MAX_TOKENS" {
				responseErr = errors.Join(responseErr, errors.New("Gemini 未正常完成保活生成"))
			}
		}
		usage := &types.Usage{InputTokens: input - cached, CacheReadInputTokens: cached, OutputTokens: output + thinking}
		_, _, _, _, err := usageNumbers(usage)
		return usage, errors.Join(responseErr, err)
	}
	u := root.Get("usage")
	input, output, cached := "input_tokens", "output_tokens", "input_tokens_details.cached_tokens"
	if protocol == "openai" {
		input, output, cached = "prompt_tokens", "completion_tokens", "prompt_tokens_details.cached_tokens"
		if !root.Get("choices").IsArray() {
			responseErr = errors.Join(responseErr, errors.New("Chat 未返回 choices"))
		}
		for _, choice := range root.Get("choices").Array() {
			if choice.Get("finish_reason").String() == "content_filter" {
				responseErr = errors.Join(responseErr, errors.New("Chat 保活生成被内容过滤"))
			}
		}
	} else if protocol == "responses" {
		if state := root.Get("status").String(); state != "completed" && state != "incomplete" {
			responseErr = errors.Join(responseErr, errors.New("Responses 未返回成功终态响应"))
		} else if state == "incomplete" && root.Get("incomplete_details.reason").String() != "max_output_tokens" {
			responseErr = errors.Join(responseErr, errors.New("Responses 因输出上限之外的原因中断"))
		}
	} else {
		return nil, errors.New("无法识别保活响应协议")
	}
	if u.Get("cache_read_input_tokens").Exists() {
		cached = "cache_read_input_tokens"
	}
	for _, path := range []string{input, output, cached} {
		if _, err := tokenField(u, path, true); err != nil {
			return nil, errors.Join(responseErr, err)
		}
	}
	usage, err := tokenusage.ParseJSONUsage([]byte(u.Raw))
	if err != nil {
		return nil, fmt.Errorf("解析保活用量失败: %w", err)
	}
	_, _, _, _, err = usageNumbers(usage)
	return usage, errors.Join(responseErr, err)
}

// 只聚合上游用量，不缓存回复，不经过任何会话写回或工具执行链。
func parseStreamResponse(protocol string, body []byte, status int) (*types.Usage, error) {
	scanner := bufio.NewScanner(bytes.NewReader(body))
	scanner.Buffer(make([]byte, 4096), 1<<20)
	var data []string
	eventName := ""
	var envelope []byte
	var merged = []byte(`{}`)
	done := false
	claudeFinalUsage := false
	var streamErr error
	geminiFinish := ""
	consume := func() error {
		if len(data) == 0 {
			eventName = ""
			return nil
		}
		event := eventName
		eventName = ""
		payload := strings.Join(data, "\n")
		data = nil
		if payload == codec.SSEDoneMarker {
			if protocol == "openai" {
				done = true
			}
			return nil
		}
		if !gjson.Valid(payload) {
			return errors.New("保活 SSE 数据不是完整 JSON")
		}
		root := gjson.Parse(payload)
		kind := root.Get("type").String()
		if kind == "" {
			kind = event
		}
		if event == "error" || kind == "error" || kind == "response.failed" || root.Get("error").Exists() && root.Get("error").Type != gjson.Null {
			streamErr = errors.Join(streamErr, errors.New("上游流式保活返回错误事件"))
		}
		switch protocol {
		case "claude":
			u := root.Get("usage")
			if kind == "message_start" {
				u = root.Get("message.usage")
			}
			if kind == "message_delta" && u.Get("output_tokens").Exists() {
				claudeFinalUsage = true
			}
			var mergeErr error
			u.ForEach(func(key, value gjson.Result) bool {
				merged, mergeErr = sjson.SetRawBytes(merged, key.String(), []byte(value.Raw))
				return mergeErr == nil
			})
			if mergeErr != nil {
				return mergeErr
			}
			if kind == "message_stop" {
				done = true
			}
		case "responses":
			if kind == "response.completed" || kind == "response.incomplete" || kind == "response.failed" {
				envelope = []byte(root.Get("response").Raw)
				done = true
			}
		case "openai":
			for _, choice := range root.Get("choices").Array() {
				if choice.Get("finish_reason").String() == "content_filter" {
					streamErr = errors.Join(streamErr, errors.New("Chat 保活生成被内容过滤"))
				}
			}
			if u := root.Get("usage"); u.IsObject() {
				envelope = []byte(payload)
			}
		case "gemini":
			if root.Get("usageMetadata").IsObject() {
				envelope = []byte(payload)
			}
			if reason := root.Get("candidates.0.finishReason").String(); reason != "" {
				geminiFinish = reason
				done = true
			}
		default:
			return errors.New("无法识别保活流式协议")
		}
		return nil
	}
	for scanner.Scan() {
		line := strings.TrimSuffix(scanner.Text(), "\r")
		if line == "" {
			if err := consume(); err != nil {
				streamErr = errors.Join(streamErr, err)
				break
			}
			continue
		}
		if value, ok := codec.ParseSSEDataLine(line); ok {
			data = append(data, value)
		} else if strings.HasPrefix(line, "event:") {
			eventName = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		}
	}
	if err := scanner.Err(); err != nil {
		streamErr = errors.Join(streamErr, fmt.Errorf("读取保活 SSE 失败: %w", err))
	}
	if err := consume(); err != nil {
		streamErr = errors.Join(streamErr, err)
	}
	if !done {
		streamErr = errors.Join(streamErr, errors.New("保活流式响应未完整结束"))
	}
	if protocol == "claude" {
		if !claudeFinalUsage {
			return nil, errors.Join(streamErr, errors.New("Claude 流缺少最终输出用量，不能把起始零值作为实际用量"))
		}
		envelope = append(append([]byte(`{"type":"message","usage":`), merged...), '}')
	} else if protocol == "gemini" && geminiFinish != "" && len(envelope) > 0 {
		// Gemini 可先发 finishReason，再单独发送最终 usageMetadata。
		var err error
		envelope, err = sjson.SetBytes(envelope, "candidates", []map[string]string{{"finishReason": geminiFinish}})
		streamErr = errors.Join(streamErr, err)
	}
	usage, err := parseProtocolResponse(protocol, envelope, status)
	return usage, errors.Join(streamErr, err)
}
