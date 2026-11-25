package toimages

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"insistanan/api-proxy/internal/protocol/contract"
)

// ResponsesRequestOptions 由渠道编排解析模型与输出策略，转换器不依赖渠道配置。
type ResponsesRequestOptions struct {
	ImageModel     string
	ResponsesModel string
	ForceNonStream bool
	IsEdit         bool
}

// ResponsesRequest 将 Images 参数映射为 Responses 的输入与 image_generation 工具。
func ResponsesRequest(payload map[string]interface{}, options ResponsesRequestOptions) (map[string]interface{}, error) {
	prompt, _ := payload["prompt"].(string)
	if strings.TrimSpace(options.ImageModel) == "" || strings.TrimSpace(prompt) == "" {
		return nil, fmt.Errorf("Responses 图片渠道要求 Images 请求包含 model 和 prompt")
	}
	if strings.TrimSpace(options.ResponsesModel) == "" {
		return nil, fmt.Errorf("Responses 图片渠道缺少主模型")
	}
	if format, exists := payload["response_format"]; exists && format != "b64_json" {
		return nil, contract.Unsupported("Responses 图片渠道只能返回 b64_json，无法生成托管图片 URL")
	}
	if stream, exists := payload["stream"]; exists {
		if _, ok := stream.(bool); !ok {
			return nil, fmt.Errorf("Images 参数 stream 必须是布尔值")
		}
	}
	if n, exists := payload["n"]; exists && n != nil {
		count, err := IntegerParameter("n", n)
		if err != nil {
			return nil, err
		}
		if count < 1 {
			return nil, fmt.Errorf("Images 参数 n 必须是正整数")
		}
		if count != 1 {
			return nil, contract.Unsupported("Responses 图片渠道只支持 n=1；多图生成请使用 Images API 渠道")
		}
	}
	// 顶层 model 是 Responses 主模型。真正的生图模型只写进 image_generation 工具。
	// 已知 Images 参数映射到工具；无法表达的字段明确拒绝。
	forceNonStream := options.ForceNonStream
	tool := map[string]interface{}{"type": "image_generation", "model": options.ImageModel}
	for key, value := range payload {
		switch key {
		case "size", "quality", "background", "output_format", "moderation", "input_fidelity", "output_compression", "complexity", "frame_count", "frame_duration_ms", "svg":
			if value != nil {
				tool[key] = value
			}
		case "partial_images":
			if !forceNonStream {
				tool[key] = value
			}
		case "model", "prompt", "stream", "response_format", "images", "image", "mask", "user", "metadata", "safety_identifier", "n":
			// 通用字段在下方分别写入 Responses 请求。
		default:
			return nil, contract.Unsupported("Images 参数 %q 无法转换为 Responses 图片请求", key)
		}
	}
	content, mask, err := responsesInput(payload, prompt, options.IsEdit)
	if err != nil {
		return nil, err
	}
	if mask != nil {
		tool["input_image_mask"] = mask
	}
	if options.IsEdit {
		tool["action"] = "edit"
	} else {
		tool["action"] = "generate"
	}
	if err := ValidateOutputParameters(tool, true); err != nil {
		return nil, err
	}
	result := map[string]interface{}{
		"model": options.ResponsesModel,
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
	if value, exists := payload["safety_identifier"]; exists {
		if value != nil {
			identifier, ok := value.(string)
			if !ok || utf8.RuneCountInString(identifier) > 128 {
				return nil, fmt.Errorf("Images safety_identifier 必须是长度不超过 128 的字符串")
			}
		}
		result["safety_identifier"] = value
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
