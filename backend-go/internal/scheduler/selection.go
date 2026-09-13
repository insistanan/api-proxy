package scheduler

import (
	"context"
	"fmt"
	"log"

	"github.com/BenedictKing/api-proxy/internal/circuit"
	"github.com/BenedictKing/api-proxy/internal/config"
	"github.com/BenedictKing/api-proxy/internal/conversation"
	"github.com/BenedictKing/api-proxy/internal/metrics"
)

// SelectionResult 渠道选择结果
type SelectionResult struct {
	Upstream     *config.UpstreamConfig
	ChannelIndex int
	Reason       string // 选择原因（用于日志）
	// Kind 记录选择时的协议类型，便于调用方正确释放/转移在途预留。
	Kind ChannelKind
	// Reserved 表示本次选择是否已占用 in-flight 预留。
	// 调用方在失败重试前应 ReleaseChannelReservation；真正发出上游请求期间应保持预留，请求结束后再释放。
	Reserved bool
	// Probe 表示本次选择占用了渠道熔断器的 HalfOpen 探测名额（限额 1）。
	// 调用方必须在请求结束后以 RecordCircuitSuccess/Failure/Neutral 释放，
	// 否则该渠道会卡在 HalfOpen 无法被再次选中。
	Probe bool
}

// reserveAndReturn 原子预留选中渠道的在途计数，避免并发选渠时都看到相同负载。
// 选中渠道若处于熔断半开状态，同时以 CircuitAllow 正式占用探测名额并记录
// Probe 标记（候选过滤阶段只做了 Peek 预判，未占位）。
func (s *ChannelScheduler) reserveAndReturn(result *SelectionResult, kind ChannelKind) *SelectionResult {
	if result == nil {
		return nil
	}
	result.Kind = kind
	if s == nil {
		return result
	}
	s.ReserveChannel(kind, result.ChannelIndex)
	result.Reserved = true
	if result.Probe {
		// 候选过滤（allowByCircuitPeek）时已确认会放行；这里正式占位。
		// 并发窗口内名额被抢的极端情况下 Allow 可能返回 false，此时
		// 清掉 Probe 标记、交由调用方按"渠道不可用"处理。
		channelName := ""
		if result.Upstream != nil {
			channelName = result.Upstream.Name
		}
		if allow := s.CircuitAllow(kind, result.ChannelIndex, channelName); allow.Allowed {
			result.Probe = allow.Probe
		} else {
			result.Probe = false
		}
	}
	return result
}

// ReserveChannel 增加渠道在途预留（选渠后、请求结束前）
func (s *ChannelScheduler) ReserveChannel(kind ChannelKind, channelIndex int) {
	if s == nil || channelIndex < 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.inFlightByKind == nil {
		s.inFlightByKind = make(map[ChannelKind]map[int]int64)
	}
	byChannel := s.inFlightByKind[kind]
	if byChannel == nil {
		byChannel = make(map[int]int64)
		s.inFlightByKind[kind] = byChannel
	}
	byChannel[channelIndex]++
}

// ReleaseChannelReservation 释放选渠预留
func (s *ChannelScheduler) ReleaseChannelReservation(kind ChannelKind, channelIndex int) {
	if s == nil || channelIndex < 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	byChannel := s.inFlightByKind[kind]
	if byChannel == nil {
		return
	}
	if byChannel[channelIndex] <= 1 {
		delete(byChannel, channelIndex)
		if len(byChannel) == 0 {
			delete(s.inFlightByKind, kind)
		}
		return
	}
	byChannel[channelIndex]--
}

