package common

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
	"insistanan/api-proxy/internal/protocol/types"
	"insistanan/api-proxy/internal/safety/pipeline"
)

// WriteStream 将所有写回分支的错误标记为下游故障，包括短写。
func WriteStream(w io.Writer, flusher http.Flusher, body []byte) error {
	n, err := w.Write(body)
	if err == nil && n != len(body) {
		err = io.ErrShortWrite
	}
	if err != nil {
		return &types.DownstreamWriteError{Err: err}
	}
	if flusher != nil {
		flusher.Flush()
	}
	return nil
}

// ReportStreamFailure 只为已经开始交付的流发送一次失败事件。
// 安全错误由编排层按绑定的协议处理；未提交的响应仍可由上层决定重试。
func ReportStreamFailure(c *gin.Context, protocol string, err error) error {
	if err == nil || !c.Writer.Written() || c.Request.Context().Err() != nil ||
		types.IsDownstreamWriteError(err) || pipeline.ContentSafetyErrorFrom(err) != nil ||
		pipeline.ContentSafetyHookErrorFrom(err) != nil {
		return err
	}
	detail := map[string]interface{}{"type": "upstream_error", "message": err.Error()}
	var payload interface{}
	eventName := ""
	switch protocol {
	case "chat":
		payload = map[string]interface{}{"error": detail}
	case "responses":
		eventName = "error"
		payload = map[string]interface{}{"type": "error", "code": "upstream_error", "message": err.Error()}
	case "gemini":
		payload = map[string]interface{}{"error": map[string]interface{}{
			"code": http.StatusBadGateway, "status": "UNAVAILABLE", "message": err.Error(),
		}}
	case "images":
		eventName = "error"
		payload = map[string]interface{}{"type": "error", "error": detail}
	default:
		return errors.Join(err, fmt.Errorf("流错误通知不支持协议 %q", protocol))
	}
	body, encodeErr := json.Marshal(payload)
	if encodeErr != nil {
		return errors.Join(err, encodeErr)
	}
	event := "\ndata: " + string(body) + "\n\n"
	if eventName != "" {
		event = "\nevent: " + eventName + "\ndata: " + string(body) + "\n\n"
	}
	return errors.Join(err, WriteStream(c.Writer, c.Writer, []byte(event)))
}
