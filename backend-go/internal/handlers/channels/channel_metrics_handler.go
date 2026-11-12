package channels

import (
	"github.com/insistanan/api-proxy/internal/handlers/common"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/insistanan/api-proxy/internal/infra/config"
	"github.com/insistanan/api-proxy/internal/orchestration/scheduler"
	"github.com/insistanan/api-proxy/internal/storage/metrics"
)

func upstreamsByKind(cfg config.Config, kind scheduler.ChannelKind) ([]config.UpstreamConfig, string) {
	switch kind {
	case scheduler.ChannelKindMessages:
		return cfg.Upstream, cfg.LoadBalance
	case scheduler.ChannelKindResponses:
		return cfg.ResponsesUpstream, cfg.ResponsesLoadBalance
	case scheduler.ChannelKindGemini:
		return cfg.GeminiUpstream, cfg.GeminiLoadBalance
	case scheduler.ChannelKindChat:
		return cfg.ChatUpstream, cfg.ChatLoadBalance
	case scheduler.ChannelKindImages:
		return cfg.ImagesUpstream, cfg.ImagesLoadBalance
	case scheduler.ChannelKindEmbeddings:
		return cfg.EmbeddingsUpstream, cfg.EmbeddingsLoadBalance
	case scheduler.ChannelKindDecisions:
		return cfg.DecisionsUpstream, cfg.DecisionsLoadBalance
	default:
		return nil, ""
	}
}

// GetChannelMetricsWithKind 获取指定一等公民渠道类型的指标
func GetChannelMetricsWithKind(metricsManager *metrics.MetricsManager, cfgManager *config.ConfigManager, kind scheduler.ChannelKind) gin.HandlerFunc {
	return func(c *gin.Context) {
		cfg := cfgManager.GetConfig()
		upstreams, _ := upstreamsByKind(cfg, kind)

		result := make([]gin.H, 0, len(upstreams))
		for i, upstream := range upstreams {
			// 使用多 URL 聚合方法获取渠道指标（支持 failover 多端点场景）
			resp := metricsManager.ToResponseMultiURL(i, upstream.GetAllBaseURLs(), upstream.APIKeys, 0, upstream.HistoricalAPIKeys)

			item := gin.H{
				"channelIndex":        i,
				"channelName":         upstream.Name,
				"requestCount":        resp.RequestCount,
				"successCount":        resp.SuccessCount,
				"failureCount":        resp.FailureCount,
				"successRate":         resp.SuccessRate,
				"errorRate":           resp.ErrorRate,
				"consecutiveFailures": resp.ConsecutiveFailures,
				"latency":             resp.Latency,
				"keyMetrics":          resp.KeyMetrics,  // 各 Key 的详细指标
				"timeWindows":         resp.TimeWindows, // 分时段统计 (15m, 1h, 6h, 24h)
			}

			if resp.LastSuccessAt != nil {
				item["lastSuccessAt"] = *resp.LastSuccessAt
			}
			if resp.LastFailureAt != nil {
				item["lastFailureAt"] = *resp.LastFailureAt
			}
			if resp.CircuitBrokenAt != nil {
				item["circuitBrokenAt"] = *resp.CircuitBrokenAt
			}

			result = append(result, item)
		}

		c.JSON(200, result)
	}
}

// ResumeChannelByKind 恢复指定一等公民渠道的熔断状态
func ResumeChannelByKind(sch *scheduler.ChannelScheduler, kind scheduler.ChannelKind) gin.HandlerFunc {
	return func(c *gin.Context) {
		idStr := c.Param("id")
		id, err := strconv.Atoi(idStr)
		if err != nil {
			c.JSON(400, gin.H{"error": "Invalid channel ID"})
			return
		}

		sch.ResetChannelMetrics(id, kind)

		c.JSON(200, gin.H{
			"success": true,
			"message": "渠道已恢复，熔断状态已重置（历史统计保留）",
		})
	}
}

