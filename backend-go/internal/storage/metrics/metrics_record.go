package metrics

import (
	"log"
	"time"

	"github.com/insistanan/api-proxy/internal/protocol/types"
)

// RecordSuccess 记录成功请求（新方法，使用 baseURL + apiKey + channelIndex）
func (m *MetricsManager) RecordSuccess(baseURL, apiKey string, channelIndex int) {
	m.RecordSuccessWithUsage(baseURL, apiKey, channelIndex, nil)
}

// RecordSuccessWithUsage 记录成功请求（带 Usage 数据）
func (m *MetricsManager) RecordSuccessWithUsage(baseURL, apiKey string, channelIndex int, usage *types.Usage) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.recordSuccessWithUsageLocked(baseURL, apiKey, channelIndex, usage, time.Now())
}

func (m *MetricsManager) recordSuccessWithUsageLocked(baseURL, apiKey string, channelIndex int, usage *types.Usage, now time.Time) {
	metrics := m.getOrCreateKey(baseURL, apiKey, channelIndex)
	metrics.RequestCount++
	metrics.SuccessCount++
	metrics.ConsecutiveFailures = 0

	metrics.LastSuccessAt = &now

	// 成功后清除熔断标记
	if metrics.CircuitBrokenAt != nil {
		metrics.CircuitBrokenAt = nil
		log.Printf("[Metrics-Circuit] Key [%s] (%s) 因请求成功退出熔断状态", metrics.KeyMask, metrics.BaseURL)
	}

	// 更新滑动窗口
	m.appendToWindowKey(metrics, true)

	// 提取 Token 数据（如果有）
	var inputTokens, outputTokens, cacheCreationTokens, cacheReadTokens int64
	if usage != nil {
		inputTokens = int64(usage.InputTokens)
		outputTokens = int64(usage.OutputTokens)
		// cache_creation_input_tokens 有时不会返回（只返回 5m/1h 细分字段），这里做兜底汇总。
		cacheCreationTokens = int64(usage.CacheCreationInputTokens)
		if cacheCreationTokens <= 0 {
			cacheCreationTokens = int64(usage.CacheCreation5mInputTokens + usage.CacheCreation1hInputTokens)
		}
		cacheReadTokens = int64(usage.CacheReadInputTokens)
	}

	// 记录带时间戳的请求
	m.appendToHistoryKeyWithUsage(metrics, now, true, inputTokens, outputTokens, cacheCreationTokens, cacheReadTokens)

	// 写入持久化存储（异步，不阻塞）
	if m.store != nil {
		m.store.AddRecord(PersistentRecord{
			MetricsKey:          metrics.MetricsKey,
			BaseURL:             baseURL,
			KeyMask:             metrics.KeyMask,
			Timestamp:           now,
			Success:             true,
			InputTokens:         inputTokens,
			OutputTokens:        outputTokens,
			CacheCreationTokens: cacheCreationTokens,
			CacheReadTokens:     cacheReadTokens,
			APIType:             m.apiType,
		})
	}
}

// RecordFailure 记录失败请求（新方法，使用 baseURL + apiKey + channelIndex）
func (m *MetricsManager) RecordFailure(baseURL, apiKey string, channelIndex int) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.recordFailureLocked(baseURL, apiKey, channelIndex, time.Now())
}

func (m *MetricsManager) recordFailureLocked(baseURL, apiKey string, channelIndex int, now time.Time) {
	metrics := m.getOrCreateKey(baseURL, apiKey, channelIndex)
	metrics.RequestCount++
	metrics.FailureCount++
	metrics.ConsecutiveFailures++

	metrics.LastFailureAt = &now

	// 更新滑动窗口
	m.appendToWindowKey(metrics, false)

	// 检查是否刚进入熔断状态
	if metrics.CircuitBrokenAt == nil && m.isKeyCircuitBroken(metrics) {
		metrics.CircuitBrokenAt = &now
		log.Printf("[Metrics-Circuit] Key [%s] (%s) 进入熔断状态（失败率: %.1f%%）", metrics.KeyMask, metrics.BaseURL, m.calculateKeyFailureRateInternal(metrics)*100)
	}

	// 记录带时间戳的请求
	m.appendToHistoryKey(metrics, now, false)

	// 写入持久化存储（异步，不阻塞）
	if m.store != nil {
		m.store.AddRecord(PersistentRecord{
			MetricsKey:          metrics.MetricsKey,
			BaseURL:             baseURL,
			KeyMask:             metrics.KeyMask,
			Timestamp:           now,
			Success:             false,
			InputTokens:         0,
			OutputTokens:        0,
			CacheCreationTokens: 0,
			CacheReadTokens:     0,
			APIType:             m.apiType,
		})
	}
}

