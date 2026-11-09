package handlers

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/insistanan/api-proxy/internal/logger"
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

func GetLogConversations(store *logger.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		limit, ok := logPageLimit(c, 50)
		if !ok {
			return
		}
		page, err := store.ListLogConversations(c.Request.Context(), logger.LogConversationListOptions{
			APIType: c.Query("type"), Cursor: c.Query("cursor"), Limit: limit,
		})
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
