package scheduler

import (
	"log"
	"sort"
	"strings"

	"github.com/insistanan/api-proxy/internal/infra/config"
)

// ChannelInfo 渠道信息（用于排序）
type ChannelInfo struct {
	Index    int
	Name     string
	Priority int
	Status   string
	Promoted bool // 促销仅临时调整所在分组的顺序，健康检查与熔断准入照常执行

	upstream *config.UpstreamConfig // 本轮候选快照，筛选时不重复读取配置
}

// getActiveChannels 获取故障转移序列（按配置优先级排序）。
func (s *ChannelScheduler) getActiveChannels(kind ChannelKind, poolIDs ...string) []ChannelInfo {
	channelsByPool := s.getActiveChannelsByPool(kind, poolIDs)
	if len(poolIDs) == 0 {
		return channelsByPool[""]
	}
	var activeChannels []ChannelInfo
	for _, poolID := range poolIDs {
		activeChannels = append(activeChannels, channelsByPool[poolID]...)
	}
	return activeChannels
}

// getActiveChannelsByPool 从同一份配置快照中收集多个分组的故障转移序列，
// 避免一次选渠为了命中分组和兜底分组重复深拷贝完整配置。
func (s *ChannelScheduler) getActiveChannelsByPool(kind ChannelKind, poolIDs []string) map[string][]ChannelInfo {
	upstreams, _, err := s.configManager.GetChannelConfig(string(kind))
	if err != nil {
		log.Printf("[Scheduler-Config] 读取渠道配置失败: %v", err)
		return map[string][]ChannelInfo{}
	}

	requestedPools := make(map[string]struct{}, len(poolIDs))
	for _, poolID := range poolIDs {
		requestedPools[poolID] = struct{}{}
	}
	channelsByPool := make(map[string][]ChannelInfo, len(poolIDs)+1)
	if len(poolIDs) == 0 {
		channelsByPool[""] = nil
	} else {
		for _, poolID := range poolIDs {
			channelsByPool[poolID] = nil
		}
	}

	// 筛选故障转移序列中的渠道。
	for i, upstream := range upstreams {
		status := upstream.Status
		if status == "" {
			status = "active" // 默认为活跃
		}

		// 只选择故障转移序列中的渠道（suspended 也显示在序列中，备用/弃用/删除占位排除）
		upstreamPoolID := strings.TrimSpace(upstream.PoolID)
		if upstreamPoolID == "" {
			upstreamPoolID = config.DefaultChannelPoolID
		}
		if !config.IsChannelSchedulable(&upstream) || upstream.ExcludeFromConversation {
			continue
		}
		if len(requestedPools) > 0 {
			if _, requested := requestedPools[upstreamPoolID]; !requested {
				continue
			}
		}

		priority := upstream.Priority
		if priority == 0 {
			priority = i + 1
		}

		channel := ChannelInfo{
			Index:    i,
			Name:     upstream.Name,
			Priority: priority,
			Status:   status,
			Promoted: config.IsChannelInPromotion(&upstream),
			upstream: &upstreams[i],
		}
		if len(requestedPools) == 0 {
			channelsByPool[""] = append(channelsByPool[""], channel)
		} else {
			channelsByPool[upstreamPoolID] = append(channelsByPool[upstreamPoolID], channel)
		}
	}

	for poolID := range channelsByPool {
		channels := channelsByPool[poolID]
		// 健康促销渠道临时优先，其余按配置优先级和索引排列。
		sort.Slice(channels, func(i, j int) bool {
			if channels[i].Promoted != channels[j].Promoted {
				return channels[i].Promoted
			}
			if channels[i].Priority != channels[j].Priority {
				return channels[i].Priority < channels[j].Priority
			}
			return channels[i].Index < channels[j].Index
		})
		channelsByPool[poolID] = channels
	}

	return channelsByPool
}

func (s *ChannelScheduler) getChannelPools(kind ChannelKind) []config.ChannelPool {
	return s.configManager.GetChannelPools(string(kind))
}

// getUpstreamByIndex 根据索引获取上游配置
// 注意：返回的是副本，避免指向 slice 元素的指针在 slice 重分配后失效
func (s *ChannelScheduler) getUpstreamByIndex(index int, kind ChannelKind) *config.UpstreamConfig {
	upstreams, _, err := s.configManager.GetChannelConfig(string(kind))
	if err != nil {
		log.Printf("[Scheduler-Config] 读取渠道配置失败: %v", err)
		return nil
	}

	if index >= 0 && index < len(upstreams) {
		// 返回副本，避免返回指向 slice 元素的指针
		upstream := upstreams[index]
		return &upstream
	}
	return nil
}

// GetActiveChannelCount 获取活跃渠道数量
func (s *ChannelScheduler) GetActiveChannelCount(kind ChannelKind) int {
	return len(s.getActiveChannels(kind))
}

// GetActiveChannelCountForModel 返回指定模型可参与故障转移的渠道数，
// 包括命中分组及其后的兜底分组。
func (s *ChannelScheduler) GetActiveChannelCountForModel(kind ChannelKind, model string) int {
	poolRoute, err := config.SelectChannelPoolRoute(s.getChannelPools(kind), model)
	if err != nil {
		return 0
	}
	poolIDs := make([]string, 0, len(poolRoute))
	for _, pool := range poolRoute {
		poolIDs = append(poolIDs, pool.ID)
	}
	channelsByPool := s.getActiveChannelsByPool(kind, poolIDs)
	count := 0
	for _, pool := range poolRoute {
		count += len(channelsByPool[pool.ID])
	}
	return count
}

// IsMultiChannelMode 判断是否为多渠道模式
func (s *ChannelScheduler) IsMultiChannelMode(kind ChannelKind) bool {
	return s.GetActiveChannelCount(kind) > 1
}