// GetChannelInFlight 返回选渠预留的在途计数（不含性能画像中的真实 ActiveRequests）
func (s *ChannelScheduler) GetChannelInFlight(kind ChannelKind, channelIndex int) int64 {
	if s == nil {
		return 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if byChannel := s.inFlightByKind[kind]; byChannel != nil {
		return byChannel[channelIndex]
	}
	return 0
}

// SelectChannel 按配置的故障转移顺序选择渠道。
// 优先级: 对话路由覆盖 > 当前分组中按顺序排列的第一个可用渠道 > 兜底分组。
//
// 渠道顺序是显式配置，不再由促销、对话亲和、自适应评分、负载或稳定散列改写。
// 这样每个请求都会先尝试第一个渠道，只有该渠道失败后才进入下一个渠道。
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

	poolRoute, err := config.SelectChannelPoolRoute(s.getChannelPools(kind), requestedModel)
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
	selectedPool := poolRoute[len(poolRoute)-1]
	var activeChannels []ChannelInfo
	var routedChannels []ChannelInfo
	foundAttemptableGroup := false
	for _, pool := range poolRoute {
		channels := channelsByPool[pool.ID]
		routedChannels = append(routedChannels, channels...)
		if !foundAttemptableGroup && s.hasAttemptableChannel(channels, failedChannels, kind, requestedModel) {
			selectedPool = pool
			activeChannels = channels
			foundAttemptableGroup = true
		}
	}
	if !foundAttemptableGroup {
		activeChannels = channelsByPool[selectedPool.ID]
	}
	if len(poolRoute) > 1 && selectedPool.ID == config.DefaultChannelPoolID {
		prefix := kindSchedulerLogPrefix(kind)
		log.Printf("[%s-GroupFallback] 命中分组已无可用渠道，切换到兜底分组", prefix)
	}

	// 图片不参与选渠：是否直接处理图片或启用图片理解层，由被选中渠道的配置在
	// 请求发送前决定（见 prepareRequestForUpstream → visionlayer.PrepareRequest）。

	if selected, hasOverride, err := s.selectConversationRouteOverride(conversationID, kind, routedChannels, failedChannels, registry); err != nil {
		return nil, err
	} else if hasOverride {
		return s.reserveAndReturn(selected, kind), nil
	}

	if len(activeChannels) == 0 {
		return nil, noActiveChannelError(kind)
	}

	// 严格按配置优先级选择第一个可用渠道。failedChannels 只记录本次请求已经
	// 失败的渠道，因此外层 failover 会自然推进到下一个渠道。
	if selected := s.selectPriorityChannel(activeChannels, failedChannels, kind, requestedModel, s.getMetricsManager(kind)); selected != nil {
		return s.reserveAndReturn(selected, kind), nil
	}

	// 当前分组没有健康渠道时，保留最后的探测候选，让熔断恢复探测和历史配置
	// 仍能正常工作；该候选同样由 failedChannels 排除。
	fallback, err := s.selectFallbackChannel(activeChannels, failedChannels, kind)
	if err != nil {
		return nil, err
	}
	return s.reserveAndReturn(fallback, kind), nil
}

func (s *ChannelScheduler) selectConversationRouteOverride(
	conversationID string,
	kind ChannelKind,
	routedChannels []ChannelInfo,
	failedChannels map[int]bool,
	registry *conversation.Registry,
) (*SelectionResult, bool, error) {
	if conversationID == "" || registry == nil {
		return nil, false, nil
	}
	override, ok := registry.GetRouteOverride(conversationID)
	if !ok {
		return nil, false, nil
	}
	if override.Kind != string(kind) {
		return nil, true, fmt.Errorf("该对话已固定到 %s 渠道池，当前请求为 %s", override.Kind, kind)
	}
	for _, ch := range routedChannels {
		if ch.Index != override.ChannelIndex {
			continue
		}
		if failedChannels[ch.Index] {
			return nil, true, fmt.Errorf("对话固定渠道 [%d] %s 已在本次请求中失败", ch.Index, ch.Name)
		}
		if ch.Status != "active" {
			return nil, true, fmt.Errorf("对话固定渠道 [%d] %s 当前状态为 %s", ch.Index, ch.Name, ch.Status)
		}
		upstream := s.getUpstreamByIndex(ch.Index, kind)
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
	default:
		return fmt.Errorf("不支持的渠道类型: %s", kind)
	}
}

