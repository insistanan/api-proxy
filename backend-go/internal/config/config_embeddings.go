package config

import (
	"strings"
	"time"
)

// ValidateEmbeddingsUpstream 校验向量嵌入渠道。该协议按 OpenAI Embeddings API
// 原样透传到 {baseUrl}/v1/embeddings，上游协议固定为 openai。
func ValidateEmbeddingsUpstream(upstream *UpstreamConfig) error {
	if upstream == nil {
		return &ConfigError{Message: "Embeddings 渠道配置不能为空"}
	}
	upstream.ServiceType = strings.TrimSpace(upstream.ServiceType)
	switch upstream.ServiceType {
	case "", "openai":
		upstream.ServiceType = "openai"
		return nil
	default:
		return &ConfigError{Message: "Embeddings 渠道的上游协议仅支持 OpenAI Embeddings API（openai）"}
	}
}

// ============== Embeddings 渠道方法（薄委托） ==============

func (cm *ConfigManager) GetCurrentEmbeddingsUpstreamWithIndexForModel(model string) (*UpstreamConfig, int, error) {
	return cm.currentChannelForModel("embeddings", model)
}

func (cm *ConfigManager) AddEmbeddingsUpstreamWithResult(upstream UpstreamConfig) (AddedUpstream, error) {
	return cm.addChannel("embeddings", upstream)
}

func (cm *ConfigManager) UpdateEmbeddingsUpstream(index int, updates UpstreamUpdate) (shouldResetMetrics bool, err error) {
	return cm.updateChannel("embeddings", index, updates)
}

func (cm *ConfigManager) RemoveEmbeddingsUpstream(index int) (*UpstreamConfig, error) {
	return cm.removeChannel("embeddings", index)
}

func (cm *ConfigManager) AddEmbeddingsAPIKey(index int, apiKey string) error {
	return cm.addChannelKey("embeddings", index, apiKey)
}

func (cm *ConfigManager) RemoveEmbeddingsAPIKey(index int, apiKey string) error {
	return cm.removeChannelKey("embeddings", index, apiKey)
}

func (cm *ConfigManager) MoveEmbeddingsAPIKeyToTop(upstreamIndex int, apiKey string) error {
	return cm.moveChannelKeyTop("embeddings", upstreamIndex, apiKey)
}

func (cm *ConfigManager) MoveEmbeddingsAPIKeyToBottom(upstreamIndex int, apiKey string) error {
	return cm.moveChannelKeyBottom("embeddings", upstreamIndex, apiKey)
}

func (cm *ConfigManager) ReorderEmbeddingsUpstreams(order []int) error {
	return cm.reorderChannels("embeddings", order)
}

func (cm *ConfigManager) SetEmbeddingsChannelStatus(index int, status string) error {
	return cm.setChannelStatusFor("embeddings", index, status)
}

func (cm *ConfigManager) SetEmbeddingsChannelPromotion(index int, duration time.Duration, count int) error {
	return cm.setChannelPromotionFor("embeddings", index, duration, count)
}

func (cm *ConfigManager) SetEmbeddingsLoadBalance(strategy string) error {
	return cm.setLoadBalanceFor("embeddings", strategy)
}
