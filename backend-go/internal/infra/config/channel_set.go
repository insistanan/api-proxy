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
// 写操作只绑定候选配置，保存成功或已过提交点后由 saveConfigLocked 发布。
type channelSet struct {
	kind        string
	name        string            // 日志文案，如 "Messages" / "Chat"
	upstreams   *[]UpstreamConfig // 指向传入配置的对应切片
	pools       *[]ChannelPool    // 指向传入配置的对应池列表
	loadBalance *string           // 指向传入配置的对应负载均衡策略
}

// channelSets 是七组渠道字段的统一映射，供查找、迁移和快照复制复用。
func (cfg *Config) channelSets() []channelSet {
	return []channelSet{
		{"messages", "Messages", &cfg.Upstream, &cfg.MessagePools, &cfg.LoadBalance},
		{"responses", "Responses", &cfg.ResponsesUpstream, &cfg.ResponsesPools, &cfg.ResponsesLoadBalance},
		{"gemini", "Gemini", &cfg.GeminiUpstream, &cfg.GeminiPools, &cfg.GeminiLoadBalance},
		{"chat", "Chat", &cfg.ChatUpstream, &cfg.ChatPools, &cfg.ChatLoadBalance},
		{"images", "Images", &cfg.ImagesUpstream, &cfg.ImagesPools, &cfg.ImagesLoadBalance},
		{"embeddings", "Embeddings", &cfg.EmbeddingsUpstream, &cfg.EmbeddingsPools, &cfg.EmbeddingsLoadBalance},
		{"decisions", "Decisions", &cfg.DecisionsUpstream, &cfg.DecisionsPools, &cfg.DecisionsLoadBalance},
	}
}

// channelSetFor 返回指定渠道类型的存放位置。
// 未知类型显式报错，避免误写入 Messages 配置。
func (cfg *Config) channelSetFor(kind string) (*channelSet, error) {
	for _, set := range cfg.channelSets() {
		if set.kind == kind {
			return &set, nil
		}
	}
	return nil, fmt.Errorf("不支持的渠道类型: %s", kind)
}

// mutateChannelSet 统一无返回值的渠道写操作：只修改候选，提交后发布。
func (cm *ConfigManager) mutateChannelSet(kind string, mutate func(*channelSet) error) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	candidate := cloneConfig(cm.config)
	cs, err := candidate.channelSetFor(kind)
	if err != nil {
		return err
	}
	if err := mutate(cs); err != nil {
		return err
	}
	return cm.saveConfigLocked(candidate)
}

// =============================================================================
// 通用 CRUD 方法（以 messages 版为行为基准，参数化 kind）
// =============================================================================

// AddChannel 添加指定协议的上游。
// chat/images 版额外做了 DefaultModel TrimSpace，此处统一对所有 kind 做（对 messages 无害）。
func (cm *ConfigManager) AddChannel(kind string, up UpstreamConfig) (AddedUpstream, error) {
	if up.BuiltinProvider != "" {
		return AddedUpstream{}, fmt.Errorf("不能通过普通接口创建内置免费渠道")
	}
	cm.mu.Lock()
	defer cm.mu.Unlock()

	candidate := cloneConfig(cm.config)
	cs, err := candidate.channelSetFor(kind)
	if err != nil {
		return AddedUpstream{}, err
	}
	up.DefaultModel = strings.TrimSpace(up.DefaultModel)
	if kind == "images" {
		up.ResponsesModel = strings.TrimSpace(up.ResponsesModel)
		if err := ValidateImagesUpstream(&up); err != nil {
			return AddedUpstream{}, err
		}
	}
	if kind == "embeddings" {
		if err := ValidateEmbeddingsUpstream(&up); err != nil {
			return AddedUpstream{}, err
		}
	}
	if kind == "decisions" {
		if err := ValidateDecisionsUpstream(&up); err != nil {
			return AddedUpstream{}, err
		}
	}
	next, result, err := addValidatedUpstreamOp(*cs.upstreams, *cs.pools, up)
	if err != nil {
		return AddedUpstream{}, err
	}
	*cs.upstreams = next

	err = cm.saveConfigLocked(candidate)
	if err != nil && !IsCommittedError(err) {
		return AddedUpstream{}, err
	}
	log.Printf("[Config-Upstream] 已添加 %s 上游（优先级1）: %s", cs.name, (*cs.upstreams)[result.Index].Name)
	return result, err
}

