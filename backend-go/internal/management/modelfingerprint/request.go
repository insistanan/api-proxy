package modelfingerprint

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	lmfpd "github.com/Ikaleio/lm-detector/sdk/go"
	"github.com/google/uuid"
	"github.com/insistanan/api-proxy/internal/infra/config"
	"github.com/insistanan/api-proxy/internal/infra/httpclient"
	"github.com/insistanan/api-proxy/internal/infra/upstreamreq"
	"github.com/insistanan/api-proxy/internal/orchestration/upstreamcontrol"
	"github.com/insistanan/api-proxy/internal/protocol/codec"
	"github.com/insistanan/api-proxy/internal/protocol/converters"
)

const sampleTimeout = 180 * time.Second
const maxSampleBytes = 2 << 20

// buildRequest 只构造原生协议正文和既有客户端伪装；不调用会改写模型、提示或历史的 Provider。
func buildRequest(ctx context.Context, target *Target, prompt string) (*http.Request, error) {
	u := target.upstream
	messages := []any{map[string]any{"role": "user", "content": prompt}}
	var body map[string]any
	version, endpoint := "/v1", ""
	switch target.Protocol {
	case "claude":
		endpoint = "/messages"
		body = map[string]any{"model": target.Model, "messages": messages, "max_tokens": 8192, "stream": true}
	case "responses":
		endpoint = "/responses"
		body = map[string]any{"model": target.Model, "input": messages, "store": false, "stream": true}
	case "openai":
		endpoint = "/chat/completions"
		body = map[string]any{"model": target.Model, "messages": messages, "stream": true}
	case "gemini":
		version = "/v1beta"
		endpoint = "/models/" + url.PathEscape(strings.TrimPrefix(target.Model, "models/")) + ":streamGenerateContent"
		body = map[string]any{"contents": []any{map[string]any{"role": "user", "parts": []any{map[string]any{"text": prompt}}}}}
	default:
		return nil, fmt.Errorf("不支持的上游协议：%s", target.Protocol)
	}
	requestURL := upstreamreq.BuildUpstreamURL(u.BaseURL, version, endpoint, u.UseFullURL)
	parsed, err := url.Parse(requestURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil || parsed.Fragment != "" {
		return nil, fmt.Errorf("渠道的上游请求地址无效")
	}
	if target.Protocol == "gemini" && !u.UseFullURL {
		query := parsed.Query()
		query.Set("alt", "sse")
		parsed.RawQuery = query.Encode()
		requestURL = parsed.String()
	}
	header := make(http.Header)
	header.Set("Content-Type", "application/json")
	header.Set("Accept", "text/event-stream")
	upstreamreq.PrepareTargetIdentity(header, target.Protocol, target.Compatibility)
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("编码采样请求失败：%w", err)
	}
	if target.Effort != "" {
		// 只复用参数渲染，不读取渠道的固定档位，也不做路由或提示词处理。
		payload, err = converters.ApplyChannelReasoningOverride(payload, &config.UpstreamConfig{
			ServiceType: target.Protocol, ReasoningEffort: target.Effort,
		}, target.Model)
		if err != nil {
			return nil, fmt.Errorf("构造采样思考参数失败：%w", err)
		}
	}
	if target.Compatibility {
		upstreamreq.SeedDisguiseSession(header, uuid.NewString(), target.Protocol)
		switch target.Protocol {
		case "claude":
			upstreamreq.ApplyClaudeCodeDisguise(header, true)
			payload = upstreamreq.ApplyClaudeCodeBodyDisguise(payload, header.Get("X-Claude-Code-Session-Id"))
		case "responses":
			upstreamreq.ApplyCodexDisguise(header, true)
		}
	}
	if target.Protocol == "gemini" {
		upstreamreq.SetGeminiAuthenticationHeader(header, target.apiKey)
	} else {
		upstreamreq.SetAuthenticationHeader(header, target.apiKey)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, requestURL, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("创建采样请求失败：%w", err)
	}
	req.Header = header
	return req, nil
}

