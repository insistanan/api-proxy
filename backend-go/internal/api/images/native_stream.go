package images

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"insistanan/api-proxy/internal/api/streams"
	"insistanan/api-proxy/internal/protocol/codec"
	"insistanan/api-proxy/internal/protocol/converters/toimages"
	"insistanan/api-proxy/internal/protocol/providers"
	"insistanan/api-proxy/internal/protocol/tokenusage"
	"insistanan/api-proxy/internal/protocol/types"
	"insistanan/api-proxy/internal/proxycore"
)

const nativeImageCountKey = "images.native.count"

func nativeImagesCount(contentType string, body []byte) (int, error) {
	var raw string
	if boundary, multipart := codec.MultipartBoundary(contentType); multipart {
		fields, err := codec.ReadMultipartTextFields(body, boundary, []string{"n"})
		if err != nil {
			return 0, err
		}
		raw = fields["n"]
	} else {
		field := gjson.GetBytes(body, "n")
		if field.Exists() && field.Type != gjson.Null {
			raw = field.Raw
		}
	}
	if raw == "" {
		return 1, nil
	}
	count, err := toimages.IntegerParameter("n", json.Number(raw))
	if err != nil {
		return 0, err
	}
	if count <= 0 || int64(int(count)) != count {
		return 0, fmt.Errorf("Images 参数 n 必须是正整数")
	}
	return int(count), nil
}

func forwardNativeImageStream(c *gin.Context, resp *http.Response, endpoint string) (*types.Usage, error) {
	prefix := "image_generation"
	if endpoint == "/images/edits" {
		prefix = "image_edit"
	}
	// n 未设置时 Images 协议默认生成一张；构建请求时记录最终出站的 n。
	expected := c.GetInt(nativeImageCountKey)
	if expected == 0 {
		expected = 1
	}
	completed := 0
	var usage *types.Usage
	err := streams.ForwardEventStream(c, resp, "images", func(event codec.SSEEvent) (bool, error) {
		if event.Data == "" && event.Name != "error" {
			return false, nil
		}
		if !gjson.Valid(event.Data) || !gjson.Parse(event.Data).IsObject() {
			return false, fmt.Errorf("Images SSE 数据必须是有效 JSON 对象")
		}
		root := gjson.Parse(event.Data)
		name := root.Get("type").String()
		if name == "" {
			name = event.Name
		} else if event.Name != "" && name != event.Name {
			return false, fmt.Errorf("Images SSE 事件名 %q 与 type %q 不一致", event.Name, name)
		}
		if field := root.Get("usage"); field.Exists() {
			parsed, err := tokenusage.ParseJSONUsage([]byte(field.Raw))
			if err != nil {
				return false, fmt.Errorf("解析 Images 流用量失败: %w", err)
			}
			if parsed != nil {
				usage = parsed
			}
		}
		failure := root.Get("error")
		if name == "error" || name == prefix+".failed" || failure.Exists() && failure.Type != gjson.Null {
			return false, providers.NewUpstreamStreamFailedError(proxycore.IsUpstreamModelCapacityError([]byte(event.Data)), event.Data)
		}
		if name == prefix+".completed" {
			image := root.Get("b64_json")
			if image.Type != gjson.String || strings.TrimSpace(image.Str) == "" {
				return false, fmt.Errorf("Images 完成事件缺少图片数据")
			}
			completed++
		}
		return completed >= expected, nil
	})
	return usage, err
}
