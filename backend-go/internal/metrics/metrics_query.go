package metrics

import (
	"sort"
	"time"

	"github.com/insistanan/api-proxy/internal/utils"
)

// GetKeyMetrics 获取单个 Key 的指标
func (m *MetricsManager) GetKeyMetrics(baseURL, apiKey string, channelIndex int) *KeyMetrics {
	m.mu.RLock()
	defer m.mu.RUnlock()

	metricsKey := generateMetricsKey(baseURL, apiKey, channelIndex)
	if metrics, exists := m.keyMetrics[metricsKey]; exists {
		// 返回副本
		return &KeyMetrics{
			MetricsKey:          metrics.MetricsKey,
			BaseURL:             metrics.BaseURL,
			KeyMask:             metrics.KeyMask,
			RequestCount:        metrics.RequestCount,
			SuccessCount:        metrics.SuccessCount,
			FailureCount:        metrics.FailureCount,
			ConsecutiveFailures: metrics.ConsecutiveFailures,
			LastSuccessAt:       metrics.LastSuccessAt,
			LastFailureAt:       metrics.LastFailureAt,
			CircuitBrokenAt:     metrics.CircuitBrokenAt,
		}
	}
	return nil
}

// GetChannelAggregatedMetrics 获取渠道聚合指标（基于活跃 Keys）
func (m *MetricsManager) GetChannelAggregatedMetrics(channelIndex int, baseURL string, activeKeys []string) *ChannelMetrics {
	m.mu.RLock()
	defer m.mu.RUnlock()

	aggregated := &ChannelMetrics{
		ChannelIndex: channelIndex,
	}

	var latestSuccess, latestFailure, latestCircuitBroken *time.Time
	var maxConsecutiveFailures int64

	for _, apiKey := range activeKeys {
		metricsKey := generateMetricsKey(baseURL, apiKey, channelIndex)
		if metrics, exists := m.keyMetrics[metricsKey]; exists {
			aggregated.RequestCount += metrics.RequestCount
			aggregated.SuccessCount += metrics.SuccessCount
			aggregated.FailureCount += metrics.FailureCount
			if metrics.ConsecutiveFailures > maxConsecutiveFailures {
				maxConsecutiveFailures = metrics.ConsecutiveFailures
			}
			aggregated.recentResults = append(aggregated.recentResults, metrics.recentResults...)
			aggregated.requestHistory = append(aggregated.requestHistory, metrics.requestHistory...)

			// 取最新的时间戳
			if metrics.LastSuccessAt != nil && (latestSuccess == nil || metrics.LastSuccessAt.After(*latestSuccess)) {
				latestSuccess = metrics.LastSuccessAt
			}
			if metrics.LastFailureAt != nil && (latestFailure == nil || metrics.LastFailureAt.After(*latestFailure)) {
				latestFailure = metrics.LastFailureAt
			}
			if metrics.CircuitBrokenAt != nil && (latestCircuitBroken == nil || metrics.CircuitBrokenAt.After(*latestCircuitBroken)) {
				latestCircuitBroken = metrics.CircuitBrokenAt
			}
		}
	}

	aggregated.LastSuccessAt = latestSuccess
	aggregated.LastFailureAt = latestFailure
	aggregated.CircuitBrokenAt = latestCircuitBroken
	aggregated.ConsecutiveFailures = maxConsecutiveFailures

	return aggregated
}

// KeyUsageInfo Key 使用信息（用于排序筛选）
type KeyUsageInfo struct {
	APIKey       string
	KeyMask      string
	RequestCount int64
	LastUsedAt   *time.Time
}

