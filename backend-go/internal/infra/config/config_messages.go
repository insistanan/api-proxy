package config

import (
	"log"
	"time"
)

func (cm *ConfigManager) AddUpstream(upstream UpstreamConfig) error {
	_, err := cm.AddUpstreamWithResult(upstream)
	return err
}

func (cm *ConfigManager) AddUpstreamWithResult(upstream UpstreamConfig) (AddedUpstream, error) {
	return cm.AddChannel("messages", upstream)
}

func (cm *ConfigManager) UpdateUpstream(index int, updates UpstreamUpdate) (shouldResetMetrics bool, err error) {
	return cm.UpdateChannel("messages", index, updates)
}

func (cm *ConfigManager) AddAPIKey(index int, apiKey string) error {
	return cm.AddChannelKey("messages", index, apiKey)
}

func (cm *ConfigManager) MoveAPIKeyToBottom(upstreamIndex int, apiKey string) error {
	return cm.MoveChannelKeyBottom("messages", upstreamIndex, apiKey)
}

func (cm *ConfigManager) SetChannelStatus(index int, status string) error {
	return cm.SetChannelStatusFor("messages", index, status)
}

// ConsumePromotionCount 消费促销请求次数（多 kind 共享方法）
func (cm *ConfigManager) ConsumePromotionCount(channelIndex int, channelType string) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	candidate := cloneConfig(cm.config)
	cs, err := candidate.channelSetFor(channelType)
	if err != nil {
		log.Printf("[Config-Promotion] 消费促销次数失败: %v", err)
		return
	}
	if consumePromotionOp(*cs.upstreams, channelIndex, channelType) {
		if err := cm.saveConfigLocked(candidate); err != nil {
			log.Printf("[Config-Promotion] 保存促销次数失败: %v", err)
		}
	}
}

func (cm *ConfigManager) SetChannelPromotion(index int, duration time.Duration, count int) error {
	return cm.SetChannelPromotionFor("messages", index, duration, count)
}
