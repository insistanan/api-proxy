package streams

import (
	"errors"
	"insistanan/api-proxy/internal/infra/upstreamreq"
	"io"
	"mime"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"insistanan/api-proxy/internal/api/common"
	"insistanan/api-proxy/internal/infra/utils"
	"insistanan/api-proxy/internal/protocol/codec"
	"insistanan/api-proxy/internal/proxycore"
)

// IsEventStreamResponse 判断上游是否返回 SSE。
func IsEventStreamResponse(resp *http.Response) bool {
	if resp == nil {
		return false
	}
	mediaType, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if err != nil {
		return strings.HasPrefix(strings.ToLower(resp.Header.Get("Content-Type")), "text/event-stream")
	}
	return strings.EqualFold(mediaType, "text/event-stream")
}

// ForwardUpstreamResponseBody 逐块转发上游响应体，避免为图片 Base64 或 SSE 事件设置固定行长上限。
// 本函数负责取消绑定和关闭 resp.Body；SSE 必须使用有完成判据的 ForwardEventStream。
func ForwardUpstreamResponseBody(c *gin.Context, resp *http.Response, defaultContentType string, flush bool) error {
	if c == nil || c.Request == nil || resp == nil || resp.Body == nil {
		return errors.New("转发上游响应缺少请求或响应体")
	}

	defer codec.BindStreamBody(c.Request.Context(), resp.Body)()
	// 透传模式未在本地解压响应体，保留 Content-Encoding 等实体头供客户端自行解压
	upstreamreq.ForwardResponseHeadersEx(resp.Header, c.Writer, false)
	contentType := strings.TrimSpace(resp.Header.Get("Content-Type"))
	if contentType == "" {
		contentType = defaultContentType
	}
	if contentType != "" {
		c.Header("Content-Type", contentType)
	}

	if IsEventStreamResponse(resp) {
		c.Header("Cache-Control", "no-cache")
		c.Header("Connection", "keep-alive")
		c.Header("X-Accel-Buffering", "no")
		flush = true
	}

	c.Status(resp.StatusCode)
	var flusher http.Flusher
	if flush {
		flusher = c.Writer
	}
	buffer := utils.GetChunkBuffer32KB()
	defer utils.PutChunkBuffer32KB(buffer)
	markedFirstChunk := false

	for {
		if err := c.Request.Context().Err(); err != nil {
			return err
		}
		n, readErr := resp.Body.Read(buffer)
		if err := c.Request.Context().Err(); err != nil {
			return err
		}
		if n > 0 {
			if !markedFirstChunk {
				proxycore.MarkRequestLogFirstToken(c)
				markedFirstChunk = true
			}
			if writeErr := common.WriteStream(c.Writer, flusher, buffer[:n]); writeErr != nil {
				return writeErr
			}
		}

		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				return nil
			}
			return readErr
		}
	}
}
