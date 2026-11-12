package metrics

// SyncToProfile 将现有指标同步到性能画像（用于初始化）
func (m *MetricsManager) SyncToProfile(pm *ProfileManager, baseURL string, apiKeys []string, model string, channelIdx int) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	profile := pm.GetOrCreateProfile(baseURL, apiKeys, model, channelIdx)

	var totalReq, totalSuccess int64
	var recentSuccessCount int

	for _, key := range apiKeys {
		metricsKey := generateMetricsKey(baseURL, key, channelIdx)
		if km, exists := m.keyMetrics[metricsKey]; exists {
			totalReq += km.RequestCount
			totalSuccess += km.SuccessCount
			for _, r := range km.recentResults {
				if r {
					recentSuccessCount++
				}
			}
		}
	}

	profile.mu.Lock()
	defer profile.mu.Unlock()

	if totalReq > 0 {
		profile.SuccessRate = float64(totalSuccess) / float64(totalReq) * 100
	} else {
		profile.SuccessRate = 100.0
	}

	// 从现有指标的滑动窗口计算最近成功率
	totalRecent := 0
	for _, key := range apiKeys {
		metricsKey := generateMetricsKey(baseURL, key, channelIdx)
		if km, exists := m.keyMetrics[metricsKey]; exists {
			totalRecent += len(km.recentResults)
		}
	}
	if totalRecent > 0 {
		profile.RecentSuccessRate = float64(recentSuccessCount) / float64(totalRecent) * 100
	} else {
		profile.RecentSuccessRate = profile.SuccessRate
	}
}
