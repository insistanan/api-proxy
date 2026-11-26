package channels

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"insistanan/api-proxy/internal/api/common"
	"insistanan/api-proxy/internal/infra/config"
	"insistanan/api-proxy/internal/orchestration/scheduler"
)

func DuplicateChannel(cfgManager *config.ConfigManager, kind scheduler.ChannelKind) gin.HandlerFunc {
	return func(c *gin.Context) {
		channelID, err := strconv.Atoi(c.Param("id"))
		if err != nil || channelID < 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid channel id"})
			return
		}
		if err := cfgManager.DuplicateChannel(string(kind), channelID); err != nil {
			common.WriteSaveError(c, "复制渠道失败", err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"success": true})
	}
}

func TidyProblemChannels(cfgManager *config.ConfigManager, kind scheduler.ChannelKind) gin.HandlerFunc {
	return func(c *gin.Context) {
		if err := cfgManager.TidyProblemChannels(string(kind)); err != nil {
			common.WriteSaveError(c, "整理渠道失败", err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"success": true})
	}
}
