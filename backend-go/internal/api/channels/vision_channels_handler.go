package channels

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/insistanan/api-proxy/internal/infra/config"
)

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
