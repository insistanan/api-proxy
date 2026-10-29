package images

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime"
	"net/textproto"
	"reflect"
	"sort"
	"strings"

	"github.com/BenedictKing/api-proxy/internal/config"
	"github.com/BenedictKing/api-proxy/internal/utils"
)

// resolveImagesUpstreamModel 解析真正发给上游的生图模型。
// changed 为真时，调用方必须把 resolved 写进原生请求的 model，或 Responses 的 image_generation 工具。
func resolveImagesUpstreamModel(requested string, upstream *config.UpstreamConfig) (resolved string, changed bool) {
	requested = strings.TrimSpace(requested)
	resolved = strings.TrimSpace(config.ResolveUpstreamModel(requested, upstream))
	if resolved == "" {
		return requested, false
	}
	return resolved, resolved != requested
}

func logImagesModelMapping(requested, resolved string) {
	if strings.TrimSpace(requested) == "" {
		requested = "(未指定)"
	}
	log.Printf("[Images-ModelMapping] 模型重定向: %s -> %s", strings.TrimSpace(requested), resolved)
}

// applyImagesModelMapping 改写原生 Images 请求的生图模型，注入渠道自定义参数，
// 并在开启强制非流式时把 stream 写成 false。
// JSON 不能只看 Content-Type：有的客户端不带或标错类型，正文仍是 JSON 对象。
// 只在模型、参数或 stream 实际变化时重建正文，避免无意义地改 boundary 或字段顺序。
func applyImagesModelMapping(contentType string, bodyBytes []byte, upstream *config.UpstreamConfig) ([]byte, string, error) {
	var rawParams map[string]string
	forceNonStream := false
	if upstream != nil {
		rawParams = upstream.ImagesExtraParams
		forceNonStream = upstream.ImagesForceNonStream
	}
	extra, err := config.ParseImagesExtraParams(rawParams)
	if err != nil {
		return nil, "", err
	}

	if boundary, ok := utils.MultipartBoundary(contentType); ok {
		return applyImagesMultipartOverrides(boundary, contentType, bodyBytes, upstream, extra, forceNonStream)
	}

	payload, ok, err := readImagesJSONObject(contentType, bodyBytes)
	if err != nil {
		return nil, "", err
	}
	if ok {
		return applyImagesJSONOverrides(contentType, bodyBytes, payload, upstream, extra, forceNonStream)
	}
	if len(extra) > 0 {
		return nil, "", &config.ConfigError{Message: "Images 自定义参数只能写入 JSON 或 multipart 请求"}
	}
	if forceNonStream {
		return nil, "", &config.ConfigError{Message: "强制非流式只能写入 JSON 或 multipart 请求"}
	}
	return bodyBytes, contentType, nil
}

func applyImagesJSONOverrides(contentType string, bodyBytes []byte, payload map[string]interface{}, upstream *config.UpstreamConfig, extra map[string]interface{}, forceNonStream bool) ([]byte, string, error) {
	changed, err := rewriteImagesJSONModel(payload, upstream)
	if err != nil {
		return nil, "", err
	}
	if injectImagesExtraParams(payload, extra) {
		changed = true
	}
	if forceNonStream && applyImagesForceNonStreamJSON(payload) {
		changed = true
	}
	if !changed {
		return bodyBytes, contentType, nil
	}
	mappedBody, err := utils.MarshalJSONNoEscape(payload)
	if err != nil {
		return nil, "", err
	}
	if !contentTypeExpectsJSON(contentType) {
		contentType = "application/json"
	}
	return mappedBody, contentType, nil
}

func rewriteImagesJSONModel(payload map[string]interface{}, upstream *config.UpstreamConfig) (bool, error) {
	model := ""
	if raw, exists := payload["model"]; exists {
		text, ok := raw.(string)
		if !ok {
			return false, fmt.Errorf("Images 参数 model 必须是字符串")
		}
		model = text
	}
	resolved, changed := resolveImagesUpstreamModel(model, upstream)
	if !changed {
		return false, nil
	}
	payload["model"] = resolved
	logImagesModelMapping(model, resolved)
	return true, nil
}

