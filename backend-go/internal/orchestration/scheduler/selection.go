package scheduler

import (
	"context"
	"fmt"
	"log"
	"sort"
	"sync"

	"github.com/insistanan/api-proxy/internal/dialogue/conversation"
	"github.com/insistanan/api-proxy/internal/infra/config"
	"github.com/insistanan/api-proxy/internal/orchestration/circuit"
	"github.com/insistanan/api-proxy/internal/storage/metrics"
)

// SelectionResult 渠道选择结果
type SelectionResult struct {
	Upstream     *config.UpstreamConfig
	ChannelIndex int
	Reason       string // 选择原因（用于日志）
	// Kind 记录选择时的协议类型，便于调用方正确释放/转移在途预留。
	Kind ChannelKind
	// Permit 是本次选择取得的许可句柄（R2-b）：持有 in-flight 预留和
	// 可能取得的半开探测名额。调用方在请求结束时先提交健康结果，
	// 再 Release 释放；重复调用无副作用。
	Permit *ChannelPermit

	// probeIntent 内部标记：该候选来自需要正式探测占位的路径
	// （selectPriorityChannel / selectFallbackChannel）。
	probeIntent bool
}

// ChannelPermit 持有一次选渠的资源。预留释放和健康记账分别通过
// 标准库的一次性回调完成；Release 把尚未记账的结果按中性结束。
// 创建后仅调用方法，不再修改回调；熔断代次由记账回调捕获。
type ChannelPermit struct {
	releaseReservation func()
	finishHealth       func(circuitOutcome)
}

func (p *ChannelPermit) RecordHealthSuccess() { p.recordHealth(circuitOutcomeSuccess) }
func (p *ChannelPermit) RecordHealthFailure() { p.recordHealth(circuitOutcomeFailure) }
func (p *ChannelPermit) RecordHealthNeutral() { p.recordHealth(circuitOutcomeNeutral) }

func (p *ChannelPermit) recordHealth(outcome circuitOutcome) {
	if p != nil && p.finishHealth != nil {
		p.finishHealth(outcome)
	}
}

// Release 释放预留，并中性结束尚未记账的熔断许可；重复调用无副作用。
func (p *ChannelPermit) Release() {
	p.ReleaseReservation()
	p.RecordHealthNeutral()
}

// ReleaseReservation 在渠道切换前归还预留，健康结果仍可随后记账。
func (p *ChannelPermit) ReleaseReservation() {
	if p != nil && p.releaseReservation != nil {
		p.releaseReservation()
	}
}

func (s *ChannelScheduler) newChannelPermit(kind ChannelKind, index int, allow circuit.AllowResult, reserved bool) *ChannelPermit {
	permit := &ChannelPermit{}
	if reserved {
		permit.releaseReservation = sync.OnceFunc(func() { s.ReleaseChannelReservation(kind, index) })
	}
	var once sync.Once
	permit.finishHealth = func(outcome circuitOutcome) {
		once.Do(func() {
			if allow.Allowed || outcome != circuitOutcomeNeutral {
				s.recordCircuitResult(kind, index, allow.Generation, outcome)
			}
		})
	}
	return permit
}

type circuitOutcome uint8

const (
	circuitOutcomeSuccess circuitOutcome = iota
	circuitOutcomeFailure
	circuitOutcomeNeutral
)

