package config

import (
	"fmt"
	"net/url"
	"strings"
)

// validateConfig 是磁盘加载和管理写入共用的完整校验入口。
// 加载阶段先补齐缺失字段和转换旧格式；两条入口共用现有校验及其归一化规则。
func validateConfig(candidate *Config) (err error) {
	defer func() {
		if err != nil && !IsConfigError(err) {
			err = &ConfigError{Message: err.Error(), Cause: err}
		}
	}()
	if candidate.SchemaVersion != currentConfigSchema {
		return proxyConfigErrorf("不支持的配置 schemaVersion: %d", candidate.SchemaVersion)
	}
	if candidate.CurrentUpstream != 0 || candidate.CurrentResponsesUpstream != 0 {
		return proxyConfigErrorf("currentUpstream/currentResponsesUpstream 已退役，请使用渠道 status")
	}
	seenIDs := make(map[string]struct{})
	for _, set := range candidate.channelSets() {
		if *set.loadBalance == "" {
			*set.loadBalance = "failover"
		}
		if err := validateLoadBalanceStrategy(*set.loadBalance); err != nil {
			return err
		}
		for _, upstream := range *set.upstreams {
			id := strings.TrimSpace(upstream.ID)
			if id == "" {
				return proxyConfigErrorf("%s 渠道 %q 缺少稳定 ID", set.name, upstream.Name)
			}
			if _, exists := seenIDs[id]; exists {
				return proxyConfigErrorf("渠道 ID %q 重复，ID 必须跨协议唯一", id)
			}
			seenIDs[id] = struct{}{}
		}
	}
	validateChannelKeysIn(candidate)
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
	if err := validateSafetyChannelReferences(candidate); err != nil {
		return err
	}
	for _, set := range candidate.channelSets() {
		upstreams := *set.upstreams
		for index := range upstreams {
			upstream := &upstreams[index]
			if err := validateChannelBasics(set.kind, upstream); err != nil {
				return fmt.Errorf("%s 渠道 %q 配置无效: %w", set.name, upstream.Name, err)
			}
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

func validateChannelBasics(kind string, upstream *UpstreamConfig) error {
	switch upstream.Status {
	case ChannelStatusDeleted:
		return nil
	case ChannelStatusActive, ChannelStatusDisabled, ChannelStatusSuspended, ChannelStatusDeprecated:
	default:
		return proxyConfigErrorf("无效的渠道状态 %q", upstream.Status)
	}
	// 省略协议采用所在入口的原生协议；显式未知值始终拒绝。
	if upstream.ServiceType == "" {
		switch kind {
		case "messages":
			upstream.ServiceType = "claude"
		case "responses", "gemini":
			upstream.ServiceType = kind
		default:
			upstream.ServiceType = "openai"
		}
	}
	switch upstream.ServiceType {
	case "claude", "openai", "responses", "gemini":
	default:
		return proxyConfigErrorf("不支持的上游协议 %q", upstream.ServiceType)
	}
	urls := upstream.GetAllBaseURLs()
	if len(upstream.BaseURLs) > 0 && upstream.BaseURL != "" {
		urls = append(append([]string{}, urls...), upstream.BaseURL)
	}
	if len(urls) == 0 {
		return proxyConfigErrorf("至少需要一个上游地址")
	}
	for _, address := range urls {
		parsed, err := url.Parse(address)
		if err != nil || strings.TrimSpace(address) != address || parsed == nil || parsed.Hostname() == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Fragment != "" {
			return proxyConfigErrorf("上游地址必须是有效的 HTTP(S) URL，不能包含空白或 fragment")
		}
	}
	for _, key := range upstream.APIKeys {
		// 内置免费池使用明确的匿名凭据槽位。
		if upstream.BuiltinProvider == "kilo" && key == "" {
			continue
		}
		if strings.TrimSpace(key) == "" || strings.ContainsAny(key, "\r\n") {
			return proxyConfigErrorf("API Key 不能为空白或包含换行")
		}
	}
	return nil
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
