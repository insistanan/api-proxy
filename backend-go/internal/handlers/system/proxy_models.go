package system

import (
	"github.com/gin-gonic/gin"
	"github.com/insistanan/api-proxy/internal/handlers/modelapi"
	"github.com/insistanan/api-proxy/internal/infra/config"
)

// ProxyModelsHandler 返回本代理自身暴露的模型列表（即 /v1/models 的内容），
// 供客户端配置页（DSH/OpenCode/ClaudeCode/PiAgent）按协议分组导入模型。
//
// 入口筛选与公开目录一致，响应固定为 OpenAI 格式；鉴权由 /api 管理路由组负责。
func ProxyModelsHandler(cfgManager *config.ConfigManager) gin.HandlerFunc {
	return modelapi.AdminListHandler(cfgManager)
}