// RecordRequestConnected 记录“开始发起上游请求（TCP 建连阶段）”的请求（用于更实时的活跃度统计）。
// 返回 requestID，用于后续在请求结束时回写成功/失败与 token。
func (m *MetricsManager) RecordRequestConnected(baseURL, apiKey string, channelIndex int, model string) uint64 {
	return m.RecordRequestConnectedAt(baseURL, apiKey, channelIndex, model, time.Now())
}

// RecordRequestConnectedAt 与 RecordRequestConnected 相同，但允许注入时间戳（用于测试）。
func (m *MetricsManager) RecordRequestConnectedAt(baseURL, apiKey string, channelIndex int, model string, timestamp time.Time) uint64 {
	m.mu.Lock()
	defer m.mu.Unlock()

	metrics := m.getOrCreateKey(baseURL, apiKey, channelIndex)
	// RequestCount 改为在 finalize 阶段统一增加，避免 fallback 路径二次计数

	m.nextRequestID++
	requestID := m.nextRequestID

	if metrics.pendingHistoryIdx == nil {
		metrics.pendingHistoryIdx = make(map[uint64]int)
	}

	metrics.requestHistory = append(metrics.requestHistory, RequestRecord{
		Timestamp: timestamp,
		Success:   true, // 先按成功计数；结束时会回写真实结果
		Model:     model,
	})
	metrics.pendingHistoryIdx[requestID] = len(metrics.requestHistory) - 1

	// 清理历史并同步修正索引
	m.cleanupHistoryLocked(metrics)

	return requestID
}

// RecordRequestFinalizeSuccess 回写成功结果与 token（requestID 来自 RecordRequestConnected）。
func (m *MetricsManager) RecordRequestFinalizeSuccess(baseURL, apiKey string, channelIndex int, requestID uint64, usage *types.Usage) {
	m.mu.Lock()
	defer m.mu.Unlock()

	metricsKey := generateMetricsKey(baseURL, apiKey, channelIndex)
	metrics, exists := m.keyMetrics[metricsKey]
	if !exists {
		m.recordSuccessWithUsageLocked(baseURL, apiKey, channelIndex, usage, time.Now())
		return
	}

	idx, ok := metrics.pendingHistoryIdx[requestID]
	if !ok || idx < 0 || idx >= len(metrics.requestHistory) {
		m.recordSuccessWithUsageLocked(baseURL, apiKey, channelIndex, usage, time.Now())
		return
	}
	delete(metrics.pendingHistoryIdx, requestID)

	// 正常路径：在此统一增加 RequestCount
	metrics.RequestCount++
	metrics.SuccessCount++
	metrics.ConsecutiveFailures = 0

	now := time.Now()
	metrics.LastSuccessAt = &now

	// 成功后清除熔断标记
	if metrics.CircuitBrokenAt != nil {
		metrics.CircuitBrokenAt = nil
		log.Printf("[Metrics-Circuit] Key [%s] (%s) 因请求成功退出熔断状态", metrics.KeyMask, metrics.BaseURL)
	}

	// 更新滑动窗口
	m.appendToWindowKey(metrics, true)

	// 提取 Token 数据（如果有）
	var inputTokens, outputTokens, cacheCreationTokens, cacheReadTokens int64
	if usage != nil {
		inputTokens = int64(usage.InputTokens)
		outputTokens = int64(usage.OutputTokens)
		// cache_creation_input_tokens 有时不会返回（只返回 5m/1h 细分字段），这里做兜底汇总。
		cacheCreationTokens = int64(usage.CacheCreationInputTokens)
		if cacheCreationTokens <= 0 {
			cacheCreationTokens = int64(usage.CacheCreation5mInputTokens + usage.CacheCreation1hInputTokens)
		}
		cacheReadTokens = int64(usage.CacheReadInputTokens)
	}

	// 回写历史记录（时间戳保持为“请求开始（TCP 建连阶段）”时刻）
	record := &metrics.requestHistory[idx]
	record.Success = true
	record.InputTokens = inputTokens
	record.OutputTokens = outputTokens
	record.CacheCreationInputTokens = cacheCreationTokens
	record.CacheReadInputTokens = cacheReadTokens

	// 写入持久化存储（异步，不阻塞）
	if m.store != nil {
		m.store.AddRecord(PersistentRecord{
			MetricsKey:          metrics.MetricsKey,
			BaseURL:             baseURL,
			KeyMask:             metrics.KeyMask,
			Timestamp:           record.Timestamp,
			Model:               record.Model,
			Success:             true,
			InputTokens:         inputTokens,
			OutputTokens:        outputTokens,
			CacheCreationTokens: cacheCreationTokens,
			CacheReadTokens:     cacheReadTokens,
			APIType:             m.apiType,
		})
	}
}

