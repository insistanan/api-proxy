package proxycore

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/BenedictKing/api-proxy/internal/config"
	"github.com/BenedictKing/api-proxy/internal/handlers/hooks"
	"github.com/BenedictKing/api-proxy/internal/httpclient"
	"github.com/BenedictKing/api-proxy/internal/logger"
	"github.com/BenedictKing/api-proxy/internal/scheduler"
	"github.com/BenedictKing/api-proxy/internal/utils"
)

// ShouldForceIdentityEncoding 判定是否应强制上游使用 identity 编码（禁止压缩）。
// 判据唯一出处已收敛到 internal/httpclient（R4-a），此处保留导出供既有调用方使用。
func ShouldForceIdentityEncoding(req *http.Request, isStream bool) bool {
	return httpclient.ShouldForceIdentityEncoding(req, isStream)
}

// SendRequest 发送 HTTP 请求到上游（R4-a：网络传输委托 internal/httpclient.Send，
// 本函数只保留业务日志与流量记录职责）。
// isStream: 是否为流式请求（流式请求使用无超时客户端）
// apiType: 接口类型（Messages/Responses/Gemini），用于日志标签前缀
// permit: 可选的选渠许可句柄（R2-b）。仅在"客户端创建失败、请求从未发出"
// 这类本地错误时由本函数中性归还；请求已发出的健康结果由调用方收尾。
func SendRequest(req *http.Request, upstream *config.UpstreamConfig, envCfg *config.EnvConfig, isStream bool, apiType string, proxyURL string, permit *scheduler.ChannelPermit, requestTimeout time.Duration) (*http.Response, error) {
	if err := req.Context().Err(); err != nil {
		if req.Body != nil {
			_ = req.Body.Close()
		}
		return nil, err
	}
	if logger.AttemptIDFromContext(req.Context()) == "" {
		req = req.WithContext(logger.ContextWithAttemptID(req.Context(), logger.NewRequestID()))
	}
	if upstream.InsecureSkipVerify && envCfg.EnableRequestLogs {
		log.Printf("[%s-Request-TLS] 警告: 正在跳过对 %s 的TLS证书验证", apiType, req.URL.String())
	}

	if envCfg.EnableRequestLogs {
		log.Printf("[%s-Request] requestId=%s attemptId=%s %s %s", apiType, logger.RequestIDFromContext(req.Context()), logger.AttemptIDFromContext(req.Context()), req.Method, req.URL.String())
		if err := logRequestDetails(req, envCfg, apiType, isStream); err != nil {
			return nil, err
		}
	}

	if requestTimeout <= 0 {
		requestTimeout = time.Duration(envCfg.RequestTimeout) * time.Millisecond
	}
	streamIdleTimeout := time.Duration(envCfg.StreamIdleTimeout) * time.Second
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
		if envCfg.EnableResponseLogs {
			logger.RecordTraffic(logger.TrafficLog{
				RequestID: logger.RequestIDFromContext(req.Context()), AttemptID: logger.AttemptIDFromContext(req.Context()),
				Phase: logger.PhaseUpstreamError, APIType: apiType, Method: req.Method, URL: req.URL.String(), Body: err.Error(), Stream: isStream,
			})
		}
		// 客户端创建失败（请求从未发出）：探测名额按中性归还，
		// 不构成渠道故障证据；网络发送失败（请求已发出）由调用方收尾。
		if strings.Contains(err.Error(), "创建上游 HTTP 客户端失败") && permit != nil {
			permit.RecordHealthNeutral()
			permit.Release()
		}
		return nil, err
	}

	if envCfg.EnableResponseLogs && resp != nil && resp.Body != nil {
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
		})
		logger.RecordTraffic(logger.TrafficLog{
			RequestID: requestID, AttemptID: logger.AttemptIDFromContext(req.Context()), Phase: logger.PhaseUpstreamHeaders,
			APIType: apiType, StatusCode: resp.StatusCode, Stream: isStream,
		})
	}

	return resp, nil
}

// logRequestDetails 记录请求头与请求体（受 ENABLE_REQUEST_LOGS / RAW_LOG_OUTPUT 控制）
// apiType: 接口类型（Messages/Responses/Gemini），用于日志标签前缀
func logRequestDetails(req *http.Request, envCfg *config.EnvConfig, apiType string, isStream bool) error {
	// 对请求头做敏感信息脱敏
	reqHeaders := make(map[string]string)
	for key, values := range req.Header {
		if len(values) > 0 {
			reqHeaders[key] = values[0]
		}
	}
	maskedReqHeaders := utils.MaskSensitiveHeaders(reqHeaders)
	var reqHeadersJSON []byte
	if envCfg.RawLogOutput {
		reqHeadersJSON, _ = json.Marshal(maskedReqHeaders)
	} else {
		reqHeadersJSON, _ = json.MarshalIndent(maskedReqHeaders, "", "  ")
	}
	if envCfg.ShouldLog("debug") {
		log.Printf("[%s-Request-Headers] 实际请求头:\n%s", apiType, string(reqHeadersJSON))
	}

	if req.Body != nil {
		var (
			bodyBytes     []byte
			preparedBody  string
			originalBytes int
			truncated     bool
		)
		if req.ContentLength > logger.MaxBodyBytes {
			// 长度已知的超大正文无需读取；日志不消费实际转发的 reader。
			originalBytes = int(req.ContentLength)
			preparedBody = logger.OmittedBody(originalBytes)
			truncated = true
		} else if prepared, ok := hooks.PreparedRequestBody(req); ok {
			bodyBytes = prepared
			preparedBody, originalBytes, truncated = logger.PrepareBody(bodyBytes)
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
			bodyBytes, err = io.ReadAll(io.LimitReader(reader, logger.MaxBodyBytes+1))
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
			if len(bodyBytes) > logger.MaxBodyBytes {
				// 只读取了前缀，不能把其长度冒充原始正文大小。
				preparedBody = logger.OmittedBody(0)
				truncated = true
			} else {
				preparedBody, originalBytes, truncated = logger.PrepareBody(bodyBytes)
			}
		}
		if envCfg.ShouldLog("debug") {
			var formattedBody string
			if truncated {
				formattedBody = preparedBody
			} else if envCfg.RawLogOutput {
				formattedBody = utils.FormatJSONBytesRaw(bodyBytes)
			} else {
				formattedBody = utils.FormatJSONBytesForLog(bodyBytes, 500)
			}
			log.Printf("[%s-Request-Body] 实际请求体:\n%s", apiType, formattedBody)
		}
		requestID := logger.RequestIDFromContext(req.Context())
		urlText := ""
		if req.URL != nil {
			urlText = req.URL.String()
		}
		logger.RecordTraffic(logger.TrafficLog{
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
