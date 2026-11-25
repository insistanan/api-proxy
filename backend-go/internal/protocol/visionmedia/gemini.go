package visionmedia

import "insistanan/api-proxy/internal/protocol/types"

// ToGeminiImagePart 与请求校验共用媒体提取规则，避免校验通过后转换时丢图。
func ToGeminiImagePart(block map[string]interface{}, allowFileURI bool) (types.GeminiPart, bool) {
	typ, _ := block["type"].(string)
	uri, ok := extractImageURL(block, typ)
	if !ok || uri == "" {
		return types.GeminiPart{}, false
	}
	if mime, data, ok := ParseImageDataURL(uri); ok {
		return types.GeminiPart{InlineData: &types.GeminiInlineData{MimeType: mime, Data: data}}, true
	}
	if allowFileURI {
		mime := DefaultImageMediaType
		if source, ok := block["source"].(map[string]interface{}); ok {
			if value, ok := source["media_type"].(string); ok && value != "" {
				mime = value
			}
		}
		return types.GeminiPart{FileData: &types.GeminiFileData{MimeType: mime, FileURI: uri}}, true
	}
	return types.GeminiPart{}, false
}
