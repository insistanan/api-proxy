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

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"insistanan/api-proxy/internal/protocol/codec"
	"insistanan/api-proxy/internal/protocol/converters/toimages"
	"insistanan/api-proxy/internal/protocol/types"
)

// convertResponsesImageStream 即时转发预览；最终图片按 ID 合并，
// 等 response.completed 提供最终元数据后交付一次。
// 若随后收到 failed/incomplete 或连接截断，返回 Images 错误事件。
func convertResponsesImageStream(c *gin.Context, reader io.Reader, headers http.Header, endpoint string, requestedStream bool, maximumBytes int64) (*types.Usage, error) {
	lifecycle := responsesLifecycle(c)
	defer lifecycle.close()
	if body, ok := reader.(io.ReadCloser); ok {
		defer codec.BindStreamBody(lifecycle.ctx, body)()
	}
	writer := newImagesStreamWriter(c, headers, endpoint)
	buffered := bufio.NewReaderSize(reader, 32*1024)
	readContext := lifecycle.ctx
	seen := make(map[string]int)
	var pending []toimages.ImageItem
	var pendingBytes int64
	collect := func(item toimages.ImageItem, created int64) error {
		if item.Type != "image_generation_call" {
			return nil
		}
		if item.Status != "" && item.Status != "completed" {
			return fmt.Errorf("Responses 图片生成未完成，状态为 %s", item.Status)
		}
		key := imageStreamItemKey(item)
		if index, exists := seen[key]; exists {
			item = toimages.MergeFinalItem(pending[index], item)
			pendingBytes -= int64(len(pending[index].ResultJSON))
			pendingBytes += int64(len(item.ResultJSON))
			pending[index] = item
		} else {
			seen[key] = len(pending)
			pendingBytes += int64(len(item.ResultJSON))
			pending = append(pending, item)
		}
		if pendingBytes > maximumBytes {
			return fmt.Errorf("Responses 图片结果超过大小限制")
		}

		return nil
	}
	for {
		name, raw, readErr := readResponsesImageEvent(readContext, buffered, maximumBytes)
		if cause := context.Cause(readContext); cause != nil {
			return nil, writer.failure(fmt.Errorf("读取 Responses 图片流中止: %w", cause))
		}
		raw = strings.TrimSpace(raw)
		if raw != "" && raw != codec.SSEDoneMarker {
			event, err := parseResponsesImageEvent(raw, name)
			if err != nil {
				return nil, writer.failure(errors.Join(err, readErr))
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
					return usage, writer.failure(err)
				}
				if created == 0 {
					created = time.Now().Unix()
				}
				if output.Exists() && !output.IsArray() {
					return usage, writer.failure(fmt.Errorf("Responses 完成事件的 output 必须是数组"))
				}
				output.ForEach(func(_, value gjson.Result) bool {
					err = collect(imageItemFromJSON(value), created)
					return err == nil
				})
				if err != nil {
					return usage, writer.failure(err)
				}
				if len(seen) == 0 {
					return usage, writer.failure(fmt.Errorf("Responses 接口未返回任何已完成的图片生成结果"))
				}
				for i := range pending {
					if pending[i].ResultJSON == "" {
						return usage, writer.failure(fmt.Errorf("Responses 图片生成结果缺少图片数据"))
					}
					if requestedStream {
						if err := writer.completed(pending[i], created); err != nil {
							return usage, err
						}
					} else if err := json.Unmarshal([]byte(pending[i].ResultJSON), &pending[i].Result); err != nil {
						return usage, err
					}
				}
				if !requestedStream {
					result, err := toimages.ConvertResponse(toimages.ResponsesResponse{CreatedAt: created, Status: "completed", Output: pending})
					if err != nil {
						return usage, err
					}
					if err := writeImagesJSON(c, headers, result); err != nil {
						return usage, err
					}
				}
				return usage, nil
			case "response.failed", "response.incomplete", "error":
				usage, usageErr := responsesImageUsage(json.RawMessage(event.Response.Get("usage").Raw))
				if usageErr != nil {
					usageErr = fmt.Errorf("解析 Responses 图片用量失败: %w", usageErr)
				}
				failure := event.Error
				if !failure.Exists() || failure.Type == gjson.Null {
					failure = event.Response.Get("error")
				}
				if failure.Exists() && failure.Type != gjson.Null {
					return usage, writer.failure(errors.Join(toimages.ResponseError(json.RawMessage(failure.Raw)), usageErr))
				}
				return usage, writer.failure(errors.Join(fmt.Errorf("Responses 图片流失败（%s）: %s", event.Type, event.Message), usageErr))
			}
		}
		if readErr == io.EOF {
			return nil, writer.failure(fmt.Errorf("Responses 图片流提前结束，未收到 response.completed: %w", io.ErrUnexpectedEOF))
		}
		if readErr != nil {
			return nil, writer.failure(fmt.Errorf("读取 Responses 图片流失败: %w", readErr))
		}
	}
}
