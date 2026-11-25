package usage

import (
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"insistanan/api-proxy/internal/storage/usageledger"
)

func Register(parent *gin.RouterGroup, store *usageledger.Store) {
	routes := parent.Group("/usage-journal", func(c *gin.Context) {
		if store == nil {
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": "用量账本不可用，请查看网关运行日志"})
			return
		}
		c.Next()
	})
	routes.GET("/status", func(c *gin.Context) {
		c.JSON(http.StatusOK, store.Status(time.Now()))
	})
	routes.POST("/enable", func(c *gin.Context) {
		var input struct {
			TimeZone string `json:"timeZone"`
		}
		if err := c.ShouldBindJSON(&input); err != nil || input.TimeZone == "" || input.TimeZone == "Local" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "请提供有效的统计时区"})
			return
		}
		zone, err := time.LoadLocation(input.TimeZone)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "无法识别统计时区"})
			return
		}
		if err := store.Enable(c.Request.Context(), zone, time.Now()); err != nil {
			reportError(c, "启用用量账本失败", err)
			return
		}
		c.JSON(http.StatusOK, store.Status(time.Now()))
	})
	routes.GET("/calendar", func(c *gin.Context) {
		year, err := strconv.Atoi(c.Query("year"))
		if err != nil || year < 1970 || year > 9998 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "无效的统计年份"})
			return
		}
		data, err := store.Calendar(c.Request.Context(), year, time.Now())
		if err != nil {
			reportError(c, "读取用量日历失败", err)
			return
		}
		c.JSON(http.StatusOK, data)
	})
	routes.GET("/days/:date", func(c *gin.Context) {
		date := c.Param("date")
		if parsed, err := time.Parse("2006-01-02", date); err != nil || parsed.Year() < 1970 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "无效的统计日期"})
			return
		}
		data, err := store.Daily(c.Request.Context(), date, time.Now())
		if err != nil {
			reportError(c, "读取每日用量失败", err)
			return
		}
		c.JSON(http.StatusOK, data)
	})
}

func reportError(c *gin.Context, message string, err error) {
	log.Printf("[UsageJournal-API] %s: %v", message, err)
	c.JSON(http.StatusInternalServerError, gin.H{"error": message})
}