// selectConversationAffinity 对话级敏感：若对话已有"最近成功/尝试"命中的渠道，
// 且该渠道在本次分组中仍可用、健康且在途负载未过载，则沿用（保证同一对话不来回乱切）。
// 一旦渠道过载 / 不健康 / 已失败 / 不可用，便放行给后续负载均衡处理。
func (s *ChannelScheduler) selectConversationAffinity(
	activeChannels []ChannelInfo,
	failedChannels map[int]bool,
	kind ChannelKind,
	conversationID string,
	metricsManager *metrics.MetricsManager,
) *SelectionResult {
	if conversationID == "" {
		return nil
	}
	last, ok := s.GetConversationLastResolved(conversationID)
	if !ok || last == nil || last.Kind != string(kind) {
		return nil
	}
	preferredIdx := last.ChannelIndex

	for _, ch := range activeChannels {
		if ch.Index != preferredIdx || failedChannels[preferredIdx] {
			continue
		}
		if ch.Status != "active" {
			prefix := kindSchedulerLogPrefix(kind)
			log.Printf("[%s-ConvAffinity] 跳过对话亲和渠道 [%d] %s: 状态为 %s (conversation: %s)", prefix, preferredIdx, ch.Name, ch.Status, maskConversationID(conversationID))
			return nil
		}
		upstream := s.getUpstreamByIndex(preferredIdx, kind)
		if upstream == nil || len(upstream.APIKeys) == 0 {
			prefix := kindSchedulerLogPrefix(kind)
			log.Printf("[%s-ConvAffinity] 跳过对话亲和渠道 [%d]: 无可用密钥 (conversation: %s)", prefix, preferredIdx, maskConversationID(conversationID))
			return nil
		}
		if !metricsManager.IsChannelHealthyWithKeys(upstream.BaseURL, upstream.APIKeys, preferredIdx) {
			prefix := kindSchedulerLogPrefix(kind)
			log.Printf("[%s-ConvAffinity] 跳过对话亲和渠道 [%d] %s: 不健康 (conversation: %s)", prefix, preferredIdx, ch.Name, maskConversationID(conversationID))
			return nil
		}
		// 负载未过载才粘滞；过载时放行给负载均衡，避免单渠道被多对话打满。
		if !s.channelWithinAffinityLoad(kind, preferredIdx, upstream) {
			prefix := kindSchedulerLogPrefix(kind)
			log.Printf("[%s-ConvAffinity] 对话亲和渠道 [%d] %s 负载过载，放行给负载均衡 (inFlight: %d, conversation: %s)",
				prefix, preferredIdx, ch.Name, s.GetChannelInFlight(kind, preferredIdx), maskConversationID(conversationID))
			return nil
		}
		prefix := kindSchedulerLogPrefix(kind)
		log.Printf("[%s-ConvAffinity] 使用对话亲和渠道: [%d] %s (conversation: %s)", prefix, preferredIdx, ch.Name, maskConversationID(conversationID))
		return &SelectionResult{
			Upstream:     upstream,
			ChannelIndex: preferredIdx,
			Reason:       "conversation_affinity",
		}
	}
	return nil
}

// channelWithinAffinityLoad 判定某渠道是否仍在上限内，允许对话亲和沿用。
// 结合真实 ActiveRequests（性能画像）与选渠预留 in-flight，避免"空转排队"也不换渠道。
func (s *ChannelScheduler) channelWithinAffinityLoad(
	kind ChannelKind,
	channelIndex int,
	upstream *config.UpstreamConfig,
) bool {
	baseURLs := upstream.GetAllBaseURLs()
	inflight := s.GetChannelInFlight(kind, channelIndex)

	// profileManager 提供真实并发余量感知；其 ActiveRequests 按 baseURL+model 聚合，
	// 这里用聚合快照(全 model)近似。没有画像时退化为仅看选渠预留。
	if s.profileManager != nil {
		agg := s.profileManager.GetAggregateProfileSnapshot(baseURLs, upstream.APIKeys, "", channelIndex)
		if agg.ActiveRequests+inflight >= int64(affinityLoadThreshold) {
			return false
		}
	} else if inflight >= int64(affinityLoadThreshold) {
		return false
	}
	return true
}

// affinityLoadThreshold 对话亲和过载阈值：在途(预留+真实)达到该值即放行给负载均衡。
const affinityLoadThreshold = 3

