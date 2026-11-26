package scheduler

import (
	"context"
	"fmt"
	"log"
	"sort"

	"insistanan/api-proxy/internal/dialogue/conversation"
	"insistanan/api-proxy/internal/infra/config"
	"insistanan/api-proxy/internal/storage/metrics"
)

type SelectionResult struct {
	Upstream     *config.UpstreamConfig
	ChannelIndex int
	Reason       string // 选择原因（用于日志）
	// Kind 记录选择时的协议类型，便于调用方正确释放/转移在途预留。
	Kind ChannelKind
	// Permit 是本次选择取得的许可句柄：持有 in-flight 预留和
	// 可能取得的半开探测名额。调用方在请求结束时先提交健康结果，
	// 再 Release 释放；重复调用无副作用。
	Permit *ChannelPermit
}

// SelectChannel 按配置的故障转移顺序选择渠道。
// 优先级: 对话路由覆盖 > 命中分组内健康促销渠道 > 配置顺序 > 兜底分组。
//
// 促销结束后恢复配置顺序；对话亲和、自适应评分、负载不改变故障转移顺序。
//
// 图片不参与选渠：是否直接处理图片或启用图片理解层，由被选中渠道的配置在
// 请求发送前决定（见 prepareRequestForUpstream → visionlayer.PrepareRequest）。
func (s *ChannelScheduler) SelectChannel(
	ctx context.Context,
	conversationID string,
	failedChannels map[int]bool,
	kind ChannelKind,
	requestedModel string,
) (*SelectionResult, error) {
	if ctx != nil {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
	}

	// 仅在读取 conversationRegistry 时短暂持锁，提取引用后立即释放
	s.mu.RLock()
	registry := s.conversationRegistry
	s.mu.RUnlock()

	poolRoute, err := s.poolRouteForRequest(ctx, kind, requestedModel)
	if err != nil {
		return nil, fmt.Errorf("选择 %s 分组失败: %w", kind, err)
	}

	// 严格按“命中分组 -> 兜底分组”的顺序选择。只有前一分组没有可实际尝试的渠道时，
	// 才进入下一分组，避免兜底分组中的渠道抢占正常模型路由。
	poolIDs := make([]string, 0, len(poolRoute))
	for _, pool := range poolRoute {
		poolIDs = append(poolIDs, pool.ID)
	}
	channelsByPool := s.getActiveChannelsByPool(kind, poolIDs)
	if route := PoolConstraintFrom(ctx); route.ChannelID != "" {
		for id, channels := range channelsByPool {
			filtered := make([]ChannelInfo, 0, len(channels))
			for _, ch := range channels {
				up := ch.upstream
				if up != nil && up.ID == route.ChannelID {
					filtered = append(filtered, ch)
				}
			}
			channelsByPool[id] = filtered
		}
	}
	var routedChannels []ChannelInfo
	for _, pool := range poolRoute {
		routedChannels = append(routedChannels, channelsByPool[pool.ID]...)
	}

	if selected, hasOverride, err := s.selectConversationRouteOverride(ctx, conversationID, kind, routedChannels, failedChannels, registry); err != nil {
		return nil, err
	} else if hasOverride {
		result, err := s.reserveAndReturn(selected, kind)
		if err != nil {
			return nil, fmt.Errorf("对话固定渠道 [%d] %s 暂不可准入: %w", selected.ChannelIndex, selected.Upstream.Name, err)
		}
		return result, nil
	}

	if len(routedChannels) == 0 {
		return nil, noActiveChannelError(kind)
	}
	// 准入竞争只排除本轮候选，不改写调用方记录的真实请求失败，也不递归重读配置。
	excluded := make(map[int]bool, len(failedChannels))
	for index, failed := range failedChannels {
		excluded[index] = failed
	}
	manager := s.getMetricsManager(kind)
	for poolIndex, pool := range poolRoute {
		channels := s.preferIntentChannel(ctx, channelsByPool[pool.ID])
		for range len(channels) {
			if ctx != nil && ctx.Err() != nil {
				return nil, ctx.Err()
			}
			selected := s.selectPriorityChannel(channels, excluded, kind, manager)
			if selected == nil {
				selected = s.selectFallbackChannel(channels, excluded, kind)
			}
			if selected == nil {
				break
			}
			result, err := s.reserveAndReturn(selected, kind)
			if IsCandidateUnavailable(err) {
				excluded[selected.ChannelIndex] = true
				continue
			}
			if err != nil {
				return nil, err
			}
			if poolIndex > 0 {
				log.Printf("[%s-GroupFallback] 前序分组已无可准入渠道，切换到分组 %s", kindSchedulerLogPrefix(kind), pool.Name)
			}
			return result, nil
		}
	}
	return nil, fmt.Errorf("所有渠道都不可用")
}