// GetChannelKeyUsageInfo 获取渠道下所有 Key 的使用信息（用于排序筛选）
// 返回的 keys 已按最近使用时间排序
func (m *MetricsManager) GetChannelKeyUsageInfo(baseURL string, apiKeys []string, channelIndex int) []KeyUsageInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()

	infos := make([]KeyUsageInfo, 0, len(apiKeys))

	for _, apiKey := range apiKeys {
		metricsKey := generateMetricsKey(baseURL, apiKey, channelIndex)
		metrics, exists := m.keyMetrics[metricsKey]

		var keyMask string
		var requestCount int64
		var lastUsedAt *time.Time

		if exists {
			keyMask = metrics.KeyMask
			requestCount = metrics.RequestCount
			lastUsedAt = metrics.LastSuccessAt
			if lastUsedAt == nil {
				lastUsedAt = metrics.LastFailureAt
			}
		} else {
			// Key 还没有指标记录，使用默认脱敏
			keyMask = utils.MaskAPIKey(apiKey)
			requestCount = 0
		}

		infos = append(infos, KeyUsageInfo{
			APIKey:       apiKey,
			KeyMask:      keyMask,
			RequestCount: requestCount,
			LastUsedAt:   lastUsedAt,
		})
	}

	// 按最近使用时间排序（最近的在前面）
	sort.Slice(infos, func(i, j int) bool {
		if infos[i].LastUsedAt == nil && infos[j].LastUsedAt == nil {
			return infos[i].RequestCount > infos[j].RequestCount // 都未使用时，按访问量排序
		}
		if infos[i].LastUsedAt == nil {
			return false // i 未使用，排后面
		}
		if infos[j].LastUsedAt == nil {
			return true // j 未使用，i 排前面
		}
		return infos[i].LastUsedAt.After(*infos[j].LastUsedAt)
	})

	return infos
}

// GetChannelKeyUsageInfoMultiURL 获取渠道 Key 使用信息（支持多 URL 聚合）
func (m *MetricsManager) GetChannelKeyUsageInfoMultiURL(baseURLs []string, apiKeys []string, channelIndex int) []KeyUsageInfo {
	if len(baseURLs) == 0 {
		return []KeyUsageInfo{}
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	infos := make([]KeyUsageInfo, 0, len(apiKeys))

	for _, apiKey := range apiKeys {
		var keyMask string
		var requestCount int64
		var lastUsedAt *time.Time
		hasMetrics := false

		// 遍历所有 BaseURL 聚合同一 Key 的指标
		for _, baseURL := range baseURLs {
			metricsKey := generateMetricsKey(baseURL, apiKey, channelIndex)
			if metrics, exists := m.keyMetrics[metricsKey]; exists {
				hasMetrics = true
				if keyMask == "" {
					keyMask = metrics.KeyMask
				}
				requestCount += metrics.RequestCount

				// 取最近的使用时间
				var usedAt *time.Time
				if metrics.LastSuccessAt != nil {
					usedAt = metrics.LastSuccessAt
				}
				if usedAt == nil {
					usedAt = metrics.LastFailureAt
				}
				if usedAt != nil && (lastUsedAt == nil || usedAt.After(*lastUsedAt)) {
					lastUsedAt = usedAt
				}
			}
		}

		if !hasMetrics {
			// Key 还没有指标记录，使用默认脱敏
			keyMask = utils.MaskAPIKey(apiKey)
			requestCount = 0
		}

		infos = append(infos, KeyUsageInfo{
			APIKey:       apiKey,
			KeyMask:      keyMask,
			RequestCount: requestCount,
			LastUsedAt:   lastUsedAt,
		})
	}

	// 按最近使用时间排序（最近的在前面）
	sort.Slice(infos, func(i, j int) bool {
		if infos[i].LastUsedAt == nil && infos[j].LastUsedAt == nil {
			return infos[i].RequestCount > infos[j].RequestCount // 都未使用时，按访问量排序
		}
		if infos[i].LastUsedAt == nil {
			return false // i 未使用，排后面
		}
		if infos[j].LastUsedAt == nil {
			return true // j 未使用，i 排前面
		}
		return infos[i].LastUsedAt.After(*infos[j].LastUsedAt)
	})

	return infos
}

// SelectTopKeys 筛选展示的 Key
// 策略：先取最近使用的 5 个，再从其他 Key 中按访问量补全到 10 个
func SelectTopKeys(infos []KeyUsageInfo, maxDisplay int) []KeyUsageInfo {
	if len(infos) <= maxDisplay {
		return infos
	}

	// 分离：最近使用的和未使用的
	var recentKeys []KeyUsageInfo
	var otherKeys []KeyUsageInfo

	for i, info := range infos {
		if i < 5 {
			recentKeys = append(recentKeys, info)
		} else {
			otherKeys = append(otherKeys, info)
		}
	}

	// 其他 Key 按访问量排序（降序）
	sort.Slice(otherKeys, func(i, j int) bool {
		return otherKeys[i].RequestCount > otherKeys[j].RequestCount
	})

	// 补全到 maxDisplay 个
	result := make([]KeyUsageInfo, 0, maxDisplay)
	result = append(result, recentKeys...)

	needCount := maxDisplay - len(recentKeys)
	if needCount > 0 && len(otherKeys) > 0 {
		if len(otherKeys) > needCount {
			otherKeys = otherKeys[:needCount]
		}
		result = append(result, otherKeys...)
	}

	return result
}

// GetAllKeyMetrics 获取所有 Key 的指标
func (m *MetricsManager) GetAllKeyMetrics() []*KeyMetrics {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]*KeyMetrics, 0, len(m.keyMetrics))
	for _, metrics := range m.keyMetrics {
		result = append(result, &KeyMetrics{
			MetricsKey:          metrics.MetricsKey,
			BaseURL:             metrics.BaseURL,
			KeyMask:             metrics.KeyMask,
			RequestCount:        metrics.RequestCount,
			SuccessCount:        metrics.SuccessCount,
			FailureCount:        metrics.FailureCount,
			ConsecutiveFailures: metrics.ConsecutiveFailures,
			LastSuccessAt:       metrics.LastSuccessAt,
			LastFailureAt:       metrics.LastFailureAt,
			CircuitBrokenAt:     metrics.CircuitBrokenAt,
		})
	}
	return result
}