// UpdateChannel 部分更新上游（通用版）。
func (cm *ConfigManager) UpdateChannel(kind string, index int, updates UpstreamUpdate) (bool, error) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	candidate := cloneConfig(cm.config)
	cs, err := candidate.channelSetFor(kind)
	if err != nil {
		return false, err
	}
	upstreams := *cs.upstreams

	if err := rejectBuiltinChannel(upstreams, index); err != nil {
		return false, err
	}

	if index < 0 || index >= len(upstreams) {
		return false, fmt.Errorf("无效的 %s 上游索引: %d", cs.name, index)
	}

	shouldReset, err := applyCommonUpdatesToList(upstreams, *cs.pools, index, updates, cs.name)
	if err != nil {
		return false, err
	}
	if kind == "images" {
		if err := ValidateImagesUpstream(&upstreams[index]); err != nil {
			return false, err
		}
	}
	if kind == "embeddings" {
		if err := ValidateEmbeddingsUpstream(&upstreams[index]); err != nil {
			return false, err
		}
	}
	if kind == "decisions" {
		if err := ValidateDecisionsUpstream(&upstreams[index]); err != nil {
			return false, err
		}
	}

	err = cm.saveConfigLocked(candidate)
	if err != nil && !IsCommittedError(err) {
		return false, err
	}
	log.Printf("[Config-Upstream] 已更新 %s 上游: [%d] %s", cs.name, index, (*cs.upstreams)[index].Name)
	return shouldReset, err
}

// RemoveChannel 删除上游（通用版）。
func (cm *ConfigManager) RemoveChannel(kind string, index int) (*UpstreamConfig, error) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	candidate := cloneConfig(cm.config)
	cs, err := candidate.channelSetFor(kind)
	if err != nil {
		return nil, err
	}
	if err := rejectBuiltinChannel(*cs.upstreams, index); err != nil {
		return nil, err
	}
	newSlice, removed, err := removeFromSlice(*cs.upstreams, index, cs.name)
	if err != nil {
		return nil, err
	}
	*cs.upstreams = newSlice
	err = cm.saveConfigLocked(candidate)
	if err != nil && !IsCommittedError(err) {
		return nil, err
	}
	cm.clearFailedKeysForUpstream(removed, cs.name)
	log.Printf("[Config-Upstream] 已删除 %s 上游: %s", cs.name, removed.Name)
	return removed, err
}

// AddChannelKey 为指定渠道添加 API 密钥。
func (cm *ConfigManager) AddChannelKey(kind string, index int, apiKey string) error {
	return cm.mutateChannelSet(kind, func(cs *channelSet) error {
		if err := rejectBuiltinChannel(*cs.upstreams, index); err != nil {
			return err
		}
		return addAPIKeyOp(*cs.upstreams, index, apiKey, cs.name)
	})
}

// RemoveChannelKey 从指定渠道移除 API 密钥。
func (cm *ConfigManager) RemoveChannelKey(kind string, index int, apiKey string) error {
	return cm.mutateChannelSet(kind, func(cs *channelSet) error {
		if err := rejectBuiltinChannel(*cs.upstreams, index); err != nil {
			return err
		}
		return removeAPIKeyOp(*cs.upstreams, index, apiKey, cs.name)
	})
}

