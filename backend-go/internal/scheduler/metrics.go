package scheduler

import (
	"log"

	"github.com/insistanan/api-proxy/internal/config"
	"github.com/insistanan/api-proxy/internal/types"
	"github.com/insistanan/api-proxy/internal/urlhealth"
)

// RecordSuccess 记录渠道成功（使用 baseURL + apiKey + channelIndex）
func (s *ChannelScheduler) RecordSuccess(baseURL, apiKey string, channelIndex int, kind ChannelKind) {
	s.getMetricsManager(kind).RecordSuccess(baseURL, apiKey, channelIndex)
}

// RecordSuccessWithUsage 记录渠道成功（带 Usage 数据）
func (s *ChannelScheduler) RecordSuccessWithUsage(baseURL, apiKey string, channelIndex int, usage *types.Usage, kind ChannelKind) {
	s.getMetricsManager(kind).RecordSuccessWithUsage(baseURL, apiKey, channelIndex, usage)
}

// RecordFailure 记录渠道失败（使用 baseURL + apiKey + channelIndex）
func (s *ChannelScheduler) RecordFailure(baseURL, apiKey string, channelIndex int, kind ChannelKind) {
	s.getMetricsManager(kind).RecordFailure(baseURL, apiKey, channelIndex)
}

// RecordRequestStart 记录请求开始
func (s *ChannelScheduler) RecordRequestStart(baseURL, apiKey string, channelIndex int, kind ChannelKind) {
	s.getMetricsManager(kind).RecordRequestStart(baseURL, apiKey, channelIndex)
}

// RecordRequestEnd 记录请求结束
func (s *ChannelScheduler) RecordRequestEnd(baseURL, apiKey string, channelIndex int, kind ChannelKind) {
	s.getMetricsManager(kind).RecordRequestEnd(baseURL, apiKey, channelIndex)
}

// RecordRequestConnected 记录已经开始连接上游的请求，用于实时活跃度和调用历史。
func (s *ChannelScheduler) RecordRequestConnected(baseURL, apiKey, model string, channelIndex int, kind ChannelKind) uint64 {
	return s.getMetricsManager(kind).RecordRequestConnected(baseURL, apiKey, channelIndex, model)
}

// RecordRequestFinalizeSuccess 回写已连接请求的成功结果与用量。
func (s *ChannelScheduler) RecordRequestFinalizeSuccess(baseURL, apiKey string, channelIndex int, requestID uint64, usage *types.Usage, kind ChannelKind) {
	s.getMetricsManager(kind).RecordRequestFinalizeSuccess(baseURL, apiKey, channelIndex, requestID, usage)
}

// RecordRequestFinalizeFailure 回写已连接请求的失败结果。
func (s *ChannelScheduler) RecordRequestFinalizeFailure(baseURL, apiKey string, channelIndex int, requestID uint64, kind ChannelKind) {
	s.getMetricsManager(kind).RecordRequestFinalizeFailure(baseURL, apiKey, channelIndex, requestID)
}

// ShouldSuspendKey 返回指定 Key 是否因熔断而不应继续使用。
func (s *ChannelScheduler) ShouldSuspendKey(baseURL, apiKey string, channelIndex int, kind ChannelKind) bool {
	return s.getMetricsManager(kind).ShouldSuspendKey(baseURL, apiKey, channelIndex)
}

// ResetChannelMetrics 重置渠道级熔断器及所有 Key 的熔断/失败状态（保留历史统计）
// 用于：1) 手动恢复熔断 2) 更换 API Key 后重置熔断状态
func (s *ChannelScheduler) ResetChannelMetrics(channelIndex int, kind ChannelKind) {
	upstream := s.getUpstreamByIndex(channelIndex, kind)
	if upstream == nil {
		return
	}
	metricsManager := s.getMetricsManager(kind)
	for _, baseURL := range upstream.GetAllBaseURLs() {
		for _, apiKey := range upstream.APIKeys {
			metricsManager.ResetKeyFailureState(baseURL, apiKey, channelIndex)
		}
	}
	s.ResetCircuitBreaker(string(kind), channelIndex)
	prefix := kindSchedulerLogPrefix(kind)
	log.Printf("[%s-Reset] 渠道 [%d] %s 的熔断状态已重置（保留历史统计）", prefix, channelIndex, upstream.Name)
}

// DeleteChannelMetrics 删除渠道的所有指标数据（内存 + 持久化）
// 用于删除渠道时清理相关的统计数据
// channelIndex 用于区分同 URL 同 Key 的不同渠道（指标键的一部分）
func (s *ChannelScheduler) DeleteChannelMetrics(upstream *config.UpstreamConfig, channelIndex int, kind ChannelKind) {
	if upstream == nil {
		return
	}
	metricsManager := s.getMetricsManager(kind)
	// 合并活跃 Key 和历史 Key，一起清理
	allKeys := append([]string{}, upstream.APIKeys...)
	allKeys = append(allKeys, upstream.HistoricalAPIKeys...)
	// MetricsManager 内部已有 apiType，无需外部传递
	metricsManager.DeleteChannelMetrics(upstream.GetAllBaseURLs(), allKeys, channelIndex)
	prefix := kindSchedulerLogPrefix(kind)
	log.Printf("[%s-Delete] 渠道 %s [%d] 的指标数据已清理", prefix, upstream.Name, channelIndex)
}

// GetSortedURLsForChannel 获取渠道排序后的 URL 列表（非阻塞，立即返回）
// 返回按动态排序的 URL 结果列表，包含原始索引用于指标记录
func (s *ChannelScheduler) GetSortedURLsForChannel(
	kind ChannelKind,
	channelIndex int,
	urls []string,
) []urlhealth.URLLatencyResult {
	if s.urlManager == nil || len(urls) <= 1 {
		// 无 URL 管理器或单 URL，返回默认结果
		results := make([]urlhealth.URLLatencyResult, len(urls))
		for i, url := range urls {
			results[i] = urlhealth.URLLatencyResult{
				URL:         url,
				OriginalIdx: i,
				Success:     true,
			}
		}
		return results
	}
	return s.urlManager.GetSortedURLs(urlManagerChannelKey(kind, channelIndex), urls)
}

// MarkURLSuccess 标记 URL 成功
func (s *ChannelScheduler) MarkURLSuccess(kind ChannelKind, channelIndex int, url string) {
	if s.urlManager != nil {
		s.urlManager.MarkSuccess(urlManagerChannelKey(kind, channelIndex), url)
	}
}

// MarkURLFailure 标记 URL 失败，触发动态排序
func (s *ChannelScheduler) MarkURLFailure(kind ChannelKind, channelIndex int, url string) {
	if s.urlManager != nil {
		s.urlManager.MarkFailure(urlManagerChannelKey(kind, channelIndex), url)
	}
}

func urlManagerChannelKey(kind ChannelKind, channelIndex int) int {
	const stride = 1_000_000
	return urlManagerChannelKeyOrdinal(kind)*stride + channelIndex
}

func urlManagerChannelKeyOrdinal(kind ChannelKind) int {
	switch kind {
	case ChannelKindResponses:
		return 1
	case ChannelKindGemini:
		return 2
	case ChannelKindChat:
		return 3
	case ChannelKindImages:
		return 4
	default:
		return -1
	}
}
