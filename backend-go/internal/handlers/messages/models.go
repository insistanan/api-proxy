// Package messages 提供 Claude Messages API 的处理器
package messages

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/insistanan/api-proxy/internal/config"
	"github.com/insistanan/api-proxy/internal/middleware"
	"github.com/insistanan/api-proxy/internal/modelcatalog"
	"github.com/insistanan/api-proxy/internal/scheduler"
)

// ModelsHandler 处理 /v1/models 请求。
// 对外暴露稳定家族别名、各协议分组捕获规则，以及已启用的 free/ 免费模型别名。
// 渠道级上游模型与 Chat 路由别名不再出现在公开列表中。
func ModelsHandler(envCfg *config.EnvConfig, cfgManager *config.ConfigManager, channelScheduler *scheduler.ChannelScheduler) gin.HandlerFunc {
	return func(c *gin.Context) {
		middleware.ProxyAuthMiddleware(envCfg)(c)
		if c.IsAborted() {
			return
		}

		response := modelcatalog.OpenAIModels(c.Request.Context(), cfgManager)
		if len(response.Data) == 0 {
			c.JSON(http.StatusNotFound, gin.H{
				"error": gin.H{
					"message": "models endpoint not available",
					"type":    "not_found_error",
				},
			})
			return
		}

		c.JSON(http.StatusOK, response)
	}
}

// ModelsDetailHandler 处理 /v1/models/:model 请求。
func ModelsDetailHandler(envCfg *config.EnvConfig, cfgManager *config.ConfigManager, channelScheduler *scheduler.ChannelScheduler) gin.HandlerFunc {
	return func(c *gin.Context) {
		middleware.ProxyAuthMiddleware(envCfg)(c)
		if c.IsAborted() {
			return
		}

		modelID := c.Param("model")
		if modelID == "" {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": gin.H{
					"message": "model id is required",
					"type":    "invalid_request_error",
				},
			})
			return
		}

		if model, ok := modelcatalog.ModelDetail(c.Request.Context(), cfgManager, modelID); ok {
			c.JSON(http.StatusOK, model)
			return
		}

		c.JSON(http.StatusNotFound, gin.H{
			"error": gin.H{
				"message": "model not found",
				"type":    "not_found_error",
			},
		})
	}
}
