package cachekeepalive

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"insistanan/api-proxy/internal/infra/config"
	"insistanan/api-proxy/internal/infra/httpclient"
	"insistanan/api-proxy/internal/orchestration/upstreamcontrol"
	"insistanan/api-proxy/internal/protocol/tokenusage"
	"insistanan/api-proxy/internal/protocol/types"
	"insistanan/api-proxy/internal/storage/logger"
)

type sendResult struct {
	sent       bool
	sentAt     time.Time
	usage      *types.Usage
	err        error
	expiresAt  time.Time
	statusCode int
}

func newSender(cfg *config.ConfigManager, recorders ...func(tokenusage.Fact)) func(context.Context, *snapshot) sendResult {
	var renewing sync.Map
	return func(ctx context.Context, s *snapshot) (result sendResult) {
		attemptID := logger.NewRequestID()
		defer func() {
			if s.mode == "explicit" {
				log.Printf("[CacheKeepalive-Result] 会话=%s 渠道=%s attemptId=%s 模式=显式缓存续期 已更新=%t 到期=%s 结果=%v",
					s.source.ConversationID, s.source.Upstream.ID, attemptID, result.sent, result.expiresAt.Format(time.RFC3339), result.err)
				return
			}
			input, read, creation, output, usageErr := usageNumbers(result.usage)
			if result.sent && s.mode != "explicit" && (result.statusCode != http.StatusTooManyRequests || result.usage != nil) {
				fact := tokenusage.Fact{AttemptID: attemptID, At: time.Now(), Protocol: "cache_keepalive/" + s.source.Upstream.ServiceType,
					Model: s.model, Source: tokenusage.SourceUpstream, Input: input, CacheRead: read, CacheCreation: creation, Output: output}
				if usageErr != nil {
					fact.Source = tokenusage.SourceMissing
				}
				for _, record := range recorders {
					if record != nil {
						record(fact)
					}
				}
			}
			log.Printf("[CacheKeepalive-Result] 会话=%s 渠道=%s attemptId=%s 已发送=%t 输入=%d 缓存读取=%d 缓存写入=%d 输出=%d 用量状态=%v 结果=%v",
				s.source.ConversationID, s.source.Upstream.ID, attemptID, result.sent,
				input, read, creation, output, usageErr, result.err)
		}()
		// 缓存窗口限制开始发送的时间；整轮截止时间同时约束发送和响应读取。
		ctx, cancel := context.WithTimeout(ctx, requestTimeout)
		defer cancel()
		if !s.deadline.IsZero() {
			var cancelDeadline context.CancelFunc
			ctx, cancelDeadline = context.WithDeadline(ctx, s.deadline)
			defer cancelDeadline()
		}
		if ctx.Err() != nil {
			result.err = errors.New("保活已取消或达到持续时间上限，不发送请求")
			return
		}
		ctx = logger.ContextWithAttemptID(ctx, attemptID)
		if s.mode == "explicit" {
			resource, err := url.Parse(s.cacheURL)
			if err != nil {
				result.err = errors.New("缓存资源地址无效")
				return
			}
			resource.RawQuery, resource.Fragment = "", ""
			// GET 和 PATCH 跨两次准入；整轮互斥，避免共享资源被旧读取结果缩短期限。
			key := resource.String()
			if _, busy := renewing.LoadOrStore(key, struct{}{}); busy {
				result.err = errors.New("同一 Gemini 缓存资源正在续期，停止重复任务")
				return
			}
			defer renewing.Delete(key)
			return renewGeminiCache(ctx, cfg, s)
		}
		ctx = cfg.UpstreamRequestContext(upstreamcontrol.WithScope(ctx), s.source.Upstream, s.source.APIKey)
		ctx = upstreamcontrol.WithBackgroundAdmission(ctx)
		ctx = httpclient.WithSendObserver(ctx, func() { result.sent, result.sentAt = true, time.Now() })
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.source.URL, bytes.NewReader(s.body))
		if err != nil {
			result.err = errors.New("无法构造保活上游请求")
			return
		}
		req.Header = s.source.Header.Clone()
		for _, header := range []string{"Content-Length", "Accept-Encoding", "Idempotency-Key", "X-Idempotency-Key", "X-Request-Id", "X-Client-Request-Id"} {
			req.Header.Del(header)
		}
		req.Header.Set("Accept", "application/json")
		if s.stream {
			req.Header.Set("Accept", "text/event-stream")
		}
		proxyURL, err := cfg.ResolveUpstreamProxyURL(s.source.Upstream)
		if err != nil {
			result.err = errors.New("无法解析保活渠道的代理配置")
			return
		}
		if s.ttl > 0 && !time.Now().Before(s.lastSent.Add(s.ttl-expiryMargin)) {
			result.err = errors.New("已错过缓存续期窗口，不重建过期缓存")
			return
		}
		resp, err := httpclient.Send(req, httpclient.SendOptions{IsStream: s.stream, Timeout: requestTimeout,
			ResponseHeaderTimeout: requestTimeout, StreamIdleTimeout: requestTimeout, StreamLabel: "CacheKeepalive",
			ProxyURL: proxyURL, InsecureSkipVerify: s.source.Upstream.InsecureSkipVerify})
		if err != nil {
			switch {
			case errors.Is(err, upstreamcontrol.ErrBackgroundBusy):
				result.err = upstreamcontrol.ErrBackgroundBusy
			case errors.Is(err, context.Canceled):
				result.err = errors.New("保活请求已取消")
			case errors.Is(err, context.DeadlineExceeded):
				result.err = errors.New("保活请求超时")
			default:
				// 网络错误可包含目标 URL 和查询凭据，管理状态只展示明确的失败类别。
				result.err = errors.New("保活上游发送失败，本轮已停止")
			}
			return
		}
		result.statusCode = resp.StatusCode
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, 1<<20+1))
		closeErr := resp.Body.Close()
		if strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "text/event-stream") || bytes.HasPrefix(bytes.TrimSpace(body), []byte("data:")) || bytes.HasPrefix(bytes.TrimSpace(body), []byte("event:")) {
			result.usage, result.err = parseStreamResponse(s.source.Upstream.ServiceType, body, resp.StatusCode)
		} else {
			result.usage, result.err = parseProtocolResponse(s.source.Upstream.ServiceType, body, resp.StatusCode)
		}
		if readErr != nil || closeErr != nil || len(body) > 1<<20 {
			result.err = errors.Join(result.err, errors.New("保活上游响应不完整或超过大小上限"))
		}
		return
	}
}

