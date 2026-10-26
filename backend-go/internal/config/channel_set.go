package config

import (
	"fmt"
	"log"
	"strings"
	"time"
)

// channelSet 把"某一种渠道类型在 Config 里的存放位置"抽象出来，
// 使 CRUD 逻辑不必为每种渠道类型复制一遍。
//
// 注意所有字段都是**指针**：调用方拿到的是 cm.config 里真实切片的地址，
// 赋值 *upstreams = next 才能真正改到配置。
type channelSet struct {
	name        string            // 日志文案，如 "Messages" / "Chat"
	upstreams   *[]UpstreamConfig // 指向 cm.config 的对应切片
	pools       *[]ChannelPool    // 指向 cm.config 的对应池列表
	loadBalance *string           // 指向 cm.config 的对应负载均衡策略
}

// channelSetFor 返回指定渠道类型的存放位置。
// 调用方**必须已持有 cm.mu**（读或写锁，取决于后续操作）。
func (cm *ConfigManager) channelSetFor(kind string) *channelSet {
	switch kind {
	case "responses":
		return &channelSet{"Responses", &cm.config.ResponsesUpstream, &cm.config.ResponsesPools, &cm.config.ResponsesLoadBalance}
	case "gemini":
		return &channelSet{"Gemini", &cm.config.GeminiUpstream, &cm.config.GeminiPools, &cm.config.GeminiLoadBalance}
	case "chat":
		return &channelSet{"Chat", &cm.config.ChatUpstream, &cm.config.ChatPools, &cm.config.ChatLoadBalance}
	case "images":
		return &channelSet{"Images", &cm.config.ImagesUpstream, &cm.config.ImagesPools, &cm.config.ImagesLoadBalance}
	default:
		return &channelSet{"Messages", &cm.config.Upstream, &cm.config.MessagePools, &cm.config.LoadBalance}
	}
}

// =============================================================================
// 通用 CRUD 方法（以 messages 版为行为基准，参数化 kind）
// =============================================================================

// addChannel 添加上游（通用版）。
// chat/images 版额外做了 DefaultModel TrimSpace，此处统一对所有 kind 做（对 messages 无害）。
func (cm *ConfigManager) addChannel(kind string, up UpstreamConfig) (AddedUpstream, error) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	cs := cm.channelSetFor(kind)
	up.DefaultModel = strings.TrimSpace(up.DefaultModel)
	if kind == "images" {
		up.ResponsesModel = strings.TrimSpace(up.ResponsesModel)
		if err := ValidateImagesUpstream(&up); err != nil {
			return AddedUpstream{}, err
		}
	}
	previous := *cs.upstreams
	next, result, err := addValidatedUpstreamOp(previous, *cs.pools, up)
	if err != nil {
		return AddedUpstream{}, err
	}
	*cs.upstreams = next

	if err := cm.saveConfigLocked(cm.config); err != nil {
		*cs.upstreams = previous
		return AddedUpstream{}, err
	}
	log.Printf("[Config-Upstream] 已添加 %s 上游（优先级1）: %s", cs.name, (*cs.upstreams)[result.Index].Name)
	return result, nil
}

// updateChannel 部分更新上游（通用版）。
func (cm *ConfigManager) updateChannel(kind string, index int, updates UpstreamUpdate) (bool, error) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	cs := cm.channelSetFor(kind)
	upstreams := *cs.upstreams

	if index < 0 || index >= len(upstreams) {
		return false, fmt.Errorf("无效的 %s 上游索引: %d", cs.name, index)
	}

	previous := upstreams
	next := cloneUpstreamList(previous)
	shouldReset, err := applyCommonUpdatesToList(next, *cs.pools, index, updates, cs.name)
	if err != nil {
		return false, err
	}
	if kind == "images" {
		if err := ValidateImagesUpstream(&next[index]); err != nil {
			return false, err
		}
	}

	*cs.upstreams = next
	if err := cm.saveConfigLocked(cm.config); err != nil {
		*cs.upstreams = previous
		return false, err
	}
	log.Printf("[Config-Upstream] 已更新 %s 上游: [%d] %s", cs.name, index, (*cs.upstreams)[index].Name)
	return shouldReset, nil
}

