package config

import (
	"strings"
)

// ValidateImagesUpstream 保证图片渠道的协议选择与实际适配能力一致。
// openai 沿用旧配置值，在 Images 分组中表示 Images API。
func ValidateImagesUpstream(upstream *UpstreamConfig) error {
	if upstream == nil {
		return &ConfigError{Message: "Images 渠道配置不能为空"}
	}
	normalized, err := normalizeImagesExtraParams(upstream.ImagesExtraParams)
	if err != nil {
		return err
	}
	upstream.ImagesExtraParams = normalized
	upstream.ImagesOutputFormat = strings.TrimSpace(upstream.ImagesOutputFormat)
	if err := validateImagesOutputConfig(upstream); err != nil {
		return err
	}
	switch upstream.ServiceType {
	case "openai":
		return nil
	case "responses":
		if strings.TrimSpace(upstream.ResponsesModel) == "" {
			return &ConfigError{Message: "Responses 图片渠道必须配置 Responses 主模型"}
		}
		return nil
	default:
		return &ConfigError{Message: "Images 渠道的上游协议仅支持 Images API（openai）或 Responses API（responses）"}
	}
}

func validateImagesOutputConfig(upstream *UpstreamConfig) error {
	switch upstream.ImagesOutputFormat {
	case "", "png", "jpeg", "webp", "gif", "svg":
	default:
		return &ConfigError{Message: "Images 输出格式必须为 png、jpeg、webp、gif、svg 或空值（跟随请求）"}
	}
	if upstream.ImagesOutputFormat != "gif" && (upstream.ImagesFrameCount != 0 || upstream.ImagesFrameDurationMS != 0) {
		return &ConfigError{Message: "GIF 帧参数只能在渠道输出格式为 gif 时配置"}
	}
	if count := upstream.ImagesFrameCount; count != 0 && (count < 2 || count > 16) {
		return &ConfigError{Message: "GIF 帧数必须在 2–16 之间"}
	}
	if duration := upstream.ImagesFrameDurationMS; duration != 0 {
		if duration < 20 || (upstream.ServiceType == "responses" && duration > 2000) {
			return &ConfigError{Message: "GIF 帧时长至少为 20 毫秒，Responses 渠道不能超过 2000 毫秒"}
		}
	}
	return nil
}

func (cm *ConfigManager) AddImagesUpstreamWithResult(upstream UpstreamConfig) (AddedUpstream, error) {
	return cm.AddChannel("images", upstream)
}

func (cm *ConfigManager) UpdateImagesUpstream(index int, updates UpstreamUpdate) (shouldResetMetrics bool, err error) {
	return cm.UpdateChannel("images", index, updates)
}

// GetNextImagesAPIKey 获取下一个 API 密钥
func (cm *ConfigManager) GetNextImagesAPIKey(upstream *UpstreamConfig, failedKeys map[string]bool) (string, error) {
	return cm.GetNextAPIKey(upstream, failedKeys, "Images")
}