func parseResponse(body []byte, statusCode int) (*types.Usage, error) {
	var envelope struct {
		Type  string          `json:"type"`
		Error json.RawMessage `json:"error"`
		Usage *struct {
			types.Usage
			InputTokens          *int `json:"input_tokens"`
			OutputTokens         *int `json:"output_tokens"`
			CacheReadInputTokens *int `json:"cache_read_input_tokens"`
			CacheCreation        *struct {
				FiveMinutes int `json:"ephemeral_5m_input_tokens"`
				OneHour     int `json:"ephemeral_1h_input_tokens"`
			} `json:"cache_creation"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, errors.New("保活上游未返回完整 JSON 响应")
	}
	var usage *types.Usage
	if envelope.Usage != nil {
		u := envelope.Usage
		if u.InputTokens == nil || u.OutputTokens == nil || u.CacheReadInputTokens == nil {
			return nil, errors.New("保活上游缺少完整输入、输出或缓存读取用量")
		}
		usage = &u.Usage
		usage.InputTokens, usage.OutputTokens, usage.CacheReadInputTokens = *u.InputTokens, *u.OutputTokens, *u.CacheReadInputTokens
		if u.CacheCreation != nil {
			usage.CacheCreation5mInputTokens = u.CacheCreation.FiveMinutes
			usage.CacheCreation1hInputTokens = u.CacheCreation.OneHour
		}
	}
	if statusCode < 200 || statusCode >= 300 || envelope.Type == "error" || len(envelope.Error) > 0 && string(envelope.Error) != "null" {
		return usage, fmt.Errorf("保活上游拒绝请求（HTTP %d），不重试或切换目标", statusCode)
	}
	if envelope.Type != "message" {
		return usage, errors.New("保活上游未返回 Messages 响应")
	}
	_, _, _, _, err := usageNumbers(usage)
	return usage, err
}
