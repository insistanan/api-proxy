package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/insistanan/api-proxy/internal/config"
	"github.com/insistanan/api-proxy/internal/modelcatalog"
)

// ProxyModelsHandler 返回本代理自身暴露的模型列表（即 /v1/models 的内容），
// 供客户端配置页（DSH/OpenCode/ClaudeCode/PiAgent）按协议分组导入模型。
//
// 与 /v1/models 的差异：走 /api 组的 Web 鉴权而非代理鉴权，
// 前端管理界面无需持有 PROXY_ACCESS_KEY 即可获取模型列表。
func ProxyModelsHandler(cfgManager *config.ConfigManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		response := modelcatalog.OpenAIModels(c.Request.Context(), cfgManager)
		c.JSON(http.StatusOK, response)
	}
}
