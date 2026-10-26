package images

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"mime"
	"net/http"
	"strings"

	"github.com/BenedictKing/api-proxy/internal/config"
	"github.com/BenedictKing/api-proxy/internal/handlers/proxycore"
	"github.com/BenedictKing/api-proxy/internal/utils"
	"github.com/gin-gonic/gin"
)

// buildImagesResponsesRequest 将 Images 的图片模型和生成参数放进 image_generation
// 工具，Responses 顶层 model 单独取渠道配置，避免把图片模型当作对话主模型。
func buildImagesResponsesRequest(c *gin.Context, upstream *config.UpstreamConfig, apiKey, endpoint string, body []byte) (*http.Request, error) {
	if endpoint != "/images/generations" && endpoint != "/images/edits" {
		return nil, fmt.Errorf("Responses 图片渠道不支持 %s，请使用 Images API 渠道", endpoint)
	}
	payload, err := readImagesPayload(c.GetHeader("Content-Type"), body)
	if err != nil {
		return nil, err
	}
	converted, err := convertImagesRequestToResponses(payload, upstream, endpoint == "/images/edits")
	if err != nil {
		return nil, err
	}
	encoded, err := utils.MarshalJSONNoEscape(converted)
	if err != nil {
		return nil, fmt.Errorf("编码 Responses 图片请求失败: %w", err)
	}
	req, err := http.NewRequestWithContext(responsesImageUpstreamContext(c), http.MethodPost,
		buildOpenAIEndpointURL(upstream.GetEffectiveBaseURL(), "/responses"), bytes.NewReader(encoded))
	if err != nil {
		return nil, fmt.Errorf("创建 Responses 图片请求失败: %w", err)
	}
	req.Header = utils.PrepareUpstreamHeaders(c, req.URL.Host)
	req.Header.Set("Content-Type", "application/json")
	// 响应需要转换，使用同一解压入口处理上游仍然返回的压缩正文。
	req.Header.Del("Content-Length")
	utils.SetAuthenticationHeader(req.Header, apiKey)
	return req, nil
}

func readImagesPayload(contentType string, body []byte) (map[string]interface{}, error) {
	if boundary, ok := utils.MultipartBoundary(contentType); ok {
		parts, err := utils.ParseMultipartParts(body, boundary)
		if err != nil {
			return nil, fmt.Errorf("解析 Images multipart 请求失败: %w", err)
		}
		payload := make(map[string]interface{})
		var images []interface{}
		for _, part := range parts {
			if part.FileName != "" {
				image, err := multipartImageReference(part)
				if err != nil {
					return nil, err
				}
				switch part.FormName {
				case "image", "image[]":
					images = append(images, image)
				case "mask":
					if _, exists := payload["mask"]; exists {
						return nil, fmt.Errorf("Images 请求不能包含多个蒙版")
					}
					payload["mask"] = image
				default:
					return nil, fmt.Errorf("Responses 图片渠道不支持文件字段 %q", part.FormName)
				}
				continue
			}
			if _, exists := payload[part.FormName]; exists {
				return nil, fmt.Errorf("Images 请求重复包含字段 %q", part.FormName)
			}
			value := strings.TrimSpace(string(part.Content))
			switch part.FormName {
			case "n", "output_compression", "partial_images":
				var number json.Number
				if err := json.Unmarshal([]byte(value), &number); err != nil {
					return nil, fmt.Errorf("Images 参数 %s 必须是数字: %w", part.FormName, err)
				}
				payload[part.FormName] = number
			case "stream":
				var stream bool
				if err := json.Unmarshal([]byte(value), &stream); err != nil {
					return nil, fmt.Errorf("Images 参数 stream 必须是布尔值: %w", err)
				}
				payload[part.FormName] = stream
			default:
				payload[part.FormName] = string(part.Content)
			}
		}
		if len(images) > 0 {
			payload["images"] = images
		}
		return payload, nil
	}
	payload, ok, err := readImagesJSONObject(contentType, body)
	if err != nil {
		return nil, err
	}
	if !ok || payload == nil {
		return nil, fmt.Errorf("Responses 图片渠道只接受 JSON 或 multipart/form-data 请求")
	}
	return payload, nil
}

func multipartImageReference(part utils.MultipartPart) (map[string]interface{}, error) {
	if len(part.Content) == 0 {
		return nil, fmt.Errorf("Images 图片文件 %q 为空", part.FormName)
	}
	mediaType := part.Header.Get("Content-Type")
	if mediaType == "" || mediaType == "application/octet-stream" {
		mediaType = http.DetectContentType(part.Content)
	}
	parsedType, _, err := mime.ParseMediaType(mediaType)
	if err != nil || !strings.HasPrefix(parsedType, "image/") {
		return nil, fmt.Errorf("Images 文件 %q 的媒体类型无效: %s", part.FormName, mediaType)
	}
	return map[string]interface{}{
		"image_url": "data:" + parsedType + ";base64," + base64.StdEncoding.EncodeToString(part.Content),
	}, nil
}