// RecordRequestFinalizeFailure 回写失败结果（requestID 来自 RecordRequestConnected）。
func (m *MetricsManager) RecordRequestFinalizeFailure(baseURL, apiKey string, channelIndex int, requestID uint64) {
	m.mu.Lock()
	defer m.mu.Unlock()

	metricsKey := generateMetricsKey(baseURL, apiKey, channelIndex)
	metrics, exists := m.keyMetrics[metricsKey]
	if !exists {
		m.recordFailureLocked(baseURL, apiKey, channelIndex, time.Now())
		return
	}

	idx, ok := metrics.pendingHistoryIdx[requestID]
	if !ok || idx < 0 || idx >= len(metrics.requestHistory) {
		m.recordFailureLocked(baseURL, apiKey, channelIndex, time.Now())
		return
	}
	delete(metrics.pendingHistoryIdx, requestID)

	// 正常路径：在此统一增加 RequestCount
	metrics.RequestCount++
	metrics.FailureCount++
	metrics.ConsecutiveFailures++

	now := time.Now()
	metrics.LastFailureAt = &now

	// 更新滑动窗口
	m.appendToWindowKey(metrics, false)

	// 检查是否刚进入熔断状态
	if metrics.CircuitBrokenAt == nil && m.isKeyCircuitBroken(metrics) {
		metrics.CircuitBrokenAt = &now
		log.Printf("[Metrics-Circuit] Key [%s] (%s) 进入熔断状态（失败率: %.1f%%）", metrics.KeyMask, metrics.BaseURL, m.calculateKeyFailureRateInternal(metrics)*100)
	}

	// 回写历史记录（时间戳保持为“请求开始（TCP 建连阶段）”时刻）
	record := &metrics.requestHistory[idx]
	record.Success = false
	record.InputTokens = 0
	record.OutputTokens = 0
	record.CacheCreationInputTokens = 0
	record.CacheReadInputTokens = 0

	// 写入持久化存储（异步，不阻塞）
	if m.store != nil {
		m.store.AddRecord(PersistentRecord{
			MetricsKey:          metrics.MetricsKey,
			BaseURL:             baseURL,
			KeyMask:             metrics.KeyMask,
			Timestamp:           record.Timestamp,
			Success:             false,
			InputTokens:         0,
			OutputTokens:        0,
			CacheCreationTokens: 0,
			CacheReadTokens:     0,
			APIType:             m.apiType,
		})
	}
}

// RecordRequestFinalizeNeutral 结束不代表上游质量的请求，不计入成功率或熔断。
func (m *MetricsManager) RecordRequestFinalizeNeutral(baseURL, apiKey string, channelIndex int, requestID uint64) {
	m.recordRequestFinalizeNeutral(baseURL, apiKey, channelIndex, requestID, false)
}

// RecordRequestFinalizeClientCancel 记录客户端取消（计入总请求数，但不计入成功率窗口）。
func (m *MetricsManager) RecordRequestFinalizeClientCancel(baseURL, apiKey string, channelIndex int, requestID uint64) {
	m.recordRequestFinalizeNeutral(baseURL, apiKey, channelIndex, requestID, true)
}

func (m *MetricsManager) recordRequestFinalizeNeutral(baseURL, apiKey string, channelIndex int, requestID uint64, countRequest bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	metricsKey := generateMetricsKey(baseURL, apiKey, channelIndex)
	metrics, exists := m.keyMetrics[metricsKey]
	if !exists {
		return
	}

	idx, ok := metrics.pendingHistoryIdx[requestID]
	if !ok || idx < 0 || idx >= len(metrics.requestHistory) {
		return
	}
	delete(metrics.pendingHistoryIdx, requestID)
	if countRequest {
		metrics.RequestCount++
	}

	// 从历史记录中移除（中性结果不进入成功率历史）
	metrics.requestHistory = append(metrics.requestHistory[:idx], metrics.requestHistory[idx+1:]...)
	// 更新后续索引
	for rid, ridx := range metrics.pendingHistoryIdx {
		if ridx > idx {
			metrics.pendingHistoryIdx[rid] = ridx - 1
		}
	}
}

// RecordRequestStart 记录请求开始（增加进行中计数）
func (m *MetricsManager) RecordRequestStart(baseURL, apiKey string, channelIndex int) {
	m.mu.Lock()
	defer m.mu.Unlock()

	metrics := m.getOrCreateKey(baseURL, apiKey, channelIndex)
	metrics.ActiveRequests++
}

// RecordRequestEnd 记录请求结束（减少进行中计数）
func (m *MetricsManager) RecordRequestEnd(baseURL, apiKey string, channelIndex int) {
	m.mu.Lock()
	defer m.mu.Unlock()

	metricsKey := generateMetricsKey(baseURL, apiKey, channelIndex)
	if metrics, exists := m.keyMetrics[metricsKey]; exists {
		if metrics.ActiveRequests > 0 {
			metrics.ActiveRequests--
		}
	}
}

