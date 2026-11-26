package httpclient

import (
	"fmt"
	"net/http"
	"time"

	"insistanan/api-proxy/internal/infra/config"
)

// DownloadClient 只用于公开辅助资源，沿用项目全局代理；关闭代理时明确直连。
// 每个任务拥有独立连接池，调用方负责 CloseIdleConnections，不修改共享上游客户端。
func DownloadClient(cfg *config.ConfigManager, timeout time.Duration) (*http.Client, error) {
	proxyURL := ""
	if cfg != nil {
		var err error
		proxyURL, err = cfg.ResolveUpstreamProxyURL(&config.UpstreamConfig{ProxyMode: config.ProxyModeInherit})
		if err != nil {
			return nil, fmt.Errorf("解析资源下载代理失败: %w", err)
		}
	}
	transport, err := newTransport(ClientOptions{ProxyURL: proxyURL, ResponseHeaderTimeout: 30 * time.Second})
	if err != nil {
		return nil, err
	}
	return &http.Client{Transport: transport, Timeout: timeout, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 8 || req.URL.Scheme != "https" || req.URL.Hostname() == "" || req.URL.User != nil {
			return fmt.Errorf("资源下载重定向必须为 HTTPS 且不超过 8 次")
		}
		return nil
	}}, nil
}
