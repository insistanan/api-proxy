package scheduler

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/insistanan/api-proxy/internal/orchestration/circuit"
)

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
			s.recordCircuitResult(kind, index, allow.Generation, outcome)
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
	return errors.Is(err, errCandidateUnavailable)
}

// AdmitChannelCandidate 为列表候选取得正式许可，不修改候选快照。
// 调用方仅在即将尝试时准入，并在取得许可后立即安排 Release。
func (s *ChannelScheduler) AdmitChannelCandidate(ctx context.Context, candidate *SelectionResult) (*SelectionResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if candidate == nil || candidate.Upstream == nil {
		return nil, fmt.Errorf("选渠候选缺少上游配置")
	}
	if candidate.Permit != nil {
		return nil, fmt.Errorf("选渠候选已持有许可，不能重复准入")
	}
	selected := *candidate
	return s.reserveAndReturn(&selected, selected.Kind)
}

// reserveAndReturn 先取得熔断许可，再预留渠道在途计数。
// 正式准入失败时返回 errCandidateUnavailable，由 SelectChannel 有界重选。
func (s *ChannelScheduler) reserveAndReturn(result *SelectionResult, kind ChannelKind) (*SelectionResult, error) {
	if result == nil || result.Upstream == nil {
		return nil, fmt.Errorf("选渠结果缺少上游配置")
	}
	// 所有选渠入口都正式准入，不能用调用方标记绕过熔断或半开探测限制。
	allow := s.CircuitAllow(kind, result.ChannelIndex, result.Upstream.Name)
	if !allow.Allowed {
		return nil, errCandidateUnavailable
	}
	result.Kind = kind
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

// NewCircuitProbePermit 为不经选渠编排的直查路径（如 llmgate 按 ID 选渠）
// 构造只持有半开探测名额的许可句柄。调用方负责在调用结束/探测结束时
// 提交健康结果并 Release。
func (s *ChannelScheduler) NewCircuitProbePermit(kind ChannelKind, channelIndex int, generation uint64) *ChannelPermit {
	return s.newChannelPermit(kind, channelIndex, circuit.AllowResult{Allowed: true, Generation: generation}, false)
}
