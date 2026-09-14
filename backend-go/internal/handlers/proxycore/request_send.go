package proxycore

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/BenedictKing/api-proxy/internal/config"
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
func SendRequest(req *http.Request, upstream *config.UpstreamConfig, envCfg *config.EnvConfig, isStream bool, apiType string, proxyURL string, permit *scheduler.ChannelPermit) (*http.Response, error) {
	if upstream.InsecureSkipVerify && envCfg.EnableRequestLogs {
		log.Printf("[%s-Request-TLS] 警告: 正在跳过对 %s 的TLS证书验证", apiType, req.URL.String())
	}

	if envCfg.EnableRequestLogs {
		log.Printf("[%s-Request-URL] 实际请求URL: %s", apiType, req.URL.String())
		log.Printf("[%s-Request-Method] 请求方法: %s", apiType, req.Method)
		logRequestDetails(req, envCfg, apiType)
	}

	resp, err := httpclient.Send(req, httpclient.SendOptions{
		IsStream:           isStream,
		Timeout:            time.Duration(envCfg.RequestTimeout) * time.Millisecond,
		ProxyURL:           proxyURL,
		InsecureSkipVerify: upstream.InsecureSkipVerify,
		StreamIdleTimeout:  time.Duration(envCfg.StreamIdleTimeout) * time.Second,
		StreamLabel:        apiType,
	})
	if err != nil {
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
			APIType:     apiType,
			Method:      req.Method,
			URL:         urlText,
			StatusCode:  resp.StatusCode,
			Stream:      isStream,
			HeadersJSON: logger.MaskedHeaderJSON(resp.Header),
		})
	}

	return resp, nil
}

// logRequestDetails 记录请求头与请求体（受 ENABLE_REQUEST_LOGS / RAW_LOG_OUTPUT 控制）
// apiType: 接口类型（Messages/Responses/Gemini），用于日志标签前缀
func logRequestDetails(req *http.Request, envCfg *config.EnvConfig, apiType string) {
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
	log.Printf("[%s-Request-Headers] 实际请求头:\n%s", apiType, string(reqHeadersJSON))

	if req.Body != nil {
		bodyBytes, err := io.ReadAll(req.Body)
		if err == nil {
			req.Body = io.NopCloser(bytes.NewReader(bodyBytes))
			var formattedBody string
			if envCfg.RawLogOutput {
				formattedBody = utils.FormatJSONBytesRaw(bodyBytes)
			} else {
				formattedBody = utils.FormatJSONBytesForLog(bodyBytes, 500)
			}
			log.Printf("[%s-Request-Body] 实际请求体:\n%s", apiType, formattedBody)
			requestID := logger.RequestIDFromContext(req.Context())
			urlText := ""
			if req.URL != nil {
				urlText = req.URL.String()
			}
			logger.RecordTraffic(logger.TrafficLog{
				RequestID:   requestID,
				Phase:       logger.PhaseUpstreamRequest,
				APIType:     apiType,
				Method:      req.Method,
				URL:         urlText,
				HeadersJSON: string(reqHeadersJSON),
				Body:        string(bodyBytes),
			})
		}
	}
}
