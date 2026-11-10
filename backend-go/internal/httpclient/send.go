// send.go 提供推理请求的共享发送实现（R4-a）：
// 选择客户端与身份头 → 共享准入与发送 → 流式响应包装空闲超时 reader。
//
// 业务日志（请求/响应流量记录）与结果分类由调用层负责；本文件只做
// 与 HTTP 传输相关的事，不引入协议分派，也不成为新的杂物包。
package httpclient

import (
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/insistanan/api-proxy/internal/config"
	"github.com/insistanan/api-proxy/internal/logger"
	"github.com/insistanan/api-proxy/internal/upstreamcontrol"
	"github.com/insistanan/api-proxy/internal/utils"
)

// SendOptions 描述一次上游发送的传输选项（由调用方解析后传入）。
type SendOptions struct {
	// IsStream 流式请求：使用无超时客户端 + identity 编码 + 空闲超时 reader。
	IsStream bool
	// Timeout 非流式请求的整体超时；IsStream 时忽略。
	Timeout time.Duration
	// ProxyURL 代理地址，空为直连。
	ProxyURL string
	// InsecureSkipVerify 跳过上游 TLS 证书验证。
	InsecureSkipVerify bool
	// StreamIdleTimeout 流式响应的空闲超时（IsStream 时生效）。
	StreamIdleTimeout time.Duration
	// StreamLabel 流式空闲超时日志的协议前缀（如 "Messages"）。
	StreamLabel string
}

// ShouldForceIdentityEncoding 判定是否应强制上游使用 identity 编码（禁止压缩）。
// 对流式响应（SSE）、流式端点（如 Gemini streamGenerateContent / alt=sse）
// 或指定 text/event-stream 的请求，必须强制 identity，防止上游压缩 SSE 流
// 导致分块按行解析失败。
func ShouldForceIdentityEncoding(req *http.Request, isStream bool) bool {
	if isStream {
		return true
	}
	if req == nil || req.URL == nil {
		return false
	}
	if strings.Contains(req.URL.Path, "streamGenerateContent") || strings.Contains(req.URL.RawQuery, "alt=sse") {
		return true
	}
	acceptHeader := strings.ToLower(req.Header.Get("Accept"))
	return strings.Contains(acceptHeader, "text/event-stream")
}

// Send 发送上游推理请求，复用客户端并遵守共享准入策略。
// 请求体重放（Body 为 *bytes.Reader/bytes.Buffer 时可重试）由调用方负责
// 构造；resp.Body 的关闭责任归调用方；取消经 req 自带的 context 传播。
// 返回错误时 resp 恒为 nil（无资源泄漏窗口）。
func Send(req *http.Request, opts SendOptions) (*http.Response, error) {
	PrepareInferenceHeaders(req, opts.IsStream)
	manager := GetManager()

	var client *http.Client
	var err error
	if opts.IsStream {
		client, err = manager.GetStreamClient(opts.InsecureSkipVerify, opts.ProxyURL)
	} else {
		client, err = manager.GetStandardClient(opts.Timeout, opts.InsecureSkipVerify, opts.ProxyURL)
	}
	if err != nil {
		if req.Body != nil {
			_ = req.Body.Close()
		}
		return nil, fmt.Errorf("创建上游 HTTP 客户端失败: %w", err)
	}

	// 流式请求或 SSE 端点强制 Accept-Encoding: identity，防止上游压缩 SSE 流；
	// 普通非流式请求保持清空，由 Go Transport 自动添加 gzip 并处理。
	if ShouldForceIdentityEncoding(req, opts.IsStream) {
		req.Header.Set("Accept-Encoding", "identity")
	}

	resp, err := doWithTLSHandshakeRetry(client, req)
	if err != nil {
		return nil, err
	}

	// 流式响应：包装 body 为带空闲超时的 reader，检测"TCP 通但流中挂起"的上游。
	if opts.IsStream && resp != nil && resp.Body != nil {
		resp.Body = NewIdleTimeoutReader(resp.Body, opts.StreamIdleTimeout, opts.StreamLabel)
	}
	return resp, nil
}

