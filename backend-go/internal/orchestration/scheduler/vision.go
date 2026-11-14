package scheduler

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/insistanan/api-proxy/internal/infra/config"
)

// SelectVisionChannel 跨四个会话协议按稳定渠道 ID 解析图片理解层指定的渠道，
// 并为这次内部调用预留在途负载，避免其从渠道调度与实时指标中消失。
// ownerKind/ownerPoolID 是启用图片理解层的渠道所属协议与分组；
// 非公用渠道仍限同协议同分组，公用纯图片理解渠道可跨协议使用。
func (s *ChannelScheduler) SelectVisionChannel(ctx context.Context, ownerKind ChannelKind, channelID string, ownerPoolID string) (*SelectionResult, error) {
	if ctx != nil {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
	}
	channelID = strings.TrimSpace(channelID)
	ownerPoolID = strings.TrimSpace(ownerPoolID)
	if ownerPoolID == "" {
		ownerPoolID = config.DefaultChannelPoolID
	}
	if channelID == "" {
		return nil, fmt.Errorf("图片理解层未指定图片理解渠道")
	}

	for _, kindName := range config.ConversationChannelKinds() {
		kind := ChannelKind(kindName)
		upstreams, _, err := s.configManager.GetChannelConfig(kindName)
		if err != nil {
			return nil, err
		}
		for index := range upstreams {
			upstream := &upstreams[index]
			if upstream.ID != channelID {
				continue
			}
			if !upstream.VisionCapable {
				return nil, fmt.Errorf("渠道 %q 未标记为支持图片理解", upstream.Name)
			}
			if !upstream.ExcludeFromConversation {
				targetPoolID := effectiveVisionPoolID(upstream.PoolID)
				if kind != ownerKind || targetPoolID != ownerPoolID {
					return nil, fmt.Errorf("图片理解渠道 %q 不在当前协议分组或公用纯图片理解池", upstream.Name)
				}
			}
			if config.GetChannelStatus(upstream) != config.ChannelStatusActive || len(upstream.APIKeys) == 0 {
				return nil, fmt.Errorf("图片理解渠道 %q 当前不可用", upstream.Name)
			}
			metricsManager := s.getMetricsManager(kind)
			if !metricsManager.IsChannelHealthyWithKeys(upstream.BaseURL, upstream.APIKeys, index) {
				return nil, fmt.Errorf("图片理解渠道 %q 当前不健康", upstream.Name)
			}
			return s.reserveAndReturn(&SelectionResult{
				Upstream:     upstream.Clone(),
				ChannelIndex: index,
				Reason:       "vision_layer",
			}, kind)
		}
	}

	return nil, fmt.Errorf("图片理解渠道 %q 不存在", channelID)
}

// ListPublicVisionChannels 按配置顺序返回四个会话协议共享的公用图片理解池渠道。
// excludeChannelID 用于排除已经尝试过的显式图片理解渠道。
func (s *ChannelScheduler) ListPublicVisionChannels(ctx context.Context, excludeChannelID string) []*SelectionResult {
	if !visionContextAlive(ctx) {
		return nil
	}
	channelID := strings.TrimSpace(excludeChannelID)
	var results []*SelectionResult
	for _, kindName := range config.ConversationChannelKinds() {
		kind := ChannelKind(kindName)
		upstreams, _, err := s.configManager.GetChannelConfig(kindName)
		if err != nil {
			log.Printf("[Scheduler-Config] 读取 %s 渠道配置失败: %v", kindName, err)
			continue
		}
		results = append(results, s.filterVisionChannels(kind, upstreams, channelID, "", true)...)
	}
	return results
}

// ListPoolVisionChannels 按配置序号返回 ownerKind 协议 ownerPoolID 分组中可用的图片理解渠道。
// 公共图片理解渠道不属于分组候选，确保公共池全部失败后才进入该阶段。
func (s *ChannelScheduler) ListPoolVisionChannels(ctx context.Context, ownerKind ChannelKind, excludeChannelID string, ownerPoolID string) []*SelectionResult {
	if !visionContextAlive(ctx) {
		return nil
	}
	upstreams, _, err := s.configManager.GetChannelConfig(string(ownerKind))
	if err != nil {
		log.Printf("[Scheduler-Config] 读取渠道配置失败: %v", err)
		return nil
	}
	return s.filterVisionChannels(ownerKind, upstreams, strings.TrimSpace(excludeChannelID), effectiveVisionPoolID(ownerPoolID), false)
}

func (s *ChannelScheduler) filterVisionChannels(kind ChannelKind, upstreams []config.UpstreamConfig, excludeChannelID string, ownerPoolID string, public bool) []*SelectionResult {
	var results []*SelectionResult
	for index := range upstreams {
		upstream := &upstreams[index]
		if upstream.ID == excludeChannelID {
			continue
		}
		if !upstream.VisionCapable {
			continue
		}
		if public != upstream.ExcludeFromConversation {
			continue
		}
		if !public && effectiveVisionPoolID(upstream.PoolID) != ownerPoolID {
			continue
		}
		if config.GetChannelStatus(upstream) != config.ChannelStatusActive || len(upstream.APIKeys) == 0 {
			continue
		}
		metricsManager := s.getMetricsManager(kind)
		if !metricsManager.IsChannelHealthyWithKeys(upstream.BaseURL, upstream.APIKeys, index) {
			continue
		}
		reason := "vision_layer_public_pool"
		if !public {
			reason = "vision_layer_owner_pool"
		}
		results = append(results, &SelectionResult{
			Upstream:     upstream.Clone(),
			ChannelIndex: index,
			Kind:         kind,
			Reason:       reason,
		})
	}
	return results
}

func visionContextAlive(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	select {
	case <-ctx.Done():
		return false
	default:
		return true
	}
}

func effectiveVisionPoolID(poolID string) string {
	if trimmed := strings.TrimSpace(poolID); trimmed != "" {
		return trimmed
	}
	return config.DefaultChannelPoolID
}
