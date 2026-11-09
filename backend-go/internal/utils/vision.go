package utils

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/insistanan/api-proxy/internal/types"
)

var visionContentTypes = map[string]struct{}{
	"image":       {},
	"image_url":   {},
	"input_image": {},
}

// DetectImageContent 检测请求体中是否包含图片内容。
func DetectImageContent(body []byte) bool {
	if len(body) == 0 {
		return false
	}

	var payload map[string]interface{}
	if err := json.Unmarshal(body, &payload); err != nil {
		return false
	}

	if messages, ok := payload["messages"]; ok && ValueHasVisionContent(messages) {
		return true
	}
	if input, ok := payload["input"]; ok && ValueHasVisionContent(input) {
		return true
	}

	return false
}

// ExtractImageFingerprints 提取请求中图片的不可逆指纹。
// 内嵌图片按解码后的原始字节计算 SHA-256；远程图片不会下载，
// 仅记录 URL 的 SHA-256，避免代理因会话记录额外访问外部地址。
func ExtractImageFingerprints(body []byte) []string {
	if len(body) == 0 {
		return nil
	}

	var payload interface{}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil
	}

	seen := make(map[string]struct{})
	result := make([]string, 0)
	add := func(fingerprint string) {
		if fingerprint == "" {
			return
		}
		if _, ok := seen[fingerprint]; ok {
			return
		}
		seen[fingerprint] = struct{}{}
		result = append(result, fingerprint)
	}

	var visit func(interface{})
	visit = func(value interface{}) {
		switch v := value.(type) {
		case []interface{}:
			for _, item := range v {
				visit(item)
			}
		case map[string]interface{}:
			if inlineData, ok := v["inlineData"].(map[string]interface{}); ok {
				if data, ok := inlineData["data"].(string); ok {
					add(base64Fingerprint(data))
				}
			}

			typeValue, _ := v["type"].(string)
			typeValue = strings.ToLower(strings.TrimSpace(typeValue))
			if typeValue == "image" {
				if source, ok := v["source"].(map[string]interface{}); ok {
					sourceType, _ := source["type"].(string)
					switch strings.ToLower(strings.TrimSpace(sourceType)) {
					case "base64":
						if data, ok := source["data"].(string); ok {
							add(base64Fingerprint(data))
						}
					case "url":
						if url, ok := source["url"].(string); ok {
							add(imageReferenceFingerprint(url))
						}
					}
				}
			}
			if typeValue == "image_url" || typeValue == "input_image" {
				if url, ok := extractImageURL(v, typeValue); ok {
					add(imageReferenceFingerprint(url))
				}
			}
			if fileData, ok := v["fileData"].(map[string]interface{}); ok {
				mimeType, _ := fileData["mimeType"].(string)
				fileURI, _ := fileData["fileUri"].(string)
				if strings.HasPrefix(strings.ToLower(strings.TrimSpace(mimeType)), "image/") {
					add(imageReferenceFingerprint(fileURI))
				}
			}

			for _, item := range v {
				visit(item)
			}
		}
	}
	visit(payload)
	return result
}

// ImageFingerprintForBlock 为单个图片内容块生成不可逆指纹。
// 该指纹用于图片理解结果的逐图缓存，不保存图片原文。
func ImageFingerprintForBlock(block map[string]interface{}) string {
	if block == nil {
		return ""
	}
	if inlineData, ok := block["inlineData"].(map[string]interface{}); ok {
		if data, ok := inlineData["data"].(string); ok {
			return base64Fingerprint(data)
		}
	}

	typeValue, _ := block["type"].(string)
	typeValue = strings.ToLower(strings.TrimSpace(typeValue))
	if typeValue == "image" {
		if source, ok := block["source"].(map[string]interface{}); ok {
			sourceType, _ := source["type"].(string)
			switch strings.ToLower(strings.TrimSpace(sourceType)) {
			case "base64":
				if data, ok := source["data"].(string); ok {
					return base64Fingerprint(data)
				}
			case "url":
				if url, ok := source["url"].(string); ok {
					return imageReferenceFingerprint(url)
				}
			}
		}
	}
	if typeValue == "image_url" || typeValue == "input_image" {
		if url, ok := extractImageURL(block, typeValue); ok {
			return imageReferenceFingerprint(url)
		}
	}
	if fileData, ok := block["fileData"].(map[string]interface{}); ok {
		mimeType, _ := fileData["mimeType"].(string)
		fileURI, _ := fileData["fileUri"].(string)
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(mimeType)), "image/") {
			return imageReferenceFingerprint(fileURI)
		}
	}
	return ""
}

func base64Fingerprint(value string) string {
	value = strings.Map(func(r rune) rune {
		if r == '\r' || r == '\n' || r == '\t' || r == ' ' {
			return -1
		}
		return r
	}, strings.TrimSpace(value))
	if value == "" {
		return ""
	}

	decoded, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		decoded, err = base64.RawStdEncoding.DecodeString(value)
		if err != nil {
			return ""
		}
	}
	sum := sha256.Sum256(decoded)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func imageReferenceFingerprint(reference string) string {
	reference = strings.TrimSpace(reference)
	if reference == "" {
		return ""
	}
	if _, data, ok := ParseImageDataURL(reference); ok {
		return base64Fingerprint(data)
	}
	sum := sha256.Sum256([]byte(reference))
	return "url-sha256:" + hex.EncodeToString(sum[:])
}

