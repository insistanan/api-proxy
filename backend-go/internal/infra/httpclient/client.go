package httpclient

import (
	"container/list"
	"crypto/sha256"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/insistanan/api-proxy/internal/infra/config"
)

const maxCachedClients = 128

// ClientOptions 固定一次客户端选择的传输策略；调用方从自己的配置快照传入。
type ClientOptions struct {
	IsStream              bool
	Timeout               time.Duration
	ResponseHeaderTimeout time.Duration
	InsecureSkipVerify    bool
	ProxyURL              string
}

type clientKey struct {
	stream        bool
	timeout       time.Duration
	headerTimeout time.Duration
	insecure      bool
	proxyDigest   [32]byte
}

type cachedClient struct {
	key    clientKey
	client *http.Client
}

// ClientManager 有界复用客户端。淘汰仅关闭空闲连接，已取得客户端的请求可以继续完成。
// 返回的客户端及 Transport 由管理器共享，调用方不得修改。
type ClientManager struct {
	mu      sync.Mutex
	clients map[clientKey]*list.Element
	recent  list.List
}

func NewClientManager() *ClientManager {
	return &ClientManager{clients: make(map[clientKey]*list.Element)}
}

var globalManager = NewClientManager()

func GetManager() *ClientManager {
	return globalManager
}

// GetClient 统一普通与流式客户端的校验、构造和缓存，始终将重定向作为响应返回。
func (cm *ClientManager) GetClient(opts ClientOptions) (*http.Client, error) {
	if opts.ResponseHeaderTimeout < 0 || opts.IsStream && opts.ResponseHeaderTimeout == 0 {
		return nil, fmt.Errorf("上游响应头超时不能为负，流式请求必须大于零")
	}
	if opts.Timeout < 0 {
		return nil, fmt.Errorf("上游请求整体超时不能为负")
	}
	if opts.IsStream {
		opts.Timeout = 0 // 流式正文只受请求取消和空闲超时约束。
	}
	opts.ProxyURL = strings.TrimSpace(opts.ProxyURL)
	key := clientKey{
		stream: opts.IsStream, timeout: opts.Timeout, headerTimeout: opts.ResponseHeaderTimeout,
		insecure: opts.InsecureSkipVerify, proxyDigest: sha256.Sum256([]byte(opts.ProxyURL)),
	}

	cm.mu.Lock()
	defer cm.mu.Unlock()
	if element, ok := cm.clients[key]; ok {
		cm.recent.MoveToFront(element)
		return element.Value.(cachedClient).client, nil
	}
	transport, err := newTransport(opts)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Transport: transport, Timeout: opts.Timeout, CheckRedirect: stopRedirect}
	cm.clients[key] = cm.recent.PushFront(cachedClient{key: key, client: client})
	if cm.recent.Len() > maxCachedClients {
		oldest := cm.recent.Back()
		entry := oldest.Value.(cachedClient)
		delete(cm.clients, entry.key)
		cm.recent.Remove(oldest)
		entry.client.CloseIdleConnections()
	}
	return client, nil
}

// CloseIdleConnections 清空缓存并释放空闲连接；装配层在请求和后台任务结束后调用。
func (cm *ClientManager) CloseIdleConnections() {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	for _, element := range cm.clients {
		element.Value.(cachedClient).client.CloseIdleConnections()
	}
	clear(cm.clients)
	cm.recent.Init()
}

func stopRedirect(*http.Request, []*http.Request) error {
	// 上游地址、凭据和准入身份必须保持一致；显式返回 3xx 交给调用方处理。
	return http.ErrUseLastResponse
}

// GetStandardClientForUpstream 供模型查询等管理请求复用渠道代理与 TLS 策略。
func (cm *ClientManager) GetStandardClientForUpstream(timeout time.Duration, cfgManager *config.ConfigManager, upstream *config.UpstreamConfig) (*http.Client, error) {
	if cfgManager == nil || upstream == nil {
		return nil, fmt.Errorf("创建上游 HTTP 客户端时缺少配置")
	}
	proxyURL, err := cfgManager.ResolveUpstreamProxyURL(upstream)
	if err != nil {
		return nil, err
	}
	return cm.GetClient(ClientOptions{
		Timeout: timeout, ResponseHeaderTimeout: time.Duration(cfgManager.GetGatewaySettings().ResponseHeaderTimeout) * time.Second,
		InsecureSkipVerify: upstream.InsecureSkipVerify, ProxyURL: proxyURL,
	})
}
