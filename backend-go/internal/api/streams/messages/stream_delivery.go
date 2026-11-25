package messages

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/insistanan/api-proxy/internal/api/common"
	"github.com/insistanan/api-proxy/internal/infra/config"
	"github.com/insistanan/api-proxy/internal/protocol/codec"
	"github.com/insistanan/api-proxy/internal/protocol/providers"
	"github.com/insistanan/api-proxy/internal/protocol/types"
	"github.com/insistanan/api-proxy/internal/proxycore"
	"github.com/insistanan/api-proxy/internal/safety/pipeline"
)

func (ctx *Context) writeEvent(c *gin.Context, w gin.ResponseWriter, flusher http.Flusher, event string) error {
	proxycore.MarkRequestLogFirstToken(c)
	if err := common.WriteStream(w, flusher, []byte(event)); err != nil {
		ctx.ClientGone = true
		return err
	}
	ctx.trackDeliveredBlocks(event)
	return nil
}

// failStream 统一首事件、正文和读取失败的收尾，不把未完成工具调用补成可执行结果。
func (ctx *Context) failStream(c *gin.Context, w gin.ResponseWriter, flusher http.Flusher, envCfg *config.EnvConfig, err error) error {
	logPartialResponse(c, ctx, envCfg)
	if ctx.ClientGone || types.IsDownstreamWriteError(err) || c.Request.Context().Err() != nil {
		return err
	}
	if closeErr := ctx.closeDeliveredBlocks(w, flusher); closeErr != nil {
		return errors.Join(err, closeErr)
	}
	if pipeline.ContentSafetyErrorFrom(err) != nil || pipeline.ContentSafetyHookErrorFrom(err) != nil {
		return err
	}
	return errors.Join(err, ctx.writeEvent(c, w, flusher, BuildStreamErrorEvent(err)))
}

func streamEventError(event string) error {
	if !IsStreamErrorEvent(event) {
		return nil
	}
	payload := event
	for _, line := range strings.Split(event, "\n") {
		if data, ok := codec.SSEDataJSON(line); ok {
			payload = data
			break
		}
	}
	return providers.NewUpstreamStreamFailedError(proxycore.IsUpstreamModelCapacityError([]byte(payload)), payload)
}

// 无 data 的控制帧和 ping 不能单独触发 HTTP 提交；未知业务载荷仍保留。
func isStreamControlEvent(event string) bool {
	for _, line := range strings.Split(event, "\n") {
		if data, ok := codec.SSEDataJSON(line); ok {
			parsed, valid := ParseSSEEventData("data: " + data)
			return valid && parsed["type"] == "ping"
		}
	}
	return true
}
