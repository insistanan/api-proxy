package handlers

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/insistanan/api-proxy/internal/logger"
)

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
