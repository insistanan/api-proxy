package metrics

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"time"

	"github.com/insistanan/api-proxy/internal/utils"
)

// NewMetricsManager 创建指标管理器
func NewMetricsManager() *MetricsManager {
	m := &MetricsManager{
		keyMetrics:          make(map[string]*KeyMetrics),
		windowSize:          10,               // 默认基于最近 10 次请求计算失败率
		failureThreshold:    0.5,              // 默认 50% 失败率阈值
		circuitRecoveryTime: 15 * time.Minute, // 默认 15 分钟自动恢复
		stopCh:              make(chan struct{}),
		doneCh:              make(chan struct{}),
	}
	// 启动后台熔断恢复任务
	go m.cleanupCircuitBreakers()
	return m
}

// NewMetricsManagerWithConfig 创建带配置的指标管理器
func NewMetricsManagerWithConfig(windowSize int, failureThreshold float64) *MetricsManager {
	if windowSize < 3 {
		windowSize = 3 // 最小 3
	}
	if failureThreshold <= 0 || failureThreshold > 1 {
		failureThreshold = 0.5
	}
	m := &MetricsManager{
		keyMetrics:          make(map[string]*KeyMetrics),
		windowSize:          windowSize,
		failureThreshold:    failureThreshold,
		circuitRecoveryTime: 15 * time.Minute,
		stopCh:              make(chan struct{}),
		doneCh:              make(chan struct{}),
	}
	// 启动后台熔断恢复任务
	go m.cleanupCircuitBreakers()
	return m
}

// NewMetricsManagerWithPersistence 创建带持久化的指标管理器
func NewMetricsManagerWithPersistence(windowSize int, failureThreshold float64, store PersistenceStore, apiType string) *MetricsManager {
	if windowSize < 3 {
		windowSize = 3
	}
	if failureThreshold <= 0 || failureThreshold > 1 {
		failureThreshold = 0.5
	}
	m := &MetricsManager{
		keyMetrics:          make(map[string]*KeyMetrics),
		windowSize:          windowSize,
		failureThreshold:    failureThreshold,
		circuitRecoveryTime: 15 * time.Minute,
		stopCh:              make(chan struct{}),
		doneCh:              make(chan struct{}),
		store:               store,
		apiType:             apiType,
	}

	// 从持久化存储加载历史数据
	if store != nil {
		if err := m.loadFromStore(); err != nil {
			log.Printf("[Metrics-Load] 警告: [%s] 加载历史指标数据失败: %v", apiType, err)
		}
	}

	// 启动后台熔断恢复任务
	go m.cleanupCircuitBreakers()
	return m
}

// loadFromStore 从持久化存储加载数据
func (m *MetricsManager) loadFromStore() error {
	if m.store == nil {
		return nil
	}

	// 加载最近 24 小时的数据
	since := time.Now().Add(-24 * time.Hour)
	records, err := m.store.LoadRecords(since, m.apiType)
	if err != nil {
		return err
	}

	if len(records) == 0 {
		return nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// 重建内存中的 KeyMetrics
	for _, r := range records {
		metrics := m.getOrCreateKeyLocked(r.BaseURL, r.MetricsKey, r.KeyMask)

		// 重建请求历史
		metrics.requestHistory = append(metrics.requestHistory, RequestRecord{
			Timestamp:                r.Timestamp,
			Success:                  r.Success,
			InputTokens:              r.InputTokens,
			OutputTokens:             r.OutputTokens,
			CacheCreationInputTokens: r.CacheCreationTokens,
			CacheReadInputTokens:     r.CacheReadTokens,
			Model:                    r.Model,
		})

		// 更新聚合计数
		metrics.RequestCount++
		if r.Success {
			metrics.SuccessCount++
			if metrics.LastSuccessAt == nil || r.Timestamp.After(*metrics.LastSuccessAt) {
				t := r.Timestamp
				metrics.LastSuccessAt = &t
			}
		} else {
			metrics.FailureCount++
			if metrics.LastFailureAt == nil || r.Timestamp.After(*metrics.LastFailureAt) {
				t := r.Timestamp
				metrics.LastFailureAt = &t
			}
		}
	}

	// 重建滑动窗口（只从最近 15 分钟的记录中取最近 windowSize 条）
	// 避免历史失败记录导致渠道长期处于不健康状态
	windowCutoff := time.Now().Add(-15 * time.Minute)
	for _, metrics := range m.keyMetrics {
		metrics.recentResults = make([]bool, 0, m.windowSize)
		// 从历史记录中筛选最近 15 分钟内的记录
		var recentRecords []bool
		for _, record := range metrics.requestHistory {
			if record.Timestamp.After(windowCutoff) {
				recentRecords = append(recentRecords, record.Success)
			}
		}
		// 取最近 windowSize 条
		n := len(recentRecords)
		start := 0
		if n > m.windowSize {
			start = n - m.windowSize
		}
		for i := start; i < n; i++ {
			metrics.recentResults = append(metrics.recentResults, recentRecords[i])
		}
	}

	return nil
}

// getOrCreateKeyLocked 获取或创建 Key 指标（用于加载时，已知 metricsKey 和 keyMask）
func (m *MetricsManager) getOrCreateKeyLocked(baseURL, metricsKey, keyMask string) *KeyMetrics {
	if metrics, exists := m.keyMetrics[metricsKey]; exists {
		return metrics
	}
	metrics := &KeyMetrics{
		MetricsKey:        metricsKey,
		BaseURL:           baseURL,
		KeyMask:           keyMask,
		recentResults:     make([]bool, 0, m.windowSize),
		pendingHistoryIdx: make(map[uint64]int),
	}
	m.keyMetrics[metricsKey] = metrics
	return metrics
}

// generateMetricsKey 生成指标键 hash(baseURL + apiKey + channelIndex)（内部使用）
func generateMetricsKey(baseURL, apiKey string, channelIndex int) string {
	h := sha256.New()
	if channelIndex == 0 {
		// 索引 0 沿用旧键格式，保留升级前 SQLite 中的历史指标；其他索引仍独立隔离。
		h.Write([]byte(baseURL + "|" + apiKey))
	} else {
		h.Write([]byte(fmt.Sprintf("%s|%s|%d", baseURL, apiKey, channelIndex)))
	}
	return hex.EncodeToString(h.Sum(nil))[:16] // 取前16位作为键
}

// getOrCreateKey 获取或创建 Key 指标
func (m *MetricsManager) getOrCreateKey(baseURL, apiKey string, channelIndex int) *KeyMetrics {
	metricsKey := generateMetricsKey(baseURL, apiKey, channelIndex)
	if metrics, exists := m.keyMetrics[metricsKey]; exists {
		return metrics
	}
	metrics := &KeyMetrics{
		MetricsKey:        metricsKey,
		BaseURL:           baseURL,
		KeyMask:           utils.MaskAPIKey(apiKey),
		recentResults:     make([]bool, 0, m.windowSize),
		pendingHistoryIdx: make(map[uint64]int),
	}
	m.keyMetrics[metricsKey] = metrics
	return metrics
}
