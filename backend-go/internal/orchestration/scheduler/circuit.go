package scheduler

import (
	"log"

	"github.com/insistanan/api-proxy/internal/orchestration/circuit"
)

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