func (s *ChannelScheduler) selectTraceAffinity(
	activeChannels []ChannelInfo,
	failedChannels map[int]bool,
	kind ChannelKind,
	conversationID string,
	metricsManager *metrics.MetricsManager,
) *SelectionResult {
	if conversationID == "" || s.traceAffinity == nil {
		return nil
	}
	preferredIdx, ok := s.traceAffinity.GetPreferredChannelForKind(string(kind), conversationID)
	if !ok {
		return nil
	}

	foundPreferredChannel := false
	affinityInvalidated := false
	for _, ch := range activeChannels {
		if ch.Index != preferredIdx || failedChannels[preferredIdx] {
			continue
		}
		foundPreferredChannel = true
		if ch.Status != "active" {
			prefix := kindSchedulerLogPrefix(kind)
			log.Printf("[%s-Affinity] 跳过亲和渠道 [%d] %s: 状态为 %s (conversation: %s)", prefix, preferredIdx, ch.Name, ch.Status, maskConversationID(conversationID))
			affinityInvalidated = true
			continue
		}
		upstream := s.getUpstreamByIndex(preferredIdx, kind)
		if upstream == nil || len(upstream.APIKeys) == 0 {
			prefix := kindSchedulerLogPrefix(kind)
			log.Printf("[%s-Affinity] 跳过亲和渠道 [%d]: 无可用密钥 (conversation: %s)", prefix, preferredIdx, maskConversationID(conversationID))
			affinityInvalidated = true
			continue
		}
		if !metricsManager.IsChannelHealthyWithKeys(upstream.BaseURL, upstream.APIKeys, preferredIdx) {
			failureRate := metricsManager.CalculateChannelFailureRate(upstream.BaseURL, upstream.APIKeys, preferredIdx)
			prefix := kindSchedulerLogPrefix(kind)
			log.Printf("[%s-Affinity] 跳过亲和渠道 [%d] %s: 不健康 (失败率: %.1f%%, conversation: %s)", prefix, preferredIdx, ch.Name, failureRate*100, maskConversationID(conversationID))
			affinityInvalidated = true
			continue
		}
		prefix := kindSchedulerLogPrefix(kind)
		log.Printf("[%s-Affinity] 使用 Trace 亲和渠道: [%d] %s (conversation: %s)", prefix, preferredIdx, ch.Name, maskConversationID(conversationID))
		return &SelectionResult{
			Upstream:     upstream,
			ChannelIndex: preferredIdx,
			Reason:       "trace_affinity",
		}
	}

	if !foundPreferredChannel || affinityInvalidated {
		s.traceAffinity.RemoveForKind(string(kind), conversationID)
		prefix := kindSchedulerLogPrefix(kind)
		if affinityInvalidated {
			log.Printf("[%s-Affinity] 清除失效的 Trace 亲和性: conversation=%s, channel=%d (渠道不健康或状态异常)", prefix, maskConversationID(conversationID), preferredIdx)
		} else {
			log.Printf("[%s-Affinity] 清除失效的 Trace 亲和性: conversation=%s, channel=%d (渠道不存在)", prefix, maskConversationID(conversationID), preferredIdx)
		}
	}
	return nil
}

func (s *ChannelScheduler) selectAdaptiveChannel(
	activeChannels []ChannelInfo,
	failedChannels map[int]bool,
	kind ChannelKind,
	requestedModel string,
	conversationID string,
	metricsManager *metrics.MetricsManager,
) *SelectionResult {
	s.mu.RLock()
	adaptiveScheduler := s.adaptiveScheduler
	s.mu.RUnlock()
	if adaptiveScheduler == nil {
		return nil
	}

	result := adaptiveScheduler.SelectBestChannel(
		activeChannels,
		failedChannels,
		kind,
		requestedModel,
		conversationID,
		metricsManager.IsChannelHealthyMultiURL,
		s.getUpstreamByIndex,
		func(channelIndex int) int64 {
			return s.GetChannelInFlight(kind, channelIndex)
		},
	)
	if result != nil {
		return result
	}
	prefix := kindSchedulerLogPrefix(kind)
	log.Printf("[%s-Adaptive] 自适应调度未找到可用渠道，降级到优先级调度", prefix)
	return nil
}

