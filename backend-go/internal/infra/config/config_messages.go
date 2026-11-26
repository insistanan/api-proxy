package config

import "log"

// ConsumePromotionCount 消费促销请求次数（多 kind 共享方法）
func (cm *ConfigManager) ConsumePromotionCount(channelIndex int, channelType string) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	cs, err := cm.config.channelSetFor(channelType)
	if err != nil {
		log.Printf("[Config-Promotion] 消费促销次数失败: %v", err)
		return
	}
	consumePromotionOp(*cs.upstreams, channelIndex, channelType)
}

// 按次数促销属于进程运行态。混合限制也整体结束，避免重启后变成仅按时间促销。
func clearRuntimePromotions(cfg *Config) bool {
	changed := false
	for _, set := range cfg.channelSets() {
		for i := range *set.upstreams {
			channel := &(*set.upstreams)[i]
			if channel.PromotionCount > 0 {
				channel.PromotionCount = 0
				channel.PromotionUntil = nil
				changed = true
			}
		}
	}
	return changed
}

// 外部配置重载不消耗运行中的额度；稳定 ID 消失或显式设置时间促销时不再继承。
func inheritRuntimePromotions(next *Config, current Config) {
	active := make(map[string]UpstreamConfig)
	for _, set := range current.channelSets() {
		for _, channel := range *set.upstreams {
			if channel.PromotionCount > 0 && IsChannelInPromotion(&channel) {
				active[channel.ID] = channel
			}
		}
	}
	for _, set := range next.channelSets() {
		for i := range *set.upstreams {
			channel := &(*set.upstreams)[i]
			previous, ok := active[channel.ID]
			if ok && channel.PromotionUntil == nil && GetChannelStatus(channel) != ChannelStatusDeleted {
				channel.PromotionCount = previous.PromotionCount
				channel.PromotionUntil = previous.PromotionUntil
			}
		}
	}
}
