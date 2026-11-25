package images

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"mime"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"insistanan/api-proxy/internal/infra/config"
	"insistanan/api-proxy/internal/infra/upstreamreq"
	"insistanan/api-proxy/internal/protocol/codec"
	"insistanan/api-proxy/internal/protocol/contract"
	"insistanan/api-proxy/internal/protocol/converters/toimages"
	"insistanan/api-proxy/internal/protocol/types"
	"insistanan/api-proxy/internal/proxycore"
)

// buildImagesResponsesRequest 将 Images 的图片模型和生成参数放进 image_generation
// 工具，Responses 顶层 model 单独取渠道配置，避免把图片模型当作对话主模型。
func buildImagesResponsesRequest(c *gin.Context, upstream *config.UpstreamConfig, apiKey, endpoint string, body []byte) (*http.Request, error) {
	if endpoint != "/images/generations" && endpoint != "/images/edits" {
		return nil, contract.Unsupported("Responses 图片渠道不支持 %s，请使用 Images API 渠道", endpoint)
	}
	payload, err := readImagesPayload(c.GetHeader("Content-Type"), body)
	if err != nil {
		return nil, &proxycore.RequestValidationError{Err: err}
	}
	converted, err := convertImagesRequestToResponses(payload, upstream, endpoint == "/images/edits")
	if err != nil {
		if types.IsUnsupportedProtocol(err) {
			return nil, err
		}
		return nil, &proxycore.RequestValidationError{Err: err}
	}
	encoded, err := codec.MarshalJSONNoEscape(converted)
	if err != nil {
		return nil, fmt.Errorf("编码 Responses 图片请求失败: %w", err)
	}
	req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodPost,
		buildOpenAIEndpointURL(upstream.GetEffectiveBaseURL(), "/responses", upstream.UseFullURL), bytes.NewReader(encoded))
	if err != nil {
		return nil, &config.ConfigError{Message: fmt.Sprintf("创建 Responses 图片请求失败: %s", err)}
	}
	req = req.WithContext(resetResponsesLifecycle(c).ctx)
	req.Header = upstreamreq.PrepareUpstreamHeaders(c, req.URL.Host)
	req.Header.Set("Content-Type", "application/json")
	// 响应需要转换，使用同一解压入口处理上游仍然返回的压缩正文。
	req.Header.Del("Content-Length")
	upstreamreq.SetAuthenticationHeader(req.Header, apiKey)
	return req, nil
}

func readImagesPayload(contentType string, body []byte) (map[string]interface{}, error) {
	if boundary, ok := codec.MultipartBoundary(contentType); ok {
		parts, err := codec.ParseMultipartParts(body, boundary)
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
			case "n", "output_compression", "partial_images", "frame_count", "frame_duration_ms":
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
			case "svg", "metadata":
				var object map[string]interface{}
				if err := json.Unmarshal([]byte(value), &object); err != nil || object == nil {
					return nil, fmt.Errorf("Images 参数 %s 必须是 JSON 对象", part.FormName)
				}
				payload[part.FormName] = object
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

func multipartImageReference(part codec.MultipartPart) (map[string]interface{}, error) {
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
	if upstream == nil {
		return nil, fmt.Errorf("Responses 图片渠道配置缺失")
	}
	applyImagesOutputConfig(payload, upstream)
	model, _ := payload["model"].(string)
	resolvedModel, changed := resolveImagesUpstreamModel(model, upstream)
	if changed {
		logImagesModelMapping(model, resolvedModel)
	}
	return toimages.ResponsesRequest(payload, toimages.ResponsesRequestOptions{
		ImageModel: resolvedModel, ResponsesModel: upstream.ResponsesModel,
		ForceNonStream: upstream.ImagesForceNonStream, IsEdit: isEdit,
	})
}
