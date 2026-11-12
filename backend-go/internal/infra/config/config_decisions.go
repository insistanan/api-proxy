package config

import (
	"strings"
	"time"
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

// ============== Decisions 渠道方法（薄委托） ==============

func (cm *ConfigManager) GetCurrentDecisionsUpstreamWithIndexForModel(model string) (*UpstreamConfig, int, error) {
	return cm.currentChannelForModel("decisions", model)
}

func (cm *ConfigManager) AddDecisionsUpstreamWithResult(upstream UpstreamConfig) (AddedUpstream, error) {
	return cm.addChannel("decisions", upstream)
}

func (cm *ConfigManager) UpdateDecisionsUpstream(index int, updates UpstreamUpdate) (shouldResetMetrics bool, err error) {
	return cm.updateChannel("decisions", index, updates)
}

func (cm *ConfigManager) RemoveDecisionsUpstream(index int) (*UpstreamConfig, error) {
	return cm.removeChannel("decisions", index)
}

func (cm *ConfigManager) AddDecisionsAPIKey(index int, apiKey string) error {
	return cm.addChannelKey("decisions", index, apiKey)
}

func (cm *ConfigManager) RemoveDecisionsAPIKey(index int, apiKey string) error {
	return cm.removeChannelKey("decisions", index, apiKey)
}

func (cm *ConfigManager) MoveDecisionsAPIKeyToTop(upstreamIndex int, apiKey string) error {
	return cm.moveChannelKeyTop("decisions", upstreamIndex, apiKey)
}

func (cm *ConfigManager) MoveDecisionsAPIKeyToBottom(upstreamIndex int, apiKey string) error {
	return cm.moveChannelKeyBottom("decisions", upstreamIndex, apiKey)
}

func (cm *ConfigManager) ReorderDecisionsUpstreams(order []int) error {
	return cm.reorderChannels("decisions", order)
}

func (cm *ConfigManager) SetDecisionsChannelStatus(index int, status string) error {
	return cm.setChannelStatusFor("decisions", index, status)
}

func (cm *ConfigManager) SetDecisionsChannelPromotion(index int, duration time.Duration, count int) error {
	return cm.setChannelPromotionFor("decisions", index, duration, count)
}

func (cm *ConfigManager) SetDecisionsLoadBalance(strategy string) error {
	return cm.setLoadBalanceFor("decisions", strategy)
}