// GetTimeWindowStatsForKey 获取指定 Key 在时间窗口内的统计
func (m *MetricsManager) GetTimeWindowStatsForKey(baseURL, apiKey string, channelIndex int, duration time.Duration) TimeWindowStats {
	m.mu.RLock()
	defer m.mu.RUnlock()

	metricsKey := generateMetricsKey(baseURL, apiKey, channelIndex)
	metrics, exists := m.keyMetrics[metricsKey]
	if !exists {
		return TimeWindowStats{SuccessRate: 100}
	}

	cutoff := time.Now().Add(-duration)
	var requestCount, successCount, failureCount int64

	for _, record := range metrics.requestHistory {
		if record.Timestamp.After(cutoff) {
			requestCount++
			if record.Success {
				successCount++
			} else {
				failureCount++
			}
		}
	}

	successRate := float64(100)
	if requestCount > 0 {
		successRate = float64(successCount) / float64(requestCount) * 100
	}

	return TimeWindowStats{
		RequestCount: requestCount,
		SuccessCount: successCount,
		FailureCount: failureCount,
		SuccessRate:  successRate,
	}
}

// GetAllTimeWindowStatsForKey 获取单个 Key 所有时间窗口的统计
func (m *MetricsManager) GetAllTimeWindowStatsForKey(baseURL, apiKey string, channelIndex int) map[string]TimeWindowStats {
	return map[string]TimeWindowStats{
		"15m": m.GetTimeWindowStatsForKey(baseURL, apiKey, channelIndex, 15*time.Minute),
		"1h":  m.GetTimeWindowStatsForKey(baseURL, apiKey, channelIndex, 1*time.Hour),
		"6h":  m.GetTimeWindowStatsForKey(baseURL, apiKey, channelIndex, 6*time.Hour),
		"24h": m.GetTimeWindowStatsForKey(baseURL, apiKey, channelIndex, 24*time.Hour),
	}
}

// ============ 兼容旧 API 的方法（基于 channelIndex，需要调用方提供 baseURL 和 keys）============

