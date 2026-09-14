package metrics

import (
	"strings"
	"time"
)

// ============ 历史数据查询方法（用于图表可视化）============

// HistoryDataPoint 历史数据点（用于时间序列图表）
type HistoryDataPoint struct {
	Timestamp    time.Time `json:"timestamp"`
	RequestCount int64     `json:"requestCount"`
	SuccessCount int64     `json:"successCount"`
	FailureCount int64     `json:"failureCount"`
	SuccessRate  float64   `json:"successRate"`
}

// KeyHistoryDataPoint Key 级别历史数据点（包含 Token 和 Cache 数据）
type KeyHistoryDataPoint struct {
	Timestamp                time.Time `json:"timestamp"`
	RequestCount             int64     `json:"requestCount"`
	SuccessCount             int64     `json:"successCount"`
	FailureCount             int64     `json:"failureCount"`
	SuccessRate              float64   `json:"successRate"`
	InputTokens              int64     `json:"inputTokens"`
	OutputTokens             int64     `json:"outputTokens"`
	CacheCreationInputTokens int64     `json:"cacheCreationTokens"`
	CacheReadInputTokens     int64     `json:"cacheReadTokens"`
	Model                    string    `json:"model"`
	CacheHitRate             float64   `json:"cacheHitRate"`
}