// GetSchedulerStats 获取调度器统计信息
func GetSchedulerStats(sch *scheduler.ChannelScheduler) gin.HandlerFunc {
	return func(c *gin.Context) {
		kind := scheduler.ChannelKindMessages
		switch strings.ToLower(c.Query("type")) {
		case "responses":
			kind = scheduler.ChannelKindResponses
		case "gemini":
			kind = scheduler.ChannelKindGemini
		case "chat":
			kind = scheduler.ChannelKindChat
		case "images":
			kind = scheduler.ChannelKindImages
		case "embeddings":
			kind = scheduler.ChannelKindEmbeddings
		case "decisions":
			kind = scheduler.ChannelKindDecisions
		}

		metricsManager := sch.MetricsManager(kind)

		stats := gin.H{
			"multiChannelMode":    sch.IsMultiChannelMode(kind),
			"activeChannelCount":  sch.GetActiveChannelCount(kind),
			"traceAffinityCount":  sch.GetTraceAffinityManager().SizeForKind(string(kind)),
			"traceAffinityTTL":    sch.GetTraceAffinityManager().GetTTL().String(),
			"failureThreshold":    metricsManager.GetFailureThreshold() * 100,
			"windowSize":          metricsManager.GetWindowSize(),
			"circuitRecoveryTime": metricsManager.GetCircuitRecoveryTime().String(),
		}

		c.JSON(200, stats)
	}
}

// SetChannelPromotion 设置渠道促销期（通用版，通过 common.ConfigManager 接口调用）
// 促销期内的渠道会被优先选择，忽略 trace 亲和性
func SetChannelPromotion(cfgManager common.ConfigManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		idStr := c.Param("id")
		id, err := strconv.Atoi(idStr)
		if err != nil {
			c.JSON(400, gin.H{"error": "无效的渠道 ID"})
			return
		}

		var req struct {
			Duration int `json:"duration"` // 促销期时长（秒），0 表示不设时间限制
			Count    int `json:"count"`    // 促销请求次数，0 表示不设次数限制
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(400, gin.H{"error": "无效的请求参数"})
			return
		}

		if req.Duration <= 0 && req.Count <= 0 {
			if err := cfgManager.SetChannelPromotion(id, 0, 0); err != nil {
				c.JSON(400, gin.H{"error": err.Error()})
				return
			}
			c.JSON(200, gin.H{
				"success": true,
				"message": "渠道促销期已清除",
			})
			return
		}

		duration := time.Duration(req.Duration) * time.Second
		if err := cfgManager.SetChannelPromotion(id, duration, req.Count); err != nil {
			c.JSON(400, gin.H{"error": err.Error()})
			return
		}

		c.JSON(200, gin.H{
			"success":  true,
			"message":  "渠道促销期已设置",
			"duration": req.Duration,
			"count":    req.Count,
		})
	}
}

// SetResponsesChannelPromotion 设置 Responses 渠道促销期
func SetResponsesChannelPromotion(cfgManager ResponsesConfigManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		idStr := c.Param("id")
		id, err := strconv.Atoi(idStr)
		if err != nil {
			c.JSON(400, gin.H{"error": "无效的渠道 ID"})
			return
		}

		var req struct {
			Duration int `json:"duration"` // 促销期时长（秒），0 表示不设时间限制
			Count    int `json:"count"`    // 促销请求次数，0 表示不设次数限制
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(400, gin.H{"error": "无效的请求参数"})
			return
		}

		if req.Duration <= 0 && req.Count <= 0 {
			if err := cfgManager.SetResponsesChannelPromotion(id, 0, 0); err != nil {
				c.JSON(400, gin.H{"error": err.Error()})
				return
			}
			c.JSON(200, gin.H{
				"success": true,
				"message": "Responses 渠道促销期已清除",
			})
			return
		}

		duration := time.Duration(req.Duration) * time.Second
		if err := cfgManager.SetResponsesChannelPromotion(id, duration, req.Count); err != nil {
			c.JSON(400, gin.H{"error": err.Error()})
			return
		}

		c.JSON(200, gin.H{
			"success":  true,
			"message":  "Responses 渠道促销期已设置",
			"duration": req.Duration,
			"count":    req.Count,
		})
	}
}

// ResponsesConfigManager Responses 渠道促销期配置管理接口
type ResponsesConfigManager interface {
	SetResponsesChannelPromotion(index int, duration time.Duration, count int) error
}

// ChatConfigManager Chat 渠道促销期配置管理接口
type ChatConfigManager interface {
	SetChatChannelPromotion(index int, duration time.Duration, count int) error
}

