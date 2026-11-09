package handlers

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/insistanan/api-proxy/internal/config"
	"github.com/insistanan/api-proxy/internal/scheduler"
)

func DuplicateChannel(cfgManager *config.ConfigManager, kind scheduler.ChannelKind) gin.HandlerFunc {
	return func(c *gin.Context) {
		channelID, err := strconv.Atoi(c.Param("id"))
		if err != nil || channelID < 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid channel id"})
			return
		}
		if err := cfgManager.DuplicateChannel(string(kind), channelID); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"success": true})
	}
}

func TidyProblemChannels(cfgManager *config.ConfigManager, kind scheduler.ChannelKind) gin.HandlerFunc {
	return func(c *gin.Context) {
		if err := cfgManager.TidyProblemChannels(string(kind)); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"success": true})
	}
}