// ResponsesItemHasVisionContent 检测单个 ResponsesItem 是否包含图片内容。
func ResponsesItemHasVisionContent(item types.ResponsesItem) bool {
	if isVisionContentType(item.Type) {
		return true
	}
	if ValueHasVisionContent(item.Content) {
		return true
	}
	if item.ToolUse != nil && ValueHasVisionContent(item.ToolUse.Input) {
		return true
	}
	return false
}

// ValueHasVisionContent 宽松遍历 JSON 值，检测是否包含图片内容。
func ValueHasVisionContent(value interface{}) bool {
	switch v := value.(type) {
	case nil, string, bool, float64, int, int32, int64, json.Number:
		return false
	case map[string]interface{}:
		if typeVal, _ := v["type"].(string); isVisionContentType(typeVal) {
			return true
		}
		// Gemini 的内联图片不使用 type 字段，而是以 inlineData 表示。
		if inlineData, ok := v["inlineData"].(map[string]interface{}); ok {
			if data, _ := inlineData["data"].(string); strings.TrimSpace(data) != "" {
				return true
			}
		}
		for _, item := range v {
			if ValueHasVisionContent(item) {
				return true
			}
		}
	case []interface{}:
		for _, item := range v {
			if ValueHasVisionContent(item) {
				return true
			}
		}
	case []types.ResponsesItem:
		for _, item := range v {
			if ResponsesItemHasVisionContent(item) {
				return true
			}
		}
	case []types.ContentBlock:
		for _, block := range v {
			if isVisionContentType(block.Type) {
				return true
			}
		}
	case []types.ClaudeContent:
		for _, block := range v {
			if isVisionContentType(block.Type) {
				return true
			}
		}
	case types.ResponsesItem:
		return ResponsesItemHasVisionContent(v)
	case types.ContentBlock:
		return isVisionContentType(v.Type)
	case types.ClaudeContent:
		return isVisionContentType(v.Type)
	}

	return false
}

// NormalizeContentBlocks 将 content 统一规整为 map block 列表。
func NormalizeContentBlocks(content interface{}) []map[string]interface{} {
	switch v := content.(type) {
	case []interface{}:
		blocks := make([]map[string]interface{}, 0, len(v))
		for _, item := range v {
			block, ok := item.(map[string]interface{})
			if ok {
				blocks = append(blocks, block)
			}
		}
		return blocks
	case []map[string]interface{}:
		return v
	case []types.ContentBlock:
		blocks := make([]map[string]interface{}, 0, len(v))
		for _, block := range v {
			blocks = append(blocks, map[string]interface{}{
				"type": block.Type,
				"text": block.Text,
			})
		}
		return blocks
	case []types.ClaudeContent:
		blocks := make([]map[string]interface{}, 0, len(v))
		for _, block := range v {
			normalized := map[string]interface{}{
				"type": block.Type,
			}
			if block.Text != "" {
				normalized["text"] = block.Text
			}
			if block.ID != "" {
				normalized["id"] = block.ID
			}
			if block.Name != "" {
				normalized["name"] = block.Name
			}
			if block.Input != nil {
				normalized["input"] = block.Input
			}
			if block.ToolUseID != "" {
				normalized["tool_use_id"] = block.ToolUseID
			}
			blocks = append(blocks, normalized)
		}
		return blocks
	default:
		return nil
	}
}

// ExtractTextFromBlock 提取通用文本 block 的文本。
func ExtractTextFromBlock(block map[string]interface{}) (string, bool) {
	if block == nil {
		return "", false
	}

	typeVal, _ := block["type"].(string)
	switch typeVal {
	case "text", "input_text", "output_text":
		text, ok := block["text"].(string)
		if ok && text != "" {
			return text, true
		}
	}

	return "", false
}

// ToOpenAIImageContentBlock 将通用图片块转换为 OpenAI Chat content block。
func ToOpenAIImageContentBlock(block map[string]interface{}) (map[string]interface{}, bool) {
	if block == nil {
		return nil, false
	}

	typeVal, _ := block["type"].(string)
	url, ok := extractImageURL(block, typeVal)
	if !ok || url == "" {
		return nil, false
	}

	return map[string]interface{}{
		"type": "image_url",
		"image_url": map[string]interface{}{
			"url": url,
		},
	}, true
}