// SetChatChannelPromotion 设置 Chat 渠道促销期
func SetChatChannelPromotion(cfgManager ChatConfigManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		idStr := c.Param("id")
		id, err := strconv.Atoi(idStr)
		if err != nil {
			c.JSON(400, gin.H{"error": "无效的渠道 ID"})
			return
		}

		var req struct {
			Duration int `json:"duration"`
			Count    int `json:"count"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(400, gin.H{"error": "无效的请求参数"})
			return
		}

		if req.Duration <= 0 && req.Count <= 0 {
			if err := cfgManager.SetChatChannelPromotion(id, 0, 0); err != nil {
				c.JSON(400, gin.H{"error": err.Error()})
				return
			}
			c.JSON(200, gin.H{"success": true, "message": "Chat 渠道促销期已清除"})
			return
		}

		duration := time.Duration(req.Duration) * time.Second
		if err := cfgManager.SetChatChannelPromotion(id, duration, req.Count); err != nil {
			c.JSON(400, gin.H{"error": err.Error()})
			return
		}

		c.JSON(200, gin.H{"success": true, "message": "Chat 渠道促销期已设置", "duration": req.Duration, "count": req.Count})
	}
}

// MetricsHistoryResponse 历史指标响应
type MetricsHistoryResponse struct {
	ChannelIndex int                        `json:"channelIndex"`
	ChannelName  string                     `json:"channelName"`
	DataPoints   []metrics.HistoryDataPoint `json:"dataPoints"`
}

// GetChannelMetricsHistoryByKind 获取指定一等公民渠道的指标历史数据
func GetChannelMetricsHistoryByKind(metricsManager *metrics.MetricsManager, cfgManager *config.ConfigManager, kind scheduler.ChannelKind) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 解析 duration 参数
		durationStr := c.DefaultQuery("duration", "24h")
		duration, err := time.ParseDuration(durationStr)
		if err != nil {
			c.JSON(400, gin.H{"error": "Invalid duration parameter"})
			return
		}

		// 限制最大查询范围为 24 小时
		if duration > 24*time.Hour {
			duration = 24 * time.Hour
		}

		// 解析或自动选择 interval
		intervalStr := c.Query("interval")
		var interval time.Duration
		if intervalStr != "" {
			interval, err = time.ParseDuration(intervalStr)
			if err != nil {
				c.JSON(400, gin.H{"error": "Invalid interval parameter"})
				return
			}
			// 限制 interval 最小值为 1 分钟，防止生成过多 bucket
			if interval < time.Minute {
				interval = time.Minute
			}
		} else {
			// 根据 duration 自动选择合适的聚合粒度
			// 目标：每个时间段约 60-100 个数据点，保持图表清晰
			// 1h = 60 points (1m interval)
			// 6h = 72 points (5m interval)
			// 24h = 96 points (15m interval)
			switch {
			case duration <= time.Hour:
				interval = time.Minute
			case duration <= 6*time.Hour:
				interval = 5 * time.Minute
			default:
				interval = 15 * time.Minute
			}
		}

		cfg := cfgManager.GetConfig()
		upstreams, _ := upstreamsByKind(cfg, kind)

		result := make([]MetricsHistoryResponse, 0, len(upstreams))
		for i, upstream := range upstreams {
			// 使用多 URL 聚合方法获取历史数据（支持 failover 多端点场景）
			dataPoints := metricsManager.GetHistoricalStatsMultiURL(upstream.GetAllBaseURLs(), upstream.APIKeys, i, duration, interval)

			result = append(result, MetricsHistoryResponse{
				ChannelIndex: i,
				ChannelName:  upstream.Name,
				DataPoints:   dataPoints,
			})
		}

		c.JSON(200, result)
	}
}

// ChannelKeyMetricsHistoryResponse Key 级别历史指标响应
type ChannelKeyMetricsHistoryResponse struct {
	ChannelIndex int                       `json:"channelIndex"`
	ChannelName  string                    `json:"channelName"`
	Keys         []KeyMetricsHistoryResult `json:"keys"`
}

// KeyMetricsHistoryResult 单个 Key 的历史数据
type KeyMetricsHistoryResult struct {
	KeyMask    string                        `json:"keyMask"`
	Color      string                        `json:"color"`
	DataPoints []metrics.KeyHistoryDataPoint `json:"dataPoints"`
}

// Key 颜色配置（与前端一致）
var keyColors = []string{
	"#3b82f6", // Blue - Primary
	"#f97316", // Orange - Backup 1
	"#10b981", // Emerald - Backup 2
	"#8b5cf6", // Violet - Fallback
	"#ec4899", // Pink - Canary
}

// GetChannelKeyMetricsHistoryByKind 获取指定一等公民渠道下各 Key 的历史数据
func GetChannelKeyMetricsHistoryByKind(metricsManager *metrics.MetricsManager, cfgManager *config.ConfigManager, kind scheduler.ChannelKind) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 解析 duration 参数
		durationStr := c.DefaultQuery("duration", "6h")

		var duration time.Duration
		var err error

		// 特殊处理 "today" 参数
		if durationStr == "today" {
			duration = metrics.CalculateTodayDuration()
			// 如果刚过零点，duration 可能非常小，设置最小值
			if duration < time.Minute {
				duration = time.Minute
			}
		} else {
			duration, err = time.ParseDuration(durationStr)
			if err != nil {
				c.JSON(400, gin.H{"error": "Invalid duration parameter. Use: 1h, 6h, 24h, or today"})
				return
			}
		}

		// 限制最大查询范围为 24 小时
		if duration > 24*time.Hour {
			duration = 24 * time.Hour
		}

		// 解析或自动选择 interval
		intervalStr := c.Query("interval")
		var interval time.Duration
		if intervalStr != "" {
			interval, err = time.ParseDuration(intervalStr)
			if err != nil {
				c.JSON(400, gin.H{"error": "Invalid interval parameter"})
				return
			}
			// 限制 interval 最小值为 1 分钟，防止生成过多 bucket
			if interval < time.Minute {
				interval = time.Minute
			}
		} else {
			// 根据 duration 自动选择合适的聚合粒度
			// 目标：每个时间段约 60-100 个数据点，保持图表清晰
			// 1h = 60 points (1m interval)
			// 6h = 72 points (5m interval)
			// 24h = 96 points (15m interval)
			switch {
			case duration <= time.Hour:
				interval = time.Minute
			case duration <= 6*time.Hour:
				interval = 5 * time.Minute
			default:
				interval = 15 * time.Minute
			}
		}

		// 解析 channel ID
		channelIDStr := c.Param("id")
		channelID, err := strconv.Atoi(channelIDStr)
		if err != nil {
			c.JSON(400, gin.H{"error": "Invalid channel ID"})
			return
		}

		cfg := cfgManager.GetConfig()
		upstreams, _ := upstreamsByKind(cfg, kind)

		// 检查 channel ID 是否有效
		if channelID < 0 || channelID >= len(upstreams) {
			c.JSON(400, gin.H{"error": "Channel not found"})
			return
		}

		upstream := upstreams[channelID]

		// 获取所有 Key 的使用信息并筛选（最多显示 10 个）
		const maxDisplayKeys = 10
		// 使用多 URL 聚合方法获取 Key 使用信息（支持 failover 多端点场景）
		allKeyInfos := metricsManager.GetChannelKeyUsageInfoMultiURL(upstream.GetAllBaseURLs(), upstream.APIKeys, channelID)
		displayKeys := metrics.SelectTopKeys(allKeyInfos, maxDisplayKeys)

		// 构建响应
		result := ChannelKeyMetricsHistoryResponse{
			ChannelIndex: channelID,
			ChannelName:  upstream.Name,
			Keys:         make([]KeyMetricsHistoryResult, 0, len(displayKeys)),
		}

		// 为筛选后的 Key 获取历史数据
		for i, keyInfo := range displayKeys {
			// 使用多 URL 聚合方法获取单个 Key 的历史数据（支持 failover 多端点场景）
			dataPoints := metricsManager.GetKeyHistoricalStatsMultiURL(upstream.GetAllBaseURLs(), keyInfo.APIKey, channelID, duration, interval)

			// 获取 Key 的颜色
			color := keyColors[i%len(keyColors)]

			// 获取 Key 的脱敏显示（只取前 8 个字符）
			keyMask := truncateKeyMask(keyInfo.KeyMask, 8)

			result.Keys = append(result.Keys, KeyMetricsHistoryResult{
				KeyMask:    keyMask,
				Color:      color,
				DataPoints: dataPoints,
			})
		}

		c.JSON(200, result)
	}
}

// truncateKeyMask 截取 keyMask 的前 N 个字符
func truncateKeyMask(keyMask string, maxLen int) string {
	if len(keyMask) <= maxLen {
		return keyMask
	}
	return keyMask[:maxLen]
}

// GetChannelDashboard 获取渠道仪表盘数据（合并 channels + metrics + stats）
// 通过 kind 参数直接指定渠道类型，不再依赖 ?type= 查询参数
func GetChannelDashboard(cfgManager *config.ConfigManager, sch *scheduler.ChannelScheduler, kind scheduler.ChannelKind) gin.HandlerFunc {
	return func(c *gin.Context) {
		cfg := cfgManager.GetConfig()
		upstreams, loadBalance := upstreamsByKind(cfg, kind)
		metricsManager := sch.MetricsManager(kind)

		// 1. 构建 channels 数据（统一使用 ChannelListToDTO）
		channels := config.ChannelListToDTO(upstreams)

		// 2. 构建 metrics 数据
		circuitSnapshots := sch.GetCircuitSnapshotForKind(kind)
		metricsResult := make([]gin.H, 0, len(upstreams))
		for i, upstream := range upstreams {
			if config.GetChannelStatus(&upstream) == config.ChannelStatusDeleted {
				continue
			}
			resp := metricsManager.ToResponseMultiURL(i, upstream.GetAllBaseURLs(), upstream.APIKeys, 0, upstream.HistoricalAPIKeys)

			item := gin.H{
				"channelIndex":        i,
				"channelName":         upstream.Name,
				"requestCount":        resp.RequestCount,
				"successCount":        resp.SuccessCount,
				"failureCount":        resp.FailureCount,
				"successRate":         resp.SuccessRate,
				"errorRate":           resp.ErrorRate,
				"consecutiveFailures": resp.ConsecutiveFailures,
				"latency":             resp.Latency,
				"keyMetrics":          resp.KeyMetrics,
				"timeWindows":         resp.TimeWindows,
			}

			// 渠道级熔断状态（internal/orchestration/circuit 运行时态；无记录时为 closed 零值）
			if snap, ok := circuitSnapshots[i]; ok {
				item["circuitState"] = snap.State
				item["circuitConsecutiveFailures"] = snap.ConsecutiveFailures
				if snap.LastOpenedAt != "" {
					item["circuitOpenedAt"] = snap.LastOpenedAt
				}
			} else {
				item["circuitState"] = "closed"
				item["circuitConsecutiveFailures"] = 0
			}

			if resp.LastSuccessAt != nil {
				item["lastSuccessAt"] = *resp.LastSuccessAt
			}
			if resp.LastFailureAt != nil {
				item["lastFailureAt"] = *resp.LastFailureAt
			}
			if resp.CircuitBrokenAt != nil {
				item["circuitBrokenAt"] = *resp.CircuitBrokenAt
			}

			metricsResult = append(metricsResult, item)
		}

		// 3. 构建 stats 数据
		stats := gin.H{
			"multiChannelMode":    sch.IsMultiChannelMode(kind),
			"activeChannelCount":  sch.GetActiveChannelCount(kind),
			"traceAffinityCount":  sch.GetTraceAffinityManager().SizeForKind(string(kind)),
			"traceAffinityTTL":    sch.GetTraceAffinityManager().GetTTL().String(),
			"failureThreshold":    metricsManager.GetFailureThreshold() * 100,
			"windowSize":          metricsManager.GetWindowSize(),
			"circuitRecoveryTime": metricsManager.GetCircuitRecoveryTime().String(),
		}

		// 4. 构建 recentActivity 数据（最近 15 分钟分段活跃度）
		recentActivity := make([]*metrics.ChannelRecentActivity, 0, len(upstreams))
		for i, upstream := range upstreams {
			if config.GetChannelStatus(&upstream) == config.ChannelStatusDeleted {
				continue
			}
			recentActivity = append(recentActivity, metricsManager.GetRecentActivityMultiURL(i, upstream.GetAllBaseURLs(), upstream.APIKeys))
		}

		// 返回合并数据
		c.JSON(200, gin.H{
			"channels":       channels,
			"loadBalance":    loadBalance,
			"metrics":        metricsResult,
			"stats":          stats,
			"recentActivity": recentActivity,
		})
	}
}

// GetGeminiChannelMetricsHistory 获取 Gemini 渠道指标历史数据（用于时间序列图表）
// 已收敛：与 GetChannelMetricsHistoryByKind 完全同构，直接委托 Gemini 渠道类型。
func GetGeminiChannelMetricsHistory(metricsManager *metrics.MetricsManager, cfgManager *config.ConfigManager) gin.HandlerFunc {
	return GetChannelMetricsHistoryByKind(metricsManager, cfgManager, scheduler.ChannelKindGemini)
}

// GetGeminiChannelKeyMetricsHistory 获取 Gemini 渠道下各 Key 的历史数据（用于 Key 趋势图表）
// GET /api/gemini/channels/:id/keys/metrics/history?duration=6h
func GetGeminiChannelKeyMetricsHistory(metricsManager *metrics.MetricsManager, cfgManager *config.ConfigManager) gin.HandlerFunc {
	return GetChannelKeyMetricsHistoryByKind(metricsManager, cfgManager, scheduler.ChannelKindGemini)
}

// GetGeminiChannelMetrics 获取 Gemini 渠道指标
// 已收敛：与 GetChannelMetricsWithKind 完全同构，直接委托 Gemini 渠道类型。
func GetGeminiChannelMetrics(metricsManager *metrics.MetricsManager, cfgManager *config.ConfigManager) gin.HandlerFunc {
	return GetChannelMetricsWithKind(metricsManager, cfgManager, scheduler.ChannelKindGemini)
}
