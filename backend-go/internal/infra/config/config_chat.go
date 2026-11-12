package config

import (
	"time"
)

// ============== Chat 渠道方法（薄委托） ==============

func (cm *ConfigManager) GetCurrentChatUpstream() (*UpstreamConfig, error) {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	return getFirstActive(cm.config.ChatUpstream, "Chat")
}

func (cm *ConfigManager) GetCurrentChatUpstreamWithIndex() (*UpstreamConfig, int, error) {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	return getFirstActiveWithIndex(cm.config.ChatUpstream, "Chat")
}

func (cm *ConfigManager) GetCurrentChatUpstreamWithIndexForModel(model string) (*UpstreamConfig, int, error) {
	return cm.currentChannelForModel("chat", model)
}

func (cm *ConfigManager) AddChatUpstream(upstream UpstreamConfig) error {
	_, err := cm.AddChatUpstreamWithResult(upstream)
	return err
}

func (cm *ConfigManager) AddChatUpstreamWithResult(upstream UpstreamConfig) (AddedUpstream, error) {
	return cm.addChannel("chat", upstream)
}

func (cm *ConfigManager) UpdateChatUpstream(index int, updates UpstreamUpdate) (shouldResetMetrics bool, err error) {
	return cm.updateChannel("chat", index, updates)
}

func (cm *ConfigManager) RemoveChatUpstream(index int) (*UpstreamConfig, error) {
	return cm.removeChannel("chat", index)
}

func (cm *ConfigManager) AddChatAPIKey(index int, apiKey string) error {
	return cm.addChannelKey("chat", index, apiKey)
}

func (cm *ConfigManager) RemoveChatAPIKey(index int, apiKey string) error {
	return cm.removeChannelKey("chat", index, apiKey)
}

// GetNextChatAPIKey 获取下一个 API 密钥
func (cm *ConfigManager) GetNextChatAPIKey(upstream *UpstreamConfig, failedKeys map[string]bool) (string, error) {
	return cm.GetNextAPIKey(upstream, failedKeys, "Chat")
}

func (cm *ConfigManager) SetChatLoadBalance(strategy string) error {
	return cm.setLoadBalanceFor("chat", strategy)
}

func (cm *ConfigManager) MoveChatAPIKeyToTop(upstreamIndex int, apiKey string) error {
	return cm.moveChannelKeyTop("chat", upstreamIndex, apiKey)
}

func (cm *ConfigManager) MoveChatAPIKeyToBottom(upstreamIndex int, apiKey string) error {
	return cm.moveChannelKeyBottom("chat", upstreamIndex, apiKey)
}

func (cm *ConfigManager) ReorderChatUpstreams(order []int) error {
	return cm.reorderChannels("chat", order)
}

func (cm *ConfigManager) SetChatChannelStatus(index int, status string) error {
	return cm.setChannelStatusFor("chat", index, status)
}

func (cm *ConfigManager) SetChatChannelPromotion(index int, duration time.Duration, count int) error {
	return cm.setChannelPromotionFor("chat", index, duration, count)
}

func (cm *ConfigManager) GetPromotedChatChannel() (int, bool) {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	return getPromotedOp(cm.config.ChatUpstream)
}
