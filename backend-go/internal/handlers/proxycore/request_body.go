package proxycore

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"

	"github.com/gin-gonic/gin"
	"github.com/insistanan/api-proxy/internal/config"
	"github.com/insistanan/api-proxy/internal/logger"
	"github.com/insistanan/api-proxy/internal/utils"
)

// ReadRequestBody 读取并验证请求体大小
// 返回: (bodyBytes, error)
// 如果请求体过大，会自动返回 413 错误并排空剩余数据
func ReadRequestBody(c *gin.Context, maxBodySize int64) ([]byte, error) {
	limitedReader := io.LimitReader(c.Request.Body, maxBodySize+1)
	bodyBytes, err := io.ReadAll(limitedReader)
	if err != nil {
		c.JSON(400, gin.H{"error": "Failed to read request body"})
		return nil, err
	}

	if int64(len(bodyBytes)) > maxBodySize {
		// 排空剩余请求体，避免 keep-alive 连接污染
		io.Copy(io.Discard, c.Request.Body)
		c.JSON(413, gin.H{"error": fmt.Sprintf("Request body too large, maximum size is %d MB", maxBodySize/1024/1024)})
		return nil, fmt.Errorf("request body too large")
	}

	// 检测入站压缩请求体（例如 Codex Desktop 在登录态发送的 zstd 压缩体）
	contentEncoding := utils.GetContentEncoding(c.Request.Header)
	if contentEncoding != "" {
		if !utils.IsSupportedContentEncoding(contentEncoding) {
			log.Printf("[Request-Decompress] 警告: 不支持的请求 Content-Encoding: %s", contentEncoding)
			c.JSON(400, gin.H{"error": fmt.Sprintf("Unsupported request content-encoding: %s", contentEncoding)})
			return nil, fmt.Errorf("unsupported request content-encoding: %s", contentEncoding)
		}

		decompressedBytes, wasDecompressed, decompressErr := utils.DecompressBodyWithLimit(contentEncoding, bodyBytes, maxBodySize)
		if decompressErr != nil {
			if errors.Is(decompressErr, utils.ErrDecompressedBodyTooLarge) {
				log.Printf("[Request-Decompress] 警告: 请求体解压后超过大小上限 (%d MB)", maxBodySize/1024/1024)
				c.JSON(413, gin.H{"error": fmt.Sprintf("Decompressed request body too large, maximum size is %d MB", maxBodySize/1024/1024)})
				return nil, decompressErr
			}
			log.Printf("[Request-Decompress] 警告: 解压请求体失败 (%s): %v", contentEncoding, decompressErr)
			c.JSON(400, gin.H{"error": fmt.Sprintf("Failed to decompress request body (%s): %v", contentEncoding, decompressErr)})
			return nil, decompressErr
		}

		if wasDecompressed {
			bodyBytes = decompressedBytes
			// 剥除失真的实体头，后续转发层基于解压后的明文 JSON 处理
			utils.StripEntityHeadersForRebuiltBody(c.Request.Header)
			c.Request.ContentLength = int64(len(bodyBytes))
		}
	}

	// 恢复请求体供后续使用
	c.Request.Body = io.NopCloser(bytes.NewReader(bodyBytes))
	return bodyBytes, nil
}

// RestoreRequestBody 恢复请求体供后续使用
func RestoreRequestBody(c *gin.Context, bodyBytes []byte) {
	c.Request.Body = io.NopCloser(bytes.NewReader(bodyBytes))
}

// LogOriginalRequest 记录原始请求信息
func LogOriginalRequest(c *gin.Context, bodyBytes []byte, envCfg *config.EnvConfig, apiType string) {
	if !envCfg.Current().EnableRequestLogs {
		return
	}

	requestID := BindRequestLogID(c)
	log.Printf("[Request-Receive] requestId=%s 收到%s请求: %s %s", requestID, apiType, c.Request.Method, c.Request.URL.Path)
	preparedBody, originalBytes, truncated := logger.PrepareBody(bodyBytes)

	if envCfg.ShouldLog("debug") {
		var formattedBody string
		if len(bodyBytes) > logger.MaxBodyBytes {
			formattedBody = preparedBody
		} else if envCfg.RawLogOutput {
			formattedBody = utils.FormatJSONBytesRaw(bodyBytes)
		} else {
			formattedBody = utils.FormatJSONBytesForLog(bodyBytes, 500)
		}
		log.Printf("[Request-OriginalBody] 原始请求体:\n%s", formattedBody)
	}

	sanitizedHeaders := make(map[string]string)
	for key, values := range c.Request.Header {
		if len(values) > 0 {
			sanitizedHeaders[key] = values[0]
		}
	}
	maskedHeaders := utils.MaskSensitiveHeaders(sanitizedHeaders)
	var headersJSON []byte
	if envCfg.RawLogOutput {
		headersJSON, _ = json.Marshal(maskedHeaders)
	} else {
		headersJSON, _ = json.MarshalIndent(maskedHeaders, "", "  ")
	}
	if envCfg.ShouldLog("debug") {
		log.Printf("[Request-OriginalHeaders] 原始请求头:\n%s", string(headersJSON))
	}

	logger.RecordTraffic(logger.TrafficLog{
		RequestID:   requestID,
		Phase:       logger.PhaseClientRequest,
		APIType:     apiType,
		Method:      c.Request.Method,
		URL:         c.Request.URL.Path,
		HeadersJSON: string(headersJSON),
	}.WithPreparedBody(preparedBody, originalBytes, truncated))
	if truncated && len(bodyBytes) > logger.MaxBodyBytes {
		structuredBody, err := logger.PrepareStructuredRequest(bodyBytes)
		if err != nil {
			log.Printf("[Request-Log] requestId=%s 提取结构化输入失败: %v", requestID, err)
			return
		}
		logger.RecordTraffic(logger.TrafficLog{
			RequestID: requestID, Phase: logger.PhaseClientInput, APIType: apiType,
		}.WithPreparedBody(structuredBody, originalBytes, false))
	}
}
