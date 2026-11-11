// Package modelapi 将统一分组目录转换为 OpenAI、Anthropic 和 Gemini 的查询响应。
// 查询只读取配置，不触发上游模型发现或推理请求。
package modelapi

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/insistanan/api-proxy/internal/config"
	"github.com/insistanan/api-proxy/internal/middleware"
	"github.com/insistanan/api-proxy/internal/modelcatalog"
)

// RegisterRoutes 集中注册目录端点，详情使用通配路径以保留 free/vendor/model 等完整名称。
func RegisterRoutes(r *gin.Engine, env *config.EnvConfig, cfg *config.ConfigManager) {
	auth := middleware.ProxyAuthMiddleware(env)
	r.GET("/v1/models", auth, publicHandler(cfg, false, false))
	r.GET("/v1/models/*model", auth, publicHandler(cfg, false, true))
	r.GET("/v1beta/models", auth, publicHandler(cfg, true, false))
	r.GET("/v1beta/models/*model", auth, publicHandler(cfg, true, true))
}

// AdminListHandler 复用相同的入口筛选，管理组负责鉴权；响应始终采用 OpenAI 目录格式。
func AdminListHandler(cfg *config.ConfigManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		kind, err := requestKind(c)
		if err != nil {
			writeError(c, formatOpenAI, http.StatusBadRequest, err.Error())
			return
		}
		models, err := modelcatalog.ModelsForKind(cfg, kind)
		if err != nil {
			writeError(c, formatOpenAI, http.StatusBadRequest, err.Error())
			return
		}
		c.JSON(http.StatusOK, models)
	}
}

func publicHandler(cfg *config.ConfigManager, nativeGemini, detail bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		options, err := requestOptions(c, nativeGemini)
		if err != nil {
			writeError(c, options.format, http.StatusBadRequest, err.Error())
			return
		}
		models, err := modelcatalog.ModelsForKind(cfg, options.kind)
		if err != nil {
			writeError(c, options.format, http.StatusBadRequest, err.Error())
			return
		}
		if detail {
			id := strings.TrimPrefix(c.Param("model"), "/")
			if id == "" {
				writeError(c, options.format, http.StatusBadRequest, "模型名称不能为空")
				return
			}
			for _, model := range models.Data {
				if model.ID == id {
					c.JSON(http.StatusOK, formatModel(model, options.format))
					return
				}
			}
			// 列表只公布 free 分组；显式模型仍可按完整 ID 查询详情，兼容手动配置的客户端。
			if strings.HasPrefix(id, "free/") && cfg != nil {
				kind := options.kind
				if kind == "" {
					kind = "chat"
				}
				if err := cfg.ValidateFreeRequest(kind, id, nil); err == nil {
					owner := "pool:" + kind
					if options.kind == "" {
						owner = "pool:chat,gemini,messages,responses"
					}
					c.JSON(http.StatusOK, formatModel(modelcatalog.ModelEntry{ID: id, Object: "model", OwnedBy: owner}, options.format))
					return
				}
			}
			writeError(c, options.format, http.StatusNotFound, "该入口协议的公开目录中没有此模型")
			return
		}
		response, err := listResponse(c, models, options.format)
		if err != nil {
			writeError(c, options.format, http.StatusBadRequest, err.Error())
			return
		}
		c.JSON(http.StatusOK, response)
	}
}

func writeError(c *gin.Context, format string, status int, message string) {
	if format == formatGemini {
		name := "INVALID_ARGUMENT"
		if status == http.StatusNotFound {
			name = "NOT_FOUND"
		}
		c.JSON(status, gin.H{"error": gin.H{"code": status, "message": message, "status": name}})
		return
	}
	kind := "invalid_request_error"
	if status == http.StatusNotFound {
		kind = "not_found_error"
	}
	errorBody := gin.H{"error": gin.H{"type": kind, "message": message}}
	if format == formatAnthropic {
		errorBody["type"] = "error"
	}
	c.JSON(status, errorBody)
}
