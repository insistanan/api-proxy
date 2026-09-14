package scheduler

import (
	"context"
	"fmt"
	"log"
	"sync"

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
	// Permit 是本次选择取得的许可句柄（R2-b）：持有 in-flight 预留和
	// 可能取得的半开探测名额。调用方必须在请求结束时 RecordHealth
	// 记账一次 + Release 释放；两者均可安全重复调用（幂等）。
	Permit *ChannelPermit

	// probeIntent 内部标记：该候选来自需要正式探测占位的路径
	// （selectPriorityChannel / selectFallbackChannel）。
	probeIntent bool
}

// Reserved 表示本次选择是否已占用 in-flight 预留（许可视图）。
func (r *SelectionResult) Reserved() bool {
	return r != nil && r.permitReserved()
}

// Probe 表示本次选择占用了渠道熔断器的 HalfOpen 探测名额（许可视图）。
func (r *SelectionResult) Probe() bool {
	return r != nil && r.permitProbe()
}

// permitReserved / permitProbe 供 SelectionResult 兼容视图读取。
func (r *SelectionResult) permitReserved() bool {
	return r != nil && r.Permit != nil && r.Permit.isReserved()
}

func (r *SelectionResult) permitProbe() bool {
	return r != nil && r.Permit != nil && r.Permit.isProbe()
}

// permitNeedsProbe 表示该选择是否需要正式占用探测名额
// （候选来自 priority / fallback 探测路径，Peek 预检已通过）。
func (r *SelectionResult) permitNeedsProbe() bool {
	return r != nil && r.probeIntent
}

// ChannelPermit 渠道许可句柄（R2-b）：聚合一次选渠取得的资源——
// in-flight 预留与半开探测名额（后者绑定取得时的熔断器状态代次）。
// 生命周期：取得后立即安排 defer 释放；正常结束先 RecordHealth（恰好
// 一次），再 Release；异常退出仅 Release 自有资源。重复记账视为契约
// 错误，会记录警告（测试可发现）。
type ChannelPermit struct {
	scheduler  *ChannelScheduler
	kind       ChannelKind
	index      int
	generation uint64 // 取得探测名额时的熔断器状态代次

	mu          sync.Mutex
	reserved    bool // 持有 in-flight 预留
	probe       bool // 持有半开探测名额
	recorded    bool // 健康结果已记账
	releasedRes bool // in-flight 已释放
}

// RecordHealth 按渠道健康结果记账（恰好一次；重复记录警告并跳过）。
func (p *ChannelPermit) RecordHealth(success bool) {
	if success {
		p.RecordHealthSuccess()
	} else {
		p.RecordHealthFailure()
	}
}

// RecordHealthSuccess 成功记账（恰好一次）。
func (p *ChannelPermit) RecordHealthSuccess() {
	p.recordHealth(circuitOutcomeSuccess)
}

// RecordHealthFailure 真实渠道故障记账（恰好一次）。
func (p *ChannelPermit) RecordHealthFailure() {
	p.recordHealth(circuitOutcomeFailure)
}

// RecordHealthNeutral 中性结束记账（恰好一次）：取消、审核拒绝、本地错误。
func (p *ChannelPermit) RecordHealthNeutral() {
	p.recordHealth(circuitOutcomeNeutral)
}

func (p *ChannelPermit) recordHealth(outcome circuitOutcome) {
	if p == nil {
		return
	}
	p.mu.Lock()
	if p.recorded {
		p.mu.Unlock()
		log.Printf("[Circuit-Permit] 警告: 渠道 [%d] 重复提交健康结果，忽略后续记账", p.index)
		return
	}
	p.recorded = true
	probe := p.probe
	p.probe = false
	generation := p.generation
	p.mu.Unlock()

	if p.scheduler == nil {
		return
	}
	p.scheduler.recordCircuitResult(p.kind, p.index, generation, probe, outcome)
}

// Release 释放许可剩余资源（幂等）：in-flight 预留与未记账的探测名额。
// 未记账即释放时探测名额按中性释放，不改变健康度。
func (p *ChannelPermit) Release() {
	if p == nil {
		return
	}
	p.mu.Lock()
	reserved := p.reserved
	p.reserved = false
	probe := p.probe
	p.probe = false
	p.mu.Unlock()

	if p.scheduler == nil {
		return
	}
	if reserved {
		p.scheduler.ReleaseChannelReservation(p.kind, p.index)
	}
	if probe {
		p.scheduler.RecordCircuitNeutralGeneration(p.kind, p.index, p.generation)
	}
}

// ReleaseReservation 仅释放 in-flight 预留（幂等），不影响探测名额——
// 渠道失败继续 failover 时调用：预留立即归还，探测名额留给本次请求的
// 健康记账。
func (p *ChannelPermit) ReleaseReservation() {
	if p == nil {
		return
	}
	p.mu.Lock()
	reserved := p.reserved
	p.reserved = false
	p.mu.Unlock()

	if reserved && p.scheduler != nil {
		p.scheduler.ReleaseChannelReservation(p.kind, p.index)
	}
}

