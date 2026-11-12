package config

import (
	"time"
)

func (cm *ConfigManager) AddChatUpstream(upstream UpstreamConfig) error {
	_, err := cm.AddChatUpstreamWithResult(upstream)
	return err
}

func (cm *ConfigManager) AddChatUpstreamWithResult(upstream UpstreamConfig) (AddedUpstream, error) {
	return cm.AddChannel("chat", upstream)
}

func (cm *ConfigManager) UpdateChatUpstream(index int, updates UpstreamUpdate) (shouldResetMetrics bool, err error) {
	return cm.UpdateChannel("chat", index, updates)
}

// GetNextChatAPIKey 获取下一个 API 密钥
func (cm *ConfigManager) GetNextChatAPIKey(upstream *UpstreamConfig, failedKeys map[string]bool) (string, error) {
	return cm.GetNextAPIKey(upstream, failedKeys, "Chat")
}

func (cm *ConfigManager) SetChatChannelPromotion(index int, duration time.Duration, count int) error {
	return cm.SetChannelPromotionFor("chat", index, duration, count)
}