// MoveChannelKeyTop 将 API 密钥移到列表顶部。
func (cm *ConfigManager) MoveChannelKeyTop(kind string, index int, apiKey string) error {
	return cm.mutateChannelSet(kind, func(cs *channelSet) error {
		if err := rejectBuiltinChannel(*cs.upstreams, index); err != nil {
			return err
		}
		return moveKeyToTopOp(*cs.upstreams, index, apiKey)
	})
}

// MoveChannelKeyBottom 将 API 密钥移到底部。
func (cm *ConfigManager) MoveChannelKeyBottom(kind string, index int, apiKey string) error {
	return cm.mutateChannelSet(kind, func(cs *channelSet) error {
		if err := rejectBuiltinChannel(*cs.upstreams, index); err != nil {
			return err
		}
		return moveKeyToBottomOp(*cs.upstreams, index, apiKey)
	})
}

// ReorderChannels 按给定顺序重排渠道。
func (cm *ConfigManager) ReorderChannels(kind string, order []int) error {
	return cm.mutateChannelSet(kind, func(cs *channelSet) error {
		return reorderOp(*cs.upstreams, order, cs.name)
	})
}

// SetChannelStatusFor 设置渠道状态。
func (cm *ConfigManager) SetChannelStatusFor(kind string, index int, status string) error {
	return cm.mutateChannelSet(kind, func(cs *channelSet) error {
		if err := rejectBuiltinChannel(*cs.upstreams, index); err != nil {
			return err
		}
		return setStatusOp(*cs.upstreams, index, status, cs.name)
	})
}

// SetChannelPromotionFor 设置渠道促销期。
func (cm *ConfigManager) SetChannelPromotionFor(kind string, index int, duration time.Duration, count int) error {
	return cm.mutateChannelSet(kind, func(cs *channelSet) error {
		if err := rejectBuiltinChannel(*cs.upstreams, index); err != nil {
			return err
		}
		return setPromotionOp(*cs.upstreams, index, duration, count, cs.name)
	})
}

// SetLoadBalanceFor 设置负载均衡策略。
func (cm *ConfigManager) SetLoadBalanceFor(kind string, strategy string) error {
	err := cm.mutateChannelSet(kind, func(cs *channelSet) error {
		if err := validateLoadBalanceStrategy(strategy); err != nil {
			return err
		}
		*cs.loadBalance = strategy
		return nil
	})
	if err != nil {
		return err
	}
	log.Printf("[Config-LoadBalance] 已设置 %s 负载均衡策略: %s", kind, strategy)
	return nil
}

// CurrentChannelForModel 返回当前活跃渠道（指定模型）。
func (cm *ConfigManager) CurrentChannelForModel(kind string, model string) (*UpstreamConfig, int, error) {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	cs, err := cm.config.channelSetFor(kind)
	if err != nil {
		return nil, -1, err
	}
	return getFirstActiveWithIndexForModel(*cs.upstreams, *cs.pools, cs.name, model)
}

// MoveAPIKeyToBottomForKind 将指定渠道类型的 API 密钥移到底部。
func (cm *ConfigManager) MoveAPIKeyToBottomForKind(kind string, index int, apiKey string) error {
	return cm.MoveChannelKeyBottom(kind, index, apiKey)
}

// GetCurrentUpstreamWithIndexForModelForKind 返回指定渠道类型的当前活跃渠道（指定模型）。
func (cm *ConfigManager) GetCurrentUpstreamWithIndexForModelForKind(kind string, model string) (*UpstreamConfig, int, error) {
	return cm.CurrentChannelForModel(kind, model)
}

// GetChannelConfig 返回同一快照中的上游列表和负载均衡策略。
func (cm *ConfigManager) GetChannelConfig(kind string) ([]UpstreamConfig, string, error) {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	cs, err := cm.config.channelSetFor(kind)
	if err != nil {
		return nil, "", err
	}
	return cloneUpstreamList(*cs.upstreams), *cs.loadBalance, nil
}