func (s *Service) sample(ctx context.Context, target *Target, challenge lmfpd.Challenge, index int) Sample {
	started := time.Now()
	sample := Sample{Index: index, State: "failed", Prompt: challenge.Prompt, ExpectedCount: challenge.ExpectedCount}
	fail := func(err error) Sample {
		sample.Error = strings.ReplaceAll(err.Error(), target.apiKey, "[已隐藏密钥]")
		sample.DurationMS = time.Since(started).Milliseconds()
		return sample
	}
	ctx, cancel := context.WithTimeout(ctx, sampleTimeout)
	defer cancel()
	req, err := buildRequest(ctx, target, challenge.Prompt)
	if err != nil {
		return fail(err)
	}
	ctx = s.cfg.UpstreamRequestContext(upstreamcontrol.WithScope(ctx), target.upstream, target.apiKey)
	resp, err := httpclient.Send(req.WithContext(ctx), httpclient.SendOptions{
		IsStream: true, ProxyURL: target.proxyURL, InsecureSkipVerify: target.upstream.InsecureSkipVerify,
		StreamIdleTimeout: sampleTimeout, StreamLabel: "ModelFingerprint",
		ResponseHeaderTimeout: time.Duration(s.cfg.GetGatewaySettings().ResponseHeaderTimeout) * time.Second,
	})
	if err != nil {
		return fail(fmt.Errorf("请求上游失败：%w", err))
	}
	defer codec.BindStreamBody(ctx, resp.Body)()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, readErr := io.ReadAll(io.LimitReader(resp.Body, 2048))
		if readErr != nil {
			return fail(fmt.Errorf("读取 HTTP %d 错误回答失败：%w", resp.StatusCode, readErr))
		}
		return fail(fmt.Errorf("上游返回 HTTP %d：%s", resp.StatusCode, strings.TrimSpace(string(data))))
	}
	var text, model string
	var truncated bool
	if strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "text/event-stream") {
		text, model, truncated, err = readStream(ctx, resp.Body, target.Protocol)
	} else {
		var data []byte
		data, err = io.ReadAll(io.LimitReader(resp.Body, maxSampleBytes+1))
		if err == nil && len(data) > maxSampleBytes {
			err = fmt.Errorf("上游回答超过 2 MiB 限制")
		}
		if err == nil {
			text, model, truncated, err = parseReply(data, target.Protocol)
		}
	}
	sample.Text, sample.ResponseModel = text, model
	sample.Count = len(lmfpd.ParseNumbers(text))
	if err != nil {
		return fail(err)
	}
	if truncated {
		sample.State = "truncated"
		return fail(fmt.Errorf("上游回答被截断，本条不参与评分"))
	}
	minimum := max(80, (challenge.ExpectedCount*55+99)/100)
	if sample.Count < minimum {
		return fail(fmt.Errorf("有效整数不足：%d/%d，至少需要 %d 个", sample.Count, challenge.ExpectedCount, minimum))
	}
	sample.State = "complete"
	sample.DurationMS = time.Since(started).Milliseconds()
	return sample
}

type textPart struct {
	Type    string `json:"type"`
	Text    string `json:"text"`
	Thought bool   `json:"thought"`
}

func parseReply(data []byte, protocol string) (text, model string, truncated bool, err error) {
	var reply struct {
		Model        string          `json:"model"`
		ModelVersion string          `json:"modelVersion"`
		Error        json.RawMessage `json:"error"`
		Status       string          `json:"status"`
		StopReason   string          `json:"stop_reason"`
		Content      []textPart      `json:"content"`
		Choices      []struct {
			Message struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Output []struct {
			Type    string     `json:"type"`
			Content []textPart `json:"content"`
		} `json:"output"`
		Candidates []struct {
			Content struct {
				Parts []textPart `json:"parts"`
			} `json:"content"`
			FinishReason string `json:"finishReason"`
		} `json:"candidates"`
	}
	if err = json.Unmarshal(data, &reply); err != nil {
		return "", "", false, fmt.Errorf("上游未返回有效 JSON：%w", err)
	}
	if len(reply.Error) > 0 && string(reply.Error) != "null" {
		return "", reply.Model, false, fmt.Errorf("上游报告错误：%s", reply.Error)
	}
	var parts []textPart
	model = reply.Model
	switch protocol {
	case "claude":
		parts, truncated = reply.Content, reply.StopReason == "max_tokens"
		if reply.StopReason != "end_turn" && !truncated {
			return "", model, false, fmt.Errorf("Messages 未正常结束：%s", reply.StopReason)
		}
	case "responses":
		truncated = reply.Status == "incomplete"
		if reply.Status != "completed" && !truncated {
			return "", model, false, fmt.Errorf("Responses 未正常完成：%s", reply.Status)
		}
		for _, output := range reply.Output {
			if output.Type == "message" {
				parts = append(parts, output.Content...)
			}
		}
	case "openai":
		if len(reply.Choices) == 0 {
			return "", model, false, fmt.Errorf("上游回答缺少 choices")
		}
		choice := reply.Choices[0]
		truncated = choice.FinishReason == "length"
		if choice.FinishReason != "stop" && !truncated {
			return "", model, false, fmt.Errorf("Chat 未正常结束：%s", choice.FinishReason)
		}
		if err = json.Unmarshal(choice.Message.Content, &text); err != nil {
			if err = json.Unmarshal(choice.Message.Content, &parts); err != nil {
				return "", model, false, fmt.Errorf("无法解析 Chat 回答正文：%w", err)
			}
		}
	case "gemini":
		model = reply.ModelVersion
		if len(reply.Candidates) == 0 {
			return "", model, false, fmt.Errorf("上游回答缺少 candidates（可能已被拦截）")
		}
		candidate := reply.Candidates[0]
		parts, truncated = candidate.Content.Parts, candidate.FinishReason == "MAX_TOKENS"
		if candidate.FinishReason != "STOP" && !truncated {
			return "", model, false, fmt.Errorf("Gemini 未正常结束：%s", candidate.FinishReason)
		}
	default:
		return "", model, false, fmt.Errorf("不支持的响应协议：%s", protocol)
	}
	for _, part := range parts {
		if !part.Thought && (part.Type == "" || part.Type == "text" || part.Type == "output_text") {
			text += part.Text
		}
	}
	if strings.TrimSpace(text) == "" {
		return "", model, truncated, fmt.Errorf("上游未返回可用于检测的文本回答")
	}
	return text, model, truncated, nil
}
