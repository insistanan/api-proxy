package streams

import (
	"bufio"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"insistanan/api-proxy/internal/api/common"
	"insistanan/api-proxy/internal/infra/upstreamreq"
	"insistanan/api-proxy/internal/protocol/codec"
	"insistanan/api-proxy/internal/proxycore"
)

// ForwardEventStream 统一透传流的读取、取消、写回和失败通知。
// observe 由入口提供协议完成判据；只有终止事件实际交付后才能返回成功。
// 每次只暂存一个完整事件，避免把畸形或错误事件当作成功正文写出。
func ForwardEventStream(c *gin.Context, resp *http.Response, protocol string, observe func(codec.SSEEvent) (bool, error)) (resultErr error) {
	defer func() { resultErr = common.ReportStreamFailure(c, protocol, resultErr) }()
	defer codec.BindStreamBody(c.Request.Context(), resp.Body)()
	reader, _, err := codec.WrapStreamReaderIfNeeded(resp)
	if err != nil {
		if cancelErr := c.Request.Context().Err(); cancelErr != nil {
			return cancelErr
		}
		return fmt.Errorf("解压上游流失败: %w", err)
	}
	defer reader.Close()
	upstreamreq.ForwardResponseHeadersEx(resp.Header, c.Writer, true)
	upstreamreq.StripEntityHeadersForRebuiltBody(c.Writer.Header())
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("X-Accel-Buffering", "no")
	c.Status(resp.StatusCode)
	buffered := bufio.NewReaderSize(reader, 32*1024)
	for {
		event, readErr := codec.ReadSSEEvent(c.Request.Context(), buffered)
		if err := c.Request.Context().Err(); err != nil {
			return err
		}
		if event.Data != "" || event.Name != "" {
			completed, err := observe(event)
			if err != nil {
				return errors.Join(err, readErr)
			}
			if event.Data != "" {
				// 无末尾空行的最终事件需要补分隔符，否则客户端不会分派。
				wire := event.Raw
				if !strings.HasSuffix(wire, "\n\n") && !strings.HasSuffix(wire, "\r\n\r\n") {
					wire = strings.TrimRight(wire, "\r\n") + "\n\n"
				}
				proxycore.MarkRequestLogFirstToken(c)
				if err := common.WriteStream(c.Writer, c.Writer, []byte(wire)); err != nil {
					return err
				}
				if completed {
					return nil
				}
			}
		}
		if readErr != nil {
			return codec.CheckStreamCompletion(c.Request.Context(), false, readErr)
		}
	}
}
