// 共享图片理解池：跨四个会话协议枚举、定位公用纯图片理解渠道。
package config

import (
	"fmt"
	"strings"
)

// conversationChannelKinds 是共享图片理解池覆盖的会话协议，顺序固定，
// 用于跨协议查找与前端展示的稳定排序。
var conversationChannelKinds = []string{"messages", "responses", "gemini", "chat"}

// ConversationChannelKinds 返回共享图片理解池覆盖的会话协议（只读副本）。
func ConversationChannelKinds() []string {
	return append([]string(nil), conversationChannelKinds...)
}

// IsConversationChannelKind 判断 kind 是否属于共享图片理解池覆盖的会话协议。
func IsConversationChannelKind(kind string) bool {
	for _, candidate := range conversationChannelKinds {
		if candidate == kind {
			return true
		}
	}
	return false
}

// VisionChannelRef 是跨协议定位到的图片理解渠道。
// Kind 是渠道自身所属的会话协议，Index 是该协议切片内的数组下标；
// 调用方必须用这两个字段访问指标、熔断、URL 健康与渠道日志。
type VisionChannelRef struct {
	Kind     string
	Index    int
	Upstream UpstreamConfig
}

func (cfg *Config) conversationSlice(kind string) ([]UpstreamConfig, bool) {
	cs, err := cfg.channelSetFor(kind)
	if err != nil {
		return nil, false
	}
	return *cs.upstreams, true
}

// publicVisionChannels 返回四个会话切片中未删除的公用纯图片理解渠道。
func (cfg *Config) publicVisionChannels() []VisionChannelRef {
	result := make([]VisionChannelRef, 0)
	for _, kind := range conversationChannelKinds {
		upstreams, ok := cfg.conversationSlice(kind)
		if !ok {
			continue
		}
		for index := range upstreams {
			upstream := upstreams[index]
			if GetChannelStatus(&upstream) == ChannelStatusDeleted {
				continue
			}
			if !upstream.ExcludeFromConversation || !upstream.VisionCapable {
				continue
			}
			cloned := upstream.Clone()
			if cloned == nil {
				continue
			}
			result = append(result, VisionChannelRef{Kind: kind, Index: index, Upstream: *cloned})
		}
	}
	return result
}

// PublicVisionChannels 返回跨四个会话协议的共享公用图片理解渠道快照。
func (cm *ConfigManager) PublicVisionChannels() []VisionChannelRef {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	return cm.config.publicVisionChannels()
}

// locateVisionChannel 按稳定 ID 跨七类渠道切片查找图片理解渠道。
// pendingKind/pendingUpstreams 表示尚未写回 cfg 的候选切片，用于保存前校验。
func (cfg *Config) locateVisionChannel(id string, pendingKind string, pendingUpstreams []UpstreamConfig) (VisionChannelRef, bool) {
	for _, set := range cfg.channelSets() {
		upstreams := *set.upstreams
		if set.kind == pendingKind && pendingUpstreams != nil {
			upstreams = pendingUpstreams
		}
		for index := range upstreams {
			if strings.TrimSpace(upstreams[index].ID) != id {
				continue
			}
			return VisionChannelRef{Kind: set.kind, Index: index, Upstream: upstreams[index]}, true
		}
	}
	return VisionChannelRef{}, false
}

// VisionLayerDependent 只包含确认删除所需的渠道身份，不暴露上游地址或密钥。
type VisionLayerDependent struct {
	Kind  string `json:"kind"`
	Index int    `json:"index"`
	Name  string `json:"name"`
}

type ChannelDeletionImpact struct {
	Name         string                 `json:"name"`
	ChannelCount int                    `json:"channelCount"`
	Dependents   []VisionLayerDependent `json:"dependents"`
}

func (cfg *Config) visionLayerDependents(targetIDs map[string]struct{}) []VisionLayerDependent {
	result := make([]VisionLayerDependent, 0)
	for _, set := range cfg.channelSets() {
		upstreams := *set.upstreams
		for index := range upstreams {
			owner := &upstreams[index]
			if GetChannelStatus(owner) == ChannelStatusDeleted {
				continue
			}
			if _, deleting := targetIDs[owner.ID]; deleting {
				continue
			}
			if _, referenced := targetIDs[strings.TrimSpace(owner.VisionLayerChannelID)]; referenced {
				result = append(result, VisionLayerDependent{Kind: set.kind, Index: index, Name: owner.Name})
			}
		}
	}
	return result
}

// detachVisionLayers 仅修改候选快照，与渠道删除一同提交，失败时不改变现有配置。
func (cfg *Config) detachVisionLayers(targetIDs map[string]struct{}) {
	dependents := cfg.visionLayerDependents(targetIDs)
	for _, set := range cfg.channelSets() {
		for _, dependent := range dependents {
			if dependent.Kind != set.kind {
				continue
			}
			disableVisionLayer(&(*set.upstreams)[dependent.Index])
		}
	}
}

func disableVisionLayer(owner *UpstreamConfig) {
	owner.VisionCapable = false
	owner.VisionLayerEnabled = false
	owner.VisionLayerChannelID = ""
	owner.VisionLayerModel = ""
}

// GetChannelDeletionImpact 预览单渠道或分组删除的影响；真正删除时重新按最新快照解除引用。
func (cm *ConfigManager) GetChannelDeletionImpact(kind string, channelIndex *int, poolID string) (ChannelDeletionImpact, error) {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	impact := ChannelDeletionImpact{}
	cs, err := cm.config.channelSetFor(kind)
	if err != nil {
		return impact, err
	}
	targetIDs := make(map[string]struct{})
	if channelIndex != nil {
		if poolID != "" || *channelIndex < 0 || *channelIndex >= len(*cs.upstreams) {
			return impact, fmt.Errorf("无效的渠道删除目标")
		}
		channel := (*cs.upstreams)[*channelIndex]
		if GetChannelStatus(&channel) == ChannelStatusDeleted {
			return impact, fmt.Errorf("渠道已删除")
		}
		if err := rejectBuiltinChannel(*cs.upstreams, *channelIndex); err != nil {
			return impact, err
		}
		impact.Name, impact.ChannelCount = channel.Name, 1
		if channel.ID != "" {
			targetIDs[channel.ID] = struct{}{}
		}
	} else {
		if poolID == DefaultChannelPoolID || poolID == FreeChannelPoolID {
			return impact, fmt.Errorf("内置分组不能删除")
		}
		for _, pool := range *cs.pools {
			if pool.ID == poolID {
				impact.Name = pool.Name
				break
			}
		}
		if impact.Name == "" {
			return impact, fmt.Errorf("分组不存在")
		}
		for _, channel := range *cs.upstreams {
			if effectivePoolID(channel.PoolID) == poolID && GetChannelStatus(&channel) != ChannelStatusDeleted {
				impact.ChannelCount++
				if channel.ID != "" {
					targetIDs[channel.ID] = struct{}{}
				}
			}
		}
	}
	impact.Dependents = cm.config.visionLayerDependents(targetIDs)
	return impact, nil
}

// effectivePoolID 统一空分组到兜底分组，保持与调度器一致。
func effectivePoolID(poolID string) string {
	if trimmed := strings.TrimSpace(poolID); trimmed != "" {
		return trimmed
	}
	return DefaultChannelPoolID
}
