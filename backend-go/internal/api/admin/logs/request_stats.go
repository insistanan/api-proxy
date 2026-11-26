package logs

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"insistanan/api-proxy/internal/storage/logger"
)

var statsIntervals = []time.Duration{
	time.Minute, 5 * time.Minute, 15 * time.Minute, 30 * time.Minute,
	time.Hour, 2 * time.Hour, 6 * time.Hour, 12 * time.Hour, 24 * time.Hour, 7 * 24 * time.Hour,
}

func automaticStatsInterval(from, to time.Time) time.Duration {
	duration := to.Sub(from)
	for _, interval := range statsIntervals {
		if duration/interval+1 <= 120 {
			return interval
		}
	}
	return 7 * 24 * time.Hour
}

func parseStatsRange(c *gin.Context) (time.Time, time.Time, string, bool) {
	now := time.Now()
	fromRaw, toRaw := strings.TrimSpace(c.Query("from")), strings.TrimSpace(c.Query("to"))
	if fromRaw != "" || toRaw != "" {
		from, ok := parseRequestLogTime(c, "from")
		if !ok {
			return time.Time{}, time.Time{}, "", false
		}
		to, ok := parseRequestLogTime(c, "to")
		if !ok {
			return time.Time{}, time.Time{}, "", false
		}
		if from.IsZero() || to.IsZero() || !from.Before(to) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "自定义统计需要有效的开始和结束时间"})
			return time.Time{}, time.Time{}, "", false
		}
		if to.Sub(from) > 366*24*time.Hour {
			c.JSON(http.StatusBadRequest, gin.H{"error": "单次统计范围不能超过 366 天"})
			return time.Time{}, time.Time{}, "", false
		}
		return from, to, "custom", true
	}

	durationText := c.DefaultQuery("duration", "24h")
	var from time.Time
	switch durationText {
	case "today":
		year, month, day := now.In(time.Local).Date()
		from = time.Date(year, month, day, 0, 0, 0, 0, time.Local)
	case "7d":
		from = now.Add(-7 * 24 * time.Hour)
	case "30d":
		from = now.Add(-30 * 24 * time.Hour)
	default:
		duration, err := time.ParseDuration(durationText)
		if err != nil || duration < time.Minute || duration > 366*24*time.Hour {
			c.JSON(http.StatusBadRequest, gin.H{"error": "无效的时间范围，支持 1h、6h、24h、7d、30d、today 或自定义时间"})
			return time.Time{}, time.Time{}, "", false
		}
		from = now.Add(-duration)
	}
	return from, now, durationText, true
}

// GetRequestStats 返回持久化调用日志的真实统计；统计对象是一行一次上游尝试。
// GET /api/request-stats?duration=24h&type=responses&compare=true
func GetRequestStats(store *logger.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		compare, ok := parseOptionalBool(c, "compare")
		if !ok || !validateLogQuery(c, true) {
			return
		}
		from, to, durationText, ok := parseStatsRange(c)
		if !ok {
			return
		}
		interval := automaticStatsInterval(from, to)
		if raw := strings.TrimSpace(c.Query("interval")); raw != "" {
			parsed, err := time.ParseDuration(raw)
			if err != nil || parsed < time.Minute {
				c.JSON(http.StatusBadRequest, gin.H{"error": "无效的聚合粒度"})
				return
			}
			interval = parsed
		}
		if count := to.Sub(from)/interval + 1; count > 200 {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("当前范围将生成 %d 个数据点，请增大聚合粒度", count)})
			return
		}
		filter := logger.RequestStatsFilter{
			From: from.UnixMilli(), To: to.UnixMilli(), APIType: c.Query("type"),
			Channel: c.Query("channel"), Model: c.Query("model"), Status: c.Query("status"),
			ErrorType: c.Query("errorType"), Search: c.Query("q"),
			Upstream: c.Query("upstream"), KeyMask: c.Query("keyMask"), DimensionSort: c.Query("dimensionSort"),
		}
		var previous *logger.RequestStatsFilter
		if compare == nil || *compare {
			duration := to.Sub(from)
			previousTo := from.Add(-time.Millisecond)
			previousFrom := previousTo.Add(-duration).Add(time.Millisecond)
			value := filter
			value.From, value.To = previousFrom.UnixMilli(), previousTo.UnixMilli()
			previous = &value
		}
		result, err := store.QueryRequestStats(c.Request.Context(), filter, interval, previous)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "读取请求统计失败"})
			return
		}
		result.Summary.Duration = durationText
		if result.PreviousSummary != nil {
			result.PreviousSummary.Duration = "previous"
		}
		c.JSON(http.StatusOK, result)
	}
}
