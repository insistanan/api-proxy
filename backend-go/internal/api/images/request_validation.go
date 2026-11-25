package images

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"strings"

	"insistanan/api-proxy/internal/protocol/codec"
	"insistanan/api-proxy/internal/protocol/converters/toimages"
)

// validateNativeImagesMultipartOutput 只读取小参数；跳过图片文件而不再次复制上传内容。
// 校验发生在渠道覆盖参数之后，因此判断的是实际发给上游的正文。
func validateNativeImagesMultipartOutput(contentType string, body []byte) error {
	boundary, ok := codec.MultipartBoundary(contentType)
	if !ok {
		return nil
	}
	payload := make(map[string]interface{})
	reader := multipart.NewReader(bytes.NewReader(body), boundary)
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("解析 Images multipart 输出参数失败: %w", err)
		}
		name := part.FormName()
		switch name {
		case "model", "response_format", "output_format", "complexity", "frame_count", "frame_duration_ms", "svg", "output_compression", "partial_images", "stream", "prompt_rewriter", "mask", "n":
		default:
			continue
		}
		if part.FileName() != "" {
			if name == "mask" {
				payload[name] = true
			}
			continue
		}
		if _, exists := payload[name]; exists {
			return fmt.Errorf("Images 请求重复包含输出参数 %q", name)
		}
		text, err := io.ReadAll(io.LimitReader(part, 64*1024+1))
		if err != nil {
			return fmt.Errorf("读取 Images 参数 %s 失败: %w", name, err)
		}
		if len(text) > 64*1024 {
			return fmt.Errorf("Images 参数 %s 超过 64KiB", name)
		}
		value := strings.TrimSpace(string(text))
		switch name {
		case "frame_count", "frame_duration_ms", "output_compression", "partial_images", "n":
			var number json.Number
			if err := json.Unmarshal([]byte(value), &number); err != nil {
				return fmt.Errorf("Images 参数 %s 必须是数字: %w", name, err)
			}
			payload[name] = number
		case "svg":
			var options map[string]interface{}
			if err := json.Unmarshal([]byte(value), &options); err != nil {
				return fmt.Errorf("Images 参数 svg 必须是 JSON 对象: %w", err)
			}
			payload[name] = options
		default:
			payload[name] = value
		}
	}
	return toimages.ValidateOutputParameters(payload, false)
}
