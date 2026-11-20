package intent

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/insistanan/api-proxy/internal/api/common"
	"github.com/insistanan/api-proxy/internal/dialogue/routing/intent"
	"github.com/insistanan/api-proxy/internal/infra/config"
)

// RegisterIntentAPI 必须在绑定 AdminAuthMiddleware 的管理路由组上调用。
func RegisterIntentAPI(api *gin.RouterGroup, service *intent.Service) {
	api.GET("/intent/options", func(c *gin.Context) {
		options, err := service.Caller.ChannelOptions(c.Request.Context())
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		embeddings := []gin.H{}
		for _, up := range service.Cfg.GetConfig().EmbeddingsUpstream {
			if config.GetChannelStatus(&up) != config.ChannelStatusActive || len(up.APIKeys) == 0 {
				continue
			}
			poolName := up.PoolID
			for _, pool := range service.Cfg.GetChannelPools("embeddings") {
				if pool.ID == up.PoolID {
					poolName = pool.Name
				}
			}
			embeddings = append(embeddings, gin.H{"id": up.ID, "name": up.Name, "kind": "embeddings", "poolId": up.PoolID, "poolName": poolName, "defaultModel": up.DefaultModel, "models": config.ConfiguredModelOptions(&up)})
		}
		c.JSON(http.StatusOK, gin.H{"classifiers": options, "embeddings": embeddings, "tasks": config.IntentTasks()})
	})
	api.GET("/intent/local", func(c *gin.Context) { c.JSON(http.StatusOK, service.Local.Status()) })
	api.GET("/intent/local/presets", func(c *gin.Context) { c.JSON(http.StatusOK, service.Local.Presets(c.Request.Context())) })
	api.PUT("/intent/local/presets-url", func(c *gin.Context) {
		var req struct {
			URL *string `json:"url"`
		}
		if err := c.ShouldBindJSON(&req); err != nil || req.URL == nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "请提供清单地址；传空字符串恢复默认"})
			return
		}
		if err := service.Local.SetPresetsURL(*req.URL); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, service.Local.Presets(c.Request.Context()))
	})
	api.POST("/intent/local/install-preset", func(c *gin.Context) {
		var req struct {
			ID string `json:"id"`
		}
		if err := c.ShouldBindJSON(&req); err != nil || req.ID == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "请选择发布清单中的预设 ID"})
			return
		}
		if err := service.Local.InstallPreset(c.Request.Context(), req.ID); err != nil {
			common.WriteSaveError(c, "安装并启动向量预设失败", err)
			return
		}
		c.JSON(http.StatusOK, service.Local.Status())
	})
	api.POST("/intent/local/install", func(c *gin.Context) {
		var manifest intent.LocalManifest
		if err := c.ShouldBindJSON(&manifest); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "下载配置无效"})
			return
		}
		if err := service.Local.Install(c.Request.Context(), manifest); err != nil {
			common.WriteSaveError(c, "安装本地向量助手失败", err)
			return
		}
		c.JSON(http.StatusOK, service.Local.Status())
	})
	api.POST("/intent/local/start", func(c *gin.Context) {
		if err := service.Local.Start(c.Request.Context()); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, service.Local.Status())
	})
	api.POST("/intent/local/stop", func(c *gin.Context) {
		if err := service.Local.Stop(); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, service.Local.Status())
	})
	group := api.Group("/intent/:protocol", func(c *gin.Context) {
		if !config.SupportsIntentRouting(c.Param("protocol")) {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "该协议不支持意图匹配"})
			return
		}
		c.Next()
	})
	group.GET("", func(c *gin.Context) { c.JSON(http.StatusOK, service.Cfg.GetIntentRouting(c.Param("protocol"))) })
	group.PUT("", func(c *gin.Context) {
		var value config.IntentRoutingConfig
		if err := c.ShouldBindJSON(&value); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "意图配置无效"})
			return
		}
		if value.Enabled {
			if err := service.ValidateIntentVector(value); err != nil {
				if !respondVectorRequired(c, err) {
					c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
				}
				return
			}
			if err := service.ValidateIntentIndexes(intent.WithProtocol(c.Request.Context(), c.Param("protocol")), value); err != nil {
				c.JSON(http.StatusConflict, gin.H{"error": err.Error(), "code": "VECTOR_INDEX_REQUIRED"})
				return
			}
		}
		if err := service.Cfg.UpdateIntentRouting(c.Param("protocol"), value); err != nil {
			common.WriteSaveError(c, "保存意图配置失败", err)
			return
		}
		c.JSON(http.StatusOK, service.Cfg.GetIntentRouting(c.Param("protocol")))
	})
	group.POST("/preview", func(c *gin.Context) {
		var req struct {
			Config config.IntentRoutingConfig `json:"config"`
			Text   string                     `json:"text"`
			Model  string                     `json:"model"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "预览输入无效"})
			return
		}
		if len(req.Text) == 0 || len(req.Text) > 16000 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "请输入不超过 16000 字节的模拟文本"})
			return
		}
		pools := service.Cfg.GetChannelPools(c.Param("protocol"))
		if err := service.ValidateIntentVector(req.Config); err != nil {
			if !respondVectorRequired(c, err) {
				c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			}
			return
		}
		if err := config.ValidateIntentRouting(req.Config, pools); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		decision, err := service.Classify(intent.WithProtocol(c.Request.Context(), c.Param("protocol")), req.Config, intent.Input{Text: req.Text}, "intent-preview")
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": "辅助分类失败", "code": "INTENT_CLASSIFIER_FAILED", "decision": decision})
			return
		}
		if req.Model != "" {
			routing, err := previewModelRoute(service.Cfg, c.Param("protocol"), req.Config, decision, req.Model)
			if err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
				return
			}
			c.JSON(http.StatusOK, gin.H{"trace": decision.Trace, "task": decision.Task, "poolId": decision.PoolID, "routeId": decision.RouteID, "reason": decision.Reason, "score": decision.Score, "tier": decision.Tier, "durationMs": decision.DurationMS, "routing": routing})
			return
		}
		c.JSON(http.StatusOK, decision)
	})
	group.POST("/index-status", func(c *gin.Context) {
		var cfg config.IntentRoutingConfig
		if err := c.ShouldBindJSON(&cfg); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "索引配置无效"})
			return
		}
		if err := service.ValidateIntentIndexes(intent.WithProtocol(c.Request.Context(), c.Param("protocol")), cfg); err != nil {
			c.JSON(http.StatusConflict, gin.H{"error": err.Error(), "code": "VECTOR_INDEX_REQUIRED"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"ready": true})
	})
	group.GET("/events", func(c *gin.Context) { c.JSON(http.StatusOK, service.Events(c.Param("protocol"))) })
	group.POST("/index", func(c *gin.Context) {
		var cfg config.IntentRoutingConfig
		if err := c.ShouldBindJSON(&cfg); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "索引配置无效"})
			return
		}
		if err := service.ValidateVector(config.SemanticEmbeddingConfig{Source: cfg.EmbeddingSource, Channel: cfg.EmbeddingChannel, Model: cfg.EmbeddingModel}); err != nil {
			if !respondVectorRequired(c, err) {
				c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			}
			return
		}
		if err := config.ValidateIntentRouting(cfg, service.Cfg.GetChannelPools(c.Param("protocol"))); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if err := service.BuildIndex(intent.WithProtocol(c.Request.Context(), c.Param("protocol")), cfg); err != nil {
			if respondVectorRequired(c, err) {
				return
			}
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"success": true})
	})
	group.POST("/feedback", func(c *gin.Context) {
		var req struct {
			RequestID string `json:"requestId"`
			Good      *bool  `json:"good"`
		}
		if err := c.ShouldBindJSON(&req); err != nil || req.Good == nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "反馈参数无效"})
			return
		}
		if err := service.Feedback(c.Param("protocol"), req.RequestID, *req.Good); err != nil {
			common.WriteSaveError(c, "记录反馈失败", err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"success": true})
	})
}
