// 共享图片理解池：跨四个会话协议枚举、定位公用纯图片理解渠道。
package config

import "strings"

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

// findVisionLayerReferrer 跨七类渠道切片查找启用图片理解层并指向 targetID 的渠道。
// 同一协议内删除时也必须走这里，避免跨协议引用被遗漏。
func (cfg *Config) findVisionLayerReferrer(targetID string, targetKind string, targetIndex int) (string, bool) {
	targetID = strings.TrimSpace(targetID)
	if targetID == "" {
		return "", false
	}
	for _, set := range cfg.channelSets() {
		upstreams := *set.upstreams
		for index := range upstreams {
			if set.kind == targetKind && index == targetIndex {
				continue
			}
			owner := &upstreams[index]
			if GetChannelStatus(owner) == ChannelStatusDeleted || !owner.VisionLayerEnabled {
				continue
			}
			if strings.TrimSpace(owner.VisionLayerChannelID) == targetID {
				return owner.Name, true
			}
		}
	}
	return "", false
}

// effectivePoolID 统一空分组到兜底分组，保持与调度器一致。
func effectivePoolID(poolID string) string {
	if trimmed := strings.TrimSpace(poolID); trimmed != "" {
		return trimmed
	}
	return DefaultChannelPoolID
}