func (s *ChannelScheduler) selectPriorityChannel(
	activeChannels []ChannelInfo,
	failedChannels map[int]bool,
	kind ChannelKind,
	requestedModel string,
	metricsManager *metrics.MetricsManager,
) *SelectionResult {
	type priorityCandidate struct {
		channel  ChannelInfo
		upstream *config.UpstreamConfig
	}
	var samePriorityCandidates []priorityCandidate
	currentPriority := -1

	for _, ch := range activeChannels {
		if failedChannels[ch.Index] {
			continue
		}
		if ch.Status != "active" {
			prefix := kindSchedulerLogPrefix(kind)
			log.Printf("[%s-Channel] 跳过非活跃渠道: [%d] %s (状态: %s)", prefix, ch.Index, ch.Name, ch.Status)
			continue
		}

		upstream := s.getUpstreamByIndex(ch.Index, kind)
		if upstream == nil || len(upstream.APIKeys) == 0 {
			continue
		}
		if requestedModel != "" && len(config.ResolveUpstreamModelList(requestedModel, upstream)) == 0 {
			prefix := kindSchedulerLogPrefix(kind)
			log.Printf("[%s-Channel] 跳过不支持模型的渠道: [%d] %s (模型: %s)", prefix, ch.Index, ch.Name, requestedModel)
			continue
		}
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

		if len(samePriorityCandidates) == 0 {
			currentPriority = ch.Priority
		} else if ch.Priority != currentPriority {
			break
		}
		samePriorityCandidates = append(samePriorityCandidates, priorityCandidate{
			channel:  ch,
			upstream: upstream,
		})
	}

	if len(samePriorityCandidates) == 0 {
		return nil
	}
	// 同一优先级也保持配置顺序，不再按 in-flight 或动态评分选择，避免请求
	// 在多个渠道之间随机分散。reorder 接口会为每个分组写入连续优先级，
	// 因此这里的第一个候选就是本次应该优先尝试的渠道。
	best := samePriorityCandidates[0]
	bestLoad := s.GetChannelInFlight(kind, best.channel.Index)
	prefix := kindSchedulerLogPrefix(kind)
	log.Printf("[%s-Channel] 选择渠道: [%d] %s (配置优先级: %d, 动态分数: %.1f, inFlight: %d, samePriorityCandidates: %d)",
		prefix, best.channel.Index, best.upstream.Name, best.channel.Priority, best.channel.Score, bestLoad, len(samePriorityCandidates))
	return &SelectionResult{
		Upstream:     best.upstream,
		ChannelIndex: best.channel.Index,
		Reason:       "priority_order",
		// Peek 预检通过不代表已占位；标记待 reserveAndReturn 以 CircuitAllow
		// 正式占用（可能取得 HalfOpen 探测名额）。
		Probe: true,
	}
}

// hasAttemptableChannel 判断当前分组是否仍有渠道值得在本次请求中尝试。
// 健康度不在这里过滤：调度器会先跳过不健康渠道，但仍保留一次组内降级尝试；
// 真正请求失败后 failedChannels 会确保下一次选择进入后续分组。
func (s *ChannelScheduler) hasAttemptableChannel(
	channels []ChannelInfo,
	failedChannels map[int]bool,
	kind ChannelKind,
	requestedModel string,
) bool {
	for _, channel := range channels {
		if failedChannels[channel.Index] || channel.Status != config.ChannelStatusActive {
			continue
		}
		upstream := s.getUpstreamByIndex(channel.Index, kind)
		if upstream == nil || len(upstream.APIKeys) == 0 {
			continue
		}
		if requestedModel != "" && len(config.ResolveUpstreamModelList(requestedModel, upstream)) == 0 {
			continue
		}
		return true
	}
	return false
}

// allowByCircuit 渠道级熔断放行判定。未启用（circuitManager 为 nil）时恒放行。
// Open 未到期返回 false；冷却到期惰性转 HalfOpen 并占用探测名额放行。
// 探测名额由渠道级记账（scheduler 层 RecordCircuit* 系列）释放。
func (s *ChannelScheduler) allowByCircuit(kind ChannelKind, channelIndex int, channelName string) bool {
	return s.AllowChannelByCircuit(kind, channelIndex, channelName)
}

// allowByCircuitPeek 渠道级熔断预判（选渠候选过滤用）：只读不占探测名额。
// 候选通过后由 reserveAndReturn 以 CircuitAllow 正式占位。
func (s *ChannelScheduler) allowByCircuitPeek(kind ChannelKind, channelIndex int) bool {
	manager := s.CircuitManager()
	if manager == nil {
		return true
	}
	return manager.Peek(string(kind), channelIndex).Allowed
}

// CircuitAllow 正式占用熔断放行判定（可能占用 HalfOpen 探测名额）。
// 选中渠道后调用；结果的 Probe 标记必须由调用方以 RecordCircuit* 记账释放。
// 日志口径与 AllowChannelByCircuit 一致。
func (s *ChannelScheduler) CircuitAllow(kind ChannelKind, channelIndex int, channelName string) circuit.AllowResult {
	manager := s.CircuitManager()
	if manager == nil {
		return circuit.AllowResult{Allowed: true}
	}
	result := manager.Allow(string(kind), channelIndex)
	if result.Allowed {
		if result.Probe {
			log.Printf("[Circuit-Probe] 渠道 [%d] %s 处于半开状态，放行探测请求", channelIndex, channelName)
		}
		return result
	}
	prefix := kindSchedulerLogPrefix(kind)
	snap := manager.SnapshotEntry(string(kind), channelIndex)
	log.Printf("[%s-Circuit] 跳过熔断中的渠道: [%d] %s (冷却剩余: %ds)", prefix, channelIndex, channelName, snap.CooldownRemainingSec)
	return result
}

