package handlers

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/BenedictKing/api-proxy/internal/logger"
	"github.com/BenedictKing/api-proxy/internal/metrics"
	"github.com/gin-gonic/gin"
)

func GetRequestLogs(store *metrics.RequestLogStore) gin.HandlerFunc {
	return func(c *gin.Context) {
		limit := 50
		if rawLimit := c.Query("limit"); rawLimit != "" {
			parsed, err := strconv.Atoi(rawLimit)
			if err != nil || parsed < 1 {
				c.JSON(http.StatusBadRequest, gin.H{"error": "invalid limit"})
				return
			}
			limit = parsed
		}
		if limit > 50 {
			limit = 50
		}
		from, ok := parseRequestLogTime(c, "from")
		if !ok {
			return
		}
		to, ok := parseRequestLogTime(c, "to")
		if !ok {
			return
		}
		if !from.IsZero() && !to.IsZero() && from.After(to) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "开始时间不能晚于结束时间"})
			return
		}

		logs, err := store.List(metrics.RequestLogListOptions{
			APIType: c.Query("type"),
			Limit:   limit,
			From:    from.UnixMilli(),
			To:      to.UnixMilli(),
		})
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load request logs"})
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"logs":  logs,
			"limit": limit,
		})
	}
}

// parseRequestLogTime 解析 RFC3339 时间参数；缺零值表示不限制。
func parseRequestLogTime(c *gin.Context, name string) (time.Time, bool) {
	raw := strings.TrimSpace(c.Query(name))
	if raw == "" {
		return time.Time{}, true
	}
	value, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的时间参数 " + name})
		return time.Time{}, false
	}
	return value, true
}

// ListLogDates 返回三张日志表中存在记录的本地日历日（倒序），供清理入口选择日期。
func ListLogDates(store *logger.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		if store == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "日志存储未初始化"})
			return
		}
		dates, err := store.ListAvailableDates(c.Request.Context())
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load log dates"})
			return
		}
		if dates == nil {
			dates = []logger.AvailableDate{}
		}
		c.JSON(http.StatusOK, gin.H{"dates": dates})
	}
}

// purgeLogRequest 是清理请求体：scope=all 清空全部，scope=day 清理 date 指定的本地日历日。
type purgeLogRequest struct {
	Scope string `json:"scope"`
	Date  string `json:"date"`
}

// PurgeLogs 手动清理 logs.db（app_logs/traffic_logs/request_logs 三表），清理后 VACUUM 收缩文件。
func PurgeLogs(store *logger.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		if store == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "日志存储未初始化"})
			return
		}
		var req purgeLogRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
			return
		}
		options := logger.PurgeOptions{}
		switch strings.ToLower(strings.TrimSpace(req.Scope)) {
		case "all":
			options.All = true
		case "day":
			options.Date = strings.TrimSpace(req.Date)
			if options.Date == "" {
				c.JSON(http.StatusBadRequest, gin.H{"error": "date is required when scope is day"})
				return
			}
		default:
			c.JSON(http.StatusBadRequest, gin.H{"error": "scope must be all or day"})
			return
		}
		result, err := store.Purge(c.Request.Context(), options)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"success": true, "purged": result})
	}
}
