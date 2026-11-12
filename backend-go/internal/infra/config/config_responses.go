package config

import (
	"time"
)

func (cm *ConfigManager) GetCurrentResponsesUpstreamWithIndexForModel(model string) (*UpstreamConfig, int, error) {
	return cm.CurrentChannelForModel("responses", model)
}

func (cm *ConfigManager) AddResponsesUpstream(upstream UpstreamConfig) error {
	_, err := cm.AddResponsesUpstreamWithResult(upstream)
	return err
}

func (cm *ConfigManager) AddResponsesUpstreamWithResult(upstream UpstreamConfig) (AddedUpstream, error) {
	return cm.AddChannel("responses", upstream)
}

func (cm *ConfigManager) UpdateResponsesUpstream(index int, updates UpstreamUpdate) (shouldResetMetrics bool, err error) {
	return cm.UpdateChannel("responses", index, updates)
}

// GetNextResponsesAPIKey 获取下一个 API 密钥
func (cm *ConfigManager) GetNextResponsesAPIKey(upstream *UpstreamConfig, failedKeys map[string]bool) (string, error) {
	return cm.GetNextAPIKey(upstream, failedKeys, "Responses")
}

func (cm *ConfigManager) SetResponsesChannelStatus(index int, status string) error {
	return cm.SetChannelStatusFor("responses", index, status)
}

func (cm *ConfigManager) SetResponsesChannelPromotion(index int, duration time.Duration, count int) error {
	return cm.SetChannelPromotionFor("responses", index, duration, count)
}
