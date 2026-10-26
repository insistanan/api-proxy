package httpclient

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/BenedictKing/api-proxy/internal/config"
)

// ipv4Dialer 优先使用 IPv4 建立 TCP 连接。
// 直接请求上游时，这会把上游域名解析到 IPv4（避免服务商/渠道只回 IPv6 导致连接异常）；
// 走 HTTP/HTTPS/SOCKS5 代理时，http.Transport 的 DialContext 只负责连接代理服务器，
// 因此这里强制的是“本机 -> 代理”这一段使用 IPv4，不会改变代理转发上游的原有逻辑。
type ipv4Dialer struct {
	d *net.Dialer
}

// DialContext 实现 net.ContextDialer。优先解析 A 记录并尝试 IPv4；仅当域名没有 IPv4 记录时回退默认拨号。
func (d *ipv4Dialer) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	if network != "tcp" && network != "tcp4" {
		return d.d.DialContext(ctx, network, addr)
	}

	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return d.d.DialContext(ctx, network, addr)
	}

	// 已经是 IP 字面量时交给标准拨号器；IPv6 字面量不强改，避免完全不可达。
	if ip := net.ParseIP(host); ip != nil {
		if ip.To4() != nil {
			return d.d.DialContext(ctx, "tcp4", addr)
		}
		return d.d.DialContext(ctx, network, addr)
	}

	// 域名：优先取 IPv4 地址；若解析失败或没有 A 记录，再回退默认拨号（兼容纯 IPv6 上游）。
	ips, err := net.DefaultResolver.LookupIP(ctx, "ip4", host)
	if err != nil || len(ips) == 0 {
		return d.d.DialContext(ctx, network, addr)
	}

	var lastErr error
	for _, ip := range ips {
		ipv4 := ip.To4()
		if ipv4 == nil {
			continue
		}
		ipv4Addr := net.JoinHostPort(ipv4.String(), port)
		conn, dialErr := d.d.DialContext(ctx, "tcp4", ipv4Addr)
		if dialErr == nil {
			return conn, nil
		}
		lastErr = dialErr
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return d.d.DialContext(ctx, network, addr)
}

// ipv4PreferredDialer 全局共享的 IPv4 优先拨号器。
var ipv4PreferredDialer = &ipv4Dialer{
	d: &net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
	},
}

// ClientManager HTTP 客户端管理器
type ClientManager struct {
	mu      sync.RWMutex
	clients map[string]*http.Client
}

var globalManager = &ClientManager{
	clients: make(map[string]*http.Client),
}

// GetManager 获取全局客户端管理器
func GetManager() *ClientManager {
	return globalManager
}

// GetStandardClient 获取标准客户端（有超时，用于普通请求）
// 注意：启用自动压缩让Go处理gzip，配合请求头清理确保正确解压
func (cm *ClientManager) GetStandardClient(timeout time.Duration, insecure bool, proxyURL string) (*http.Client, error) {
	envConfig := config.NewEnvConfig()
	responseHeaderTimeout := time.Duration(envConfig.ResponseHeaderTimeout) * time.Second
	proxyURL = strings.TrimSpace(proxyURL)

	key := fmt.Sprintf("standard-%d-%t-%d-%s", timeout, insecure, envConfig.ResponseHeaderTimeout, proxyKey(proxyURL))

	cm.mu.RLock()
	if client, ok := cm.clients[key]; ok {
		cm.mu.RUnlock()
		return client, nil
	}
	cm.mu.RUnlock()

	cm.mu.Lock()
	defer cm.mu.Unlock()

	if client, ok := cm.clients[key]; ok {
		return client, nil
	}
	proxyFunc, err := buildProxyFunc(proxyURL)
	if err != nil {
		return nil, err
	}

	transport := &http.Transport{
		Proxy:                 proxyFunc,
		DialContext:           ipv4PreferredDialer.DialContext,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   10,
		IdleConnTimeout:       90 * time.Second,
		DisableCompression:    false,
		TLSHandshakeTimeout:   15 * time.Second,
		ResponseHeaderTimeout: responseHeaderTimeout,
		ExpectContinueTimeout: 1 * time.Second,
		// 统一走 HTTP/1.1，避免 HTTP/2 在部分代理/上游下触发 "http2: timeout awaiting response headers"
		ForceAttemptHTTP2: false,
	}

	tlsCfg := &tls.Config{
		NextProtos: []string{"http/1.1"},
	}
	if insecure {
		tlsCfg.InsecureSkipVerify = true
	}
	transport.TLSClientConfig = tlsCfg

	client := &http.Client{
		Transport: transport,
		Timeout:   timeout,
	}

	cm.clients[key] = client
	return client, nil
}

