package intent

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"insistanan/api-proxy/internal/api/common"
	"insistanan/api-proxy/internal/dialogue/routing/intent"
	"insistanan/api-proxy/internal/infra/config"
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
		groups := map[string][]gin.H{}
		for _, kind := range []string{"messages", "responses", "chat", "gemini"} {
			counts, err := service.ActivePoolChannels(kind)
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
				return
			}
			for _, pool := range service.Cfg.GetChannelPools(kind) {
				groups[kind] = append(groups[kind], gin.H{"id": pool.ID, "activeChannels": counts[pool.ID]})
			}
		}
		c.JSON(http.StatusOK, gin.H{"classifiers": options, "embeddings": embeddings, "tasks": config.IntentTasks(), "groups": groups})
	})
	api.GET("/intent/local", func(c *gin.Context) { c.JSON(http.StatusOK, service.LocalStatus()) })
	cacheHandler := func(cleanup bool) gin.HandlerFunc {
		return func(c *gin.Context) {
			result, err := service.Local.LocalCache(c.Request.Context(), cleanup)
			if err != nil {
				c.JSON(http.StatusConflict, gin.H{"error": err.Error(), "committed": result.RemovedEntries > 0, "cache": result})
				return
			}
			c.JSON(http.StatusOK, result)
		}
	}
	api.GET("/intent/local/cache", cacheHandler(false))
	api.DELETE("/intent/local/cache", cacheHandler(true))
	api.GET("/intent/preparation", func(c *gin.Context) { c.JSON(http.StatusOK, service.PreparationStatus()) })
	api.DELETE("/intent/preparation", func(c *gin.Context) {
		task, err := service.CancelPreparation(c.Query("taskId"))
		if err != nil {
			c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusAccepted, task)
	})
	api.GET("/intent/local/presets", func(c *gin.Context) { c.JSON(http.StatusOK, service.Local.Presets(c.Request.Context())) })
	api.POST("/intent/local/import", func(c *gin.Context) {
		if service.PreparationStatus().Running {
			c.JSON(http.StatusConflict, gin.H{"error": "请等待或取消当前准备任务后导入"})
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Minute)
		defer cancel()
		// 原始二进制流直写校验缓存，不解析用户文件名、不将大文件读入内存。
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, (1<<30)+1)
		stopClose := context.AfterFunc(ctx, func() { _ = c.Request.Body.Close() })
		defer stopClose()
		if err := service.Local.ImportResource(ctx, c.Query("presetId"), c.Query("resource"), c.Request.Body); err != nil {
			common.WriteSaveError(c, "导入离线资源失败", err)
			return
		}
		c.JSON(http.StatusOK, service.LocalStatus())
	})
	api.PUT("/intent/local/presets-url", func(c *gin.Context) {
		var req struct {
			URL *string `json:"url"`
		}
		if err := c.ShouldBindJSON(&req); err != nil || req.URL == nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "请提供清单地址；传空字符串恢复默认"})
			return
		}
		if err := service.Local.SetPresetsURL(*req.URL); err != nil {
			common.WriteSaveError(c, "保存预设清单地址失败", err)
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
		if _, err := service.StartLocalAction("install", req.ID); err != nil {
			common.WriteSaveError(c, "安装并启动向量预设失败", err)
			return
		}
		c.JSON(http.StatusAccepted, service.LocalStatus())
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
		if _, err := service.StartLocalAction("start", ""); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusAccepted, service.LocalStatus())
	})
	api.POST("/intent/local/stop", func(c *gin.Context) {
		if err := service.StopLocal(); err != nil {
			c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
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
	group.GET("/status", func(c *gin.Context) {
		cfg := service.Cfg.GetIntentRouting(c.Param("protocol"))
		state, reason := "disabled", "未启用"
		if cfg.Enabled {
			state, reason = "ready", "已启用且就绪"
			if err := service.ValidateAvailability(c.Request.Context(), c.Param("protocol"), cfg); err != nil {
				state, reason = "error", err.Error()
			} else if err := service.ValidateIntentVector(cfg); err != nil {
				state, reason = "error", err.Error()
			} else if err := service.ValidateIntentIndexes(intent.WithProtocol(c.Request.Context(), c.Param("protocol")), cfg); err != nil {
				state, reason = "error", err.Error()
			}
			if config.IntentUsesVector(cfg) && cfg.EmbeddingSource == "local" && service.LocalStatus().Busy {
				state, reason = "preparing", "本地模型正在准备"
			}
		}
		c.JSON(http.StatusOK, gin.H{"state": state, "reason": reason})
	})
	group.POST("/prepare", func(c *gin.Context) {
		var req struct {
			Config       config.IntentRoutingConfig `json:"config"`
			PresetID     string                     `json:"presetId"`
			AdoptBuiltin bool                       `json:"adoptBuiltin"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "准备参数无效"})
			return
		}
		// 草稿的依赖会在任务中补齐，先校验其余结构，任务不会自动保存或启用路由。
		candidate := req.Config
		candidate.Enabled = false
		if err := config.ValidateIntentRouting(candidate, service.Cfg.GetChannelPools(c.Param("protocol"))); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		job, err := service.PrepareIntent(c.Param("protocol"), req.Config, req.PresetID, req.AdoptBuiltin)
		if err != nil {
			c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusAccepted, job)
	})
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
			if err := service.ValidateAvailability(c.Request.Context(), c.Param("protocol"), value); err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
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
	group.POST("/evaluate", func(c *gin.Context) {
		var req struct {
			Config    config.IntentRoutingConfig `json:"config"`
			DatasetID string                     `json:"datasetId"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "评测参数无效"})
			return
		}
		if req.Config.Strategy != "auto" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "路由评测用于按任务与分组能力匹配的模式"})
			return
		}
		if err := service.ValidateAvailability(c.Request.Context(), c.Param("protocol"), req.Config); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		result, err := service.EvaluateRouting(c.Request.Context(), c.Param("protocol"), req.Config, req.DatasetID)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, result)
	})
	group.POST("/correction", func(c *gin.Context) {
		var req struct {
			RequestID string `json:"requestId"`
			Task      string `json:"task"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "纠正参数无效"})
			return
		}
		id, err := service.CorrectTask(c.Request.Context(), c.Param("protocol"), req.RequestID, req.Task)
		if err != nil {
			common.WriteSaveError(c, "保存意图纠正失败", err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"datasetId": id})
	})
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