// AllowChannelByCircuit 供外部（如单渠道代理 handleSingleChannelProxy）调用渠道级熔断放行判定。
func (s *ChannelScheduler) AllowChannelByCircuit(kind ChannelKind, channelIndex int, channelName string) bool {
	manager := s.CircuitManager()
	if manager == nil {
		return true
	}
	result := manager.Allow(string(kind), channelIndex)
	if result.Allowed {
		if result.Probe {
			log.Printf("[Circuit-Probe] 渠道 [%d] %s 处于半开状态，放行探测请求", channelIndex, channelName)
		}
		return true
	}
	prefix := kindSchedulerLogPrefix(kind)
	snap := manager.SnapshotEntry(string(kind), channelIndex)
	log.Printf("[%s-Circuit] 跳过熔断中的渠道: [%d] %s (冷却剩余: %ds)",
		prefix, channelIndex, channelName, snap.CooldownRemainingSec)
	return false
}

// RecordCircuitSuccess / RecordCircuitFailure / RecordCircuitNeutral 渠道级熔断记账。
// 与 Key 级 Record* 正交：这里的粒度是 (kind, channelIndex) 整个渠道。
// Neutral 用于客户端取消、内容审核拦截等"结果不应计入渠道健康度"的场景。
func (s *ChannelScheduler) RecordCircuitSuccess(kind ChannelKind, channelIndex int) {
	if manager := s.CircuitManager(); manager != nil {
		manager.RecordSuccess(string(kind), channelIndex)
	}
}

func (s *ChannelScheduler) RecordCircuitFailure(kind ChannelKind, channelIndex int) {
	if manager := s.CircuitManager(); manager != nil {
		manager.RecordFailure(string(kind), channelIndex)
	}
}

func (s *ChannelScheduler) RecordCircuitNeutral(kind ChannelKind, channelIndex int) {
	if manager := s.CircuitManager(); manager != nil {
		manager.RecordNeutral(string(kind), channelIndex)
	}
}

// GetCircuitSnapshotForKind 返回指定协议的渠道熔断快照（dashboard 展示用）。
func (s *ChannelScheduler) GetCircuitSnapshotForKind(kind ChannelKind) map[int]circuit.Snapshot {
	manager := s.CircuitManager()
	if manager == nil {
		return nil
	}
	result := make(map[int]circuit.Snapshot)
	for _, entry := range manager.Snapshot() {
		if entry.Kind != string(kind) || entry.ChannelIndex < 0 {
			continue
		}
		result[entry.ChannelIndex] = entry.Snapshot
	}
	return result
}

// CircuitManagerSnapshot 返回全部渠道熔断器快照（管理 API 用）。
// 未启用熔断时返回空切片（非 nil，便于 JSON 序列化为 []）。
func (s *ChannelScheduler) CircuitManagerSnapshot() []circuit.Entry {
	manager := s.CircuitManager()
	if manager == nil {
		return []circuit.Entry{}
	}
	return manager.Snapshot()
}

// ResetCircuitBreaker 手动重置指定渠道的熔断器。
func (s *ChannelScheduler) ResetCircuitBreaker(kind string, channelIndex int) bool {
	manager := s.CircuitManager()
	if manager == nil {
		return false
	}
	return manager.Reset(kind, channelIndex)
}

