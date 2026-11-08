package logger

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/BenedictKing/api-proxy/internal/utils"
)

var dataURLPattern = regexp.MustCompile(`data:[^,\s]+;base64,[A-Za-z0-9+/=]+`)

func omittedImagePlaceholder(mediaType string, payload string) string {
	mediaType = strings.TrimSpace(mediaType)
	if mediaType == "" {
		mediaType = "image"
	}
	return fmt.Sprintf("[omitted image; media_type=%s; bytes=%d]", mediaType, len(payload))
}

// OmittedBody 为超大日志正文生成明确的省略说明；非正数表示原始长度未知。
func OmittedBody(originalBytes int) string {
	if originalBytes <= 0 {
		return fmt.Sprintf("[正文已省略：原始大小未知，超过 %d 字节日志上限]", MaxBodyBytes)
	}
	return fmt.Sprintf("[正文已省略：原始大小 %d 字节，超过 %d 字节日志上限]", originalBytes, MaxBodyBytes)
}

// PrepareBody 先限制解析输入，再替换图片和敏感占位符，最后限制输出大小。
// 大于 1MiB 的原始正文只记录省略说明，避免日志处理分配完整的媒体对象树。
func PrepareBody(raw []byte) (body string, originalBytes int, truncated bool) {
	originalBytes = len(raw)
	if originalBytes == 0 {
		return "", 0, false
	}
	if originalBytes > MaxBodyBytes {
		return OmittedBody(originalBytes), originalBytes, true
	}
	sanitized := stripImages([]byte(utils.RedactSensitivePlaceholdersForLog(string(raw))))
	if len(sanitized) <= MaxBodyBytes {
		return string(sanitized), originalBytes, false
	}
	return string(sanitized[:MaxBodyBytes]), originalBytes, true
}

func stripImages(raw []byte) []byte {
	var payload interface{}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return []byte(stripImagesInText(string(raw)))
	}
	stripImagesValue(payload)
	encoded, err := json.Marshal(payload)
	if err != nil {
		return []byte(stripImagesInText(string(raw)))
	}
	return encoded
}

func stripImagesInText(text string) string {
	if text == "" {
		return text
	}
	if replaced, did := replaceImageString(text); did {
		return replaced
	}
	return text
}

func stripImagesValue(value interface{}) {
	switch typed := value.(type) {
	case []interface{}:
		for index, item := range typed {
			if text, ok := item.(string); ok {
				if replaced, did := replaceImageString(text); did {
					typed[index] = replaced
					continue
				}
			}
			stripImagesValue(item)
		}
	case map[string]interface{}:
		stripImagesObject(typed)
	}
}

func stripImagesObject(object map[string]interface{}) {
	if payload, ok := object["b64_json"].(string); ok && payload != "" {
		object["b64_json"] = omittedImagePlaceholder("image", payload)
	}
	if payload, ok := object["b64Json"].(string); ok && payload != "" {
		object["b64Json"] = omittedImagePlaceholder("image", payload)
	}

	for _, key := range []string{"inlineData", "inline_data"} {
		nested, ok := object[key].(map[string]interface{})
		if !ok {
			continue
		}
		if payload, ok := nested["data"].(string); ok && payload != "" {
			nested["data"] = omittedImagePlaceholder(firstString(nested, "mimeType", "mime_type", "mediaType", "media_type"), payload)
		}
	}

	if source, ok := object["source"].(map[string]interface{}); ok {
		sourceType, _ := source["type"].(string)
		if strings.EqualFold(strings.TrimSpace(sourceType), "base64") {
			if payload, ok := source["data"].(string); ok && payload != "" {
				source["data"] = omittedImagePlaceholder(firstString(source, "media_type", "mediaType", "mimeType"), payload)
			}
		}
		if url, ok := source["url"].(string); ok {
			if replaced, did := replaceImageString(url); did {
				source["url"] = replaced
			}
		}
	}

	replaceMappedString(object, "image_url")
	if nested, ok := object["image_url"].(map[string]interface{}); ok {
		replaceMappedString(nested, "url")
	}
	replaceMappedString(object, "imageUrl")
	if nested, ok := object["imageUrl"].(map[string]interface{}); ok {
		replaceMappedString(nested, "url")
	}
	replaceMappedString(object, "url")

	for key, child := range object {
		if text, ok := child.(string); ok {
			if replaced, did := replaceImageString(text); did {
				object[key] = replaced
			}
			continue
		}
		stripImagesValue(child)
	}
}

func replaceMappedString(object map[string]interface{}, key string) {
	text, ok := object[key].(string)
	if !ok || text == "" {
		return
	}
	if replaced, did := replaceImageString(text); did {
		object[key] = replaced
	}
}

func replaceImageString(text string) (string, bool) {
	if mediaType, data, ok := utils.ParseImageDataURL(text); ok {
		return omittedImagePlaceholder(mediaType, data), true
	}
	if !dataURLPattern.MatchString(text) {
		return text, false
	}
	replaced := dataURLPattern.ReplaceAllStringFunc(text, func(match string) string {
		mediaType, data, ok := utils.ParseImageDataURL(match)
		if !ok {
			return omittedImagePlaceholder("image", match)
		}
		return omittedImagePlaceholder(mediaType, data)
	})
	return replaced, replaced != text
}

func firstString(object map[string]interface{}, keys ...string) string {
	for _, key := range keys {
		value, ok := object[key].(string)
		if ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
