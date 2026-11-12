package config

import (
	"time"
)

// ============== Gemini 渠道方法（薄委托） ==============

func (cm *ConfigManager) GetCurrentGeminiUpstream() (*UpstreamConfig, error) {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	return getFirstActive(cm.config.GeminiUpstream, "Gemini")
}

func (cm *ConfigManager) GetCurrentGeminiUpstreamWithIndex() (*UpstreamConfig, int, error) {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	return getFirstActiveWithIndex(cm.config.GeminiUpstream, "Gemini")
}

func (cm *ConfigManager) GetCurrentGeminiUpstreamWithIndexForModel(model string) (*UpstreamConfig, int, error) {
	return cm.currentChannelForModel("gemini", model)
}

func (cm *ConfigManager) AddGeminiUpstream(upstream UpstreamConfig) error {
	_, err := cm.AddGeminiUpstreamWithResult(upstream)
	return err
}

func (cm *ConfigManager) AddGeminiUpstreamWithResult(upstream UpstreamConfig) (AddedUpstream, error) {
	return cm.addChannel("gemini", upstream)
}

func (cm *ConfigManager) UpdateGeminiUpstream(index int, updates UpstreamUpdate) (shouldResetMetrics bool, err error) {
	return cm.updateChannel("gemini", index, updates)
}

func (cm *ConfigManager) RemoveGeminiUpstream(index int) (*UpstreamConfig, error) {
	return cm.removeChannel("gemini", index)
}

func (cm *ConfigManager) AddGeminiAPIKey(index int, apiKey string) error {
	return cm.addChannelKey("gemini", index, apiKey)
}

func (cm *ConfigManager) RemoveGeminiAPIKey(index int, apiKey string) error {
	return cm.removeChannelKey("gemini", index, apiKey)
}

// GetNextGeminiAPIKey 获取下一个 API 密钥
func (cm *ConfigManager) GetNextGeminiAPIKey(upstream *UpstreamConfig, failedKeys map[string]bool) (string, error) {
	return cm.GetNextAPIKey(upstream, failedKeys, "Gemini")
}

func (cm *ConfigManager) MoveGeminiAPIKeyToTop(upstreamIndex int, apiKey string) error {
	return cm.moveChannelKeyTop("gemini", upstreamIndex, apiKey)
}

func (cm *ConfigManager) MoveGeminiAPIKeyToBottom(upstreamIndex int, apiKey string) error {
	return cm.moveChannelKeyBottom("gemini", upstreamIndex, apiKey)
}

func (cm *ConfigManager) ReorderGeminiUpstreams(order []int) error {
	return cm.reorderChannels("gemini", order)
}

func (cm *ConfigManager) SetGeminiChannelStatus(index int, status string) error {
	return cm.setChannelStatusFor("gemini", index, status)
}

func (cm *ConfigManager) SetGeminiChannelPromotion(index int, duration time.Duration, count int) error {
	return cm.setChannelPromotionFor("gemini", index, duration, count)
}

func (cm *ConfigManager) GetPromotedGeminiChannel() (int, bool) {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	return getPromotedOp(cm.config.GeminiUpstream)
}

func (cm *ConfigManager) SetGeminiLoadBalance(strategy string) error {
	return cm.setLoadBalanceFor("gemini", strategy)
}
