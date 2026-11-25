package httpclient

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"insistanan/api-proxy/internal/infra/config"
)

// 使用标准双栈拨号，交给系统解析器和 Happy Eyeballs 选择 IPv4 / IPv6。
var upstreamDialer = &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}

// ErrResponseHeaderTimeout 与客户端取消分开，调用方可以识别上游阶段超时。
var ErrResponseHeaderTimeout error = responseHeaderTimeoutError{}

type responseHeaderTimeoutError struct{}

func (responseHeaderTimeoutError) Error() string   { return "上游发送至响应头阶段超时" }
func (responseHeaderTimeoutError) Timeout() bool   { return true }
func (responseHeaderTimeoutError) Temporary() bool { return true }

func newTransport(opts ClientOptions) (http.RoundTripper, error) {
	proxy, err := buildProxyFunc(opts.ProxyURL)
	if err != nil {
		return nil, err
	}
	transport := &http.Transport{
		Proxy: proxy, DialContext: upstreamDialer.DialContext,
		MaxIdleConns: 100, MaxIdleConnsPerHost: 10, IdleConnTimeout: 90 * time.Second,
		TLSHandshakeTimeout: 15 * time.Second, ResponseHeaderTimeout: opts.ResponseHeaderTimeout,
		ExpectContinueTimeout: time.Second,
		// 部分代理的 HTTP/2 响应头超时行为不稳定，沿用 HTTP/1.1 传输约定。
		TLSClientConfig: &tls.Config{NextProtos: []string{"http/1.1"}, InsecureSkipVerify: opts.InsecureSkipVerify},
	}
	if opts.IsStream {
		transport.MaxIdleConns = 200
		transport.MaxIdleConnsPerHost = 20
		transport.IdleConnTimeout = 120 * time.Second
		transport.DisableCompression = true
		return &responseHeaderTransport{inner: transport, timeout: opts.ResponseHeaderTimeout}, nil
	}
	return transport, nil
}

func buildProxyFunc(rawURL string) (func(*http.Request) (*url.URL, error), error) {
	if rawURL == "" {
		return nil, nil
	}
	if err := config.ValidateProxyURL(rawURL); err != nil {
		return nil, err
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("解析代理地址失败: %w", err)
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	return http.ProxyURL(parsed), nil
}

// responseHeaderTransport 的预算包含连接、上传及等待响应头，不包含本地准入排队和流式正文。
type responseHeaderTransport struct {
	inner   http.RoundTripper
	timeout time.Duration
}

func (t *responseHeaderTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	ctx, cancel := context.WithCancelCause(req.Context())
	var completed sync.Once
	timer := time.AfterFunc(t.timeout, func() {
		completed.Do(func() { cancel(ErrResponseHeaderTimeout) })
	})
	resp, err := t.inner.RoundTrip(req.WithContext(ctx))
	// 超时回调与响应头返回只能有一方胜出，避免计时器迟到取消已经交付的流。
	completed.Do(func() {})
	timer.Stop()
	if cause := context.Cause(ctx); cause != nil {
		err = cause
	}
	if err != nil {
		cancel(err)
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
		return nil, err
	}
	resp.Body = &cancelBody{ReadCloser: resp.Body, cancel: cancel}
	return resp, nil
}

func (t *responseHeaderTransport) CloseIdleConnections() {
	if closer, ok := t.inner.(interface{ CloseIdleConnections() }); ok {
		closer.CloseIdleConnections()
	}
}

// 子 context 必须活到正文结束；仅停止响应头计时不能提前取消成功的 SSE 连接。
type cancelBody struct {
	io.ReadCloser
	cancel context.CancelCauseFunc
	once   sync.Once
	err    error
}

func (b *cancelBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if err != nil {
		_ = b.Close()
	}
	return n, err
}

func (b *cancelBody) Close() error {
	b.once.Do(func() {
		b.err = b.ReadCloser.Close()
		b.cancel(nil)
	})
	return b.err
}
