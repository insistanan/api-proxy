package config

import (
	"strings"
	"time"
)

// ValidateImagesUpstream 保证图片渠道的协议选择与实际适配能力一致。
// openai 沿用旧配置值，在 Images 分组中表示 Images API。
func ValidateImagesUpstream(upstream *UpstreamConfig) error {
	if upstream == nil {
		return &ConfigError{Message: "Images 渠道配置不能为空"}
	}
	normalized, err := normalizeImagesExtraParams(upstream.ImagesExtraParams)
	if err != nil {
		return err
	}
	upstream.ImagesExtraParams = normalized
	switch upstream.ServiceType {
	case "openai":
		return nil
	case "responses":
		if strings.TrimSpace(upstream.ResponsesModel) == "" {
			return &ConfigError{Message: "Responses 图片渠道必须配置 Responses 主模型"}
		}
		return nil
	default:
		return &ConfigError{Message: "Images 渠道的上游协议仅支持 Images API（openai）或 Responses API（responses）"}
	}
}

// ============== Images 渠道方法（薄委托） ==============

func (cm *ConfigManager) GetCurrentImagesUpstream() (*UpstreamConfig, error) {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	return getFirstActive(cm.config.ImagesUpstream, "Images")
}

func (cm *ConfigManager) GetCurrentImagesUpstreamWithIndex() (*UpstreamConfig, int, error) {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	return getFirstActiveWithIndex(cm.config.ImagesUpstream, "Images")
}

func (cm *ConfigManager) GetCurrentImagesUpstreamWithIndexForModel(model string) (*UpstreamConfig, int, error) {
	return cm.currentChannelForModel("images", model)
}

func (cm *ConfigManager) AddImagesUpstream(upstream UpstreamConfig) error {
	_, err := cm.AddImagesUpstreamWithResult(upstream)
	return err
}

func (cm *ConfigManager) AddImagesUpstreamWithResult(upstream UpstreamConfig) (AddedUpstream, error) {
	return cm.addChannel("images", upstream)
}

func (cm *ConfigManager) UpdateImagesUpstream(index int, updates UpstreamUpdate) (shouldResetMetrics bool, err error) {
	return cm.updateChannel("images", index, updates)
}

func (cm *ConfigManager) RemoveImagesUpstream(index int) (*UpstreamConfig, error) {
	return cm.removeChannel("images", index)
}

func (cm *ConfigManager) AddImagesAPIKey(index int, apiKey string) error {
	return cm.addChannelKey("images", index, apiKey)
}

func (cm *ConfigManager) RemoveImagesAPIKey(index int, apiKey string) error {
	return cm.removeChannelKey("images", index, apiKey)
}

// GetNextImagesAPIKey 获取下一个 API 密钥
func (cm *ConfigManager) GetNextImagesAPIKey(upstream *UpstreamConfig, failedKeys map[string]bool) (string, error) {
	return cm.GetNextAPIKey(upstream, failedKeys, "Images")
}

func (cm *ConfigManager) SetImagesLoadBalance(strategy string) error {
	return cm.setLoadBalanceFor("images", strategy)
}

func (cm *ConfigManager) MoveImagesAPIKeyToTop(upstreamIndex int, apiKey string) error {
	return cm.moveChannelKeyTop("images", upstreamIndex, apiKey)
}

func (cm *ConfigManager) MoveImagesAPIKeyToBottom(upstreamIndex int, apiKey string) error {
	return cm.moveChannelKeyBottom("images", upstreamIndex, apiKey)
}

func (cm *ConfigManager) ReorderImagesUpstreams(order []int) error {
	return cm.reorderChannels("images", order)
}

func (cm *ConfigManager) SetImagesChannelStatus(index int, status string) error {
	return cm.setChannelStatusFor("images", index, status)
}

func (cm *ConfigManager) SetImagesChannelPromotion(index int, duration time.Duration, count int) error {
	return cm.setChannelPromotionFor("images", index, duration, count)
}

func (cm *ConfigManager) GetPromotedImagesChannel() (int, bool) {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	return getPromotedOp(cm.config.ImagesUpstream)
}
