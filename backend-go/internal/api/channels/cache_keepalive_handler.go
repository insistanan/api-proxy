package channels

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"insistanan/api-proxy/internal/infra/config"
	"insistanan/api-proxy/internal/orchestration/cachekeepalive"
)

func GetCacheKeepalive(cfg *config.ConfigManager, manager *cachekeepalive.Manager) gin.HandlerFunc {
	return func(c *gin.Context) {
		if manager == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "缓存保活服务未初始化"})
			return
		}
		located, ok := cfg.FindChannelByID(c.Param("id"))
		if !ok || config.GetChannelStatus(&located.Upstream) == config.ChannelStatusDeleted {
			c.JSON(http.StatusNotFound, gin.H{"error": "渠道不存在"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"sessions": manager.Status(located.Upstream.ID), "capacityRejections": manager.CapacityRejections()})
	}
}

func StopCacheKeepalive(cfg *config.ConfigManager, manager *cachekeepalive.Manager) gin.HandlerFunc {
	return func(c *gin.Context) {
		if manager == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "缓存保活服务未初始化"})
			return
		}
		located, ok := cfg.FindChannelByID(c.Param("id"))
		if !ok || config.GetChannelStatus(&located.Upstream) == config.ChannelStatusDeleted {
			c.JSON(http.StatusNotFound, gin.H{"error": "渠道不存在"})
			return
		}
		var input struct {
			SessionID string `json:"sessionId"`
		}
		if err := c.ShouldBindJSON(&input); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "停止保活参数无效"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"stopped": manager.StopChannel(located.Upstream.ID, input.SessionID)})
	}
}
