package scheduler

import (
	"sort"
	"strings"

	"github.com/BenedictKing/api-proxy/internal/config"
)

// ChannelInfo 渠道信息（用于排序）
type ChannelInfo struct {
	Index    int
	Name     string
	Priority int
	Status   string
	Score    float64 // 动态分数（基于失败率），分数越高优先级越高
}

// calculateChannelScore 计算渠道分数（基于失败率）
// 分数范围: 0-100，分数越高优先级越高
// - 无历史数据：100 分（新渠道给满分）
// - 失败率 0%：100 分
// - 失败率 20%：80 分
// - 失败率 50%：50 分
// - 失败率 80%：20 分
// - 失败率 100%：0 分
func (s *ChannelScheduler) calculateChannelScore(upstream *config.UpstreamConfig, channelIndex int, kind ChannelKind) float64 {
	if upstream == nil || len(upstream.APIKeys) == 0 {
		return 0
	}

	metricsManager := s.getMetricsManager(kind)
	failureRate := metricsManager.CalculateChannelFailureRate(upstream.BaseURL, upstream.APIKeys, channelIndex)

	// 计算基础分数：100 * (1 - failureRate)
	baseScore := 100 * (1 - failureRate)

	// 检查请求数，如果请求数小于 5，给予新渠道奖励（避免因样本不足被低估）
	requestCount := metricsManager.GetChannelRequestCount(upstream.BaseURL, upstream.APIKeys, channelIndex)
	if requestCount < 5 {
		// 新渠道给予满分
		return 100
	}

	return baseScore
}

// getActiveChannels 获取故障转移序列（按配置优先级排序）。
// 动态分数只用于指标展示，不能改变用户配置的 failover 顺序。
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
	cfg := s.configManager.GetConfig()

	var upstreams []config.UpstreamConfig
	switch kind {
	case ChannelKindMessages:
		upstreams = cfg.Upstream
	case ChannelKindResponses:
		upstreams = cfg.ResponsesUpstream
	case ChannelKindGemini:
		upstreams = cfg.GeminiUpstream
	case ChannelKindChat:
		upstreams = cfg.ChatUpstream
	case ChannelKindImages:
		upstreams = cfg.ImagesUpstream
	default:
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

		// 计算渠道动态分数
		upstreamCopy := upstream
		score := s.calculateChannelScore(&upstreamCopy, i, kind)
		channel := ChannelInfo{
			Index:    i,
			Name:     upstream.Name,
			Priority: priority,
			Status:   status,
			Score:    score,
		}
		if len(requestedPools) == 0 {
			channelsByPool[""] = append(channelsByPool[""], channel)
		} else {
			channelsByPool[upstreamPoolID] = append(channelsByPool[upstreamPoolID], channel)
		}
	}

	for poolID := range channelsByPool {
		channels := channelsByPool[poolID]
		// 配置优先级和配置索引共同决定故障转移顺序。动态分数只用于展示，
		// 不能改变用户拖拽后确定的首选渠道。
		sort.Slice(channels, func(i, j int) bool {
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
	cfg := s.configManager.GetConfig()
	switch kind {
	case ChannelKindMessages:
		return cfg.MessagePools
	case ChannelKindResponses:
		return cfg.ResponsesPools
	case ChannelKindGemini:
		return cfg.GeminiPools
	case ChannelKindChat:
		return cfg.ChatPools
	case ChannelKindImages:
		return cfg.ImagesPools
	default:
		return nil
	}
}

// getUpstreamByIndex 根据索引获取上游配置
// 注意：返回的是副本，避免指向 slice 元素的指针在 slice 重分配后失效
func (s *ChannelScheduler) getUpstreamByIndex(index int, kind ChannelKind) *config.UpstreamConfig {
	cfg := s.configManager.GetConfig()

	var upstreams []config.UpstreamConfig
	switch kind {
	case ChannelKindMessages:
		upstreams = cfg.Upstream
	case ChannelKindResponses:
		upstreams = cfg.ResponsesUpstream
	case ChannelKindGemini:
		upstreams = cfg.GeminiUpstream
	case ChannelKindChat:
		upstreams = cfg.ChatUpstream
	case ChannelKindImages:
		upstreams = cfg.ImagesUpstream
	default:
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

func (s *ChannelScheduler) IsMultiChannelModeForModel(kind ChannelKind, model string) bool {
	return s.GetActiveChannelCountForModel(kind, model) > 1
}
