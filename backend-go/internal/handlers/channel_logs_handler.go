package handlers

import (
	"net/http"
	"strconv"

	"github.com/BenedictKing/api-proxy/internal/config"
	"github.com/BenedictKing/api-proxy/internal/metrics"
	"github.com/BenedictKing/api-proxy/internal/scheduler"
	"github.com/gin-gonic/gin"
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

	cfg := cfgManager.GetConfig()
	var upstreams []config.UpstreamConfig
	switch kind {
	case scheduler.ChannelKindMessages:
		upstreams = cfg.Upstream
	case scheduler.ChannelKindResponses:
		upstreams = cfg.ResponsesUpstream
	case scheduler.ChannelKindGemini:
		upstreams = cfg.GeminiUpstream
	case scheduler.ChannelKindChat:
		upstreams = cfg.ChatUpstream
	case scheduler.ChannelKindImages:
		upstreams = cfg.ImagesUpstream
	default:
		return "", false
	}

	if channelID < 0 || channelID >= len(upstreams) {
		return "", false
	}
	return upstreams[channelID].Name, true
}