// GetHistoricalStats 获取历史统计数据（按时间间隔聚合）
// duration: 查询时间范围 (如 1h, 6h, 24h)
// interval: 聚合间隔 (如 5m, 15m, 1h)
func (m *MetricsManager) GetHistoricalStats(baseURL string, activeKeys []string, channelIndex int, duration, interval time.Duration) []HistoryDataPoint {
	// 参数验证
	if interval <= 0 || duration <= 0 {
		return []HistoryDataPoint{}
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	now := time.Now()
	// 时间对齐到 interval 边界
	startTime := now.Add(-duration).Truncate(interval)
	// endTime 延伸一个 interval，确保当前时间段的请求也被包含
	endTime := now.Truncate(interval).Add(interval)

	// 计算需要多少个数据点（+1 用于包含延伸的当前时间段）
	numPoints := int(duration / interval)
	if numPoints <= 0 {
		numPoints = 1
	}
	numPoints++ // 额外的一个桶用于当前时间段

	// 使用 map 按时间分桶，优化性能：O(records) 而不是 O(records * numPoints)
	buckets := make(map[int64]*bucketData)
	for i := 0; i < numPoints; i++ {
		buckets[int64(i)] = &bucketData{}
	}

	// 收集所有相关 Key 的请求历史并放入对应桶
	for _, apiKey := range activeKeys {
		metricsKey := generateMetricsKey(baseURL, apiKey, channelIndex)
		if metrics, exists := m.keyMetrics[metricsKey]; exists {
			for _, record := range metrics.requestHistory {
				// 使用 [startTime, endTime) 的区间，避免 endTime 处 offset 越界
				if !record.Timestamp.Before(startTime) && record.Timestamp.Before(endTime) {
					// 计算记录应该属于哪个桶
					offset := int64(record.Timestamp.Sub(startTime) / interval)
					if offset >= 0 && offset < int64(numPoints) {
						b := buckets[offset]
						b.requestCount++
						if record.Success {
							b.successCount++
						} else {
							b.failureCount++
						}
					}
				}
			}
		}
	}

	// 构建结果
	result := make([]HistoryDataPoint, numPoints)
	for i := 0; i < numPoints; i++ {
		b := buckets[int64(i)]
		// 空桶成功率默认为 0，避免误导（100% 暗示完美成功）
		successRate := float64(0)
		if b.requestCount > 0 {
			successRate = float64(b.successCount) / float64(b.requestCount) * 100
		}
		result[i] = HistoryDataPoint{
			Timestamp:    startTime.Add(time.Duration(i) * interval),
			RequestCount: b.requestCount,
			SuccessCount: b.successCount,
			FailureCount: b.failureCount,
			SuccessRate:  successRate,
		}
	}

	return result
}

// GetHistoricalStatsMultiURL 获取多 URL 聚合的历史统计数据
func (m *MetricsManager) GetHistoricalStatsMultiURL(baseURLs []string, activeKeys []string, channelIndex int, duration, interval time.Duration) []HistoryDataPoint {
	// 参数验证
	if interval <= 0 || duration <= 0 || len(baseURLs) == 0 {
		return []HistoryDataPoint{}
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	now := time.Now()
	// 时间对齐到 interval 边界
	startTime := now.Add(-duration).Truncate(interval)
	// endTime 延伸一个 interval，确保当前时间段的请求也被包含
	endTime := now.Truncate(interval).Add(interval)

	// 计算需要多少个数据点（+1 用于包含延伸的当前时间段）
	numPoints := int(duration / interval)
	if numPoints <= 0 {
		numPoints = 1
	}
	numPoints++ // 额外的一个桶用于当前时间段

	// 使用 map 按时间分桶，优化性能：O(records) 而不是 O(records * numPoints)
	buckets := make(map[int64]*bucketData)
	for i := 0; i < numPoints; i++ {
		buckets[int64(i)] = &bucketData{}
	}

	// 收集所有 BaseURL 和 Key 组合的请求历史并放入对应桶
	for _, baseURL := range baseURLs {
		for _, apiKey := range activeKeys {
			metricsKey := generateMetricsKey(baseURL, apiKey, channelIndex)
			if metrics, exists := m.keyMetrics[metricsKey]; exists {
				for _, record := range metrics.requestHistory {
					// 使用 [startTime, endTime) 的区间，避免 endTime 处 offset 越界
					if !record.Timestamp.Before(startTime) && record.Timestamp.Before(endTime) {
						// 计算记录应该属于哪个桶
						offset := int64(record.Timestamp.Sub(startTime) / interval)
						if offset >= 0 && offset < int64(numPoints) {
							b := buckets[offset]
							b.requestCount++
							if record.Success {
								b.successCount++
							} else {
								b.failureCount++
							}
						}
					}
				}
			}
		}
	}

	// 构建结果
	result := make([]HistoryDataPoint, numPoints)
	for i := 0; i < numPoints; i++ {
		b := buckets[int64(i)]
		// 空桶成功率默认为 0，避免误导（100% 暗示完美成功）
		successRate := float64(0)
		if b.requestCount > 0 {
			successRate = float64(b.successCount) / float64(b.requestCount) * 100
		}
		result[i] = HistoryDataPoint{
			Timestamp:    startTime.Add(time.Duration(i) * interval),
			RequestCount: b.requestCount,
			SuccessCount: b.successCount,
			FailureCount: b.failureCount,
			SuccessRate:  successRate,
		}
	}

	return result
}

// bucketData 用于时间分桶的辅助结构
type bucketData struct {
	requestCount int64
	successCount int64
	failureCount int64
}

func (m *MetricsManager) GetAllKeysHistoricalStats(duration, interval time.Duration) []HistoryDataPoint {
	// 参数验证
	if interval <= 0 || duration <= 0 {
		return []HistoryDataPoint{}
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	now := time.Now()
	// 时间对齐到 interval 边界
	startTime := now.Add(-duration).Truncate(interval)
	// endTime 延伸一个 interval，确保当前时间段的请求也被包含
	endTime := now.Truncate(interval).Add(interval)

	numPoints := int(duration / interval)
	if numPoints <= 0 {
		numPoints = 1
	}
	numPoints++ // 额外的一个桶用于当前时间段

	// 使用 map 按时间分桶，优化性能
	buckets := make(map[int64]*bucketData)
	for i := 0; i < numPoints; i++ {
		buckets[int64(i)] = &bucketData{}
	}

	// 收集所有 Key 的请求历史并放入对应桶
	for _, metrics := range m.keyMetrics {
		for _, record := range metrics.requestHistory {
			// 使用 [startTime, endTime) 的区间，避免 endTime 处 offset 越界
			if !record.Timestamp.Before(startTime) && record.Timestamp.Before(endTime) {
				offset := int64(record.Timestamp.Sub(startTime) / interval)
				if offset >= 0 && offset < int64(numPoints) {
					b := buckets[offset]
					b.requestCount++
					if record.Success {
						b.successCount++
					} else {
						b.failureCount++
					}
				}
			}
		}
	}

	// 构建结果
	result := make([]HistoryDataPoint, numPoints)
	for i := 0; i < numPoints; i++ {
		b := buckets[int64(i)]
		// 空桶成功率默认为 0，避免误导（100% 暗示完美成功）
		successRate := float64(0)
		if b.requestCount > 0 {
			successRate = float64(b.successCount) / float64(b.requestCount) * 100
		}
		result[i] = HistoryDataPoint{
			Timestamp:    startTime.Add(time.Duration(i) * interval),
			RequestCount: b.requestCount,
			SuccessCount: b.successCount,
			FailureCount: b.failureCount,
			SuccessRate:  successRate,
		}
	}

	return result
}

// TODO: SyncToProfile 将现有指标同步到性能画像（ProfileManager 未实现）
// func (m *MetricsManager) SyncToProfile(pm *ProfileManager, baseURL string, apiKeys []string, channelIdx int) {
// 	if pm == nil {
// 		return
// 	}
//
// 	m.mu.RLock()
// 	defer m.mu.RUnlock()

// 	profile := pm.GetOrCreateProfile(baseURL, apiKeys, channelIdx)

// 	// 计算成功率
// 	var totalReq, totalSuccess int64
// 	var recentResults []bool

// 	for _, key := range apiKeys {
// 		metricsKey := generateMetricsKey(baseURL, key)
// 		if metrics, exists := m.keyMetrics[metricsKey]; exists {
// 			totalReq += metrics.RequestCount
// 			totalSuccess += metrics.SuccessCount
// 			recentResults = append(recentResults, metrics.recentResults...)
// 		}
// 	}

// 	profile.mu.Lock()
// 	defer profile.mu.Unlock()

// 	if totalReq > 0 {
// 		profile.SuccessRate = float64(totalSuccess) / float64(totalReq) * 100
// 	}

// 	// 计算最近成功率
// 	if len(recentResults) > 0 {
// 		recentSuccess := 0
// 		for _, success := range recentResults {
// 			if success {
// 				recentSuccess++
// 			}
// 		}
// 		profile.RecentSuccessRate = float64(recentSuccess) / float64(len(recentResults)) * 100
// 	} else {
// 		profile.RecentSuccessRate = profile.SuccessRate
// 	}
// }

// GetKeyHistoricalStats 获取单个 Key 的历史统计数据（包含 Token 和 Cache 数据）
func (m *MetricsManager) GetKeyHistoricalStats(baseURL, apiKey string, channelIndex int, duration, interval time.Duration) []KeyHistoryDataPoint {
	// 参数验证
	if interval <= 0 || duration <= 0 {
		return []KeyHistoryDataPoint{}
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	now := time.Now()
	// 时间对齐到 interval 边界
	startTime := now.Add(-duration).Truncate(interval)
	// endTime 延伸一个 interval，确保当前时间段的请求也被包含
	endTime := now.Truncate(interval).Add(interval)

	numPoints := int(duration / interval)
	if numPoints <= 0 {
		numPoints = 1
	}
	numPoints++ // 额外的一个桶用于当前时间段

	// 使用 map 按时间分桶
	buckets := make(map[int64]*keyBucketData)
	for i := 0; i < numPoints; i++ {
		buckets[int64(i)] = &keyBucketData{}
	}

	// 获取 Key 的指标
	metricsKey := generateMetricsKey(baseURL, apiKey, channelIndex)
	metrics, exists := m.keyMetrics[metricsKey]
	if !exists {
		// Key 不存在，返回空数据点
		result := make([]KeyHistoryDataPoint, numPoints)
		for i := 0; i < numPoints; i++ {
			result[i] = KeyHistoryDataPoint{
				Timestamp: startTime.Add(time.Duration(i+1) * interval),
			}
		}
		return result
	}

	// 收集该 Key 的请求历史并放入对应桶
	for _, record := range metrics.requestHistory {
		// 使用 Before(endTime) 排除恰好落在 endTime 的记录，避免 offset 越界
		if record.Timestamp.After(startTime) && record.Timestamp.Before(endTime) {
			offset := int64(record.Timestamp.Sub(startTime) / interval)
			if offset >= 0 && offset < int64(numPoints) {
				b := buckets[offset]
				b.requestCount++
				if record.Success {
					b.successCount++
				} else {
					b.failureCount++
				}
				// 累加 Token 数据
				b.inputTokens += record.InputTokens
				b.outputTokens += record.OutputTokens
				b.cacheCreationTokens += record.CacheCreationInputTokens
				b.cacheReadTokens += record.CacheReadInputTokens
			}
		}
	}

	// 构建结果
	result := make([]KeyHistoryDataPoint, numPoints)
	for i := 0; i < numPoints; i++ {
		b := buckets[int64(i)]
		// 空桶成功率默认为 0，避免误导（100% 暗示完美成功）
		successRate := float64(0)
		if b.requestCount > 0 {
			successRate = float64(b.successCount) / float64(b.requestCount) * 100
		}
		result[i] = KeyHistoryDataPoint{
			Timestamp:                startTime.Add(time.Duration(i+1) * interval),
			RequestCount:             b.requestCount,
			SuccessCount:             b.successCount,
			FailureCount:             b.failureCount,
			SuccessRate:              successRate,
			InputTokens:              b.inputTokens,
			OutputTokens:             b.outputTokens,
			CacheCreationInputTokens: b.cacheCreationTokens,
			CacheReadInputTokens:     b.cacheReadTokens,
		}
	}

	return result
}

// GetKeyHistoricalStatsMultiURL 获取单个 Key 的多 URL 聚合历史统计
func (m *MetricsManager) GetKeyHistoricalStatsMultiURL(baseURLs []string, apiKey string, channelIndex int, duration, interval time.Duration) []KeyHistoryDataPoint {
	// 参数验证
	if interval <= 0 || duration <= 0 || len(baseURLs) == 0 {
		return []KeyHistoryDataPoint{}
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	now := time.Now()
	// 时间对齐到 interval 边界
	startTime := now.Add(-duration).Truncate(interval)
	// endTime 延伸一个 interval，确保当前时间段的请求也被包含
	endTime := now.Truncate(interval).Add(interval)

	numPoints := int(duration / interval)
	if numPoints <= 0 {
		numPoints = 1
	}
	numPoints++ // 额外的一个桶用于当前时间段

	// 使用 map 按时间分桶
	buckets := make(map[int64]*keyBucketData)
	for i := 0; i < numPoints; i++ {
		buckets[int64(i)] = &keyBucketData{}
	}

	// 遍历所有 BaseURL 聚合同一 Key 的历史数据
	hasData := false
	for _, baseURL := range baseURLs {
		metricsKey := generateMetricsKey(baseURL, apiKey, channelIndex)
		metrics, exists := m.keyMetrics[metricsKey]
		if !exists {
			continue
		}
		hasData = true

		// 收集该 URL+Key 组合的请求历史并放入对应桶
		for _, record := range metrics.requestHistory {
			// 使用 Before(endTime) 排除恰好落在 endTime 的记录，避免 offset 越界
			if record.Timestamp.After(startTime) && record.Timestamp.Before(endTime) {
				offset := int64(record.Timestamp.Sub(startTime) / interval)
				if offset >= 0 && offset < int64(numPoints) {
					b := buckets[offset]
					b.requestCount++
					if record.Success {
						b.successCount++
					} else {
						b.failureCount++
					}
					// 累加 Token 数据
					b.inputTokens += record.InputTokens
					b.outputTokens += record.OutputTokens
					b.cacheCreationTokens += record.CacheCreationInputTokens
					b.cacheReadTokens += record.CacheReadInputTokens
					if record.Model != "" {
						b.models = append(b.models, record.Model)
					}
				}
			}
		}
	}

	// 如果没有任何数据，返回空数据点
	if !hasData {
		result := make([]KeyHistoryDataPoint, numPoints)
		for i := 0; i < numPoints; i++ {
			result[i] = KeyHistoryDataPoint{
				Timestamp: startTime.Add(time.Duration(i+1) * interval),
			}
		}
		return result
	}

	// 构建结果
	result := make([]KeyHistoryDataPoint, numPoints)
	for i := 0; i < numPoints; i++ {
		b := buckets[int64(i)]
		// 空桶成功率默认为 0，避免误导（100% 暗示完美成功）
		successRate := float64(0)
		if b.requestCount > 0 {
			successRate = float64(b.successCount) / float64(b.requestCount) * 100
		}

		// 去重并逗号连接 models
		var modelStr string
		if len(b.models) > 0 {
			uniqueModels := make(map[string]bool)
			var modelList []string
			for _, mdl := range b.models {
				if mdl != "" && !uniqueModels[mdl] {
					uniqueModels[mdl] = true
					modelList = append(modelList, mdl)
				}
			}
			modelStr = strings.Join(modelList, ", ")
		}

		// 计算缓存命中率
		var cacheHitRate float64
		if b.cacheReadTokens+b.inputTokens > 0 {
			cacheHitRate = float64(b.cacheReadTokens) / float64(b.cacheReadTokens+b.inputTokens) * 100
		}

		result[i] = KeyHistoryDataPoint{
			Timestamp:                startTime.Add(time.Duration(i+1) * interval),
			RequestCount:             b.requestCount,
			SuccessCount:             b.successCount,
			FailureCount:             b.failureCount,
			SuccessRate:              successRate,
			InputTokens:              b.inputTokens,
			OutputTokens:             b.outputTokens,
			CacheCreationInputTokens: b.cacheCreationTokens,
			CacheReadInputTokens:     b.cacheReadTokens,
			Model:                    modelStr,
			CacheHitRate:             cacheHitRate,
		}
	}

	return result
}

// keyBucketData Key 级别时间分桶的辅助结构（包含 Token 数据）
type keyBucketData struct {
	requestCount        int64
	successCount        int64
	failureCount        int64
	inputTokens         int64
	outputTokens        int64
	cacheCreationTokens int64
	cacheReadTokens     int64
	models              []string
}

// ============ 全局统计数据结构和方法（用于全局流量统计图表）============

// GlobalHistoryDataPoint 全局历史数据点（含 Token 数据）。
type GlobalHistoryDataPoint struct {
	Timestamp           time.Time `json:"timestamp"`
	RequestCount        int64     `json:"requestCount"`
	SuccessCount        int64     `json:"successCount"`
	FailureCount        int64     `json:"failureCount"`
	SuccessRate         float64   `json:"successRate"`
	InputTokens         int64     `json:"inputTokens"`
	OutputTokens        int64     `json:"outputTokens"`
	CacheCreationTokens int64     `json:"cacheCreationTokens"`
	CacheReadTokens     int64     `json:"cacheReadTokens"`
}

// GlobalStatsSummary 全局统计汇总。
type GlobalStatsSummary struct {
	TotalRequests            int64   `json:"totalRequests"`
	TotalSuccess             int64   `json:"totalSuccess"`
	TotalFailure             int64   `json:"totalFailure"`
	TotalInputTokens         int64   `json:"totalInputTokens"`
	TotalOutputTokens        int64   `json:"totalOutputTokens"`
	TotalCacheCreationTokens int64   `json:"totalCacheCreationTokens"`
	TotalCacheReadTokens     int64   `json:"totalCacheReadTokens"`
	AvgSuccessRate           float64 `json:"avgSuccessRate"`
	Duration                 string  `json:"duration"`
}

// GlobalStatsHistoryResponse 全局统计响应
type GlobalStatsHistoryResponse struct {
	DataPoints []GlobalHistoryDataPoint `json:"dataPoints"`
	Summary    GlobalStatsSummary       `json:"summary"`
}

// GetGlobalHistoricalStatsWithTokens 获取全局历史统计（包含 Token 数据）
// 聚合所有 Key 的数据，按时间间隔分桶。
func (m *MetricsManager) GetGlobalHistoricalStatsWithTokens(duration, interval time.Duration) GlobalStatsHistoryResponse {
	// 参数验证
	if interval <= 0 || duration <= 0 {
		return GlobalStatsHistoryResponse{
			DataPoints: []GlobalHistoryDataPoint{},
			Summary:    GlobalStatsSummary{Duration: duration.String()},
		}
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	now := time.Now()
	// 时间对齐到 interval 边界
	startTime := now.Add(-duration).Truncate(interval)
	// endTime 延伸一个 interval，确保当前时间段的请求也被包含
	endTime := now.Truncate(interval).Add(interval)

	numPoints := int(duration / interval)
	if numPoints <= 0 {
		numPoints = 1
	}
	numPoints++ // 额外的一个桶用于当前时间段

	// 使用 map 按时间分桶
	buckets := make(map[int64]*globalBucketData)
	for i := 0; i < numPoints; i++ {
		buckets[int64(i)] = &globalBucketData{}
	}

	// 汇总统计
	var totalRequests, totalSuccess, totalFailure int64
	var totalInputTokens, totalOutputTokens, totalCacheCreation, totalCacheRead int64

	// 遍历所有 Key 的请求历史
	for _, metrics := range m.keyMetrics {
		for _, record := range metrics.requestHistory {
			// 使用 Before(endTime) 排除恰好落在 endTime 的记录，避免 offset 越界
			if record.Timestamp.After(startTime) && record.Timestamp.Before(endTime) {
				offset := int64(record.Timestamp.Sub(startTime) / interval)
				if offset >= 0 && offset < int64(numPoints) {
					b := buckets[offset]
					b.requestCount++
					if record.Success {
						b.successCount++
					} else {
						b.failureCount++
					}
					b.inputTokens += record.InputTokens
					b.outputTokens += record.OutputTokens
					b.cacheCreationTokens += record.CacheCreationInputTokens
					b.cacheReadTokens += record.CacheReadInputTokens

					// 累加汇总
					totalRequests++
					if record.Success {
						totalSuccess++
					} else {
						totalFailure++
					}
					totalInputTokens += record.InputTokens
					totalOutputTokens += record.OutputTokens
					totalCacheCreation += record.CacheCreationInputTokens
					totalCacheRead += record.CacheReadInputTokens
				}
			}
		}
	}

	// 构建数据点结果
	dataPoints := make([]GlobalHistoryDataPoint, numPoints)
	for i := 0; i < numPoints; i++ {
		b := buckets[int64(i)]
		successRate := float64(0)
		if b.requestCount > 0 {
			successRate = float64(b.successCount) / float64(b.requestCount) * 100
		}
		dataPoints[i] = GlobalHistoryDataPoint{
			Timestamp:           startTime.Add(time.Duration(i+1) * interval),
			RequestCount:        b.requestCount,
			SuccessCount:        b.successCount,
			FailureCount:        b.failureCount,
			SuccessRate:         successRate,
			InputTokens:         b.inputTokens,
			OutputTokens:        b.outputTokens,
			CacheCreationTokens: b.cacheCreationTokens,
			CacheReadTokens:     b.cacheReadTokens,
		}
	}

	// 计算平均成功率
	avgSuccessRate := float64(0)
	if totalRequests > 0 {
		avgSuccessRate = float64(totalSuccess) / float64(totalRequests) * 100
	}

	summary := GlobalStatsSummary{
		TotalRequests:            totalRequests,
		TotalSuccess:             totalSuccess,
		TotalFailure:             totalFailure,
		TotalInputTokens:         totalInputTokens,
		TotalOutputTokens:        totalOutputTokens,
		TotalCacheCreationTokens: totalCacheCreation,
		TotalCacheReadTokens:     totalCacheRead,
		AvgSuccessRate:           avgSuccessRate,
		Duration:                 duration.String(),
	}

	return GlobalStatsHistoryResponse{
		DataPoints: dataPoints,
		Summary:    summary,
	}
}

// globalBucketData 全局统计时间分桶的辅助结构
type globalBucketData struct {
	requestCount        int64
	successCount        int64
	failureCount        int64
	inputTokens         int64
	outputTokens        int64
	cacheCreationTokens int64
	cacheReadTokens     int64
}

// CalculateTodayDuration 计算"今日"时间范围（从今天 0 点到现在）
func CalculateTodayDuration() time.Duration {
	now := time.Now()
	startOfDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	return now.Sub(startOfDay)
}

// ============ 渠道实时活跃度数据（用于渐变背景显示）============

// ActivitySegment 活跃度分段数据（每 6 秒一段）
type ActivitySegment struct {
	RequestCount int64 `json:"requestCount"`
	SuccessCount int64 `json:"successCount"`
	FailureCount int64 `json:"failureCount"`
	InputTokens  int64 `json:"inputTokens"`
	OutputTokens int64 `json:"outputTokens"`
}

// ChannelRecentActivity 渠道最近活跃度数据
type ChannelRecentActivity struct {
	ChannelIndex int               `json:"channelIndex"`
	Segments     []ActivitySegment `json:"segments"` // 150 段，每段 6 秒，从旧到新（共 15 分钟）
	RPM          float64           `json:"rpm"`      // 15分钟平均 RPM
	TPM          float64           `json:"tpm"`      // 15分钟平均 TPM
}

// GetRecentActivityMultiURL 获取渠道最近活跃度数据（支持多 URL 和多 Key 聚合）
// 参数：
//   - channelIndex: 渠道索引
//   - baseURLs: 渠道的所有故障转移 URL（支持多个）
//   - activeKeys: 渠道的所有活跃 API Key（支持多个）
//
// 返回：
//   - 150 段活跃度数据（每段 6 秒，共 15 分钟）
//   - 自动聚合所有 URL × Key 组合的请求数据
//   - RPM/TPM 为 15 分钟平均值
func (m *MetricsManager) GetRecentActivityMultiURL(channelIndex int, baseURLs []string, activeKeys []string) *ChannelRecentActivity {
	// 150 段，每段 6 秒 = 900 秒 = 15 分钟
	const numSegments = 150
	const segmentDuration = 6 * time.Second

	if len(baseURLs) == 0 || len(activeKeys) == 0 {
		return &ChannelRecentActivity{
			ChannelIndex: channelIndex,
			Segments:     make([]ActivitySegment, numSegments),
			RPM:          0,
			TPM:          0,
		}
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	now := time.Now()

	// 时间边界对齐：将 endTime 向上对齐到下一个 segmentDuration 边界
	// 这样每次请求的分段边界都是固定的，不会因为 now 的微小变化而导致数据跳动
	// 例如：segmentDuration=6s，now=12:34:57，则 endTime=12:35:00（包含当前正在进行的段）
	endTimeUnix := now.Unix()
	segmentSeconds := int64(segmentDuration.Seconds())
	alignedEndUnix := ((endTimeUnix / segmentSeconds) + 1) * segmentSeconds
	endTime := time.Unix(alignedEndUnix, 0)
	startTime := endTime.Add(-time.Duration(numSegments) * segmentDuration)

	// 初始化分段数据
	segments := make([]ActivitySegment, numSegments)

	// 汇总统计
	var totalRequests, totalInputTokens, totalOutputTokens int64

	// 遍历所有 BaseURL 和 Key 的组合
	for _, baseURL := range baseURLs {
		for _, apiKey := range activeKeys {
			metricsKey := generateMetricsKey(baseURL, apiKey, channelIndex)
			metrics, exists := m.keyMetrics[metricsKey]
			if !exists {
				continue
			}

			// 遍历该 Key 的请求历史，放入对应分段
			for _, record := range metrics.requestHistory {
				// 检查是否在 [startTime, endTime) 范围内
				if record.Timestamp.Before(startTime) || !record.Timestamp.Before(endTime) {
					continue
				}

				// 计算属于哪个分段
				offset := int(record.Timestamp.Sub(startTime) / segmentDuration)
				if offset < 0 || offset >= numSegments {
					continue
				}

				seg := &segments[offset]
				seg.RequestCount++
				if record.Success {
					seg.SuccessCount++
				} else {
					seg.FailureCount++
				}
				seg.InputTokens += record.InputTokens
				seg.OutputTokens += record.OutputTokens

				// 累加汇总
				totalRequests++
				totalInputTokens += record.InputTokens
				totalOutputTokens += record.OutputTokens
			}
		}
	}

	// 计算 RPM 和 TPM（基于实际窗口时长）
	// TPM 只计算输出 tokens（包含思考），不包含输入 tokens 和缓存 tokens
	windowMinutes := float64(numSegments) * segmentDuration.Minutes()
	rpm := float64(totalRequests) / windowMinutes
	tpm := float64(totalOutputTokens) / windowMinutes

	return &ChannelRecentActivity{
		ChannelIndex: channelIndex,
		Segments:     segments,
		RPM:          rpm,
		TPM:          tpm,
	}
}
