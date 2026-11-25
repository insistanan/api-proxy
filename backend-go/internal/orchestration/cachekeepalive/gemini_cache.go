package cachekeepalive

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/insistanan/api-proxy/internal/infra/config"
	"github.com/insistanan/api-proxy/internal/infra/httpclient"
	"github.com/insistanan/api-proxy/internal/orchestration/upstreamcontrol"
)

type geminiCacheResource struct {
	Name       string    `json:"name"`
	ExpireTime time.Time `json:"expireTime"`
}

// 显式缓存的读取不会刷新 TTL；必须更新资源，并核验服务端返回的到期时间。
func renewGeminiCache(ctx context.Context, cfg *config.ConfigManager, s *snapshot) (result sendResult) {
	proxyURL, err := cfg.ResolveUpstreamProxyURL(s.source.Upstream)
	if err != nil {
		result.err = errors.New("无法解析缓存续期的代理配置")
		return
	}
	ctx = cfg.UpstreamRequestContext(upstreamcontrol.WithScope(ctx), s.source.Upstream, s.source.APIKey)
	ctx = upstreamcontrol.WithBackgroundAdmission(ctx)
	call := func(method, target string, body []byte) (geminiCacheResource, error) {
		resourceURL, err := url.Parse(target)
		if err != nil {
			return geminiCacheResource{}, errors.New("缓存资源地址无效")
		}
		query := resourceURL.Query()
		// CachedContent 的完整资源包含上下文，续期只读取元数据。
		query.Set("fields", "name,expireTime")
		resourceURL.RawQuery = query.Encode()
		requestCtx := ctx
		if method == http.MethodPatch {
			requestCtx = httpclient.WithSendObserver(ctx, func() { result.sent = true; result.sentAt = time.Now() })
		}
		req, err := http.NewRequestWithContext(requestCtx, method, resourceURL.String(), bytes.NewReader(body))
		if err != nil {
			return geminiCacheResource{}, errors.New("构造缓存续期请求失败")
		}
		req.Header = s.source.Header.Clone()
		for _, name := range []string{"Content-Length", "Accept-Encoding", "Idempotency-Key", "X-Idempotency-Key", "X-Request-Id", "X-Client-Request-Id"} {
			req.Header.Del(name)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
		resp, err := httpclient.Send(req, httpclient.SendOptions{Timeout: requestTimeout, ResponseHeaderTimeout: requestTimeout, ProxyURL: proxyURL, InsecureSkipVerify: s.source.Upstream.InsecureSkipVerify})
		if err != nil {
			if errors.Is(err, upstreamcontrol.ErrBackgroundBusy) {
				return geminiCacheResource{}, upstreamcontrol.ErrBackgroundBusy
			}
			return geminiCacheResource{}, errors.New("Gemini 缓存资源请求失败或已取消，不重试")
		}
		result.statusCode = resp.StatusCode
		data, readErr := io.ReadAll(io.LimitReader(resp.Body, 64<<10+1))
		if err := errors.Join(readErr, resp.Body.Close()); err != nil || len(data) > 64<<10 {
			return geminiCacheResource{}, errors.New("缓存资源响应不完整或超过大小上限")
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return geminiCacheResource{}, fmt.Errorf("Gemini 缓存资源请求被拒绝（HTTP %d）", resp.StatusCode)
		}
		var resource geminiCacheResource
		if json.Unmarshal(data, &resource) != nil || resource.Name != s.cacheName || resource.ExpireTime.IsZero() {
			return resource, errors.New("Gemini 未返回匹配的缓存资源及到期时间")
		}
		return resource, nil
	}
	resource, err := call(http.MethodGet, s.cacheURL, nil)
	if err != nil {
		result.err = err
		return
	}
	now := time.Now()
	if !resource.ExpireTime.After(now) {
		result.err = errors.New("Gemini 显式缓存已过期，不重建资源")
		return
	}
	// 留出下一轮续期余量，且不把显式缓存的收费保留期延长到本轮时限之外。
	desired := minTime(now.Add(2*time.Duration(s.policy.IntervalSeconds)*time.Second+expiryMargin), s.deadline)
	if !desired.After(now) {
		result.err = errors.New("已达到最长保活时间")
		return
	}
	if !desired.After(resource.ExpireTime) {
		result.sentAt, result.expiresAt = now, resource.ExpireTime
		return
	}
	u, err := url.Parse(s.cacheURL)
	if err != nil {
		result.err = errors.New("缓存资源地址无效")
		return
	}
	query := u.Query()
	query.Set("updateMask", "expireTime")
	u.RawQuery = query.Encode()
	body, err := json.Marshal(map[string]string{"expireTime": desired.UTC().Format(time.RFC3339Nano)})
	if err != nil {
		result.err = fmt.Errorf("编码缓存续期参数: %w", err)
		return
	}
	updated, err := call(http.MethodPatch, u.String(), body)
	if err != nil {
		result.err = err
		return
	}
	if updated.ExpireTime.Before(desired.Add(-time.Second)) {
		result.err = errors.New("Gemini 返回的到期时间未达到请求的续期目标")
		return
	}
	result.expiresAt = updated.ExpireTime
	return
}
