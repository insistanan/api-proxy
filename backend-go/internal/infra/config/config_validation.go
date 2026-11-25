package config

import "fmt"

// validateConfig 是磁盘加载和管理写入共用的完整校验入口。
// 加载阶段先补齐缺失字段和转换旧格式；两条入口共用现有校验及其归一化规则。
func validateConfig(candidate *Config) error {
	if err := validateSettings(candidate.Settings); err != nil {
		return err
	}
	if err := validateFuzzyModeProtocols(candidate); err != nil {
		return err
	}
	if err := validateConcurrency(candidate); err != nil {
		return err
	}
	if err := validateIntentRouting(candidate); err != nil {
		return err
	}
	for _, set := range candidate.channelSets() {
		upstreams := *set.upstreams
		for index := range upstreams {
			upstream := &upstreams[index]
			if GetChannelStatus(upstream) == ChannelStatusDeleted {
				continue
			}
			if err := validateUpstreamProxyConfig(upstream); err != nil {
				return fmt.Errorf("%s 渠道 %q 配置无效: %w", set.name, upstream.Name, err)
			}
			if err := validateChannelProtocol(set.kind, upstream); err != nil {
				return fmt.Errorf("%s 渠道 %q 配置无效: %w", set.name, upstream.Name, err)
			}
			if err := ValidateCacheKeepalive(set.kind, upstream); err != nil {
				return fmt.Errorf("%s 渠道 %q 配置无效: %w", set.name, upstream.Name, err)
			}
		}
		if err := validateAllModelMappingChannels(upstreams); err != nil {
			return err
		}
	}
	return validateAllVisionLayerConfigs(candidate, "", nil)
}

func validateChannelProtocol(kind string, upstream *UpstreamConfig) error {
	switch kind {
	case "images":
		return ValidateImagesUpstream(upstream)
	case "embeddings":
		return ValidateEmbeddingsUpstream(upstream)
	case "decisions":
		return ValidateDecisionsUpstream(upstream)
	default:
		return nil
	}
}

func validateSettings(settings SettingsConfig) error {
	if err := ValidateGatewaySettings(settings.Gateway); err != nil {
		return err
	}
	if err := ValidateDisguiseSettings(settings.Disguise); err != nil {
		return err
	}
	if err := ValidateProxyURL(settings.Network.UpstreamProxyURL); err != nil {
		return err
	}
	if err := ValidateContentSafetyConfig(settings.ContentSafety); err != nil {
		return fmt.Errorf("内容安全配置无效: %w", err)
	}
	if err := ValidatePromptSettings(settings.Prompts); err != nil {
		return err
	}
	return ValidateExtensionsSettings(settings.Extensions)
}
