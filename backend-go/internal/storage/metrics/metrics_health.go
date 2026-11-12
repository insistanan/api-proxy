package metrics

import (
	"log"
	"time"
)

// IsChannelHealthyWithKeys 判断渠道是否健康（逐 Key 独立判断）
//
// 改为逐 Key 独立判断而非简单聚合：
//   - 每个 Key 独立计算失败率，有足够样本的 Key 中任一个不健康则渠道不健康
//   - 避免一个坏 Key 被其他大量健康 Key 的指标"稀释"掉
//   - 所有 Key 都样本不足时默认健康（给新渠道/新 Key 机会）
//
// activeKeys: 当前渠道配置的所有活跃 API Keys
func (m *MetricsManager) IsChannelHealthyWithKeys(baseURL string, activeKeys []string, channelIndex int) bool {
	if len(activeKeys) == 0 {
		return false
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	minRequests := max(3, m.windowSize/2)

	for _, apiKey := range activeKeys {
		metricsKey := generateMetricsKey(baseURL, apiKey, channelIndex)
		metrics, exists := m.keyMetrics[metricsKey]
		if !exists || len(metrics.recentResults) == 0 {
			continue
		}

		// 单 Key 样本不足，跳过对该 Key 的健康判断
		if len(metrics.recentResults) < minRequests {
			continue
		}

		// 计算该 Key 的失败率
		failures := 0
		for _, success := range metrics.recentResults {
			if !success {
				failures++
			}
		}
		failureRate := float64(failures) / float64(len(metrics.recentResults))
		if failureRate >= m.failureThreshold {
			return false
		}
	}

	return true
}

// GetChannelRequestCount 获取渠道的总请求数（所有 Key 聚合）
func (m *MetricsManager) GetChannelRequestCount(baseURL string, activeKeys []string, channelIndex int) int64 {
	if len(activeKeys) == 0 {
		return 0
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	var totalCount int64
	for _, apiKey := range activeKeys {
		metricsKey := generateMetricsKey(baseURL, apiKey, channelIndex)
		if metrics, exists := m.keyMetrics[metricsKey]; exists {
			totalCount += metrics.RequestCount
		}
	}

	return totalCount
}

func (m *MetricsManager) CalculateChannelFailureRate(baseURL string, activeKeys []string, channelIndex int) float64 {
	if len(activeKeys) == 0 {
		return 0
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	var totalResults []bool
	for _, apiKey := range activeKeys {
		metricsKey := generateMetricsKey(baseURL, apiKey, channelIndex)
		if metrics, exists := m.keyMetrics[metricsKey]; exists {
			totalResults = append(totalResults, metrics.recentResults...)
		}
	}

	if len(totalResults) == 0 {
		return 0
	}

	failures := 0
	for _, success := range totalResults {
		if !success {
			failures++
		}
	}

	return float64(failures) / float64(len(totalResults))
}

// ResetKeyFailureState 重置单个 Key 的熔断/失败状态（保留历史统计与总量计数）。
// 用于“恢复熔断”场景：清零连续失败、清空滑动窗口、解除熔断标记。
func (m *MetricsManager) ResetKeyFailureState(baseURL, apiKey string, channelIndex int) {
	m.mu.Lock()
	defer m.mu.Unlock()

	metricsKey := generateMetricsKey(baseURL, apiKey, channelIndex)
	if metrics, exists := m.keyMetrics[metricsKey]; exists {
		metrics.ConsecutiveFailures = 0
		metrics.recentResults = make([]bool, 0, m.windowSize)
		metrics.CircuitBrokenAt = nil
		log.Printf("[Metrics-Reset] Key [%s] (%s) 熔断状态已重置（保留历史统计）", metrics.KeyMask, metrics.BaseURL)
	}
}

// ResetAll 重置所有指标
func (m *MetricsManager) ResetAll() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.keyMetrics = make(map[string]*KeyMetrics)
}

// Stop 停止并等待后台清理退出；并发或重复调用共享同一次关闭。
func (m *MetricsManager) Stop() {
	m.stopOnce.Do(func() { close(m.stopCh) })
	<-m.doneCh
}

// DeleteKeysForChannel 删除指定渠道的所有内存指标
// baseURLs: 渠道的所有 BaseURL（支持多端点 failover）
// apiKeys: 渠道的所有 API Key
// channelIndex: 渠道索引（与渠道一一对应，用于区分同 URL 同 Key 的不同渠道）
// 返回所有可能的 metricsKey 列表（无论内存中是否存在，用于后续清理持久化数据）
func (m *MetricsManager) DeleteKeysForChannel(baseURLs, apiKeys []string, channelIndex int) []string {
	m.mu.Lock()
	defer m.mu.Unlock()

	var allKeys []string
	var deletedFromMemory int

	for _, baseURL := range baseURLs {
		for _, apiKey := range apiKeys {
			metricsKey := generateMetricsKey(baseURL, apiKey, channelIndex)
			allKeys = append(allKeys, metricsKey)
			if _, exists := m.keyMetrics[metricsKey]; exists {
				delete(m.keyMetrics, metricsKey)
				deletedFromMemory++
			}
		}
	}

	if deletedFromMemory > 0 {
		log.Printf("[Metrics-Delete] 已删除 %d 个内存指标记录", deletedFromMemory)
	}

	return allKeys
}

// DeleteChannelMetrics 删除渠道的所有指标数据（内存 + 持久化）
// baseURLs: 渠道的所有 BaseURL（支持多端点 failover）
// apiKeys: 渠道的所有 API Key
// channelIndex: 渠道索引
// 返回被删除的持久化记录数
func (m *MetricsManager) DeleteChannelMetrics(baseURLs, apiKeys []string, channelIndex int) int64 {
	// 1. 删除内存指标，获取 metricsKey 列表
	deletedKeys := m.DeleteKeysForChannel(baseURLs, apiKeys, channelIndex)

	// 2. 删除持久化数据（使用内部 apiType，避免外部误传）
	if m.store != nil && len(deletedKeys) > 0 {
		deleted, err := m.store.DeleteRecordsByMetricsKeys(deletedKeys, m.apiType)
		if err != nil {
			log.Printf("[Metrics-Delete] 警告: 删除持久化指标记录失败: %v", err)
			return 0
		}
		if deleted > 0 {
			log.Printf("[Metrics-Delete] 已删除 %d 条 %s 持久化指标记录", deleted, m.apiType)
		}
		return deleted
	}

	return 0
}

// cleanupCircuitBreakers 后台任务：定期检查并恢复超时的熔断 Key，清理过期指标
func (m *MetricsManager) cleanupCircuitBreakers() {
	defer close(m.doneCh)
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	// 每小时清理一次过期 Key
	cleanupTicker := time.NewTicker(1 * time.Hour)
	defer cleanupTicker.Stop()

	for {
		select {
		case <-ticker.C:
			m.recoverExpiredCircuitBreakers()
		case <-cleanupTicker.C:
			m.cleanupStaleKeys()
		case <-m.stopCh:
			return
		}
	}
}

// recoverExpiredCircuitBreakers 恢复超时的熔断 Key
func (m *MetricsManager) recoverExpiredCircuitBreakers() {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()
	for _, metrics := range m.keyMetrics {
		if metrics.CircuitBrokenAt != nil {
			elapsed := now.Sub(*metrics.CircuitBrokenAt)
			if elapsed > m.circuitRecoveryTime {
				// 重置熔断状态
				metrics.ConsecutiveFailures = 0
				metrics.recentResults = make([]bool, 0, m.windowSize)
				metrics.CircuitBrokenAt = nil
				log.Printf("[Metrics-Circuit] Key [%s] (%s) 熔断自动恢复（已超过 %v）", metrics.KeyMask, metrics.BaseURL, m.circuitRecoveryTime)
			}
		}
	}
}

// cleanupStaleKeys 清理过期的 Key 指标（超过 48 小时无活动）
func (m *MetricsManager) cleanupStaleKeys() {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()
	staleThreshold := 48 * time.Hour
	var removed []string

	for key, metrics := range m.keyMetrics {
		// 判断最后活动时间
		var lastActivity time.Time
		if metrics.LastSuccessAt != nil {
			lastActivity = *metrics.LastSuccessAt
		}
		if metrics.LastFailureAt != nil && metrics.LastFailureAt.After(lastActivity) {
			lastActivity = *metrics.LastFailureAt
		}

		// 如果从未有活动或超过阈值，删除
		if lastActivity.IsZero() || now.Sub(lastActivity) > staleThreshold {
			delete(m.keyMetrics, key)
			removed = append(removed, metrics.KeyMask)
		}
	}

	if len(removed) > 0 {
		log.Printf("[Metrics-Cleanup] 清理了 %d 个过期 Key 指标: %v", len(removed), removed)
	}
}

// GetCircuitRecoveryTime 获取熔断恢复时间
func (m *MetricsManager) GetCircuitRecoveryTime() time.Duration {
	return m.circuitRecoveryTime
}

// GetFailureThreshold 获取失败率阈值
func (m *MetricsManager) GetFailureThreshold() float64 {
	return m.failureThreshold
}

// GetWindowSize 获取滑动窗口大小
func (m *MetricsManager) GetWindowSize() int {
	return m.windowSize
}

// ShouldSuspendKey 判断单个 Key 是否应该熔断
func (m *MetricsManager) ShouldSuspendKey(baseURL, apiKey string, channelIndex int) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	metricsKey := generateMetricsKey(baseURL, apiKey, channelIndex)
	metrics, exists := m.keyMetrics[metricsKey]
	if !exists {
		return false
	}

	// 最小请求数保护：至少 max(3, windowSize/2) 次请求才判断
	minRequests := max(3, m.windowSize/2)
	if len(metrics.recentResults) < minRequests {
		return false
	}

	return m.calculateKeyFailureRateInternal(metrics) >= m.failureThreshold
}
