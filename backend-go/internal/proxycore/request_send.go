package proxycore

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/insistanan/api-proxy/internal/protocol/codec"
	"github.com/insistanan/api-proxy/internal/storage/logbody"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/insistanan/api-proxy/internal/infra/config"
	"github.com/insistanan/api-proxy/internal/infra/httpclient"
	"github.com/insistanan/api-proxy/internal/orchestration/scheduler"
	"github.com/insistanan/api-proxy/internal/safety/pipeline"
	"github.com/insistanan/api-proxy/internal/storage/logger"
)

// ShouldForceIdentityEncoding 判定是否应强制上游使用 identity 编码（禁止压缩）。
// 判据唯一出处已收敛到 internal/infra/httpclient（R4-a），此处保留导出供既有调用方使用。
func ShouldForceIdentityEncoding(req *http.Request, isStream bool) bool {
	return httpclient.ShouldForceIdentityEncoding(req, isStream)
}

// SendRequest 将请求交给 internal/infra/httpclient.Send 发送，并记录业务日志和请求流量（R4-a）。
// 流式请求使用不设整体超时的客户端，通过 context 处理取消，通过 body reader 检查空闲超时。
// apiType 用于标记入口协议的日志；共享准入则使用请求上下文中绑定的实际上游协议。
// permit: 可选的选渠许可句柄（R2-b）。仅在"客户端创建失败、请求从未发出"
// 这类本地错误时由本函数中性归还；请求已发出的健康结果由调用方收尾。
func SendRequest(req *http.Request, upstream *config.UpstreamConfig, envCfg *config.EnvConfig, isStream bool, apiType string, proxyURL string, permit *scheduler.ChannelPermit, requestTimeout time.Duration) (*http.Response, error) {
	httpclient.PrepareInferenceHeaders(req, isStream)
	if err := req.Context().Err(); err != nil {
		if req.Body != nil {
			_ = req.Body.Close()
		}
		return nil, err
	}
	if logger.AttemptIDFromContext(req.Context()) == "" {
		req = req.WithContext(logger.ContextWithAttemptID(req.Context(), logger.NewRequestID()))
	}
	policy := capturePolicy(req.Context(), envCfg)
	req = req.WithContext(logger.ContextWithCapturePolicy(req.Context(), policy))
	if upstream.InsecureSkipVerify && policy.Request {
		log.Printf("[%s-Request-TLS] 警告: 正在跳过对 %s 的TLS证书验证", apiType, req.URL.String())
	}

	log.Printf("[%s-Request] requestId=%s attemptId=%s %s %s", apiType, logger.RequestIDFromContext(req.Context()), logger.AttemptIDFromContext(req.Context()), req.Method, req.URL.String())
	if err := logRequestDetails(req, envCfg, apiType, isStream); err != nil {
		return nil, err
	}

	if requestTimeout <= 0 {
		requestTimeout = time.Duration(envCfg.Current().RequestTimeout) * time.Millisecond
	}
	streamIdleTimeout := time.Duration(envCfg.Current().StreamIdleTimeout) * time.Second
	if isStream && requestTimeout > streamIdleTimeout {
		// 生图流式请求使用同一套请求超时策略，避免 partial_images 间隔超过
		// 默认 5 分钟时被空闲 reader 提前中止。
		streamIdleTimeout = requestTimeout
	}
	resp, err := httpclient.Send(req, httpclient.SendOptions{
		IsStream:           isStream,
		Timeout:            requestTimeout,
		ProxyURL:           proxyURL,
		InsecureSkipVerify: upstream.InsecureSkipVerify,
		StreamIdleTimeout:  streamIdleTimeout,
		StreamLabel:        apiType,
	})
	if err != nil {
		logger.RecordTrafficContext(req.Context(), logger.TrafficLog{
			RequestID: logger.RequestIDFromContext(req.Context()), AttemptID: logger.AttemptIDFromContext(req.Context()),
			Phase: logger.PhaseUpstreamError, APIType: apiType, Method: req.Method, URL: req.URL.String(), Body: err.Error(), Stream: isStream,
		})
		// 客户端创建失败（请求从未发出）：探测名额按中性归还，
		// 不构成渠道故障证据；网络发送失败（请求已发出）由调用方收尾。
		if strings.Contains(err.Error(), "创建上游 HTTP 客户端失败") && permit != nil {
			permit.RecordHealthNeutral()
			permit.Release()
		}
		return nil, err
	}

	if resp != nil && resp.Body != nil {
		requestID := logger.RequestIDFromContext(req.Context())
		if requestID == "" {
			requestID = logger.NewRequestID()
		}
		urlText := ""
		if req.URL != nil {
			urlText = req.URL.String()
		}
		resp.Body = logger.CaptureResponseBody(resp.Body, logger.TrafficLog{
			RequestID:   requestID,
			AttemptID:   logger.AttemptIDFromContext(req.Context()),
			APIType:     apiType,
			Method:      req.Method,
			URL:         urlText,
			StatusCode:  resp.StatusCode,
			Stream:      isStream,
			HeadersJSON: logger.MaskedHeaderJSON(resp.Header),
		}.WithCapturePolicy(policy))
		logger.RecordTrafficContext(req.Context(), logger.TrafficLog{
			RequestID: requestID, AttemptID: logger.AttemptIDFromContext(req.Context()), Phase: logger.PhaseUpstreamHeaders,
			APIType: apiType, StatusCode: resp.StatusCode, Stream: isStream,
		})
	}

	return resp, nil
}

