// 本文件是单次上游尝试的状态载体：候选（BaseURL）级重试状态、单次尝试的观测与
// 生命周期收尾、以及跨候选累积的能力/故障转移状态。
// candidateRetryState 每进入一个 BaseURL 必须新建，避免状态跨候选泄漏。
package proxycore

import (
	"net/http"

	"github.com/BenedictKing/api-proxy/internal/config"
	"github.com/BenedictKing/api-proxy/internal/proxy"
	"github.com/BenedictKing/api-proxy/internal/types"
)

// candidateRetryState 委托 internal/proxy（R4-b：与 HTTP 框架无关的候选
// 重试状态提取到独立包，proxycore 只保留薄别名供既有调用方使用）。
type candidateRetryState = proxy.CandidateRetryState

func newCandidateRetryState(apiKeyCount int) *candidateRetryState {
	return proxy.NewCandidateRetryState(apiKeyCount)
}

type compatibilityRetryResult struct {
	Response         *http.Response
	Body             []byte
	Succeeded        bool
	PreparationStage string
	Err              error
}

type attemptObservation struct {
	ResolvedModel    string
	ProfileRequestID uint64
	RequestID        uint64
}

// attemptLifecycle 绑定单次候选尝试的观测身份和终结责任。
// 该对象只存在于当前请求的单次尝试范围内，不跨请求保存。
type attemptLifecycle struct {
	attempt     UpstreamAttempt
	baseURL     string
	apiKey      string
	observation attemptObservation
}

type upstreamCapabilityState struct {
	disablePromptCacheKey   bool
	requireReasoningContent bool
}

// upstreamFailoverState 保存一次故障转移请求跨 BaseURL 的结果状态。
// Key 失败和同候选重试状态仍由 candidateRetryState 按 BaseURL 独立维护。
type upstreamFailoverState struct {
	lastError              error
	lastFailoverError      *FailoverError
	deprioritizeCandidates map[string]bool
	// neutralTermination 标记本渠道最终结束原因为中性（容量耗尽、审核
	// 兼容重试失败等）：后续 Key 失败不能把它改回"真实渠道故障"（R2-a：
	// 最终取消或中性结束不能因为曾出现一次失败而重新算成渠道故障）。
	neutralTermination bool
}

func newUpstreamFailoverState() upstreamFailoverState {
	return upstreamFailoverState{
		deprioritizeCandidates: make(map[string]bool),
	}
}

// markNeutralTermination 记录一次明确的中性结束原因。
// 已标记为中性后不再被普通 Key 失败覆盖。
func (s *upstreamFailoverState) markNeutralTermination() {
	s.neutralTermination = true
}

func newUpstreamCapabilityState(upstream *config.UpstreamConfig) upstreamCapabilityState {
	if upstream == nil {
		return upstreamCapabilityState{}
	}
	return upstreamCapabilityState{
		disablePromptCacheKey:   upstream.DisablePromptCacheKey,
		requireReasoningContent: upstream.RequireReasoningContent,
	}
}

func (a UpstreamAttempt) startAttemptObservation(baseURL, apiKey string) attemptObservation {
	a.ChannelScheduler.RecordRequestStart(baseURL, apiKey, a.LogContext.ChannelIndex, a.Kind)
	resolvedModel := config.ResolveUpstreamModel(a.LogContext.Model, a.Upstream)
	profileRequestID := nextProfileRequestID()
	if pm := a.ChannelScheduler.GetProfileManager(); pm != nil {
		pm.StartRequest(baseURL, a.Upstream.APIKeys, resolvedModel, a.LogContext.ChannelIndex, profileRequestID)
	}
	requestID := a.MetricsManager.RecordRequestConnected(baseURL, apiKey, a.LogContext.ChannelIndex, a.LogContext.Model)
	return attemptObservation{
		ResolvedModel:    resolvedModel,
		ProfileRequestID: profileRequestID,
		RequestID:        requestID,
	}
}

func (a UpstreamAttempt) startAttemptLifecycle(baseURL, apiKey string) attemptLifecycle {
	return attemptLifecycle{
		attempt:     a,
		baseURL:     baseURL,
		apiKey:      apiKey,
		observation: a.startAttemptObservation(baseURL, apiKey),
	}
}

func (l attemptLifecycle) finalizeNeutral() {
	l.attempt.MetricsManager.RecordRequestFinalizeNeutral(l.baseURL, l.apiKey, l.attempt.LogContext.ChannelIndex, l.observation.RequestID)
	l.attempt.ChannelScheduler.RecordRequestEnd(l.baseURL, l.apiKey, l.attempt.LogContext.ChannelIndex, l.attempt.Kind)
	if pm := l.attempt.ChannelScheduler.GetProfileManager(); pm != nil {
		pm.EndRequestNeutral(l.baseURL, l.attempt.Upstream.APIKeys, l.observation.ResolvedModel, l.attempt.LogContext.ChannelIndex, l.observation.ProfileRequestID)
	}
}

func (l attemptLifecycle) finalizeClientCancelled() {
	l.attempt.MetricsManager.RecordRequestFinalizeClientCancel(l.baseURL, l.apiKey, l.attempt.LogContext.ChannelIndex, l.observation.RequestID)
	l.attempt.ChannelScheduler.RecordRequestEnd(l.baseURL, l.apiKey, l.attempt.LogContext.ChannelIndex, l.attempt.Kind)
	if pm := l.attempt.ChannelScheduler.GetProfileManager(); pm != nil {
		pm.EndRequestNeutral(l.baseURL, l.attempt.Upstream.APIKeys, l.observation.ResolvedModel, l.attempt.LogContext.ChannelIndex, l.observation.ProfileRequestID)
	}
}

func (l attemptLifecycle) finalizeFailed() {
	l.attempt.MetricsManager.RecordRequestFinalizeFailure(l.baseURL, l.apiKey, l.attempt.LogContext.ChannelIndex, l.observation.RequestID)
	l.attempt.ChannelScheduler.RecordRequestEnd(l.baseURL, l.apiKey, l.attempt.LogContext.ChannelIndex, l.attempt.Kind)
	if pm := l.attempt.ChannelScheduler.GetProfileManager(); pm != nil {
		pm.EndRequest(l.baseURL, l.attempt.Upstream.APIKeys, l.observation.ResolvedModel, l.attempt.LogContext.ChannelIndex, l.observation.ProfileRequestID, false, 0)
	}
}

func (l attemptLifecycle) finalizeSuccessful(usage *types.Usage) {
	l.attempt.MetricsManager.RecordRequestFinalizeSuccess(l.baseURL, l.apiKey, l.attempt.LogContext.ChannelIndex, l.observation.RequestID, usage)
	l.attempt.ChannelScheduler.RecordRequestEnd(l.baseURL, l.apiKey, l.attempt.LogContext.ChannelIndex, l.attempt.Kind)
	if pm := l.attempt.ChannelScheduler.GetProfileManager(); pm != nil {
		var outputTokens int64
		if usage != nil {
			outputTokens = int64(usage.OutputTokens)
		}
		pm.EndRequest(l.baseURL, l.attempt.Upstream.APIKeys, l.observation.ResolvedModel, l.attempt.LogContext.ChannelIndex, l.observation.ProfileRequestID, true, outputTokens)
	}
}
