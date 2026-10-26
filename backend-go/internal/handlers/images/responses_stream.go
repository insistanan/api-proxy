package images

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/BenedictKing/api-proxy/internal/handlers/proxycore"
	"github.com/BenedictKing/api-proxy/internal/types"
	"github.com/BenedictKing/api-proxy/internal/utils"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
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
	encoded, err := utils.MarshalJSONNoEscape(payload)
	if err != nil {
		return fmt.Errorf("编码 Images 流式事件失败: %w", err)
	}
	writer.start()
	if _, err := fmt.Fprintf(writer.context.Writer, "event: %s\ndata: %s\n\n", name, encoded); err != nil {
		return fmt.Errorf("写入 Images 流式事件失败: %w", err)
	}
	writer.context.Writer.Flush()
	return nil
}

func (writer *imagesStreamWriter) start() {
	if writer.started {
		return
	}
	utils.ForwardResponseHeadersEx(writer.headers, writer.context.Writer, true)
	utils.StripEntityHeadersForRebuiltBody(writer.context.Writer.Header())
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
	metadata["type"] = name
	encoded, err := utils.MarshalJSONNoEscape(metadata)
	if err != nil {
		return fmt.Errorf("编码 Images 图片事件元数据失败: %w", err)
	}
	writer.start()
	if _, err := fmt.Fprintf(writer.context.Writer, "event: %s\ndata: %s,\"b64_json\":", name, encoded[:len(encoded)-1]); err != nil {
		return err
	}
	buffer := utils.GetChunkBuffer32KB()
	defer utils.PutChunkBuffer32KB(buffer)
	for len(quotedBase64) > 0 {
		if err := writer.context.Request.Context().Err(); err != nil {
			return err
		}
		n := copy(buffer, quotedBase64)
		written, err := writer.context.Writer.Write(buffer[:n])
		if err != nil {
			return err
		}
		if written != n {
			return io.ErrShortWrite
		}
		quotedBase64 = quotedBase64[n:]
	}
	if _, err := io.WriteString(writer.context.Writer, "}\n\n"); err != nil {
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
	if writer.context.Request.Context().Err() != nil {
		return err
	}
	if !writer.started {
		return err
	}
	if writeErr := writer.event("error", map[string]interface{}{
		"type": "error", "error": map[string]string{"message": err.Error(), "code": "image_generation_failed"},
	}); writeErr != nil {
		return fmt.Errorf("%w；返回流式错误失败: %v", err, writeErr)
	}
	return err
}

// convertResponsesImageStream 参考工作台的增量处理：完成图片立即写出，仅保存
// 去重标识；response.completed 只读取元数据并补发尚未出现的图片。
// 若随后收到 failed/incomplete 或连接截断，返回 Images 错误事件。
func convertResponsesImageStream(c *gin.Context, reader io.Reader, headers http.Header, endpoint string, requestedStream bool, maximumBytes int64) (*types.Usage, error) {
	writer := newImagesStreamWriter(c, headers, endpoint)
	buffered := bufio.NewReaderSize(reader, 32*1024)
	readContext := responsesImageReadContext(c)
	seen := make(map[string]struct{})
	var pending []responsesImageItem
	var pendingBytes int64
	collect := func(item responsesImageItem, created int64) error {
		if item.Type != "image_generation_call" {
			return nil
		}
		if item.Status != "" && item.Status != "completed" {
			return fmt.Errorf("Responses 图片生成未完成，状态为 %s", item.Status)
		}
		if item.ResultJSON == "" {
			return fmt.Errorf("Responses 图片生成结果缺少图片数据")
		}
		key := imageStreamItemKey(item)
		if _, exists := seen[key]; exists {
			return nil
		}
		if requestedStream {
			if err := writer.completed(item, created); err != nil {
				return err
			}
		} else {
			pendingBytes += int64(len(item.ResultJSON))
			if pendingBytes > maximumBytes {
				return fmt.Errorf("Responses 非流式图片结果超过大小限制")
			}
			// 非流式客户端需要最终 data 数组，必须保存图片内容；只保留解码后的
			// 图片及克隆的小字段，避免同时持有整份原始 SSE 事件。
			if err := json.Unmarshal([]byte(item.ResultJSON), &item.Result); err != nil {
				return fmt.Errorf("解析 Responses 图片数据失败: %w", err)
			}
			item.ResultJSON = ""
			item.ID, item.RevisedPrompt = strings.Clone(item.ID), strings.Clone(item.RevisedPrompt)
			item.OutputFormat, item.Background = strings.Clone(item.OutputFormat), strings.Clone(item.Background)
			item.Size, item.Quality = strings.Clone(item.Size), strings.Clone(item.Quality)
			item.Usage = append(json.RawMessage(nil), item.Usage...)
			pending = append(pending, item)
		}
		seen[key] = struct{}{}
		return nil
	}
	for {
		name, raw, readErr := readResponsesImageEvent(readContext, buffered, maximumBytes)
		if readErr != nil && readErr != io.EOF {
			if len(seen) > 0 && errors.Is(readErr, context.Canceled) {
				return nil, nil
			}
			if c.Request.Context().Err() != nil {
				return nil, c.Request.Context().Err()
			}
			return nil, writer.failure(fmt.Errorf("读取 Responses 图片流失败: %w", readErr))
		}
		raw = strings.TrimSpace(raw)
		if raw != "" && raw != utils.SSEDoneMarker {
			event, err := parseResponsesImageEvent(raw, name)
			if err != nil {
				return nil, writer.failure(err)
			}
			switch event.Type {
			case "response.image_generation_call.partial_image":
				if requestedStream {
					if event.ImageJSON == "" {
						return nil, writer.failure(fmt.Errorf("Responses 预览事件缺少图片数据"))
					}
					if err := writer.imageEvent(writer.prefix+".partial_image", event.ImageJSON, map[string]interface{}{
						"partial_image_index": event.PartialIndex, "output_format": event.OutputFormat,
					}); err != nil {
						return nil, err
					}
				}
			case "response.output_item.done":
				if err := collect(imageItemFromJSON(event.Item), time.Now().Unix()); err != nil {
					return nil, writer.failure(err)
				}
			case "response.completed":
				created, usage, output, err := responsesImageStreamMetadata(event.Response)
				if err != nil {
					return nil, writer.failure(err)
				}
				if created == 0 {
					created = time.Now().Unix()
				}
				if output.Exists() && !output.IsArray() {
					return nil, writer.failure(fmt.Errorf("Responses 完成事件的 output 必须是数组"))
				}
				output.ForEach(func(_, value gjson.Result) bool {
					err = collect(imageItemFromJSON(value), created)
					return err == nil
				})
				if err != nil {
					return nil, writer.failure(err)
				}
				if len(seen) == 0 {
					return nil, writer.failure(fmt.Errorf("Responses 接口未返回任何已完成的图片生成结果"))
				}
				if !requestedStream {
					result, err := convertResponsesImageResponse(responsesImageResponse{CreatedAt: created, Status: "completed", Output: pending})
					if err != nil {
						return nil, err
					}
					utils.ForwardResponseHeadersEx(headers, c.Writer, true)
					utils.StripEntityHeadersForRebuiltBody(c.Writer.Header())
					c.JSON(http.StatusOK, result)
				}
				return usage, nil
			case "response.failed", "response.incomplete", "error":
				failure := event.Error
				if !failure.Exists() || failure.Type == gjson.Null {
					failure = event.Response.Get("error")
				}
				if failure.Exists() && failure.Type != gjson.Null {
					return nil, writer.failure(responsesImageError(json.RawMessage(failure.Raw)))
				}
				return nil, writer.failure(fmt.Errorf("Responses 图片流失败（%s）: %s", event.Type, event.Message))
			}
		}
		if readErr == io.EOF {
			return nil, writer.failure(fmt.Errorf("Responses 图片流提前结束，未收到 response.completed"))
		}
	}
}

// responsesImageReadContext 让已交付图片后的用量读取不受浏览器提前断开影响。
// 工作台解析到 image_generation.completed 就会取消 SSE；若继续使用请求 context，
// response.completed 中的 usage 会被 context.Canceled 截断，日志只能记成 0 token。
func responsesImageReadContext(c *gin.Context) context.Context {
	if c == nil || c.Request == nil {
		return context.Background()
	}
	return proxycore.DetachClientCancellation(c.Request.Context())
}
