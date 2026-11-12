package config

import (
	"strings"
)

// ValidateDecisionsUpstream 校验决策渠道。该协议按请求原样透传到
// {baseUrl}/v1/decisions，上游采用 Bearer 鉴权的 JSON 接口（openai 渠道类型）。
func ValidateDecisionsUpstream(upstream *UpstreamConfig) error {
	if upstream == nil {
		return &ConfigError{Message: "Decisions 渠道配置不能为空"}
	}
	upstream.ServiceType = strings.TrimSpace(upstream.ServiceType)
	switch upstream.ServiceType {
	case "", "openai":
		upstream.ServiceType = "openai"
		return nil
	default:
		return &ConfigError{Message: "Decisions 渠道的上游协议仅支持 OpenAI 兼容 JSON 接口（openai）"}
	}
}

func (cm *ConfigManager) AddDecisionsUpstreamWithResult(upstream UpstreamConfig) (AddedUpstream, error) {
	return cm.AddChannel("decisions", upstream)
}
