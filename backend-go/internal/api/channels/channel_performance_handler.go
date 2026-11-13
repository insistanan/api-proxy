package channels

import (
	"sort"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/insistanan/api-proxy/internal/storage/metrics"
)

// channelPerformanceReport 是性能观测接口的展示数据，不参与渠道调度。
type channelPerformanceReport struct {
	ChannelIndex      int     `json:"channelIndex"`
	BaseURL           string  `json:"baseUrl"`
	Model             string  `json:"model"`
	HealthScore       float64 `json:"healthScore"`
	PerformanceTier   string  `json:"performanceTier"`
	ActiveRequests    int64   `json:"activeRequests"`
	RecentTPS         float64 `json:"recentTps"`
	Peak1MinTPS       float64 `json:"peak1MinTps"`
	AvgTTFB           float64 `json:"avgTtfb"`
	AvgResponseTime   float64 `json:"avgResponseTime"`
	P50Latency        float64 `json:"p50Latency"`
	P95Latency        float64 `json:"p95Latency"`
	P99Latency        float64 `json:"p99Latency"`
	AvgTokenSpeed     float64 `json:"avgTokenSpeed"`
	SuccessRate       float64 `json:"successRate"`
	RecentSuccessRate float64 `json:"recentSuccessRate"`
	LastScoreUpdate   string  `json:"lastScoreUpdate"`
}

// GetChannelPerformanceReport 读取不可变画像快照并投影为管理接口响应。
// 保持既有字段、空列表语义和健康评分排序；不持有或调用选渠实现。
func GetChannelPerformanceReport(profileManager *metrics.ProfileManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		var reports []channelPerformanceReport
		if profileManager != nil {
			profiles := profileManager.GetAllProfileSnapshots()
			reports = make([]channelPerformanceReport, len(profiles))
			for i, p := range profiles {
				reports[i] = channelPerformanceReport{
					ChannelIndex: p.ChannelIdx, BaseURL: p.BaseURL, Model: p.Model,
					HealthScore: p.HealthScore, PerformanceTier: p.PerformanceTier,
					ActiveRequests: p.ActiveRequests, RecentTPS: p.RecentTPS, Peak1MinTPS: p.Peak1MinTPS,
					AvgTTFB: p.AvgTTFB, AvgResponseTime: p.AvgResponseTime,
					P50Latency: p.P50Latency, P95Latency: p.P95Latency, P99Latency: p.P99Latency,
					AvgTokenSpeed: p.AvgTokenSpeed, SuccessRate: p.SuccessRate, RecentSuccessRate: p.RecentSuccessRate,
					LastScoreUpdate: p.LastScoreUpdate.Format(time.RFC3339),
				}
			}
			sort.Slice(reports, func(i, j int) bool {
				return reports[i].HealthScore > reports[j].HealthScore
			})
		}
		c.JSON(200, gin.H{"success": true, "data": reports})
	}
}