// recordCircuitResult 按代次记账的统一出口（scheduler 侧）。
func (s *ChannelScheduler) recordCircuitResult(kind ChannelKind, channelIndex int, generation uint64, outcome circuitOutcome) {
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
// 被拒绝：调用方应有界地继续选择，不得发送上游请求。
var errCandidateUnavailable = fmt.Errorf("选中的渠道在正式准入阶段被拒绝")

// IsCandidateUnavailable 判断错误是否为"候选在正式准入阶段被拒绝"。
func IsCandidateUnavailable(err error) bool {
	return err == errCandidateUnavailable
}

// reserveAndReturn 先取得熔断许可，再预留渠道在途计数。
// 正式准入失败时返回 errCandidateUnavailable，由 SelectChannel 有界重选。
func (s *ChannelScheduler) reserveAndReturn(result *SelectionResult, kind ChannelKind) (*SelectionResult, error) {
	if result == nil {
		return nil, nil
	}
	result.Kind = kind
	if s == nil {
		return result, nil
	}
	allow := circuit.AllowResult{}
	if result.probeIntent {
		channelName := ""
		if result.Upstream != nil {
			channelName = result.Upstream.Name
		}
		allow = s.CircuitAllow(kind, result.ChannelIndex, channelName)
		if !allow.Allowed {
			return nil, errCandidateUnavailable
		}
	}
	s.ReserveChannel(kind, result.ChannelIndex)
	result.Permit = s.newChannelPermit(kind, result.ChannelIndex, allow, true)
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
	selectedPool := poolRoute[len(poolRoute)-1]
	var activeChannels []ChannelInfo
	var routedChannels []ChannelInfo
	foundAttemptableGroup := false
	for _, pool := range poolRoute {
		channels := channelsByPool[pool.ID]
		routedChannels = append(routedChannels, channels...)
		if !foundAttemptableGroup && s.hasAttemptableChannel(channels, failedChannels, requestedModel) {
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
	activeChannels = s.preferIntentChannel(ctx, activeChannels)

	// 按分组内临时促销和配置优先级选择可用渠道。failedChannels 只记录本次请求已经
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
	override, err := registry.LookupRouteOverride(conversationID)
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
		if ch.Index != override.ChannelIndex {
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
	requestedModel string,
	metricsManager *metrics.MetricsManager,
) *SelectionResult {
	for _, ch := range activeChannels {
		if failedChannels[ch.Index] {
			continue
		}
		if ch.Status != "active" {
			prefix := kindSchedulerLogPrefix(kind)
			log.Printf("[%s-Channel] 跳过非活跃渠道: [%d] %s (状态: %s)", prefix, ch.Index, ch.Name, ch.Status)
			continue
		}

		upstream := ch.upstream
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
			// Peek 不占探测名额，选中后仍须正式取得许可。
			probeIntent: true,
		}
	}
	return nil
}

// hasAttemptableChannel 判断当前分组是否仍有渠道值得在本次请求中尝试。
// 健康度不在这里过滤：调度器会先跳过不健康渠道，但仍保留一次组内降级尝试；
// 真正请求失败后 failedChannels 会确保下一次选择进入后续分组。
func (s *ChannelScheduler) hasAttemptableChannel(
	channels []ChannelInfo,
	failedChannels map[int]bool,
	requestedModel string,
) bool {
	for _, channel := range channels {
		if failedChannels[channel.Index] || channel.Status != config.ChannelStatusActive {
			continue
		}
		upstream := channel.upstream
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

// NewCircuitProbePermit 为不经选渠编排的直查路径（如 llmgate 按 ID 选渠）
// 构造只持有半开探测名额的许可句柄。调用方负责在调用结束/探测结束时
// 提交健康结果并 Release。
func (s *ChannelScheduler) NewCircuitProbePermit(kind ChannelKind, channelIndex int, generation uint64) *ChannelPermit {
	return s.newChannelPermit(kind, channelIndex, circuit.AllowResult{Allowed: true, Generation: generation}, false)
}

// NewReservationPermit 为已手动 ReserveChannel 的路径（如 visionlayer
// 列表候选）构造只持有 in-flight 预留的许可句柄，统一释放出口。
func (s *ChannelScheduler) NewReservationPermit(kind ChannelKind, channelIndex int) *ChannelPermit {
	return s.newChannelPermit(kind, channelIndex, circuit.AllowResult{}, true)
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
	// 不健康渠道只按配置顺序参与恢复探测，促销不会增加探测优先级。
	candidates := append([]ChannelInfo(nil), activeChannels...)
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].Priority != candidates[j].Priority {
			return candidates[i].Priority < candidates[j].Priority
		}
		return candidates[i].Index < candidates[j].Index
	})
	for _, channel := range candidates {
		ch := &channel
		if failedChannels[ch.Index] {
			continue
		}
		// 跳过非 active 状态的渠道
		if ch.Status != "active" {
			continue
		}

		upstream := ch.upstream
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