// ToClaudeImageContentBlock 将通用图片块转换为 Claude image block。
func ToClaudeImageContentBlock(block map[string]interface{}) (map[string]interface{}, bool) {
	if block == nil {
		return nil, false
	}

	typeVal, _ := block["type"].(string)
	switch typeVal {
	case "image":
		if source, ok := block["source"].(map[string]interface{}); ok {
			sourceType, _ := source["type"].(string)
			switch sourceType {
			case "base64":
				data, dataOK := source["data"].(string)
				mediaType, _ := source["media_type"].(string)
				if dataOK && data != "" {
					if mediaType == "" {
						mediaType = DefaultImageMediaType
					}
					return map[string]interface{}{
						"type": "image",
						"source": map[string]interface{}{
							"type":       "base64",
							"media_type": mediaType,
							"data":       data,
						},
					}, true
				}
			case "url":
				url, urlOK := source["url"].(string)
				if urlOK && url != "" {
					return map[string]interface{}{
						"type": "image",
						"source": map[string]interface{}{
							"type": "url",
							"url":  url,
						},
					}, true
				}
			}
		}
	}

	url, ok := extractImageURL(block, typeVal)
	if ok && url != "" {
		if mediaType, data, parsed := ParseImageDataURL(url); parsed {
			return map[string]interface{}{
				"type": "image",
				"source": map[string]interface{}{
					"type":       "base64",
					"media_type": mediaType,
					"data":       data,
				},
			}, true
		}

		return map[string]interface{}{
			"type": "image",
			"source": map[string]interface{}{
				"type": "url",
				"url":  url,
			},
		}, true
	}

	return nil, false
}

func isVisionContentType(typeVal string) bool {
	typeVal = strings.ToLower(strings.TrimSpace(typeVal))
	_, ok := visionContentTypes[typeVal]
	return ok
}

func extractImageURL(block map[string]interface{}, typeVal string) (string, bool) {
	switch typeVal {
	case "image":
		if source, ok := block["source"].(map[string]interface{}); ok {
			sourceType, _ := source["type"].(string)
			switch sourceType {
			case "base64":
				mediaType, _ := source["media_type"].(string)
				data, _ := source["data"].(string)
				if data == "" {
					return "", false
				}
				if mediaType == "" {
					mediaType = DefaultImageMediaType
				}
				return fmt.Sprintf("data:%s;base64,%s", mediaType, data), true
			case "url":
				url, ok := source["url"].(string)
				return url, ok && url != ""
			}
		}
	case "image_url":
		if nested, ok := block["image_url"].(map[string]interface{}); ok {
			if url, ok := nested["url"].(string); ok && url != "" {
				return url, true
			}
		}
		if url, ok := block["image_url"].(string); ok && url != "" {
			return url, true
		}
		if url, ok := block["url"].(string); ok && url != "" {
			return url, true
		}
	case "input_image":
		if nested, ok := block["image_url"].(map[string]interface{}); ok {
			if url, ok := nested["url"].(string); ok && url != "" {
				return url, true
			}
		}
		if url, ok := block["image_url"].(string); ok && url != "" {
			return url, true
		}
		if url, ok := block["url"].(string); ok && url != "" {
			return url, true
		}
		if mediaType, data, ok := extractBase64Payload(block); ok {
			return fmt.Sprintf("data:%s;base64,%s", mediaType, data), true
		}
	}

	return "", false
}

func extractBase64Payload(block map[string]interface{}) (string, string, bool) {
	mediaType, _ := block["media_type"].(string)
	data, _ := block["data"].(string)
	if data == "" {
		if source, ok := block["source"].(map[string]interface{}); ok {
			mediaType, _ = source["media_type"].(string)
			data, _ = source["data"].(string)
		}
	}
	if data == "" {
		return "", "", false
	}
	if mediaType == "" {
		mediaType = DefaultImageMediaType
	}
	return mediaType, data, true
}

// DefaultImageMediaType 是图片 mediaType 缺省值。
// 上游（Claude source.media_type / Gemini inlineData.mimeType）要求该字段必填，
// data URL 头部为空时按既有契约填此默认值，而非报错。
const DefaultImageMediaType = "image/png"

// ParseImageDataURL 解析 data URL 形式的图片引用，返回 (mediaType, base64 载荷, 是否解析成功)。
// 五协议图片链路（utils/vision、providers/gemini、converters/responses_gemini）的唯一出处。
//
// 规则：
//   - 非 "data:" 前缀直接返回 false（调用方无需自行前置判断）；
//   - 以首个逗号切分头部与载荷，载荷为空视为解析失败；
//   - 头部剥离 ";base64" 及其后的参数，剩余部分为 mediaType；
//   - mediaType 为空时填 DefaultImageMediaType。
//
// 注意：载荷不做 base64 校验与空白清理，需要指纹时走 base64Fingerprint。
func ParseImageDataURL(url string) (string, string, bool) {
	if !strings.HasPrefix(url, "data:") {
		return "", "", false
	}

	payload := strings.TrimPrefix(url, "data:")
	header, data, ok := strings.Cut(payload, ",")
	if !ok || data == "" {
		return "", "", false
	}

	mediaType := header
	if before, _, found := strings.Cut(header, ";base64"); found {
		mediaType = before
	}
	if mediaType == "" {
		mediaType = DefaultImageMediaType
	}

	return mediaType, data, true
}
