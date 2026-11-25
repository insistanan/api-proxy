package toimages

import (
	"fmt"
	"strings"
)

// responsesInput 构造文本、参考图与蒙版，保持生成和编辑的输入契约。
func responsesInput(payload map[string]interface{}, prompt string, isEdit bool) ([]interface{}, map[string]interface{}, error) {
	content := []interface{}{map[string]interface{}{"type": "input_text", "text": prompt}}
	var references []interface{}
	if raw, exists := payload["images"]; exists {
		var ok bool
		references, ok = raw.([]interface{})
		if !ok {
			return nil, nil, fmt.Errorf("Images 参数 images 必须是图片数组")
		}
	}
	if raw, exists := payload["image"]; exists {
		if len(references) > 0 {
			return nil, nil, fmt.Errorf("Images 请求不能同时包含 image 和 images")
		}
		if list, ok := raw.([]interface{}); ok {
			references = list
		} else {
			references = []interface{}{raw}
		}
	}
	if isEdit && len(references) == 0 {
		return nil, nil, fmt.Errorf("Images 编辑请求至少需要一张参考图")
	}
	if !isEdit && len(references) > 0 {
		return nil, nil, fmt.Errorf("带参考图的请求请使用 /v1/images/edits")
	}
	for _, reference := range references {
		image, err := responsesImageReference(reference)
		if err != nil {
			return nil, nil, err
		}
		image["type"] = "input_image"
		content = append(content, image)
	}
	var convertedMask map[string]interface{}
	if mask, exists := payload["mask"]; exists {
		if !isEdit {
			return nil, nil, fmt.Errorf("Images 蒙版只能用于编辑请求")
		}
		converted, err := responsesImageReference(mask)
		if err != nil {
			return nil, nil, fmt.Errorf("Images 蒙版无效: %w", err)
		}
		convertedMask = converted
	}
	return content, convertedMask, nil
}

func responsesImageReference(raw interface{}) (map[string]interface{}, error) {
	if text, ok := raw.(string); ok {
		raw = map[string]interface{}{"image_url": text}
	}
	image, ok := raw.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("Images 参考图必须是 URL、data URL 或包含 image_url/file_id 的对象")
	}
	for _, key := range []string{"image_url", "file_id"} {
		if value, ok := image[key].(string); ok && strings.TrimSpace(value) != "" {
			return map[string]interface{}{key: value}, nil
		}
	}
	return nil, fmt.Errorf("Images 参考图缺少有效的 image_url 或 file_id")
}
