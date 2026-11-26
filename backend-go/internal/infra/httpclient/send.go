// 共享发送流程：身份头、并发准入及流式空闲超时；业务结果由调用方处理。
package httpclient

import (
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"insistanan/api-proxy/internal/infra/config"
	"insistanan/api-proxy/internal/infra/upstreamreq"
	"insistanan/api-proxy/internal/orchestration/upstreamcontrol"
	"insistanan/api-proxy/internal/storage/logger"
)

// SendOptions 描述一次上游发送的传输选项（由调用方解析后传入）。
type SendOptions struct {
	// IsStream 流式请求：不设整体超时，使用 identity 编码和空闲超时 reader。
	IsStream bool
	// Timeout 非流式请求的整体超时；零表示仅由 context 控制，IsStream 时忽略。
	Timeout time.Duration
	// ResponseHeaderTimeout 流式时必须大于零，覆盖连接、上传至收到响应头，正文另计空闲超时。
	ResponseHeaderTimeout time.Duration
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
// 不执行额外的传输重试或重定向；重试由调用层决定。
// resp.Body 的关闭责任归调用方；取消经 req 自带的 context 传播。
// 返回错误时 resp 恒为 nil（无资源泄漏窗口）。
func Send(req *http.Request, opts SendOptions) (response *http.Response, resultErr error) {
	defer func() {
		resultErr = upstreamreq.RedactError(resultErr, []string{req.URL.String(), opts.ProxyURL},
			strings.TrimPrefix(req.Header.Get("Authorization"), "Bearer "), req.Header.Get("X-Api-Key"), req.Header.Get("X-Goog-Api-Key"))
	}()
	PrepareInferenceHeaders(req, opts.IsStream)
	manager := GetManager()

	client, err := manager.GetClient(ClientOptions{
		IsStream: opts.IsStream, Timeout: opts.Timeout, ResponseHeaderTimeout: opts.ResponseHeaderTimeout,
		InsecureSkipVerify: opts.InsecureSkipVerify, ProxyURL: opts.ProxyURL,
	})
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

	resp, err := sendWithAdmission(client, req)
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
	req.Header = upstreamreq.PrepareUpstreamHeadersFromHeader(req.Header, req.URL.Host)
	upstreamreq.PrepareTargetIdentity(req.Header, candidate.Identity.Protocol, candidate.Compatibility)
	if candidate.Compatibility {
		switch candidate.Identity.Protocol {
		case "claude":
			upstreamreq.ApplyClaudeCodeDisguise(req.Header, stream)
		case "responses":
			upstreamreq.ApplyCodexDisguise(req.Header, stream)
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
	return Send(req.WithContext(ctx), SendOptions{
		Timeout: timeout, ResponseHeaderTimeout: time.Duration(cfgManager.GetGatewaySettings().ResponseHeaderTimeout) * time.Second,
		ProxyURL: proxyURL, InsecureSkipVerify: upstream.InsecureSkipVerify,
	})
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
	notifySendObserver(req.Context())
	started := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
		lease.Release()
		return nil, err
	}
	notifyResponseHeadersObserver(req.Context(), time.Since(started))
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
