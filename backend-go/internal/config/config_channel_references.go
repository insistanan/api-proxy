// 渠道引用校验：视觉层与模型映射的目标引用、规范化和完整性检查。
package config

import (
	"fmt"
	"strings"
)

func validateAllVisionLayerConfigs(upstreams []UpstreamConfig) error {
	for index := range upstreams {
		if GetChannelStatus(&upstreams[index]) == ChannelStatusDeleted {
			continue
		}
		if err := validateVisionLayerConfig(upstreams, index); err != nil {
			return fmt.Errorf("渠道 %q 配置无效: %w", upstreams[index].Name, err)
		}
	}
	return nil
}

func validateVisionLayerConfig(upstreams []UpstreamConfig, ownerIndex int) error {
	if ownerIndex < 0 || ownerIndex >= len(upstreams) {
		return fmt.Errorf("图片理解层所属渠道索引无效: %d", ownerIndex)
	}
	owner := &upstreams[ownerIndex]
	if owner.ExcludeFromConversation && !owner.VisionCapable {
		return fmt.Errorf("不参与对话的渠道必须标记为支持图片理解")
	}
	if owner.VisionCapable && owner.VisionLayerEnabled {
		return fmt.Errorf("渠道不能同时标记为支持图片理解并启用图片理解层")
	}
	if !owner.VisionLayerEnabled {
		return nil
	}
	targetID := strings.TrimSpace(owner.VisionLayerChannelID)
	if targetID == "" {
		return fmt.Errorf("启用图片理解层后必须选择图片理解渠道")
	}
	for targetIndex := range upstreams {
		target := &upstreams[targetIndex]
		if target.ID != targetID {
			continue
		}
		if targetIndex == ownerIndex {
			return fmt.Errorf("图片理解渠道不能选择当前渠道自身")
		}
		if !target.VisionCapable {
			return fmt.Errorf("所选渠道 %q 未标记为支持图片理解", target.Name)
		}
		if !target.ExcludeFromConversation && target.PoolID != owner.PoolID {
			return fmt.Errorf("图片理解渠道必须位于当前分组或公用纯图片理解池")
		}
		return nil
	}
	return fmt.Errorf("图片理解渠道 %q 不存在", targetID)
}

// normalizeModelMappingChannels 归一化"映射目标渠道"并量化为与 ModelMapping 一致的集合：
// 只保留仍然存在于 mapping 中的 源模型 -> 目标模型 条目，丢弃空渠道 ID。
// 映射被删除或目标模型被去重移除后，对应的渠道登记随之失效，避免残留的悬空配置。
func normalizeModelMappingChannels(channels map[string]map[string]string, mapping map[string][]string) map[string]map[string]string {
	if len(channels) == 0 || len(mapping) == 0 {
		return nil
	}

	normalized := make(map[string]map[string]string)
	for rawSource, rawTargets := range channels {
		source := strings.TrimSpace(rawSource)
		targets, ok := mapping[source]
		if source == "" || !ok {
			continue
		}
		allowed := make(map[string]struct{}, len(targets))
		for _, target := range targets {
			allowed[target] = struct{}{}
		}
		for rawTarget, rawChannelID := range rawTargets {
			target := strings.TrimSpace(rawTarget)
			channelID := strings.TrimSpace(rawChannelID)
			if _, exists := allowed[target]; !exists || channelID == "" {
				continue
			}
			if normalized[source] == nil {
				normalized[source] = make(map[string]string)
			}
			normalized[source][target] = channelID
		}
	}
	if len(normalized) == 0 {
		return nil
	}
	return normalized
}

// validateAllModelMappingChannels 校验所有渠道的映射目标渠道引用。
func validateAllModelMappingChannels(upstreams []UpstreamConfig) error {
	for index := range upstreams {
		if GetChannelStatus(&upstreams[index]) == ChannelStatusDeleted {
			continue
		}
		if err := validateModelMappingChannels(upstreams, index); err != nil {
			return fmt.Errorf("渠道 %q 配置无效: %w", upstreams[index].Name, err)
		}
	}
	return nil
}

// validateModelMappingChannels 校验单个渠道的映射目标渠道：目标必须存在、不能是自身、不能已删除。
// 同协议由数组归属保证（只在同一 kind 的切片内查找）。单跳不需要在此校验：运行时目标渠道
// 以替换后的单一映射执行，自身的 ModelMappingChannels 不参与解析，结构上不可能递归。
func validateModelMappingChannels(upstreams []UpstreamConfig, ownerIndex int) error {
	if ownerIndex < 0 || ownerIndex >= len(upstreams) {
		return fmt.Errorf("映射目标渠道所属渠道索引无效: %d", ownerIndex)
	}
	owner := &upstreams[ownerIndex]
	for source, targets := range owner.ModelMappingChannels {
		for target, targetID := range targets {
			targetIndex := findUpstreamIndexByID(upstreams, targetID)
			if targetIndex < 0 {
				return fmt.Errorf("映射 %q -> %q 指定的目标渠道 %q 不存在", source, target, targetID)
			}
			if targetIndex == ownerIndex {
				return fmt.Errorf("映射 %q -> %q 不能指定当前渠道自身", source, target)
			}
			if GetChannelStatus(&upstreams[targetIndex]) == ChannelStatusDeleted {
				return fmt.Errorf("映射 %q -> %q 指定的目标渠道 %q 已删除", source, target, upstreams[targetIndex].Name)
			}
		}
	}
	return nil
}

// findUpstreamIndexByID 在同一切片内按稳定渠道 ID 查找下标，未找到返回 -1。
func findUpstreamIndexByID(upstreams []UpstreamConfig, id string) int {
	for index := range upstreams {
		if upstreams[index].ID == id {
			return index
		}
	}
	return -1
}
