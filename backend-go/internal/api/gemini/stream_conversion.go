package gemini

import (
	"bufio"
	"encoding/json"
	"net/http"

	"github.com/gin-gonic/gin"
	"insistanan/api-proxy/internal/protocol/codec"
	"insistanan/api-proxy/internal/protocol/converters/togemini"
	"insistanan/api-proxy/internal/protocol/types"
	"insistanan/api-proxy/internal/safety/pipeline"
)

// 两种上游共用交付流程；协议状态由纯转换器管理。
func streamConvertedToGemini(c *gin.Context, resp *http.Response, flusher http.Flusher, protocol string) (*types.Usage, error) {
	converter, err := togemini.NewStream(protocol)
	if err != nil {
		return nil, err
	}
	reader := bufio.NewReader(resp.Body)
	var readErr error
	for !converter.Terminated() && readErr == nil {
		if err := pipeline.AttachedStreamVetoError(c); err != nil {
			return converter.Usage(), err
		}
		var event codec.SSEEvent
		event, readErr = codec.ReadSSEEvent(c.Request.Context(), reader)
		if event.Data == "" {
			continue
		}
		chunks, err := converter.Feed([]byte(event.Data))
		if err != nil {
			return converter.Usage(), err
		}
		for _, chunk := range chunks {
			if err := feedGeminiStreamChunk(c, &chunk); err != nil {
				return converter.Usage(), err
			}
			encoded, err := json.Marshal(chunk)
			if err != nil {
				return converter.Usage(), err
			}
			if err := writeGeminiSSE(c, flusher, encoded); err != nil {
				return converter.Usage(), err
			}
		}
	}
	if err := pipeline.AttachedStreamVetoError(c); err != nil {
		return converter.Usage(), err
	}
	if err := codec.CheckStreamCompletion(c.Request.Context(), converter.Completed(), readErr); err != nil {
		return converter.Usage(), err
	}
	final, err := converter.Final()
	if err != nil {
		return converter.Usage(), err
	}
	if err := flushGeminiRestoration(c, flusher); err != nil {
		return converter.Usage(), err
	}
	if err := pipeline.FlushAttachedStreamHooks(c); err != nil {
		return converter.Usage(), err
	}
	encoded, err := json.Marshal(final)
	if err != nil {
		return converter.Usage(), err
	}
	return converter.Usage(), writeGeminiSSE(c, flusher, encoded)
}