// GetStandardClientForUpstream 根据全局与渠道配置解析最终代理后创建普通请求客户端。
func (cm *ClientManager) GetStandardClientForUpstream(timeout time.Duration, cfgManager *config.ConfigManager, upstream *config.UpstreamConfig) (*http.Client, error) {
	if cfgManager == nil || upstream == nil {
		return nil, fmt.Errorf("创建上游 HTTP 客户端时缺少配置")
	}
	proxyURL, err := cfgManager.ResolveUpstreamProxyURL(upstream)
	if err != nil {
		return nil, err
	}
	return cm.GetStandardClient(timeout, upstream.InsecureSkipVerify, proxyURL)
}

// GetStreamClient 获取流式客户端（响应体无超时，用于 SSE 流式响应）
// ResponseHeaderTimeout 限制"发出请求到收到响应头"的时间（首字节超时），
// 防止"连接建立但永不响应"的上游占住 handler；响应体读取本身不受限，
// 避免思考模型长静默期被整体超时误杀。流中挂起由 IdleTimeoutReader 检测（见 STREAM_IDLE_TIMEOUT）。
func (cm *ClientManager) GetStreamClient(insecure bool, proxyURL string) (*http.Client, error) {
	envConfig := config.NewEnvConfig()
	proxyURL = strings.TrimSpace(proxyURL)

	key := fmt.Sprintf("stream-%t-%d-%s", insecure, envConfig.ResponseHeaderTimeout, proxyKey(proxyURL))

	cm.mu.RLock()
	if client, ok := cm.clients[key]; ok {
		cm.mu.RUnlock()
		return client, nil
	}
	cm.mu.RUnlock()

	cm.mu.Lock()
	defer cm.mu.Unlock()

	if client, ok := cm.clients[key]; ok {
		return client, nil
	}
	proxyFunc, err := buildProxyFunc(proxyURL)
	if err != nil {
		return nil, err
	}

	transport := &http.Transport{
		Proxy:                 proxyFunc,
		DialContext:           ipv4PreferredDialer.DialContext,
		MaxIdleConns:          200,
		MaxIdleConnsPerHost:   20,
		IdleConnTimeout:       120 * time.Second,
		DisableCompression:    true,
		TLSHandshakeTimeout:   15 * time.Second,
		ResponseHeaderTimeout: time.Duration(envConfig.ResponseHeaderTimeout) * time.Second, // 首字节超时
		ExpectContinueTimeout: 1 * time.Second,
		// 统一走 HTTP/1.1，避免 HTTP/2 在部分代理/上游下触发 "http2: timeout awaiting response headers"
		ForceAttemptHTTP2: false,
	}

	tlsCfg := &tls.Config{
		NextProtos: []string{"http/1.1"},
	}
	if insecure {
		tlsCfg.InsecureSkipVerify = true
	}
	transport.TLSClientConfig = tlsCfg

	client := &http.Client{
		Transport: transport,
		Timeout:   0,
	}

	cm.clients[key] = client
	return client, nil
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

func proxyKey(proxyURL string) string {
	if proxyURL == "" {
		return "direct"
	}
	sum := sha256.Sum256([]byte(proxyURL))
	return fmt.Sprintf("%x", sum[:])
}