func convertImagesRequestToResponses(payload map[string]interface{}, upstream *config.UpstreamConfig, isEdit bool) (map[string]interface{}, error) {
	model, _ := payload["model"].(string)
	prompt, _ := payload["prompt"].(string)
	resolvedModel, modelChanged := resolveImagesUpstreamModel(model, upstream)
	if resolvedModel == "" || strings.TrimSpace(prompt) == "" {
		return nil, fmt.Errorf("Responses 图片渠道要求 Images 请求包含 model 和 prompt")
	}
	if modelChanged {
		logImagesModelMapping(model, resolvedModel)
	}
	if format, exists := payload["response_format"]; exists && format != "b64_json" {
		return nil, fmt.Errorf("Responses 图片渠道只能返回 b64_json，无法生成托管图片 URL")
	}
	if stream, exists := payload["stream"]; exists {
		if _, ok := stream.(bool); !ok {
			return nil, fmt.Errorf("Images 参数 stream 必须是布尔值")
		}
	}
	// 顶层 model 是 Responses 主模型。真正的生图模型只写进 image_generation 工具。
	// 渠道自定义参数不注入这条路径，未知字段会被上游拒绝。
	forceNonStream := upstream != nil && upstream.ImagesForceNonStream
	tool := map[string]interface{}{"type": "image_generation", "model": resolvedModel}
	for key, value := range payload {
		switch key {
		case "size", "quality", "background", "output_format", "moderation", "input_fidelity", "output_compression", "n":
			tool[key] = value
		case "partial_images":
			if !forceNonStream {
				tool[key] = value
			}
		case "model", "prompt", "stream", "response_format", "images", "image", "mask", "user", "metadata":
			// 通用字段在下方分别写入 Responses 请求。
		default:
			return nil, fmt.Errorf("Images 参数 %q 无法转换为 Responses 图片请求", key)
		}
	}
	content := []interface{}{map[string]interface{}{"type": "input_text", "text": prompt}}
	var references []interface{}
	if raw, exists := payload["images"]; exists {
		var ok bool
		references, ok = raw.([]interface{})
		if !ok {
			return nil, fmt.Errorf("Images 参数 images 必须是图片数组")
		}
	}
	if raw, exists := payload["image"]; exists {
		if len(references) > 0 {
			return nil, fmt.Errorf("Images 请求不能同时包含 image 和 images")
		}
		if list, ok := raw.([]interface{}); ok {
			references = list
		} else {
			references = []interface{}{raw}
		}
	}
	if isEdit && len(references) == 0 {
		return nil, fmt.Errorf("Images 编辑请求至少需要一张参考图")
	}
	if !isEdit && len(references) > 0 {
		return nil, fmt.Errorf("带参考图的请求请使用 /v1/images/edits")
	}
	for _, reference := range references {
		image, err := responsesImageReference(reference)
		if err != nil {
			return nil, err
		}
		image["type"] = "input_image"
		content = append(content, image)
	}
	if mask, exists := payload["mask"]; exists {
		if !isEdit {
			return nil, fmt.Errorf("Images 蒙版只能用于编辑请求")
		}
		converted, err := responsesImageReference(mask)
		if err != nil {
			return nil, fmt.Errorf("Images 蒙版无效: %w", err)
		}
		tool["input_image_mask"] = converted
	}
	if isEdit {
		tool["action"] = "edit"
	} else {
		tool["action"] = "generate"
	}
	result := map[string]interface{}{
		"model": upstream.ResponsesModel,
		"input": []interface{}{map[string]interface{}{"role": "user", "content": content}},
		"tools": []interface{}{tool}, "tool_choice": map[string]interface{}{"type": "image_generation"},
	}
	if forceNonStream {
		result["stream"] = false
	} else if value, exists := payload["stream"]; exists {
		result["stream"] = value
	}
	if value, exists := payload["user"]; exists {
		result["user"] = value
	}
	if raw, exists := payload["metadata"]; exists {
		metadata, ok := raw.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("Images metadata 必须是对象")
		}
		forwarded := make(map[string]interface{})
		for key, value := range metadata {
			if key != "channel_index" && key != "purpose" {
				forwarded[key] = value
			}
		}
		if len(forwarded) > 0 {
			result["metadata"] = forwarded
		}
	}
	return result, nil
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

// responsesImageUpstreamContext 保留请求值和超时，但不再跟随浏览器取消。
// 工作台收到完整图片后会立即关闭 SSE，此时上游通常还没发出 response.completed。
func responsesImageUpstreamContext(c *gin.Context) context.Context {
	if c == nil || c.Request == nil {
		return context.Background()
	}
	return proxycore.DetachClientCancellation(c.Request.Context())
}