// MetricsResponse API 响应结构
type MetricsResponse struct {
	ChannelIndex        int                        `json:"channelIndex"`
	RequestCount        int64                      `json:"requestCount"`
	SuccessCount        int64                      `json:"successCount"`
	FailureCount        int64                      `json:"failureCount"`
	SuccessRate         float64                    `json:"successRate"`
	ErrorRate           float64                    `json:"errorRate"`
	ConsecutiveFailures int64                      `json:"consecutiveFailures"`
	ActiveRequests      int64                      `json:"activeRequests"` // 进行中请求数
	Latency             int64                      `json:"latency"`
	LastSuccessAt       *string                    `json:"lastSuccessAt,omitempty"`
	LastFailureAt       *string                    `json:"lastFailureAt,omitempty"`
	CircuitBrokenAt     *string                    `json:"circuitBrokenAt,omitempty"`
	TimeWindows         map[string]TimeWindowStats `json:"timeWindows,omitempty"`
	KeyMetrics          []*KeyMetricsResponse      `json:"keyMetrics,omitempty"` // 各 Key 的详细指标
}

// KeyMetricsResponse 单个 Key 的 API 响应
type KeyMetricsResponse struct {
	KeyMask             string  `json:"keyMask"`
	RequestCount        int64   `json:"requestCount"`
	SuccessCount        int64   `json:"successCount"`
	FailureCount        int64   `json:"failureCount"`
	SuccessRate         float64 `json:"successRate"`
	ConsecutiveFailures int64   `json:"consecutiveFailures"`
	CircuitBroken       bool    `json:"circuitBroken"`
}