// isKeyCircuitBroken 判断 Key 是否达到熔断条件（内部方法，调用前需持有锁）
func (m *MetricsManager) isKeyCircuitBroken(metrics *KeyMetrics) bool {
	// 最小请求数保护：至少 max(3, windowSize/2) 次请求才判断熔断
	minRequests := max(3, m.windowSize/2)
	if len(metrics.recentResults) < minRequests {
		return false
	}
	return m.calculateKeyFailureRateInternal(metrics) >= m.failureThreshold
}

// calculateKeyFailureRateInternal 计算 Key 失败率（内部方法，调用前需持有锁）
func (m *MetricsManager) calculateKeyFailureRateInternal(metrics *KeyMetrics) float64 {
	if len(metrics.recentResults) == 0 {
		return 0
	}
	failures := 0
	for _, success := range metrics.recentResults {
		if !success {
			failures++
		}
	}
	return float64(failures) / float64(len(metrics.recentResults))
}

// appendToWindowKey 向 Key 滑动窗口添加记录
func (m *MetricsManager) appendToWindowKey(metrics *KeyMetrics, success bool) {
	metrics.recentResults = append(metrics.recentResults, success)
	// 保持窗口大小
	if len(metrics.recentResults) > m.windowSize {
		metrics.recentResults = metrics.recentResults[1:]
	}
}

// appendToHistoryKey 向 Key 历史记录添加请求（保留24小时）
func (m *MetricsManager) appendToHistoryKey(metrics *KeyMetrics, timestamp time.Time, success bool) {
	m.appendToHistoryKeyWithUsage(metrics, timestamp, success, 0, 0, 0, 0)
}

// cleanupHistoryLocked 清理超过 24 小时的历史记录，并同步修正 pendingHistoryIdx 索引。
// 注意：调用方需要持有写锁。
func (m *MetricsManager) cleanupHistoryLocked(metrics *KeyMetrics) {
	if metrics == nil || len(metrics.requestHistory) == 0 {
		return
	}

	// 1. 时间维度清理：移除超过 24 小时的记录
	cutoff := time.Now().Add(-24 * time.Hour)

	newStart := -1
	for i, record := range metrics.requestHistory {
		if record.Timestamp.After(cutoff) {
			newStart = i
			break
		}
	}

	if newStart > 0 {
		metrics.requestHistory = metrics.requestHistory[newStart:]
		// 索引平移：老数据被切走后，pending 索引需要整体减去 newStart
		if metrics.pendingHistoryIdx != nil && len(metrics.pendingHistoryIdx) > 0 {
			for id, idx := range metrics.pendingHistoryIdx {
				if idx < newStart {
					delete(metrics.pendingHistoryIdx, id)
					continue
				}
				metrics.pendingHistoryIdx[id] = idx - newStart
			}
		}
	} else if newStart == -1 {
		// 所有记录都过期，清空切片
		metrics.requestHistory = metrics.requestHistory[:0]
		if metrics.pendingHistoryIdx != nil {
			for id := range metrics.pendingHistoryIdx {
				delete(metrics.pendingHistoryIdx, id)
			}
		}
	}

	// 2. 数量维度截断：防止高频调用导致内存无限增长
	if len(metrics.requestHistory) > maxRequestHistoryPerKey {
		overflow := len(metrics.requestHistory) - maxRequestHistoryPerKey
		metrics.requestHistory = metrics.requestHistory[overflow:]
		// pending 索引平移
		if metrics.pendingHistoryIdx != nil && len(metrics.pendingHistoryIdx) > 0 {
			for id, idx := range metrics.pendingHistoryIdx {
				if idx < overflow {
					delete(metrics.pendingHistoryIdx, id)
					continue
				}
				metrics.pendingHistoryIdx[id] = idx - overflow
			}
		}
	}
}

// appendToHistoryKeyWithUsage 向 Key 历史记录添加请求（带 Usage 数据）
func (m *MetricsManager) appendToHistoryKeyWithUsage(metrics *KeyMetrics, timestamp time.Time, success bool, inputTokens, outputTokens, cacheCreationTokens, cacheReadTokens int64) {
	metrics.requestHistory = append(metrics.requestHistory, RequestRecord{
		Timestamp:                timestamp,
		Success:                  success,
		InputTokens:              inputTokens,
		OutputTokens:             outputTokens,
		CacheCreationInputTokens: cacheCreationTokens,
		CacheReadInputTokens:     cacheReadTokens,
	})

	// 清理超过 24 小时的记录
	m.cleanupHistoryLocked(metrics)
}