// PrepareInferenceHeaders 按实际上游协议处理身份；只对显式绑定的推理请求生效。
func PrepareInferenceHeaders(req *http.Request, stream bool) {
	candidate, bound := upstreamcontrol.CandidateFromContext(req.Context())
	if !bound {
		return
	}
	req.Header = utils.PrepareUpstreamHeadersFromHeader(req.Header, req.URL.Host)
	utils.PrepareTargetIdentity(req.Header, candidate.Identity.Protocol, candidate.Compatibility)
	if candidate.Compatibility {
		switch candidate.Identity.Protocol {
		case "claude":
			utils.ApplyClaudeCodeDisguise(req.Header, stream)
		case "responses":
			utils.ApplyCodexDisguise(req.Header, stream)
		}
	}
	// 下游留下的其他认证头一律剥除；已有上游认证按目标协议保留。
	switch candidate.Identity.Protocol {
	case "gemini":
		req.Header.Del("Authorization")
		req.Header.Del("X-Api-Key")
	default:
		req.Header.Del("X-Goog-Api-Key")
		if candidate.Identity.Protocol != "claude" && candidate.Identity.Protocol != "" {
			if req.Header.Get("Authorization") == "" && req.Header.Get("X-Api-Key") != "" {
				req.Header.Set("Authorization", "Bearer "+req.Header.Get("X-Api-Key"))
			}
			req.Header.Del("X-Api-Key")
		}
	}
}

// SendForUpstream 是辅助推理的入口；模型目录等管理查询仍直接使用自己的客户端。
func SendForUpstream(req *http.Request, timeout time.Duration, cfgManager *config.ConfigManager, upstream *config.UpstreamConfig, apiKey string) (*http.Response, error) {
	proxyURL, err := cfgManager.ResolveUpstreamProxyURL(upstream)
	if err != nil {
		if req.Body != nil {
			_ = req.Body.Close()
		}
		return nil, err
	}
	ctx := cfgManager.UpstreamRequestContext(upstreamcontrol.WithScope(req.Context()), upstream, apiKey)
	return Send(req.WithContext(ctx), SendOptions{Timeout: timeout, ProxyURL: proxyURL, InsecureSkipVerify: upstream.InsecureSkipVerify})
}

// TLS 握手未完成时 HTTP 请求尚未发送；可重放请求体时最多重试一次，
// 缓解上游首次握手抖动，同时不重发已被上游接收的图片生成请求。
func doWithTLSHandshakeRetry(client *http.Client, req *http.Request) (*http.Response, error) {
	resp, err := sendWithAdmission(client, req)
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "tls handshake timeout") {
		return resp, err
	}
	if req.Body != nil && req.GetBody == nil {
		return nil, err
	}

	retryRequest := req.Clone(req.Context())
	if req.GetBody != nil {
		body, bodyErr := req.GetBody()
		if bodyErr != nil {
			return nil, fmt.Errorf("重建 TLS 握手重试请求体失败: %w", bodyErr)
		}
		retryRequest.Body = body
	}
	return sendWithAdmission(client, retryRequest)
}

func sendWithAdmission(client *http.Client, req *http.Request) (*http.Response, error) {
	lease, waited, attempts, err := upstreamcontrol.AcquireRequest(req.Context())
	if attempts > 0 || err != nil {
		log.Printf("[Upstream-Admission] requestId=%s attemptId=%s 等待=%dms 上游发送次数=%d 准入结果=%v", logger.RequestIDFromContext(req.Context()), logger.AttemptIDFromContext(req.Context()), waited.Milliseconds(), attempts, err)
	}
	if err == nil {
		err = req.Context().Err()
	}
	if requestID := logger.RequestIDFromContext(req.Context()); requestID != "" && (attempts > 0 || err != nil) {
		result := "已取得执行名额"
		if err != nil {
			result = err.Error()
		}
		logger.RecordTraffic(logger.TrafficLog{
			RequestID: requestID, AttemptID: logger.AttemptIDFromContext(req.Context()),
			Phase: logger.PhaseUpstreamAdmission,
			Body:  fmt.Sprintf("本次排队 %d 毫秒，累计上游发送 %d 次。%s", waited.Milliseconds(), attempts, result),
		})
	}
	if err != nil {
		lease.Release()
		if req.Body != nil {
			_ = req.Body.Close()
		}
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
		lease.Release()
		return nil, err
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		upstreamcontrol.Record429(req.Context(), resp.Header)
	}
	if lease != nil && resp.Body != nil {
		resp.Body = newAdmissionBody(req.Context(), resp.Body, lease)
	} else {
		lease.Release()
	}
	return resp, nil
}
