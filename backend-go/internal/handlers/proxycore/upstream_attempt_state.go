// 本文件是单次上游尝试的状态载体：候选（BaseURL）级重试状态、单次尝试的观测与
// 生命周期收尾、以及跨候选累积的能力/故障转移状态。
// candidateRetryState 每进入一个 BaseURL 必须新建，避免状态跨候选泄漏。
package proxycore

import (
	"net/http"

	"github.com/insistanan/api-proxy/internal/config"
	"github.com/insistanan/api-proxy/internal/metrics"
	"github.com/insistanan/api-proxy/internal/proxy"
	"github.com/insistanan/api-proxy/internal/scheduler"
	"github.com/insistanan/api-proxy/internal/types"
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

// requestActivityRecorder 是生命周期收尾所需的最小调度能力，不暴露日志和会话存储。
type requestActivityRecorder interface {
	RecordRequestEnd(baseURL, apiKey string, channelIndex int, kind scheduler.ChannelKind)
}

// profileRecorder 是单次尝试所需的画像能力；收尾不再经调度器取回整个画像管理器。
type profileRecorder interface {
	RecordFirstByte(requestID uint64)
	EndRequest(baseURL string, apiKeys []string, model string, channelIndex int, requestID uint64, success bool, outputTokens int64)
	EndRequestNeutral(baseURL string, apiKeys []string, model string, channelIndex int, requestID uint64)
}

// attemptLifecycle 绑定单次候选尝试的观测身份和终结责任。
// 该对象只存在于当前请求的单次尝试范围内，不跨请求保存。
type attemptLifecycle struct {
	activity     requestActivityRecorder
	profiles     profileRecorder
	metrics      *metrics.MetricsManager
	kind         scheduler.ChannelKind
	channelIndex int
	apiKeys      []string
	baseURL      string
	apiKey       string
	observation  attemptObservation
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
	// 可选画像先转换为接口，避免 nil 指针进入接口后被误判为已启用。
	var profiles profileRecorder
	if manager := a.ChannelScheduler.GetProfileManager(); manager != nil {
		profiles = manager
	}
	return attemptLifecycle{
		activity:     a.ChannelScheduler,
		profiles:     profiles,
		metrics:      a.MetricsManager,
		kind:         a.Kind,
		channelIndex: a.LogContext.ChannelIndex,
		apiKeys:      a.Upstream.APIKeys,
		baseURL:      baseURL,
		apiKey:       apiKey,
		observation:  a.startAttemptObservation(baseURL, apiKey),
	}
}

func (l attemptLifecycle) finalizeNeutral() {
	l.metrics.RecordRequestFinalizeNeutral(l.baseURL, l.apiKey, l.channelIndex, l.observation.RequestID)
	l.activity.RecordRequestEnd(l.baseURL, l.apiKey, l.channelIndex, l.kind)
	if pm := l.profiles; pm != nil {
		pm.EndRequestNeutral(l.baseURL, l.apiKeys, l.observation.ResolvedModel, l.channelIndex, l.observation.ProfileRequestID)
	}
}

func (l attemptLifecycle) finalizeClientCancelled() {
	l.metrics.RecordRequestFinalizeClientCancel(l.baseURL, l.apiKey, l.channelIndex, l.observation.RequestID)
	l.activity.RecordRequestEnd(l.baseURL, l.apiKey, l.channelIndex, l.kind)
	if pm := l.profiles; pm != nil {
		pm.EndRequestNeutral(l.baseURL, l.apiKeys, l.observation.ResolvedModel, l.channelIndex, l.observation.ProfileRequestID)
	}
}

func (l attemptLifecycle) finalizeFailed() {
	l.metrics.RecordRequestFinalizeFailure(l.baseURL, l.apiKey, l.channelIndex, l.observation.RequestID)
	l.activity.RecordRequestEnd(l.baseURL, l.apiKey, l.channelIndex, l.kind)
	if pm := l.profiles; pm != nil {
		pm.EndRequest(l.baseURL, l.apiKeys, l.observation.ResolvedModel, l.channelIndex, l.observation.ProfileRequestID, false, 0)
	}
}

func (l attemptLifecycle) finalizeSuccessful(usage *types.Usage) {
	l.metrics.RecordRequestFinalizeSuccess(l.baseURL, l.apiKey, l.channelIndex, l.observation.RequestID, usage)
	l.activity.RecordRequestEnd(l.baseURL, l.apiKey, l.channelIndex, l.kind)
	if pm := l.profiles; pm != nil {
		var outputTokens int64
		if usage != nil {
			outputTokens = int64(usage.OutputTokens)
		}
		pm.EndRequest(l.baseURL, l.apiKeys, l.observation.ResolvedModel, l.channelIndex, l.observation.ProfileRequestID, true, outputTokens)
	}
}
