package scheduler

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/insistanan/api-proxy/internal/infra/config"
)

// SelectVisionChannel 通过稳定渠道 ID 解析图片理解层指定的原生图片理解渠道，
// 并为这次内部调用预留在途负载，避免其从渠道调度与实时指标中消失。
func (s *ChannelScheduler) SelectVisionChannel(ctx context.Context, kind ChannelKind, channelID string, ownerPoolID string) (*SelectionResult, error) {
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

	upstreams, _, err := s.configManager.GetChannelConfig(string(kind))
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
		targetPoolID := strings.TrimSpace(upstream.PoolID)
		if targetPoolID == "" {
			targetPoolID = config.DefaultChannelPoolID
		}
		if !upstream.ExcludeFromConversation && targetPoolID != ownerPoolID {
			return nil, fmt.Errorf("图片理解渠道 %q 不在当前分组或公用纯图片理解池", upstream.Name)
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

	return nil, fmt.Errorf("图片理解渠道 %q 不存在", channelID)
}

// ListPublicVisionChannels 按配置序号返回公共图片理解池中可用的渠道。
// excludeChannelID 用于排除已经尝试过的显式图片理解渠道。
func (s *ChannelScheduler) ListPublicVisionChannels(ctx context.Context, kind ChannelKind, excludeChannelID string) []*SelectionResult {
	return s.listVisionChannels(ctx, kind, excludeChannelID, "", true)
}

// ListPoolVisionChannels 按配置序号返回 ownerPoolID 分组中可用的图片理解渠道。
// 公共图片理解渠道不属于分组候选，确保公共池全部失败后才进入该阶段。
func (s *ChannelScheduler) ListPoolVisionChannels(ctx context.Context, kind ChannelKind, excludeChannelID string, ownerPoolID string) []*SelectionResult {
	return s.listVisionChannels(ctx, kind, excludeChannelID, ownerPoolID, false)
}

func (s *ChannelScheduler) listVisionChannels(ctx context.Context, kind ChannelKind, excludeChannelID string, ownerPoolID string, public bool) []*SelectionResult {
	if ctx == nil {
		return nil
	}
	select {
	case <-ctx.Done():
		return nil
	default:
	}
	channelID := strings.TrimSpace(excludeChannelID)
	if !public {
		ownerPoolID = strings.TrimSpace(ownerPoolID)
		if ownerPoolID == "" {
			ownerPoolID = config.DefaultChannelPoolID
		}
	}

	upstreams, _, err := s.configManager.GetChannelConfig(string(kind))
	if err != nil {
		log.Printf("[Scheduler-Config] 读取渠道配置失败: %v", err)
		return nil
	}

	var results []*SelectionResult
	for index := range upstreams {
		upstream := &upstreams[index]
		if upstream.ID == channelID {
			continue
		}
		if !upstream.VisionCapable {
			continue
		}
		if public != upstream.ExcludeFromConversation {
			continue
		}
		if !public {
			targetPoolID := strings.TrimSpace(upstream.PoolID)
			if targetPoolID == "" {
				targetPoolID = config.DefaultChannelPoolID
			}
			if targetPoolID != ownerPoolID {
				continue
			}
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
			Reason:       reason,
		})
	}
	return results
}
