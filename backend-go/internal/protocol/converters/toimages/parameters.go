package toimages

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
)

// IntegerParameter 保留 JSON 数字语义，拒绝小数与溢出，不截断参数值。
func IntegerParameter(name string, value interface{}) (int64, error) {
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

// ValidateOutputParameters 只检查已知新格式约束，原生渠道仍透传未知参数和模型。
// 原生 Images 与 Responses 的 GIF 帧时长上限不同，不能共用 Responses 的上限。
func ValidateOutputParameters(payload map[string]interface{}, responses bool) error {
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
		number, err := IntegerParameter(name, value)
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
		number, err := IntegerParameter("partial_images", value)
		if err != nil {
			return err
		}
		if number != 0 {
			return fmt.Errorf("Images %s 输出不支持 partial_images 预览", format)
		}
	}
	if !responses {
		if ParseStreamValue(payload["stream"]) {
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
		number, err := IntegerParameter("n", payload["n"])
		if err != nil {
			return err
		}
		if number != 1 {
			return fmt.Errorf("Images SVG 输出只支持 n=1")
		}
	}
	return nil
}

// ParseStreamValue 读取 JSON 或 multipart 的流式标记。
func ParseStreamValue(value interface{}) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		switch strings.ToLower(strings.TrimSpace(typed)) {
		case "true", "1", "yes", "on":
			return true
		}
	case json.Number:
		return typed.String() == "1"
	case float64:
		return typed == 1
	}
	return false
}