// injectImagesExtraParams 把渠道配置的自定义参数写入请求，同名字段以渠道配置为准，覆盖客户端传入的值。
func injectImagesExtraParams(payload map[string]interface{}, extra map[string]interface{}) bool {
	changed := false
	for key, value := range extra {
		if existing, exists := payload[key]; exists && reflect.DeepEqual(existing, value) {
			continue
		}
		payload[key] = value
		changed = true
	}
	return changed
}

// applyImagesForceNonStreamJSON 把 stream 写成布尔 false，并去掉对非流式无意义的 partial_images。
// 缺字段也补上，避免上游按默认值走流式。
func applyImagesForceNonStreamJSON(payload map[string]interface{}) bool {
	changed := false
	if !imagesJSONBoolFalse(payload["stream"]) {
		payload["stream"] = false
		changed = true
	}
	if _, exists := payload["partial_images"]; exists {
		delete(payload, "partial_images")
		changed = true
	}
	return changed
}

func imagesJSONBoolFalse(value interface{}) bool {
	flag, ok := value.(bool)
	return ok && !flag
}

func applyImagesMultipartOverrides(boundary, contentType string, bodyBytes []byte, upstream *config.UpstreamConfig, extra map[string]interface{}, forceNonStream bool) ([]byte, string, error) {
	metadata := extractImagesRequestMetadata(contentType, bodyBytes)
	resolved, modelChanged := resolveImagesUpstreamModel(metadata.Model, upstream)
	if !modelChanged && len(extra) == 0 && !forceNonStream {
		return bodyBytes, contentType, nil
	}

	parts, err := utils.ParseMultipartParts(bodyBytes, boundary)
	if err != nil {
		return nil, "", fmt.Errorf("解析 Images multipart 请求体失败: %w", err)
	}
	changed := false
	if modelChanged {
		updated, wrote, err := rewriteImagesMultipartModel(parts, resolved, metadata.Model)
		if err != nil {
			return nil, "", err
		}
		parts = updated
		changed = wrote
	}
	updated, wrote, err := appendImagesMultipartParams(parts, extra)
	if err != nil {
		return nil, "", err
	}
	parts = updated
	if wrote {
		changed = true
	}
	if forceNonStream {
		updated, wrote, err = rewriteImagesMultipartStreamFalse(parts)
		if err != nil {
			return nil, "", err
		}
		parts = updated
		if wrote {
			changed = true
		}
		updated, wrote = stripImagesMultipartPartialImages(parts)
		parts = updated
		if wrote {
			changed = true
		}
	}
	if !changed {
		return bodyBytes, contentType, nil
	}
	mappedBody, mappedContentType, err := utils.EncodeMultipartParts(parts)
	if err != nil {
		return nil, "", fmt.Errorf("重建 Images multipart 请求体失败: %w", err)
	}
	return mappedBody, mappedContentType, nil
}

func rewriteImagesMultipartModel(parts []utils.MultipartPart, resolved, requested string) ([]utils.MultipartPart, bool, error) {
	found := false
	for index := range parts {
		// 带 filename 的部件是上传文件，即使字段名恰好叫 model 也不是模型名。
		if parts[index].FormName != "model" || parts[index].FileName != "" {
			continue
		}
		found = true
		if strings.TrimSpace(string(parts[index].Content)) == resolved {
			continue
		}
		parts[index].Content = []byte(resolved)
		logImagesModelMapping(requested, resolved)
		return parts, true, nil
	}
	if found {
		return parts, false, nil
	}
	part, err := newImagesTextPart("model", resolved)
	if err != nil {
		return nil, false, err
	}
	logImagesModelMapping(requested, resolved)
	return append(parts, part), true, nil
}

func rewriteImagesMultipartStreamFalse(parts []utils.MultipartPart) ([]utils.MultipartPart, bool, error) {
	found := false
	for index := range parts {
		if parts[index].FormName != "stream" || parts[index].FileName != "" {
			continue
		}
		found = true
		if strings.TrimSpace(string(parts[index].Content)) == "false" {
			return parts, false, nil
		}
		parts[index].Content = []byte("false")
		return parts, true, nil
	}
	if found {
		return parts, false, nil
	}
	part, err := newImagesTextPart("stream", "false")
	if err != nil {
		return nil, false, err
	}
	return append(parts, part), true, nil
}

