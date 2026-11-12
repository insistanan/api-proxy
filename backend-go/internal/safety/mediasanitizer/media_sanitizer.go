package mediasanitizer

import (
	"bytes"
	"encoding/json"
	"strings"

	"github.com/insistanan/api-proxy/internal/infra/utils"
)

// UnsupportedImageMarker 是图片被降级为纯文本时的占位提示符。
const UnsupportedImageMarker = "[Unsupported Image]"

// IsUnsupportedImageError 检测上游返回的错误是否属于模型不支持图片/多模态输入（模态拒绝）。
func IsUnsupportedImageError(statusCode int, responseBodyBytes []byte) bool {
	if len(responseBodyBytes) == 0 {
		return false
	}

	if statusCode != 400 && statusCode != 415 && statusCode != 422 && statusCode != 501 {
		return false
	}

	lowerMessage := strings.ToLower(string(responseBodyBytes))

	// 自证性表述：这类短语本身就断言了“仅支持纯文本”，属于模态拒绝
	selfEvidentHints := []string{
		"only support text",
		"only supports text",
		"text only",
		"text-only",
		"model does not accept image",
		"model does not support image",
	}
	for _, hint := range selfEvidentHints {
		if strings.Contains(lowerMessage, hint) {
			return true
		}
	}

	mentionsImage := strings.Contains(lowerMessage, "image") ||
		strings.Contains(lowerMessage, "vision") ||
		strings.Contains(lowerMessage, "multimodal") ||
		strings.Contains(lowerMessage, "multi-modal") ||
		strings.Contains(lowerMessage, "modality") ||
		strings.Contains(lowerMessage, "modalities") ||
		strings.Contains(lowerMessage, "media") ||
		strings.Contains(lowerMessage, "attachment")

	if !mentionsImage {
		return false
	}

	unsupportedHints := []string{
		"unsupported",
		"not supported",
		"does not support",
		"doesn't support",
		"do not support",
		"don't support",
		"invalid content type",
		"invalid message content",
		"unknown variant",
		"unknown content type",
		"unrecognized content type",
		"cannot process",
		"cannot handle",
		"can't process",
		"can't handle",
		"unable to process",
	}

	for _, hint := range unsupportedHints {
		if strings.Contains(lowerMessage, hint) {
			return true
		}
	}

	return false
}

// SanitizeImagesWithMarker 扫描请求体并将各类图片块替换为文本占位符 [Unsupported Image]。
// 支持 Anthropic messages、Responses input、Gemini contents 与 OpenAI image_url 格式。
func SanitizeImagesWithMarker(requestBodyBytes []byte) ([]byte, bool, int) {
	if len(requestBodyBytes) == 0 {
		return requestBodyBytes, false, 0
	}

	var payload map[string]interface{}
	decoder := json.NewDecoder(bytes.NewReader(requestBodyBytes))
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil {
		return requestBodyBytes, false, 0
	}

	totalReplaced := 0

	// 1. Anthropic & OpenAI messages
	if rawMessages, exists := payload["messages"]; exists {
		if messagesSlice, ok := rawMessages.([]interface{}); ok {
			for _, rawMsg := range messagesSlice {
				if msgMap, ok := rawMsg.(map[string]interface{}); ok {
					totalReplaced += sanitizeContent(msgMap)
				}
			}
		}
	}

	// 2. Responses input items
	if rawInput, exists := payload["input"]; exists {
		if inputSlice, ok := rawInput.([]interface{}); ok {
			for _, rawItem := range inputSlice {
				if itemMap, ok := rawItem.(map[string]interface{}); ok {
					itemType, _ := itemMap["type"].(string)
					if itemType == "input_image" || itemType == "image" {
						itemMap["type"] = "input_text"
						itemMap["text"] = UnsupportedImageMarker
						delete(itemMap, "image_url")
						delete(itemMap, "source")
						totalReplaced++
					}
				}
			}
		}
	}

	// 3. Gemini contents
	if rawContents, exists := payload["contents"]; exists {
		if contentsSlice, ok := rawContents.([]interface{}); ok {
			for _, rawContent := range contentsSlice {
				if contentMap, ok := rawContent.(map[string]interface{}); ok {
					if rawParts, hasParts := contentMap["parts"]; hasParts {
						if partsSlice, ok := rawParts.([]interface{}); ok {
							for i, rawPart := range partsSlice {
								if partMap, ok := rawPart.(map[string]interface{}); ok {
									if isGeminiImagePart(partMap) {
										partsSlice[i] = map[string]interface{}{
											"text": UnsupportedImageMarker,
										}
										totalReplaced++
									}
								}
							}
						}
					}
				}
			}
		}
	}

	if totalReplaced == 0 {
		return requestBodyBytes, false, 0
	}

	modifiedBytes, err := utils.MarshalJSONNoEscape(payload)
	if err != nil {
		return requestBodyBytes, false, 0
	}

	return modifiedBytes, true, totalReplaced
}

func sanitizeContent(msgMap map[string]interface{}) int {
	rawContent, hasContent := msgMap["content"]
	if !hasContent {
		return 0
	}

	blocksSlice, isSlice := rawContent.([]interface{})
	if !isSlice || len(blocksSlice) == 0 {
		return 0
	}

	replaced := 0
	for _, rawBlock := range blocksSlice {
		blockMap, ok := rawBlock.(map[string]interface{})
		if !ok {
			continue
		}

		blockType, _ := blockMap["type"].(string)
		if blockType == "image" || blockType == "image_url" {
			blockMap["type"] = "text"
			blockMap["text"] = UnsupportedImageMarker
			delete(blockMap, "source")
			delete(blockMap, "image_url")
			replaced++
			continue
		}

		// 处理嵌套在 tool_result 中的图片
		if blockType == "tool_result" {
			if rawNested, hasNested := blockMap["content"]; hasNested {
				if nestedSlice, ok := rawNested.([]interface{}); ok {
					for _, rawNestedBlock := range nestedSlice {
						if nestedMap, ok := rawNestedBlock.(map[string]interface{}); ok {
							nestedType, _ := nestedMap["type"].(string)
							if nestedType == "image" || nestedType == "image_url" {
								nestedMap["type"] = "text"
								nestedMap["text"] = UnsupportedImageMarker
								delete(nestedMap, "source")
								delete(nestedMap, "image_url")
								replaced++
							}
						}
					}
				}
			}
		}
	}

	return replaced
}

func isGeminiImagePart(partMap map[string]interface{}) bool {
	if rawInline, exists := partMap["inlineData"]; exists {
		if inlineMap, ok := rawInline.(map[string]interface{}); ok {
			if mime, _ := inlineMap["mimeType"].(string); strings.HasPrefix(strings.ToLower(mime), "image/") {
				return true
			}
		}
	}
	if rawFile, exists := partMap["fileData"]; exists {
		if fileMap, ok := rawFile.(map[string]interface{}); ok {
			if mime, _ := fileMap["mimeType"].(string); strings.HasPrefix(strings.ToLower(mime), "image/") {
				return true
			}
		}
	}
	return false
}