// logRequestDetails 记录准备发送给上游的请求。鉴权头会做掩码处理，
// 正文中的脱敏占位符会移除映射 ID，入库时还会省略图片和超大内容。
// 敏感数据检测由内容安全管线负责；调用方根据 settings.gateway 中的开关启用日志。
// 运行日志只输出定位信息，避免正文副本绕过结构化日志的保留期。
func logRequestDetails(req *http.Request, envCfg *config.EnvConfig, apiType string, isStream bool) error {
	policy := capturePolicy(req.Context(), envCfg)
	if policy.BodyState(false) != "" {
		logger.RecordTrafficContext(req.Context(), logger.TrafficLog{RequestID: logger.RequestIDFromContext(req.Context()), AttemptID: logger.AttemptIDFromContext(req.Context()), Phase: logger.PhaseUpstreamRequest, APIType: apiType, Method: req.Method, URL: req.URL.String(), Stream: isStream, OriginalBytes: int(max(0, req.ContentLength))})
		return nil
	}
	// 对请求头做敏感信息脱敏
	reqHeaders := make(map[string]string)
	for key, values := range req.Header {
		if len(values) > 0 {
			reqHeaders[key] = values[0]
		}
	}
	maskedReqHeaders := codec.MaskSensitiveHeaders(reqHeaders)
	var reqHeadersJSON []byte
	if envCfg.RawLogOutput {
		reqHeadersJSON, _ = json.Marshal(maskedReqHeaders)
	} else {
		reqHeadersJSON, _ = json.MarshalIndent(maskedReqHeaders, "", "  ")
	}
	if envCfg.ShouldLog("debug") {
		log.Printf("[%s-Request-Headers] requestId=%s attemptId=%s 请求头已掩码保存到结构化日志", apiType, logger.RequestIDFromContext(req.Context()), logger.AttemptIDFromContext(req.Context()))
	}

	if req.Body != nil {
		var (
			bodyBytes     []byte
			preparedBody  string
			originalBytes int
			truncated     bool
		)
		if req.ContentLength > int64(policy.BodyLimit) {
			// 长度已知的超大正文无需读取；日志不消费实际转发的 reader。
			originalBytes = int(req.ContentLength)
			preparedBody = logbody.OmittedBodyWithLimit(originalBytes, policy.BodyLimit)
			truncated = true
		} else if prepared, ok := pipeline.PreparedRequestBody(req); ok {
			bodyBytes = prepared
			preparedBody, originalBytes, truncated = logbody.PrepareBodyWithLimit(bodyBytes, policy.BodyLimit)
		} else {
			reader := req.Body
			if req.GetBody != nil {
				copyReader, err := req.GetBody()
				if err != nil {
					_ = req.Body.Close()
					return fmt.Errorf("获取上游请求日志正文失败: %w", err)
				}
				if copyReader == nil {
					_ = req.Body.Close()
					return fmt.Errorf("获取上游请求日志正文失败: GetBody 返回空 reader")
				}
				reader = copyReader
			}
			var err error
			bodyBytes, err = io.ReadAll(io.LimitReader(reader, int64(policy.BodyLimit)+1))
			if req.GetBody != nil {
				_ = reader.Close()
			} else {
				// 未提供 GetBody 时回放已消费的前缀，随后继续读原正文；关闭责任不变。
				req.Body = &prefixReadCloser{Reader: io.MultiReader(bytes.NewReader(bodyBytes), reader), Closer: reader}
			}
			if err != nil {
				_ = req.Body.Close()
				return fmt.Errorf("读取上游请求正文失败: %w", err)
			}
			if len(bodyBytes) > policy.BodyLimit {
				// 只读取了前缀，不能把其长度冒充原始正文大小。
				preparedBody = logbody.OmittedBodyWithLimit(0, policy.BodyLimit)
				truncated = true
			} else {
				preparedBody, originalBytes, truncated = logbody.PrepareBodyWithLimit(bodyBytes, policy.BodyLimit)
			}
		}
		if envCfg.ShouldLog("debug") {
			log.Printf("[%s-Request-Body] requestId=%s attemptId=%s 原始正文 %d 字节，截断=%t；正文见结构化日志", apiType, logger.RequestIDFromContext(req.Context()), logger.AttemptIDFromContext(req.Context()), originalBytes, truncated)
		}
		requestID := logger.RequestIDFromContext(req.Context())
		urlText := ""
		if req.URL != nil {
			urlText = req.URL.String()
		}
		logger.RecordTrafficContext(req.Context(), logger.TrafficLog{
			RequestID:   requestID,
			AttemptID:   logger.AttemptIDFromContext(req.Context()),
			Stream:      isStream,
			Phase:       logger.PhaseUpstreamRequest,
			APIType:     apiType,
			Method:      req.Method,
			URL:         urlText,
			HeadersJSON: string(reqHeadersJSON),
		}.WithPreparedBody(preparedBody, originalBytes, truncated))
	}
	return nil
}