func (s *ChannelScheduler) selectPromotedChannel(
	activeChannels []ChannelInfo,
	failedChannels map[int]bool,
	kind ChannelKind,
	reason string,
) *SelectionResult {
	promotedChannels := s.findPromotedChannels(activeChannels, kind)
	var promotedCandidates []ChannelInfo
	for _, promotedCh := range promotedChannels {
		if failedChannels[promotedCh.Index] {
			prefix := kindSchedulerLogPrefix(kind)
			log.Printf("[%s-Promotion] 警告: 促销渠道 [%d] %s 已在本次请求中失败，跳过", prefix, promotedCh.Index, promotedCh.Name)
			continue
		}
		upstream := s.getUpstreamByIndex(promotedCh.Index, kind)
		if upstream == nil || len(upstream.APIKeys) == 0 {
			prefix := kindSchedulerLogPrefix(kind)
			log.Printf("[%s-Promotion] 警告: 促销渠道 [%d] %s 无可用密钥，跳过", prefix, promotedCh.Index, promotedCh.Name)
			continue
		}
		promotedCandidates = append(promotedCandidates, promotedCh)
	}
	if len(promotedCandidates) > 0 {
		selected := s.pickLeastLoadedChannel(promotedCandidates, kind)
		upstream := s.getUpstreamByIndex(selected.Index, kind)
		metricsManager := s.getMetricsManager(kind)
		failureRate := metricsManager.CalculateChannelFailureRate(upstream.BaseURL, upstream.APIKeys, selected.Index)
		prefix := kindSchedulerLogPrefix(kind)
		log.Printf("[%s-Promotion] 促销期优先选择渠道: [%d] %s (失败率: %.1f%%, inFlight: %d, candidates: %d)",
			prefix, selected.Index, upstream.Name, failureRate*100, s.GetChannelInFlight(kind, selected.Index), len(promotedCandidates))
		return &SelectionResult{
			Upstream:     upstream,
			ChannelIndex: selected.Index,
			Reason:       reason,
		}
	}
	return nil
}

// pickLeastLoadedChannel 在候选渠道中选择 in-flight 最低者（用于促销等多候选场景）
func (s *ChannelScheduler) pickLeastLoadedChannel(candidates []ChannelInfo, kind ChannelKind) *ChannelInfo {
	if len(candidates) == 0 {
		return nil
	}
	bestIdx := 0
	bestLoad := s.GetChannelInFlight(kind, candidates[0].Index)
	for i := 1; i < len(candidates); i++ {
		load := s.GetChannelInFlight(kind, candidates[i].Index)
		if load < bestLoad || (load == bestLoad && candidates[i].Index < candidates[bestIdx].Index) {
			bestIdx = i
			bestLoad = load
		}
	}
	return &candidates[bestIdx]
}

// findPromotedChannels 查找所有处于促销期的渠道（按优先级排序）
func (s *ChannelScheduler) findPromotedChannels(activeChannels []ChannelInfo, kind ChannelKind) []ChannelInfo {
	var promoted []ChannelInfo
	for i := range activeChannels {
		ch := &activeChannels[i]
		if ch.Status != "active" {
			continue
		}
		upstream := s.getUpstreamByIndex(ch.Index, kind)
		if upstream != nil && config.IsChannelInPromotion(upstream) {
			promoted = append(promoted, *ch)
		}
	}
	return promoted
}

// selectFallbackChannel 在正常候选都被健康检查过滤后，按配置顺序选择首个
// 可探测渠道。这里不能再按失败率或负载择优，否则会绕过用户指定的故障转移顺序。
// 熔断预检用 Peek（不占探测名额），选中后由 reserveAndReturn 正式占位。
func (s *ChannelScheduler) selectFallbackChannel(
	activeChannels []ChannelInfo,
	failedChannels map[int]bool,
	kind ChannelKind,
) (*SelectionResult, error) {
	for _, channel := range activeChannels {
		ch := &channel
		if failedChannels[ch.Index] {
			continue
		}
		// 跳过非 active 状态的渠道
		if ch.Status != "active" {
			continue
		}

		upstream := s.getUpstreamByIndex(ch.Index, kind)
		if upstream == nil || len(upstream.APIKeys) == 0 {
			continue
		}
		if !s.allowByCircuitPeek(kind, ch.Index) {
			continue
		}

		prefix := kindSchedulerLogPrefix(kind)
		log.Printf("[%s-Fallback] 警告: 按配置顺序选择探测渠道: [%d] %s", prefix, ch.Index, ch.Name)
		return &SelectionResult{
			Upstream:     upstream,
			ChannelIndex: ch.Index,
			Reason:       "fallback",
			Probe:        true,
		}, nil
	}

	return nil, fmt.Errorf("所有渠道都不可用")
}

// maskConversationID 掩码内部 conversation record ID（保护隐私）。
func maskConversationID(conversationID string) string {
	if len(conversationID) <= 16 {
		return "***"
	}
	return conversationID[:8] + "***" + conversationID[len(conversationID)-4:]
}
