package images

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/insistanan/api-proxy/internal/api/common"
	"github.com/insistanan/api-proxy/internal/infra/upstreamreq"
	"github.com/insistanan/api-proxy/internal/infra/utils"
	"github.com/insistanan/api-proxy/internal/protocol/codec"
	"github.com/insistanan/api-proxy/internal/protocol/types"
	"github.com/insistanan/api-proxy/internal/proxycore"
)

type imagesStreamWriter struct {
	context *gin.Context
	headers http.Header
	prefix  string
	started bool
}

func newImagesStreamWriter(c *gin.Context, headers http.Header, endpoint string) *imagesStreamWriter {
	prefix := "image_generation"
	if endpoint == "/images/edits" {
		prefix = "image_edit"
	}
	return &imagesStreamWriter{context: c, headers: headers, prefix: prefix}
}

func (writer *imagesStreamWriter) event(name string, payload interface{}) error {
	encoded, err := codec.MarshalJSONNoEscape(payload)
	if err != nil {
		return fmt.Errorf("编码 Images 流式事件失败: %w", err)
	}
	writer.start()
	if err := common.WriteStream(writer.context.Writer, writer.context.Writer, []byte(fmt.Sprintf("event: %s\ndata: %s\n\n", name, encoded))); err != nil {
		return fmt.Errorf("写入 Images 流式事件失败: %w", err)
	}
	return nil
}

func (writer *imagesStreamWriter) start() {
	if writer.started {
		return
	}
	upstreamreq.ForwardResponseHeadersEx(writer.headers, writer.context.Writer, true)
	upstreamreq.StripEntityHeadersForRebuiltBody(writer.context.Writer.Header())
	writer.context.Header("Content-Type", "text/event-stream")
	writer.context.Header("Cache-Control", "no-cache")
	writer.context.Header("X-Accel-Buffering", "no")
	writer.context.Status(http.StatusOK)
	writer.started = true
	proxycore.MarkRequestLogFirstToken(writer.context)
}

// imageEvent 分块写出已验证的原始 JSON 字符串，不把大 base64 再解码、转义、
// 编码或拼接成第二份完整事件。同步写入保留背压，慢客户端不会堆积图片事件。
func (writer *imagesStreamWriter) imageEvent(name, quotedBase64 string, metadata map[string]interface{}) error {
	if err := writer.context.Request.Context().Err(); err != nil {
		return err
	}
	metadata["type"] = name
	encoded, err := codec.MarshalJSONNoEscape(metadata)
	if err != nil {
		return fmt.Errorf("编码 Images 图片事件元数据失败: %w", err)
	}
	writer.start()
	if err := common.WriteStream(writer.context.Writer, nil, []byte(fmt.Sprintf("event: %s\ndata: %s,\"b64_json\":", name, encoded[:len(encoded)-1]))); err != nil {
		return err
	}
	buffer := utils.GetChunkBuffer32KB()
	defer utils.PutChunkBuffer32KB(buffer)
	for len(quotedBase64) > 0 {
		if err := writer.context.Request.Context().Err(); err != nil {
			return err
		}
		n := copy(buffer, quotedBase64)
		if err := common.WriteStream(writer.context.Writer, nil, buffer[:n]); err != nil {
			return err
		}

		quotedBase64 = quotedBase64[n:]
	}
	writeTail := func() error { return common.WriteStream(writer.context.Writer, nil, []byte("}\n\n")) }
	finish := writeTail
	if name == writer.prefix+".completed" {
		if value, exists := writer.context.Get(responsesImageLifecycleKey); exists {
			lifecycle := value.(*responsesImageLifecycle)
			finish = func() error { return lifecycle.finishDelivery(writeTail) }
		}
	}
	if err := finish(); err != nil {
		return err
	}
	writer.context.Writer.Flush()
	return nil
}

func (writer *imagesStreamWriter) completed(item responsesImageItem, created int64) error {
	name := writer.prefix + ".completed"
	payload := map[string]interface{}{"created_at": created}
	for key, value := range map[string]string{
		"output_format": item.OutputFormat, "background": item.Background,
		"size": item.Size, "quality": item.Quality, "revised_prompt": item.RevisedPrompt,
	} {
		if value != "" {
			payload[key] = value
		}
	}
	if len(item.Usage) > 0 {
		payload["usage"] = item.Usage
	}
	quoted := item.ResultJSON
	if quoted == "" {
		encoded, err := json.Marshal(item.Result)
		if err != nil {
			return err
		}
		quoted = string(encoded)
	}
	return writer.imageEvent(name, quoted, payload)
}

func (writer *imagesStreamWriter) failure(err error) error {
	if writer.context.Request.Context().Err() != nil || types.IsDownstreamWriteError(err) {
		return err
	}
	if !writer.started {
		return err
	}
	if writeErr := writer.event("error", map[string]interface{}{
		"type": "error", "error": map[string]string{"message": err.Error(), "code": "image_generation_failed"},
	}); writeErr != nil {
		return errors.Join(err, writeErr)
	}
	return err
}