// removeChannel 删除上游（通用版）。
func (cm *ConfigManager) removeChannel(kind string, index int) (*UpstreamConfig, error) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	cs := cm.channelSetFor(kind)
	newSlice, removed, err := removeFromSlice(*cs.upstreams, index, cs.name)
	if err != nil {
		return nil, err
	}
	*cs.upstreams = newSlice
	cm.clearFailedKeysForUpstream(removed, cs.name)

	if err := cm.saveConfigLocked(cm.config); err != nil {
		return nil, err
	}
	log.Printf("[Config-Upstream] 已删除 %s 上游: %s", cs.name, removed.Name)
	return removed, nil
}

// addChannelKey 为指定渠道添加 API 密钥。
func (cm *ConfigManager) addChannelKey(kind string, index int, apiKey string) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	cs := cm.channelSetFor(kind)
	if err := addAPIKeyOp(*cs.upstreams, index, apiKey, cs.name); err != nil {
		return err
	}
	return cm.saveConfigLocked(cm.config)
}

// removeChannelKey 从指定渠道移除 API 密钥。
func (cm *ConfigManager) removeChannelKey(kind string, index int, apiKey string) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	cs := cm.channelSetFor(kind)
	if err := removeAPIKeyOp(*cs.upstreams, index, apiKey, cs.name); err != nil {
		return err
	}
	return cm.saveConfigLocked(cm.config)
}

// moveChannelKeyTop 将 API 密钥移到列表顶部。
func (cm *ConfigManager) moveChannelKeyTop(kind string, index int, apiKey string) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	cs := cm.channelSetFor(kind)
	if err := moveKeyToTopOp(*cs.upstreams, index, apiKey); err != nil {
		return err
	}
	return cm.saveConfigLocked(cm.config)
}

// moveChannelKeyBottom 将 API 密钥移到底部。
func (cm *ConfigManager) moveChannelKeyBottom(kind string, index int, apiKey string) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	cs := cm.channelSetFor(kind)
	if err := moveKeyToBottomOp(*cs.upstreams, index, apiKey); err != nil {
		return err
	}
	return cm.saveConfigLocked(cm.config)
}

// reorderChannels 按给定顺序重排渠道。
func (cm *ConfigManager) reorderChannels(kind string, order []int) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	cs := cm.channelSetFor(kind)
	if err := reorderOp(*cs.upstreams, order, cs.name); err != nil {
		return err
	}
	return cm.saveConfigLocked(cm.config)
}

// setChannelStatusFor 设置渠道状态。
func (cm *ConfigManager) setChannelStatusFor(kind string, index int, status string) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	cs := cm.channelSetFor(kind)
	if err := setStatusOp(*cs.upstreams, index, status, cs.name); err != nil {
		return err
	}
	return cm.saveConfigLocked(cm.config)
}

// setChannelPromotionFor 设置渠道促销期。
func (cm *ConfigManager) setChannelPromotionFor(kind string, index int, duration time.Duration, count int) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	cs := cm.channelSetFor(kind)
	if err := setPromotionOp(*cs.upstreams, index, duration, count, cs.name); err != nil {
		return err
	}
	return cm.saveConfigLocked(cm.config)
}

// setLoadBalanceFor 设置负载均衡策略。
func (cm *ConfigManager) setLoadBalanceFor(kind string, strategy string) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	cs := cm.channelSetFor(kind)
	if err := validateLoadBalanceStrategy(strategy); err != nil {
		return err
	}
	*cs.loadBalance = strategy

	if err := cm.saveConfigLocked(cm.config); err != nil {
		return err
	}
	log.Printf("[Config-LoadBalance] 已设置 %s 负载均衡策略: %s", cs.name, strategy)
	return nil
}

// currentChannelForModel 返回当前活跃渠道（指定模型）。
func (cm *ConfigManager) currentChannelForModel(kind string, model string) (*UpstreamConfig, int, error) {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	cs := cm.channelSetFor(kind)
	return getFirstActiveWithIndexForModel(*cs.upstreams, *cs.pools, cs.name, model)
}

// MoveAPIKeyToBottomForKind 将指定渠道类型的 API 密钥移到底部。
func (cm *ConfigManager) MoveAPIKeyToBottomForKind(kind string, index int, apiKey string) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	cs := cm.channelSetFor(kind)
	if err := moveKeyToBottomOp(*cs.upstreams, index, apiKey); err != nil {
		return err
	}
	return cm.saveConfigLocked(cm.config)
}

// GetCurrentUpstreamWithIndexForModelForKind 返回指定渠道类型的当前活跃渠道（指定模型）。
func (cm *ConfigManager) GetCurrentUpstreamWithIndexForModelForKind(kind string, model string) (*UpstreamConfig, int, error) {
	return cm.currentChannelForModel(kind, model)
}