func (s *ChannelScheduler) selectConversationRouteOverride(
	ctx context.Context,
	conversationID string,
	kind ChannelKind,
	routedChannels []ChannelInfo,
	failedChannels map[int]bool,
	registry *conversation.Registry,
) (*SelectionResult, bool, error) {
	if conversationID == "" || registry == nil {
		return nil, false, nil
	}
	override, err := registry.LookupRouteOverride(ctx, conversationID)
	if err != nil {
		return nil, true, fmt.Errorf("读取对话固定渠道失败: %w", err)
	}
	if override == nil {
		return nil, false, nil
	}
	if override.Kind != string(kind) {
		return nil, true, fmt.Errorf("该对话已固定到 %s 渠道池，当前请求为 %s", override.Kind, kind)
	}
	for _, ch := range routedChannels {
		if override.ChannelID == "" {
			return nil, true, fmt.Errorf("固定渠道缺少稳定身份，请重新选择")
		}
		if ch.upstream == nil || ch.upstream.ID != override.ChannelID {
			continue
		}
		if failedChannels[ch.Index] {
			return nil, true, fmt.Errorf("对话固定渠道 [%d] %s 已在本次请求中失败", ch.Index, ch.Name)
		}
		if ch.Status != "active" {
			return nil, true, fmt.Errorf("对话固定渠道 [%d] %s 当前状态为 %s", ch.Index, ch.Name, ch.Status)
		}
		upstream := ch.upstream
		if upstream == nil || len(upstream.APIKeys) == 0 {
			return nil, true, fmt.Errorf("对话固定渠道 [%d] %s 没有可用 API 密钥", ch.Index, ch.Name)
		}
		prefix := kindSchedulerLogPrefix(kind)
		log.Printf("[%s-Override] 对话固定渠道: [%d] %s", prefix, ch.Index, ch.Name)
		return &SelectionResult{
			Upstream:     upstream,
			ChannelIndex: ch.Index,
			Reason:       "conversation_route_override",
		}, true, nil
	}
	return nil, true, fmt.Errorf("对话固定渠道 [%d] 当前不可用", override.ChannelIndex)
}

func noActiveChannelError(kind ChannelKind) error {
	switch kind {
	case ChannelKindMessages:
		return fmt.Errorf("没有可用的活跃 Messages 渠道")
	case ChannelKindGemini:
		return fmt.Errorf("没有可用的活跃 Gemini 渠道")
	case ChannelKindResponses:
		return fmt.Errorf("没有可用的活跃 Responses 渠道")
	case ChannelKindChat:
		return fmt.Errorf("没有可用的活跃 Chat 渠道")
	case ChannelKindImages:
		return fmt.Errorf("没有可用的活跃 Images 渠道")
	case ChannelKindEmbeddings:
		return fmt.Errorf("没有可用的活跃 Embeddings 渠道")
	case ChannelKindDecisions:
		return fmt.Errorf("没有可用的活跃 Decisions 渠道")
	default:
		return fmt.Errorf("不支持的渠道类型: %s", kind)
	}
}

func (s *ChannelScheduler) selectPriorityChannel(
	activeChannels []ChannelInfo,
	failedChannels map[int]bool,
	kind ChannelKind,
	metricsManager *metrics.MetricsManager,
) *SelectionResult {
	for _, ch := range activeChannels {
		if !canSelectChannel(ch, failedChannels) {
			continue
		}

		upstream := ch.upstream
		if !metricsManager.IsChannelHealthyWithKeys(upstream.BaseURL, upstream.APIKeys, ch.Index) {
			failureRate := metricsManager.CalculateChannelFailureRate(upstream.BaseURL, upstream.APIKeys, ch.Index)
			prefix := kindSchedulerLogPrefix(kind)
			log.Printf("[%s-Channel] 警告: 跳过不健康渠道: [%d] %s (失败率: %.1f%%)", prefix, ch.Index, ch.Name, failureRate*100)
			continue
		}
		// 渠道级熔断预检（Peek，不占 HalfOpen 探测名额）：Open 未到期直接跳过；
		// HalfOpen 名额空闲的渠道可作为候选，选中后由 reserveAndReturn 正式占位。
		if !s.allowByCircuitPeek(kind, ch.Index) {
			continue
		}

		// 候选已按促销和配置顺序排列，首个通过准入的渠道即为本次选择。
		prefix := kindSchedulerLogPrefix(kind)
		log.Printf("[%s-Channel] 选择渠道: [%d] %s (配置优先级: %d, 促销: %t)",
			prefix, ch.Index, upstream.Name, ch.Priority, ch.Promoted)
		reason := "priority_order"
		if ch.Promoted {
			reason = "promotion"
		}
		return &SelectionResult{
			Upstream:     upstream,
			ChannelIndex: ch.Index,
			Reason:       reason,
		}
	}
	return nil
}

// 模型映射不声明渠道能力；正常选择与恢复探测共用配置资格检查。
func canSelectChannel(channel ChannelInfo, failedChannels map[int]bool) bool {
	return !failedChannels[channel.Index] && channel.Status == config.ChannelStatusActive &&
		channel.upstream != nil && len(channel.upstream.APIKeys) > 0
}

// selectFallbackChannel 在正常候选都被健康检查过滤后，按配置顺序选择首个
// 可探测渠道。这里不能再按失败率或负载择优，否则会绕过用户指定的故障转移顺序。
// 熔断预检用 Peek（不占探测名额），选中后由 reserveAndReturn 正式占位。
func (s *ChannelScheduler) selectFallbackChannel(
	activeChannels []ChannelInfo,
	failedChannels map[int]bool,
	kind ChannelKind,
) *SelectionResult {
	// 不健康渠道只按配置顺序参与恢复探测，促销不会增加探测优先级。
	candidates := append([]ChannelInfo(nil), activeChannels...)
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].Priority != candidates[j].Priority {
			return candidates[i].Priority < candidates[j].Priority
		}
		return candidates[i].Index < candidates[j].Index
	})
	for _, channel := range candidates {
		if !canSelectChannel(channel, failedChannels) {
			continue
		}
		if !s.allowByCircuitPeek(kind, channel.Index) {
			continue
		}

		prefix := kindSchedulerLogPrefix(kind)
		log.Printf("[%s-Fallback] 警告: 按配置顺序选择探测渠道: [%d] %s", prefix, channel.Index, channel.Name)
		return &SelectionResult{
			Upstream:     channel.upstream,
			ChannelIndex: channel.Index,
			Reason:       "fallback",
		}
	}

	return nil
}
