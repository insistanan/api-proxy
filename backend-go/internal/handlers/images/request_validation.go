package images

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"mime/multipart"
	"strings"

	"github.com/BenedictKing/api-proxy/internal/utils"
)

// imagesIntegerParameter 保留 JSON 数字语义，拒绝小数与溢出，不截断参数值。
func imagesIntegerParameter(name string, value interface{}) (int64, error) {
	var number float64
	switch typed := value.(type) {
	case json.Number:
		if integer, err := typed.Int64(); err == nil {
			return integer, nil
		}
		parsed, err := typed.Float64()
		if err != nil {
			return 0, fmt.Errorf("Images 参数 %s 必须是整数: %w", name, err)
		}
		number = parsed
	case float64:
		number = typed
	case int:
		return int64(typed), nil
	case int64:
		return typed, nil
	default:
		return 0, fmt.Errorf("Images 参数 %s 必须是整数", name)
	}
	if math.IsNaN(number) || math.IsInf(number, 0) || math.Trunc(number) != number || number < -0x1p63 || number >= 0x1p63 {
		return 0, fmt.Errorf("Images 参数 %s 必须是有效整数", name)
	}
	return int64(number), nil
}

// validateImagesOutputParameters 只检查已知新格式约束，原生渠道仍透传未知参数和模型。
// 原生 Images 与 Responses 的 GIF 帧时长上限不同，不能共用 Responses 的上限。
func validateImagesOutputParameters(payload map[string]interface{}, responses bool) error {
	format, _ := payload["output_format"].(string)
	if value := payload["complexity"]; value != nil {
		if value != "low" && value != "medium" && value != "high" {
			return fmt.Errorf("Images complexity 必须是 low、medium 或 high")
		}
	}
	for _, name := range []string{"frame_count", "frame_duration_ms"} {
		value := payload[name]
		if value == nil {
			continue
		}
		if format != "gif" {
			return fmt.Errorf("Images 参数 %s 仅适用于 output_format=gif", name)
		}
		number, err := imagesIntegerParameter(name, value)
		if err != nil {
			return err
		}
		if name == "frame_count" && (number < 2 || number > 16) {
			return fmt.Errorf("Images frame_count 必须在 2–16 之间")
		}
		if name == "frame_duration_ms" && (number < 20 || (responses && number > 2000)) {
			if responses {
				return fmt.Errorf("Responses 图片 frame_duration_ms 必须在 20–2000 之间")
			}
			return fmt.Errorf("Images frame_duration_ms 不能小于 20")
		}
	}
	if value := payload["svg"]; value != nil {
		if format != "svg" {
			return fmt.Errorf("Images 参数 svg 仅适用于 output_format=svg")
		}
		if _, ok := value.(map[string]interface{}); !ok {
			return fmt.Errorf("Images 参数 svg 必须是对象")
		}
	}
	if format != "gif" && format != "svg" {
		return nil
	}
	if payload["output_compression"] != nil {
		return fmt.Errorf("Images %s 输出不支持 output_compression", format)
	}
	if value := payload["partial_images"]; value != nil {
		number, err := imagesIntegerParameter("partial_images", value)
		if err != nil {
			return err
		}
		if number != 0 {
			return fmt.Errorf("Images %s 输出不支持 partial_images 预览", format)
		}
	}
	if !responses {
		if parseImagesStreamValue(payload["stream"]) {
			return fmt.Errorf("Images API 的 %s 输出不支持 stream，请关闭流式或使用强制非流式", format)
		}
		if payload["prompt_rewriter"] != nil {
			return fmt.Errorf("Images %s 输出不支持 prompt_rewriter", format)
		}
	}
	if format == "gif" && (payload["mask"] != nil || payload["input_image_mask"] != nil) {
		return fmt.Errorf("Images GIF 输出不支持蒙版")
	}
	if format == "svg" && payload["n"] != nil {
		number, err := imagesIntegerParameter("n", payload["n"])
		if err != nil {
			return err
		}
		if number != 1 {
			return fmt.Errorf("Images SVG 输出只支持 n=1")
		}
	}
	return nil
}

// validateNativeImagesMultipartOutput 只读取小参数；跳过图片文件而不再次复制上传内容。
// 校验发生在渠道覆盖参数之后，因此判断的是实际发给上游的正文。
func validateNativeImagesMultipartOutput(contentType string, body []byte) error {
	boundary, ok := utils.MultipartBoundary(contentType)
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
		case "output_format", "complexity", "frame_count", "frame_duration_ms", "svg", "output_compression", "partial_images", "stream", "prompt_rewriter", "mask", "n":
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
	return validateImagesOutputParameters(payload, false)
}