// ToResponseMultiURL 转换为 API 响应格式（支持多 BaseURL 聚合）
// baseURLs: 渠道配置的所有 BaseURL（用于多端点 failover 场景）
// historicalKeys: 历史 API Key（用于统计聚合，只计入总数不显示在 KeyMetrics 中）
func (m *MetricsManager) ToResponseMultiURL(channelIndex int, baseURLs []string, activeKeys []string, latency int64, historicalKeys ...[]string) *MetricsResponse {
	// 如果没有配置 BaseURL，返回空响应
	if len(baseURLs) == 0 {
		return &MetricsResponse{
			ChannelIndex: channelIndex,
			Latency:      latency,
			SuccessRate:  100,
			ErrorRate:    0,
		}
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	resp := &MetricsResponse{
		ChannelIndex: channelIndex,
		Latency:      latency,
	}

	if len(activeKeys) == 0 {
		resp.SuccessRate = 100
		resp.ErrorRate = 0
		return resp
	}

	// 用于按 API Key 聚合的临时结构
	type keyAggregation struct {
		keyMask             string
		requestCount        int64
		successCount        int64
		failureCount        int64
		consecutiveFailures int64
		circuitBroken       bool
	}
	keyAggMap := make(map[string]*keyAggregation) // key: apiKey

	var latestSuccess, latestFailure, latestCircuitBroken *time.Time
	var totalResults []bool
	var maxConsecutiveFailures int64

	// 遍历所有 BaseURL 和 Key 的组合
	for _, baseURL := range baseURLs {
		for _, apiKey := range activeKeys {
			metricsKey := generateMetricsKey(baseURL, apiKey, channelIndex)
			if metrics, exists := m.keyMetrics[metricsKey]; exists {
				resp.RequestCount += metrics.RequestCount
				resp.SuccessCount += metrics.SuccessCount
				resp.FailureCount += metrics.FailureCount
				resp.ActiveRequests += metrics.ActiveRequests
				if metrics.ConsecutiveFailures > maxConsecutiveFailures {
					maxConsecutiveFailures = metrics.ConsecutiveFailures
				}
				totalResults = append(totalResults, metrics.recentResults...)

				// 取最新的时间戳
				if metrics.LastSuccessAt != nil && (latestSuccess == nil || metrics.LastSuccessAt.After(*latestSuccess)) {
					latestSuccess = metrics.LastSuccessAt
				}
				if metrics.LastFailureAt != nil && (latestFailure == nil || metrics.LastFailureAt.After(*latestFailure)) {
					latestFailure = metrics.LastFailureAt
				}
				if metrics.CircuitBrokenAt != nil && (latestCircuitBroken == nil || metrics.CircuitBrokenAt.After(*latestCircuitBroken)) {
					latestCircuitBroken = metrics.CircuitBrokenAt
				}

				// 按 API Key 聚合（同一 Key 在不同 URL 的指标合并）
				if agg, ok := keyAggMap[apiKey]; ok {
					agg.requestCount += metrics.RequestCount
					agg.successCount += metrics.SuccessCount
					agg.failureCount += metrics.FailureCount
					if metrics.ConsecutiveFailures > agg.consecutiveFailures {
						agg.consecutiveFailures = metrics.ConsecutiveFailures
					}
					if metrics.CircuitBrokenAt != nil {
						agg.circuitBroken = true
					}
				} else {
					keyAggMap[apiKey] = &keyAggregation{
						keyMask:             metrics.KeyMask,
						requestCount:        metrics.RequestCount,
						successCount:        metrics.SuccessCount,
						failureCount:        metrics.FailureCount,
						consecutiveFailures: metrics.ConsecutiveFailures,
						circuitBroken:       metrics.CircuitBrokenAt != nil,
					}
				}
			}
		}
	}

	// 聚合历史 Key 的指标（只计入总数，不显示在 KeyMetrics 中）
	if len(historicalKeys) > 0 && len(historicalKeys[0]) > 0 {
		for _, baseURL := range baseURLs {
			for _, apiKey := range historicalKeys[0] {
				metricsKey := generateMetricsKey(baseURL, apiKey, channelIndex)
				if metrics, exists := m.keyMetrics[metricsKey]; exists {
					resp.RequestCount += metrics.RequestCount
					resp.SuccessCount += metrics.SuccessCount
					resp.FailureCount += metrics.FailureCount
					// 历史 Key 不计入 totalResults（不影响实时失败率计算）
					// 历史 Key 不计入 maxConsecutiveFailures（不影响熔断判断）
				}
			}
		}
	}

	// 构建按 Key 聚合后的响应（保持 activeKeys 顺序）
	var keyResponses []*KeyMetricsResponse
	for _, apiKey := range activeKeys {
		if agg, ok := keyAggMap[apiKey]; ok {
			keySuccessRate := float64(100)
			if agg.requestCount > 0 {
				keySuccessRate = float64(agg.successCount) / float64(agg.requestCount) * 100
			}
			keyResponses = append(keyResponses, &KeyMetricsResponse{
				KeyMask:             agg.keyMask,
				RequestCount:        agg.requestCount,
				SuccessCount:        agg.successCount,
				FailureCount:        agg.failureCount,
				SuccessRate:         keySuccessRate,
				ConsecutiveFailures: agg.consecutiveFailures,
				CircuitBroken:       agg.circuitBroken,
			})
		}
	}

	// 计算聚合失败率
	resp.ConsecutiveFailures = maxConsecutiveFailures

	if len(totalResults) > 0 {
		failures := 0
		for _, success := range totalResults {
			if !success {
				failures++
			}
		}
		failureRate := float64(failures) / float64(len(totalResults))
		resp.SuccessRate = (1 - failureRate) * 100
		resp.ErrorRate = failureRate * 100
	} else {
		resp.SuccessRate = 100
		resp.ErrorRate = 0
	}

	if latestSuccess != nil {
		t := latestSuccess.Format(time.RFC3339)
		resp.LastSuccessAt = &t
	}
	if latestFailure != nil {
		t := latestFailure.Format(time.RFC3339)
		resp.LastFailureAt = &t
	}
	if latestCircuitBroken != nil {
		t := latestCircuitBroken.Format(time.RFC3339)
		resp.CircuitBrokenAt = &t
	}

	resp.KeyMetrics = keyResponses

	// 计算聚合的时间窗口统计（多 URL 版本）
	resp.TimeWindows = m.calculateAggregatedTimeWindowsMultiURL(baseURLs, activeKeys, channelIndex)

	return resp
}

// ToResponse 转换为 API 响应格式（需要提供 baseURL 和 activeKeys）
func (m *MetricsManager) ToResponse(channelIndex int, baseURL string, activeKeys []string, latency int64) *MetricsResponse {
	m.mu.RLock()
	defer m.mu.RUnlock()

	resp := &MetricsResponse{
		ChannelIndex: channelIndex,
		Latency:      latency,
	}

	if len(activeKeys) == 0 {
		resp.SuccessRate = 100
		resp.ErrorRate = 0
		return resp
	}

	var keyResponses []*KeyMetricsResponse
	var latestSuccess, latestFailure, latestCircuitBroken *time.Time
	var totalResults []bool
	var maxConsecutiveFailures int64

	for _, apiKey := range activeKeys {
		metricsKey := generateMetricsKey(baseURL, apiKey, channelIndex)
		if metrics, exists := m.keyMetrics[metricsKey]; exists {
			resp.RequestCount += metrics.RequestCount
			resp.SuccessCount += metrics.SuccessCount
			resp.FailureCount += metrics.FailureCount
			resp.ActiveRequests += metrics.ActiveRequests
			if metrics.ConsecutiveFailures > maxConsecutiveFailures {
				maxConsecutiveFailures = metrics.ConsecutiveFailures
			}
			totalResults = append(totalResults, metrics.recentResults...)

			// 取最新的时间戳
			if metrics.LastSuccessAt != nil && (latestSuccess == nil || metrics.LastSuccessAt.After(*latestSuccess)) {
				latestSuccess = metrics.LastSuccessAt
			}
			if metrics.LastFailureAt != nil && (latestFailure == nil || metrics.LastFailureAt.After(*latestFailure)) {
				latestFailure = metrics.LastFailureAt
			}
			if metrics.CircuitBrokenAt != nil && (latestCircuitBroken == nil || metrics.CircuitBrokenAt.After(*latestCircuitBroken)) {
				latestCircuitBroken = metrics.CircuitBrokenAt
			}

			// 单个 Key 的指标
			keySuccessRate := float64(100)
			if metrics.RequestCount > 0 {
				keySuccessRate = float64(metrics.SuccessCount) / float64(metrics.RequestCount) * 100
			}
			keyResponses = append(keyResponses, &KeyMetricsResponse{
				KeyMask:             metrics.KeyMask,
				RequestCount:        metrics.RequestCount,
				SuccessCount:        metrics.SuccessCount,
				FailureCount:        metrics.FailureCount,
				SuccessRate:         keySuccessRate,
				ConsecutiveFailures: metrics.ConsecutiveFailures,
				CircuitBroken:       metrics.CircuitBrokenAt != nil,
			})
		}
	}

	// 计算聚合失败率
	resp.ConsecutiveFailures = maxConsecutiveFailures

	if len(totalResults) > 0 {
		failures := 0
		for _, success := range totalResults {
			if !success {
				failures++
			}
		}
		failureRate := float64(failures) / float64(len(totalResults))
		resp.SuccessRate = (1 - failureRate) * 100
		resp.ErrorRate = failureRate * 100
	} else {
		resp.SuccessRate = 100
		resp.ErrorRate = 0
	}

	if latestSuccess != nil {
		t := latestSuccess.Format(time.RFC3339)
		resp.LastSuccessAt = &t
	}
	if latestFailure != nil {
		t := latestFailure.Format(time.RFC3339)
		resp.LastFailureAt = &t
	}
	if latestCircuitBroken != nil {
		t := latestCircuitBroken.Format(time.RFC3339)
		resp.CircuitBrokenAt = &t
	}

	resp.KeyMetrics = keyResponses

	// 计算聚合的时间窗口统计
	resp.TimeWindows = m.calculateAggregatedTimeWindowsInternal(baseURL, activeKeys, channelIndex)

	return resp
}

// calculateAggregatedTimeWindowsInternal 计算聚合的时间窗口统计（内部方法，调用前需持有锁）
func (m *MetricsManager) calculateAggregatedTimeWindowsInternal(baseURL string, activeKeys []string, channelIndex int) map[string]TimeWindowStats {
	windows := map[string]time.Duration{
		"15m": 15 * time.Minute,
		"1h":  1 * time.Hour,
		"6h":  6 * time.Hour,
		"24h": 24 * time.Hour,
	}

	result := make(map[string]TimeWindowStats)
	now := time.Now()

	for label, duration := range windows {
		cutoff := now.Add(-duration)
		var requestCount, successCount, failureCount int64
		var inputTokens, outputTokens, cacheCreationTokens, cacheReadTokens int64

		for _, apiKey := range activeKeys {
			metricsKey := generateMetricsKey(baseURL, apiKey, channelIndex)
			if metrics, exists := m.keyMetrics[metricsKey]; exists {
				for _, record := range metrics.requestHistory {
					if record.Timestamp.After(cutoff) {
						requestCount++
						if record.Success {
							successCount++
						} else {
							failureCount++
						}
						inputTokens += record.InputTokens
						outputTokens += record.OutputTokens
						cacheCreationTokens += record.CacheCreationInputTokens
						cacheReadTokens += record.CacheReadInputTokens
					}
				}
			}
		}

		successRate := float64(100)
		if requestCount > 0 {
			successRate = float64(successCount) / float64(requestCount) * 100
		}

		cacheHitRate := float64(0)
		denom := cacheReadTokens + inputTokens
		if denom > 0 {
			cacheHitRate = float64(cacheReadTokens) / float64(denom) * 100
		}

		result[label] = TimeWindowStats{
			RequestCount:        requestCount,
			SuccessCount:        successCount,
			FailureCount:        failureCount,
			SuccessRate:         successRate,
			InputTokens:         inputTokens,
			OutputTokens:        outputTokens,
			CacheCreationTokens: cacheCreationTokens,
			CacheReadTokens:     cacheReadTokens,
			CacheHitRate:        cacheHitRate,
		}
	}

	return result
}

// calculateAggregatedTimeWindowsMultiURL 计算聚合的时间窗口统计（多 URL 版本，内部方法，调用前需持有锁）
func (m *MetricsManager) calculateAggregatedTimeWindowsMultiURL(baseURLs []string, activeKeys []string, channelIndex int) map[string]TimeWindowStats {
	windows := map[string]time.Duration{
		"15m": 15 * time.Minute,
		"1h":  1 * time.Hour,
		"6h":  6 * time.Hour,
		"24h": 24 * time.Hour,
	}

	result := make(map[string]TimeWindowStats)
	now := time.Now()

	for label, duration := range windows {
		cutoff := now.Add(-duration)
		var requestCount, successCount, failureCount int64
		var inputTokens, outputTokens, cacheCreationTokens, cacheReadTokens int64

		// 遍历所有 BaseURL 和 Key 的组合
		for _, baseURL := range baseURLs {
			for _, apiKey := range activeKeys {
				metricsKey := generateMetricsKey(baseURL, apiKey, channelIndex)
				if metrics, exists := m.keyMetrics[metricsKey]; exists {
					for _, record := range metrics.requestHistory {
						if record.Timestamp.After(cutoff) {
							requestCount++
							if record.Success {
								successCount++
							} else {
								failureCount++
							}
							inputTokens += record.InputTokens
							outputTokens += record.OutputTokens
							cacheCreationTokens += record.CacheCreationInputTokens
							cacheReadTokens += record.CacheReadInputTokens
						}
					}
				}
			}
		}

		successRate := float64(100)
		if requestCount > 0 {
			successRate = float64(successCount) / float64(requestCount) * 100
		}

		cacheHitRate := float64(0)
		denom := cacheReadTokens + inputTokens
		if denom > 0 {
			cacheHitRate = float64(cacheReadTokens) / float64(denom) * 100
		}

		result[label] = TimeWindowStats{
			RequestCount:        requestCount,
			SuccessCount:        successCount,
			FailureCount:        failureCount,
			SuccessRate:         successRate,
			InputTokens:         inputTokens,
			OutputTokens:        outputTokens,
			CacheCreationTokens: cacheCreationTokens,
			CacheReadTokens:     cacheReadTokens,
			CacheHitRate:        cacheHitRate,
		}
	}

	return result
}
