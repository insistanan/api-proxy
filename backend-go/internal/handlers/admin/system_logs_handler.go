package admin

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/insistanan/api-proxy/internal/storage/logger"
)

func logPageLimit(c *gin.Context, defaultLimit int) (int, bool) {
	raw := c.Query("limit")
	if raw == "" {
		return defaultLimit, true
	}
	limit, err := strconv.Atoi(raw)
	if err != nil || limit < 1 || limit > 100 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "limit 必须在 1 到 100 之间"})
		return 0, false
	}
	return limit, true
}

func writeLogPageError(c *gin.Context, err error) {
	status := http.StatusInternalServerError
	if errors.Is(err, logger.ErrInvalidLogCursor) {
		status = http.StatusBadRequest
	} else if errors.Is(err, logger.ErrLogGroupNotFound) {
		status = http.StatusNotFound
	}
	c.JSON(status, gin.H{"error": err.Error()})
}

func logListOptions(c *gin.Context) (logger.LogConversationListOptions, bool) {
	var opts logger.LogConversationListOptions
	limit, ok := logPageLimit(c, 50)
	if !ok {
		return opts, false
	}
	search := strings.TrimSpace(c.Query("q"))
	if len([]rune(search)) > 200 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "搜索内容不能超过 200 个字符"})
		return opts, false
	}
	status := c.Query("status")
	switch status {
	case "", "completed", "failed", "processing", "retrying", "cancelled", "interrupted", "blocked":
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的调用状态"})
		return opts, false
	}
	from, ok := parseRequestLogTime(c, "from")
	if !ok {
		return opts, false
	}
	to, ok := parseRequestLogTime(c, "to")
	if !ok {
		return opts, false
	}
	if !from.IsZero() && !to.IsZero() && from.After(to) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "开始时间不能晚于结束时间"})
		return opts, false
	}
	var fromMilli, toMilli int64
	if !from.IsZero() {
		fromMilli = from.UnixMilli()
	}
	if !to.IsZero() {
		toMilli = to.UnixMilli()
	}
	return logger.LogConversationListOptions{
		APIType: c.Query("type"), Cursor: c.Query("cursor"), Limit: limit,
		Search: search, Status: status, From: fromMilli, To: toMilli,
	}, true
}

func GetLogConversations(store *logger.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		opts, ok := logListOptions(c)
		if !ok {
			return
		}
		page, err := store.ListLogConversations(c.Request.Context(), opts)
		if err != nil {
			writeLogPageError(c, err)
			return
		}
		c.JSON(http.StatusOK, page)
	}
}

func GetLogCalls(store *logger.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		opts, ok := logListOptions(c)
		if !ok {
			return
		}
		page, err := store.ListLogCalls(c.Request.Context(), opts)
		if err != nil {
			writeLogPageError(c, err)
			return
		}
		c.JSON(http.StatusOK, page)
	}
}

func GetLogCallAttempts(store *logger.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		requestID := strings.TrimSpace(c.Query("requestId"))
		if requestID == "" || len(requestID) > 2048 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "缺少或无效的调用标识"})
			return
		}
		page, err := store.GetLogCallAttempts(c.Request.Context(), requestID)
		if err != nil {
			writeLogPageError(c, err)
			return
		}
		c.JSON(http.StatusOK, page)
	}
}

func GetLogConversationRequests(store *logger.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		limit, ok := logPageLimit(c, 20)
		if !ok {
			return
		}
		page, err := store.ListLogConversationRequests(c.Request.Context(), logger.LogConversationRequestOptions{
			Key: c.Query("key"), Before: c.Query("before"), Anchor: c.Query("anchor"), Limit: limit,
		})
		if err != nil {
			writeLogPageError(c, err)
			return
		}
		c.JSON(http.StatusOK, page)
	}
}

func GetLogContextOverview(store *logger.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		key := strings.TrimSpace(c.Query("key"))
		if key == "" || len(key) > 2048 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "缺少或无效的会话标识"})
			return
		}
		overview, err := store.GetContextOverview(c.Request.Context(), key)
		if err != nil {
			writeLogPageError(c, err)
			return
		}
		c.JSON(http.StatusOK, overview)
	}
}

func GetRuntimeLogs(store *logger.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		limit, ok := logPageLimit(c, 100)
		if !ok {
			return
		}
		search := strings.TrimSpace(c.Query("q"))
		if len([]rune(search)) > 200 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "搜索内容不能超过 200 个字符"})
			return
		}
		page, err := store.ListRuntimeLogs(c.Request.Context(), logger.RuntimeLogOptions{Search: search, Before: c.Query("before"), Limit: limit})
		if err != nil {
			writeLogPageError(c, err)
			return
		}
		c.JSON(http.StatusOK, page)
	}
}

func GetSystemLogs(store *logger.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		limit := 100
		if rawLimit := c.Query("limit"); rawLimit != "" {
			parsed, err := strconv.Atoi(rawLimit)
			if err != nil || parsed < 1 {
				c.JSON(http.StatusBadRequest, gin.H{"error": "invalid limit"})
				return
			}
			limit = parsed
		}
		if limit > 200 {
			limit = 200
		}

		logs, err := store.ListSystemLogSummaries(c.Request.Context(), logger.SystemLogListOptions{
			APIType: c.Query("type"),
			Limit:   limit,
		})
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load system logs"})
			return
		}
		if logs == nil {
			logs = []logger.SystemLogSummary{}
		}

		c.JSON(http.StatusOK, gin.H{
			"logs":  logs,
			"limit": limit,
		})
	}
}

func GetSystemLog(store *logger.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		requestID := strings.TrimSpace(c.Param("requestId"))
		if requestID == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "missing requestId"})
			return
		}
		detail, err := store.GetSystemLogDetail(c.Request.Context(), requestID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load system log"})
			return
		}
		if detail == nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "system log not found"})
			return
		}
		c.JSON(http.StatusOK, detail)
	}
}
