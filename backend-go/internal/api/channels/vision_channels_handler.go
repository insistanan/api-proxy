package channels

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/insistanan/api-proxy/internal/infra/config"
)

// GetChannelDeletionImpact 返回删除确认所需的跨协议图片理解引用。
func GetChannelDeletionImpact(cfgManager *config.ConfigManager, kind string, pool bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		var index *int
		poolID := ""
		if pool {
			poolID = strings.TrimSpace(c.Param("id"))
		} else {
			parsed, err := strconv.Atoi(c.Param("id"))
			if err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": "无效的渠道索引"})
				return
			}
			index = &parsed
		}
		impact, err := cfgManager.GetChannelDeletionImpact(kind, index, poolID)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, impact)
	}
}

// GetVisionChannels 返回四个会话协议共享的公用纯图片理解池。
// 每条记录带 kind，前端据此把编辑、状态与删除操作路由到渠道自身协议。
func GetVisionChannels(cfgManager *config.ConfigManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		channels := cfgManager.PublicVisionChannels()
		payload := make([]map[string]interface{}, 0, len(channels))
		for _, channel := range channels {
			dto := config.ChannelToDTO(&channel.Upstream, channel.Index)
			dto["kind"] = channel.Kind
			payload = append(payload, dto)
		}
		c.JSON(http.StatusOK, gin.H{"channels": payload})
	}
}
