package channels

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/insistanan/api-proxy/internal/infra/config"
	"github.com/insistanan/api-proxy/internal/orchestration/scheduler"
	"github.com/insistanan/api-proxy/internal/storage/metrics"
)

func GetChannelLogs(store *metrics.ChannelLogStore, cfgManager *config.ConfigManager, kind scheduler.ChannelKind) gin.HandlerFunc {
	return func(c *gin.Context) {
		channelID, err := strconv.Atoi(c.Param("id"))
		if err != nil || channelID < 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid channel id"})
			return
		}

		channelName, ok := getChannelName(cfgManager, kind, channelID)
		if !ok {
			c.JSON(http.StatusNotFound, gin.H{"error": "channel not found"})
			return
		}

		if store == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "channel log store is not initialized"})
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"channelIndex": channelID,
			"channelName":  channelName,
			"logs":         store.Get(channelID),
		})
	}
}

func getChannelName(cfgManager *config.ConfigManager, kind scheduler.ChannelKind, channelID int) (string, bool) {
	if cfgManager == nil {
		return "", false
	}

	upstreams, _, err := cfgManager.GetChannelConfig(string(kind))
	if err != nil {
		return "", false
	}

	if channelID < 0 || channelID >= len(upstreams) {
		return "", false
	}
	return upstreams[channelID].Name, true
}
