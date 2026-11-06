package metrics

import (
	"sync"
	"time"
)

const (
	// maxRequestHistoryPerKey 每个 Key 在内存中保留的最大请求记录数
	// 超过此限制时，自动截断最早的记录，防止高频调用导致 OOM
	maxRequestHistoryPerKey = 100000
)

type RequestRecord struct {
	Timestamp                time.Time
	Success                  bool
	InputTokens              int64
	OutputTokens             int64
	CacheCreationInputTokens int64
	CacheReadInputTokens     int64
	Model                    string
}

// KeyMetrics 单个 Key 的指标（绑定到 BaseURL + Key 组合）
type KeyMetrics struct {
	MetricsKey          string     `json:"metricsKey"`          // hash(baseURL + apiKey + channelIndex)
	BaseURL             string     `json:"baseUrl"`             // 用于显示
	KeyMask             string     `json:"keyMask"`             // 脱敏的 key（用于显示）
	RequestCount        int64      `json:"requestCount"`        // 总请求数
	SuccessCount        int64      `json:"successCount"`        // 成功数
	FailureCount        int64      `json:"failureCount"`        // 失败数
	ConsecutiveFailures int64      `json:"consecutiveFailures"` // 连续失败数
	ActiveRequests      int64      `json:"activeRequests"`      // 进行中的请求数
	LastSuccessAt       *time.Time `json:"lastSuccessAt,omitempty"`
	LastFailureAt       *time.Time `json:"lastFailureAt,omitempty"`
	CircuitBrokenAt     *time.Time `json:"circuitBrokenAt,omitempty"` // 熔断开始时间
	// 滑动窗口记录（最近 N 次请求的结果）
	recentResults []bool // true=success, false=failure
	// 带时间戳的请求记录（用于分时段统计，保留24小时）
	requestHistory []RequestRecord
	// 进行中请求在 requestHistory 中的索引（用于“连接即计数”，结束后回写成功/失败与 token）
	pendingHistoryIdx map[uint64]int
}

// ChannelMetrics 渠道聚合指标（用于 API 返回，兼容旧结构）
type ChannelMetrics struct {
	ChannelIndex        int        `json:"channelIndex"`
	RequestCount        int64      `json:"requestCount"`
	SuccessCount        int64      `json:"successCount"`
	FailureCount        int64      `json:"failureCount"`
	ConsecutiveFailures int64      `json:"consecutiveFailures"`
	LastSuccessAt       *time.Time `json:"lastSuccessAt,omitempty"`
	LastFailureAt       *time.Time `json:"lastFailureAt,omitempty"`
	CircuitBrokenAt     *time.Time `json:"circuitBrokenAt,omitempty"`
	// 滑动窗口记录（兼容旧代码）
	recentResults []bool
	// 带时间戳的请求记录
	requestHistory []RequestRecord
}

// TimeWindowStats 分时段统计
type TimeWindowStats struct {
	RequestCount int64   `json:"requestCount"`
	SuccessCount int64   `json:"successCount"`
	FailureCount int64   `json:"failureCount"`
	SuccessRate  float64 `json:"successRate"`
	// Token 统计（按时间窗口聚合）
	InputTokens         int64 `json:"inputTokens,omitempty"`
	OutputTokens        int64 `json:"outputTokens,omitempty"`
	CacheCreationTokens int64 `json:"cacheCreationTokens,omitempty"`
	CacheReadTokens     int64 `json:"cacheReadTokens,omitempty"`
	// CacheHitRate 缓存命中率（Token口径），范围 0-100
	// 定义：cacheReadTokens / (cacheReadTokens + inputTokens) * 100
	CacheHitRate float64 `json:"cacheHitRate"`
}

// MetricsManager 指标管理器
type MetricsManager struct {
	mu                  sync.RWMutex
	keyMetrics          map[string]*KeyMetrics // key: hash(baseURL + apiKey + channelIndex)
	windowSize          int                    // 滑动窗口大小
	failureThreshold    float64                // 失败率阈值
	circuitRecoveryTime time.Duration          // 熔断恢复时间
	stopCh              chan struct{}          // 用于停止清理 goroutine
	doneCh              chan struct{}
	stopOnce            sync.Once
	nextRequestID       uint64 // 单进程递增请求ID（用于 pendingHistoryIdx）

	// 持久化存储（可选）
	store   PersistenceStore
	apiType string // "messages"、"responses"、"gemini" 或 "chat"
}
