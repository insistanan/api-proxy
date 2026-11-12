package config

import (
	"time"
)

// ============== Responses 渠道方法（薄委托） ==============

func (cm *ConfigManager) GetCurrentResponsesUpstream() (*UpstreamConfig, error) {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	return getFirstActive(cm.config.ResponsesUpstream, "Responses")
}

func (cm *ConfigManager) GetCurrentResponsesUpstreamWithIndex() (*UpstreamConfig, int, error) {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	return getFirstActiveWithIndex(cm.config.ResponsesUpstream, "Responses")
}

func (cm *ConfigManager) GetCurrentResponsesUpstreamWithIndexForModel(model string) (*UpstreamConfig, int, error) {
	return cm.currentChannelForModel("responses", model)
}

func (cm *ConfigManager) AddResponsesUpstream(upstream UpstreamConfig) error {
	_, err := cm.AddResponsesUpstreamWithResult(upstream)
	return err
}

func (cm *ConfigManager) AddResponsesUpstreamWithResult(upstream UpstreamConfig) (AddedUpstream, error) {
	return cm.addChannel("responses", upstream)
}

func (cm *ConfigManager) UpdateResponsesUpstream(index int, updates UpstreamUpdate) (shouldResetMetrics bool, err error) {
	return cm.updateChannel("responses", index, updates)
}

func (cm *ConfigManager) RemoveResponsesUpstream(index int) (*UpstreamConfig, error) {
	return cm.removeChannel("responses", index)
}

func (cm *ConfigManager) AddResponsesAPIKey(index int, apiKey string) error {
	return cm.addChannelKey("responses", index, apiKey)
}

func (cm *ConfigManager) RemoveResponsesAPIKey(index int, apiKey string) error {
	return cm.removeChannelKey("responses", index, apiKey)
}

// GetNextResponsesAPIKey 获取下一个 API 密钥
func (cm *ConfigManager) GetNextResponsesAPIKey(upstream *UpstreamConfig, failedKeys map[string]bool) (string, error) {
	return cm.GetNextAPIKey(upstream, failedKeys, "Responses")
}

func (cm *ConfigManager) SetResponsesLoadBalance(strategy string) error {
	return cm.setLoadBalanceFor("responses", strategy)
}

func (cm *ConfigManager) MoveResponsesAPIKeyToTop(upstreamIndex int, apiKey string) error {
	return cm.moveChannelKeyTop("responses", upstreamIndex, apiKey)
}

func (cm *ConfigManager) MoveResponsesAPIKeyToBottom(upstreamIndex int, apiKey string) error {
	return cm.moveChannelKeyBottom("responses", upstreamIndex, apiKey)
}

func (cm *ConfigManager) ReorderResponsesUpstreams(order []int) error {
	return cm.reorderChannels("responses", order)
}

func (cm *ConfigManager) SetResponsesChannelStatus(index int, status string) error {
	return cm.setChannelStatusFor("responses", index, status)
}

func (cm *ConfigManager) SetResponsesChannelPromotion(index int, duration time.Duration, count int) error {
	return cm.setChannelPromotionFor("responses", index, duration, count)
}

func (cm *ConfigManager) GetPromotedResponsesChannel() (int, bool) {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	return getPromotedOp(cm.config.ResponsesUpstream)
}