func (p *ChannelPermit) isReserved() bool {
	if p == nil {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.reserved
}

func (p *ChannelPermit) isProbe() bool {
	if p == nil {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.probe
}

// setProbe 记录取得的探测名额及其状态代次。
func (p *ChannelPermit) setProbe(generation uint64) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.probe = true
	p.generation = generation
}

type circuitOutcome uint8

const (
	circuitOutcomeSuccess circuitOutcome = iota
	circuitOutcomeFailure
	circuitOutcomeNeutral
)

// recordCircuitResult 按代次记账的统一出口（scheduler 侧）。
func (s *ChannelScheduler) recordCircuitResult(kind ChannelKind, channelIndex int, generation uint64, probe bool, outcome circuitOutcome) {
	manager := s.CircuitManager()
	if manager == nil {
		return
	}
	switch outcome {
	case circuitOutcomeSuccess:
		manager.RecordSuccessGeneration(string(kind), channelIndex, generation)
	case circuitOutcomeFailure:
		manager.RecordFailureGeneration(string(kind), channelIndex, generation)
	default:
		manager.RecordNeutralGeneration(string(kind), channelIndex, generation)
	}
}

// errCandidateUnavailable 表示选中的候选在正式准入（CircuitAllow）阶段
// 被拒绝：调用方应归还本次在途预留后有界地继续选择，不得发送上游请求。
var errCandidateUnavailable = fmt.Errorf("选中的渠道在正式准入阶段被拒绝")

// IsCandidateUnavailable 判断错误是否为"候选在正式准入阶段被拒绝"。
func IsCandidateUnavailable(err error) bool {
	return err == errCandidateUnavailable
}

// reserveAndReturn 原子预留选中渠道的在途计数，避免并发选渠时都看到相同负载。
// 选中渠道若处于熔断半开状态，同时以 CircuitAllow 正式占用探测名额并记录
// 在许可句柄上（候选过滤阶段只做了 Peek 预判，未占位）。
// 正式准入被拒绝时：立即归还本次在途预留，返回 errCandidateUnavailable，
// 由 SelectChannel 有界重选——不发送未获准的请求（R2-b）。
func (s *ChannelScheduler) reserveAndReturn(result *SelectionResult, kind ChannelKind) (*SelectionResult, error) {
	if result == nil {
		return nil, nil
	}
	result.Kind = kind
	if s == nil {
		return result, nil
	}
	s.ReserveChannel(kind, result.ChannelIndex)
	permit := &ChannelPermit{
		scheduler: s,
		kind:      kind,
		index:     result.ChannelIndex,
		reserved:  true,
	}
	result.Permit = permit

	if result.permitNeedsProbe() {
		// 候选过滤（allowByCircuitPeek）时已确认会放行；这里正式占位。
		// 并发窗口内名额被抢时 Allow 返回 false：立即归还本次在途预留，
		// 返回明确的候选不可用错误，由选渠逻辑有界地继续选择；
		// 不能清掉 Probe 标记后继续发送。
		channelName := ""
		if result.Upstream != nil {
			channelName = result.Upstream.Name
		}
		if allow := s.CircuitAllow(kind, result.ChannelIndex, channelName); allow.Allowed {
			permit.setProbe(allow.Generation)
		} else {
			permit.Release()
			result.Permit = nil
			return nil, errCandidateUnavailable
		}
	}
	return result, nil
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
		result, err := s.reserveAndReturn(selected, kind)
		if err != nil {
			return nil, err
		}
		return result, nil
	}

	if len(activeChannels) == 0 {
		return nil, noActiveChannelError(kind)
	}

	// 严格按配置优先级选择第一个可用渠道。failedChannels 只记录本次请求已经
	// 失败的渠道，因此外层 failover 会自然推进到下一个渠道。
	if selected := s.selectPriorityChannel(activeChannels, failedChannels, kind, requestedModel, s.getMetricsManager(kind)); selected != nil {
		result, err := s.reserveAndReturn(selected, kind)
		if err != nil {
			if IsCandidateUnavailable(err) {
				failedChannels[selected.ChannelIndex] = true
				return s.SelectChannel(ctx, conversationID, failedChannels, kind, requestedModel)
			}
			return nil, err
		}
		return result, nil
	}

	// 当前分组没有健康渠道时，保留最后的探测候选，让熔断恢复探测和历史配置
	// 仍能正常工作；该候选同样由 failedChannels 排除。
	fallback, err := s.selectFallbackChannel(activeChannels, failedChannels, kind)
	if err != nil {
		return nil, err
	}
	result, err := s.reserveAndReturn(fallback, kind)
	if err != nil {
		if IsCandidateUnavailable(err) {
			failedChannels[fallback.ChannelIndex] = true
			return s.SelectChannel(ctx, conversationID, failedChannels, kind, requestedModel)
		}
		return nil, err
	}
	return result, nil
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
		probeIntent: true,
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

// NewCircuitProbePermit 为不经选渠编排的直查路径（如 llmgate 按 ID 选渠）
// 构造只持有半开探测名额的许可句柄。调用方负责在调用结束/探测结束时
// RecordHealth + Release。
func (s *ChannelScheduler) NewCircuitProbePermit(kind ChannelKind, channelIndex int, generation uint64) *ChannelPermit {
	return &ChannelPermit{
		scheduler:  s,
		kind:       kind,
		index:      channelIndex,
		generation: generation,
		probe:      true,
	}
}

// NewReservationPermit 为已手动 ReserveChannel 的路径（如 visionlayer
// 列表候选）构造只持有 in-flight 预留的许可句柄，统一释放出口。
func (s *ChannelScheduler) NewReservationPermit(kind ChannelKind, channelIndex int) *ChannelPermit {
	return &ChannelPermit{
		scheduler: s,
		kind:      kind,
		index:     channelIndex,
		reserved:  true,
	}
}

// RecordCircuitNeutralGeneration 带状态代次的中性记账（许可句柄释放用）。
func (s *ChannelScheduler) RecordCircuitNeutralGeneration(kind ChannelKind, channelIndex int, generation uint64) {
	if manager := s.CircuitManager(); manager != nil {
		manager.RecordNeutralGeneration(string(kind), channelIndex, generation)
	}
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
			probeIntent:  true,
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