func stripImagesMultipartPartialImages(parts []utils.MultipartPart) ([]utils.MultipartPart, bool) {
	filtered := parts[:0]
	changed := false
	for _, part := range parts {
		if part.FormName == "partial_images" && part.FileName == "" {
			changed = true
			continue
		}
		filtered = append(filtered, part)
	}
	return filtered, changed
}

func appendImagesMultipartParams(parts []utils.MultipartPart, extra map[string]interface{}) ([]utils.MultipartPart, bool, error) {
	if len(extra) == 0 {
		return parts, false, nil
	}
	keys := make([]string, 0, len(extra))
	for key := range extra {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	changed := false
	for _, key := range keys {
		text, err := formatImagesExtraParamText(extra[key])
		if err != nil {
			return nil, false, err
		}
		// 同名文本字段全部改写为渠道配置值；同名上传文件无法用文本覆盖，明确报错而不是并存两个字段。
		found := false
		for index := range parts {
			if parts[index].FormName != key {
				continue
			}
			if parts[index].FileName != "" {
				return nil, false, &config.ConfigError{Message: fmt.Sprintf("Images 自定义参数 %s 与上传文件字段同名", key)}
			}
			found = true
			if string(parts[index].Content) != text {
				parts[index].Content = []byte(text)
				changed = true
			}
		}
		if found {
			continue
		}
		part, err := newImagesTextPart(key, text)
		if err != nil {
			return nil, false, err
		}
		parts = append(parts, part)
		changed = true
	}
	return parts, changed, nil
}

func formatImagesExtraParamText(value interface{}) (string, error) {
	if text, ok := value.(string); ok {
		return text, nil
	}
	encoded, err := utils.MarshalJSONNoEscape(value)
	if err != nil {
		return "", fmt.Errorf("编码 Images 自定义参数失败: %w", err)
	}
	return string(encoded), nil
}

func newImagesTextPart(name, value string) (utils.MultipartPart, error) {
	if strings.ContainsAny(name, "\r\n") {
		return utils.MultipartPart{}, &config.ConfigError{Message: "Images 自定义参数名不能包含换行"}
	}
	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition", fmt.Sprintf(`form-data; name="%s"`, escapeMultipartName(name)))
	return utils.MultipartPart{
		Header:   header,
		FormName: name,
		Content:  []byte(value),
	}, nil
}

func escapeMultipartName(name string) string {
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(name)
}

func readImagesJSONObject(contentType string, body []byte) (map[string]interface{}, bool, error) {
	expectsJSON := contentTypeExpectsJSON(contentType)
	trimmed := bytes.TrimSpace(bytes.TrimPrefix(bytes.TrimSpace(body), []byte{0xEF, 0xBB, 0xBF}))
	if len(trimmed) == 0 || trimmed[0] != '{' {
		if expectsJSON && len(bytes.TrimSpace(body)) > 0 {
			return nil, false, fmt.Errorf("解析 Images 请求体失败: 请求体不是 JSON 对象")
		}
		return nil, false, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.UseNumber()
	var payload map[string]interface{}
	if err := decoder.Decode(&payload); err != nil {
		if expectsJSON {
			return nil, false, fmt.Errorf("解析 Images 请求体失败: %w", err)
		}
		return nil, false, nil
	}
	if err := decoder.Decode(new(interface{})); err != io.EOF {
		if expectsJSON {
			return nil, false, fmt.Errorf("Images 请求必须是单个 JSON 对象")
		}
		return nil, false, nil
	}
	if payload == nil {
		if expectsJSON {
			return nil, false, fmt.Errorf("解析 Images 请求体失败: 请求体不是 JSON 对象")
		}
		return nil, false, nil
	}
	return payload, true, nil
}

func contentTypeExpectsJSON(contentType string) bool {
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return false
	}
	return strings.Contains(strings.ToLower(mediaType), "json")
}
