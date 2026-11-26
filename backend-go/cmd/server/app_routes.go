// app_routes.go 集中装配路由与中间件：
// 管理路由组（/api、/admin）与各协议代理端点的注册。
package main

import (
	"github.com/gin-gonic/gin"
	apiproxy "insistanan/api-proxy"
	adminconversations "insistanan/api-proxy/internal/api/admin/conversations"
	adminforest "insistanan/api-proxy/internal/api/admin/forest"
	adminintent "insistanan/api-proxy/internal/api/admin/intent"
	adminlogs "insistanan/api-proxy/internal/api/admin/logs"
	admingate "insistanan/api-proxy/internal/api/admin/security"
	adminsettings "insistanan/api-proxy/internal/api/admin/settings"
	adminusage "insistanan/api-proxy/internal/api/admin/usage"
	"insistanan/api-proxy/internal/api/channels"
	"insistanan/api-proxy/internal/api/chat"
	"insistanan/api-proxy/internal/api/decisions"
	"insistanan/api-proxy/internal/api/embeddings"
	"insistanan/api-proxy/internal/api/gemini"
	"insistanan/api-proxy/internal/api/images"
	"insistanan/api-proxy/internal/api/messages"
	"insistanan/api-proxy/internal/api/modelapi"
	"insistanan/api-proxy/internal/api/responses"
	"insistanan/api-proxy/internal/api/system"
	"insistanan/api-proxy/internal/clients"
	"insistanan/api-proxy/internal/dialogue/routing/intent"
	"insistanan/api-proxy/internal/infra/middleware"
	"insistanan/api-proxy/internal/orchestration/scheduler"
	"insistanan/api-proxy/internal/protocol/tokenusage"
	"insistanan/api-proxy/internal/storage/metrics"
)

// setupAdminAPI 注册 Web 管理界面 API 路由（/api 组）。
func (a *app) setupAdminAPI(apiGroup *gin.RouterGroup) {
	adminusage.Register(apiGroup, a.usageLedger)
	adminforest.Register(apiGroup, a.forest)
	adminintent.RegisterIntentAPI(apiGroup, a.intentService)
	adminintent.RegisterDatasetAPI(apiGroup, a.intentService)
	apiGroup.GET("/free-channels", channels.GetFreeChannels(a.cfgManager))
	apiGroup.PUT("/free-channels/kilo", channels.UpdateKiloFreeChannel(a.cfgManager))
	apiGroup.POST("/free-channels/kilo/models/sync", channels.SyncKiloFreeModels(a.cfgManager))
	apiGroup.GET("/request-logs", adminlogs.GetRequestLogs(a.requestLogStore))
	apiGroup.GET("/request-stats", adminlogs.GetRequestStats(a.logStore))
	apiGroup.GET("/log-calls", adminlogs.GetLogCalls(a.logStore))
	apiGroup.GET("/log-call-attempts", adminlogs.GetLogCallAttempts(a.logStore))
	apiGroup.GET("/model-availability", channels.GetModelAvailability(a.logStore, a.cfgManager))
	apiGroup.GET("/system-logs", adminlogs.GetSystemLogs(a.logStore))
	apiGroup.GET("/system-logs/:requestId", adminlogs.GetSystemLog(a.logStore))
	apiGroup.GET("/log-conversations", adminlogs.GetLogConversations(a.logStore, a.conversationRegistry))
	apiGroup.GET("/log-conversation-requests", adminlogs.GetLogConversationRequests(a.logStore))
	apiGroup.GET("/log-context-overview", adminlogs.GetLogContextOverview(a.logStore))
	apiGroup.GET("/runtime-logs", adminlogs.GetRuntimeLogs(a.logStore))
	// 会话注册表由装配层直接注入，管理接口不通过调度器查找依赖。
	apiGroup.GET("/blocked-logs", adminlogs.GetBlockedLogs(a.blockedStore, a.conversationRegistry))
	apiGroup.GET("/blocked-logs/:id", adminlogs.GetBlockedLog(a.blockedStore))
	apiGroup.DELETE("/blocked-logs/:id", adminlogs.DeleteBlockedLog(a.blockedStore))
	apiGroup.DELETE("/blocked-logs", adminlogs.ClearBlockedLogs(a.blockedStore))
	apiGroup.GET("/logs/dates", adminlogs.ListLogDates(a.logStore))
	apiGroup.GET("/logs/storage", adminlogs.GetLogStorageStatus(a.logStore))
	apiGroup.GET("/metrics/storage", func(c *gin.Context) {
		if a.metricsStore == nil {
			c.JSON(200, gin.H{"enabled": false})
			return
		}
		c.JSON(200, gin.H{"enabled": true, "queue": a.metricsStore.QueueStatus()})
	})
	apiGroup.POST("/logs/purge", adminlogs.PurgeLogs(a.logStore))

	// 危险命令 AI 闸门（待确认列表 / 用户决定 / 会话级白黑名单）
	apiGroup.GET("/security/gate/channels", admingate.GetGateChannels(a.cmdGate))
	apiGroup.GET("/security/gate/pending", admingate.GetGatePending(a.cmdGate))
	apiGroup.POST("/security/gate/pending/:id/decision", admingate.DecideGatePending(a.cmdGate))
	apiGroup.GET("/security/gate/decisions", admingate.ListGateDecisions(a.cmdGate))
	apiGroup.POST("/security/gate/decisions", admingate.AddGateDecision(a.cmdGate))
	apiGroup.DELETE("/security/gate/decisions/:conversationId", admingate.RemoveGateDecisions(a.cmdGate))

	a.setupChannelAdminAPI(apiGroup)

	// 本代理自身暴露的模型列表（客户端配置页按协议分组导入用，走 Web 鉴权而非代理鉴权）
	apiGroup.GET("/proxy-models", system.ProxyModelsHandler(a.cfgManager))

	// 性能报告只读取画像，观测和选渠的依赖在装配层分开。
	apiGroup.GET("/performance/report", channels.GetChannelPerformanceReport(a.profileManager))

	// 渠道级熔断器（查询 / 手动重置）
	apiGroup.GET("/circuit-breakers", channels.GetCircuitBreakers(a.channelScheduler))
	apiGroup.POST("/circuit-breakers/reset", channels.ResetCircuitBreaker(a.channelScheduler))

	// 对话与路由覆盖
	apiGroup.GET("/conversations/route-options", adminconversations.GetConversationRouteOptions(a.cfgManager))
	// 会话注册表直接注入。
	apiGroup.GET("/conversations", adminconversations.ListConversations(a.conversationRegistry))
	apiGroup.DELETE("/conversations", adminconversations.DeleteAllConversations(a.conversationRegistry, a.sessionManager, a.cacheKeepalive))
	apiGroup.GET("/conversations/:id", adminconversations.GetConversation(a.conversationRegistry))
	apiGroup.PUT("/conversations/:id/name", adminconversations.SetConversationName(a.conversationRegistry))
	apiGroup.DELETE("/conversations/:id", adminconversations.DeleteConversation(a.conversationRegistry, a.sessionManager, a.cacheKeepalive))
	apiGroup.PUT("/conversations/:id/route", adminconversations.SetConversationRouteOverride(a.conversationRegistry, a.cfgManager))
	apiGroup.DELETE("/conversations/:id/route", adminconversations.ClearConversationRouteOverride(a.conversationRegistry))

	// 管理界面设置
	apiGroup.GET("/settings/fuzzy-mode", adminsettings.GetFuzzyMode(a.cfgManager))
	apiGroup.PUT("/settings/fuzzy-mode", adminsettings.SetFuzzyMode(a.cfgManager))
	apiGroup.GET("/settings", adminsettings.GetSettings(a.cfgManager))
	apiGroup.PUT("/settings", adminsettings.UpdateSettings(a.cfgManager, a.skillsAPI, a.mcpAPI))
	apiGroup.POST("/upstream/models", channels.DiscoverUpstreamModels(a.cfgManager))
	apiGroup.GET("/settings/client-disguise", adminsettings.GetClientDisguise(a.cfgManager, a.disguiseSyncer))
	apiGroup.POST("/settings/client-disguise/sync", adminsettings.RefreshClientDisguise(a.disguiseSyncer))
	apiGroup.PUT("/settings/client-disguise", adminsettings.SetClientDisguise(a.cfgManager))
	apiGroup.GET("/settings/opencode", clients.GetOpenCodeConfig())
	apiGroup.PUT("/settings/opencode", clients.SaveOpenCodeConfig())
	apiGroup.GET("/settings/claude-code", clients.GetClaudeCodeSettings())
	apiGroup.PUT("/settings/claude-code", clients.SaveClaudeCodeSettings())
	apiGroup.GET("/settings/codex", clients.GetCodexSettings())
	apiGroup.PUT("/settings/codex", clients.SaveCodexSettings())
	apiGroup.PUT("/settings/codex/model-catalog", clients.SaveCodexModelCatalog())
	apiGroup.GET("/settings/dsh", clients.GetDSHSettings())
	apiGroup.PUT("/settings/dsh", clients.SaveDSHSettings())
	// 智能体客户端全局提示词文件读写（客户端配置页"全局提示词"卡片）
	apiGroup.GET("/settings/agent-prompt/:agent", clients.GetAgentPrompt())
	apiGroup.PUT("/settings/agent-prompt/:agent", clients.SaveAgentPrompt())
	// CLI 工具版本检测（客户端配置页顶部横幅）
	apiGroup.GET("/tools/:tool/version", clients.GetToolVersion())

	// pi-agent 配置管理（沿用 apiGroup 的统一鉴权）
	apiGroup.GET("/settings/pi-agent", a.piAgentAPI.Status())
	apiGroup.GET("/settings/pi-agent/providers", a.piAgentAPI.ListProviders())
	apiGroup.POST("/settings/pi-agent/providers", a.piAgentAPI.CreateProvider())
	apiGroup.GET("/settings/pi-agent/providers/:id", a.piAgentAPI.GetProvider())
	apiGroup.PUT("/settings/pi-agent/providers/:id", a.piAgentAPI.UpdateProvider())
	apiGroup.DELETE("/settings/pi-agent/providers/:id", a.piAgentAPI.DeleteProvider())
	apiGroup.POST("/settings/pi-agent/validate", a.piAgentAPI.ValidateProvider())
	apiGroup.POST("/settings/pi-agent/providers/:id/discover-models", a.piAgentAPI.DiscoverModels())
	apiGroup.POST("/settings/pi-agent/providers/:id/test", a.piAgentAPI.TestProvider())
	apiGroup.GET("/settings/pi-agent/model-settings", a.piAgentAPI.GetModelSettings())
	apiGroup.PATCH("/settings/pi-agent/model-settings", a.piAgentAPI.UpdateModelSettings())
	apiGroup.GET("/skills", a.skillsAPI.List())
	apiGroup.POST("/skills/content", a.skillsAPI.GetContent())
	apiGroup.POST("/skills/backup/latest", a.skillsAPI.GetLatestBackup())
	apiGroup.POST("/skills/note", a.skillsAPI.UpdateNote())
	apiGroup.POST("/skills/consolidate", a.skillsAPI.Consolidate())
	apiGroup.POST("/skills/import", a.skillsAPI.Import())
	apiGroup.GET("/skills/search", a.skillsAPI.Search())
	apiGroup.POST("/skills/remote/inspect", a.skillsAPI.InspectRemote())
	apiGroup.POST("/skills/remote/install", a.skillsAPI.InstallRemote())
	apiGroup.POST("/skills/copy", a.skillsAPI.Copy())
	apiGroup.POST("/skills/toggle", a.skillsAPI.Toggle())
	apiGroup.POST("/skills/sync", a.skillsAPI.Sync())
	apiGroup.DELETE("/skills", a.skillsAPI.Delete())
	apiGroup.POST("/skills/backup", a.skillsAPI.Backup())
	apiGroup.GET("/skills/translation", a.skillsAPI.SkillTranslationStatus)
	apiGroup.POST("/skills/translation", a.skillsAPI.StartSkillTranslation(a.skillTranslator))
	apiGroup.DELETE("/skills/translation/:taskId", a.skillsAPI.CancelSkillTranslation)
	// MCP 统一列表与智能体分发（沿用 apiGroup 的统一鉴权）
	apiGroup.GET("/mcp", a.mcpAPI.List())
	apiGroup.PUT("/mcp", a.mcpAPI.Save())
	apiGroup.POST("/mcp/toggle", a.mcpAPI.Toggle())
	// 项目扩展目录（Skills / MCP）Git 同步
	apiGroup.GET("/extensions/git/status", a.extensionsGitAPI.Status())
	apiGroup.POST("/extensions/git/init", a.extensionsGitAPI.Init())
	apiGroup.POST("/extensions/git/clone", a.extensionsGitAPI.Clone())
	apiGroup.POST("/extensions/git/pull", a.extensionsGitAPI.Pull())
	apiGroup.POST("/extensions/git/push", a.extensionsGitAPI.Push())
}

// setupChannelAdminAPI 装配各协议渠道管理路由，沿用调用方路由组的鉴权。
func (a *app) setupChannelAdminAPI(apiGroup *gin.RouterGroup) {
	apiGroup.GET("/channels/:id/cache-keepalive", channels.GetCacheKeepalive(a.cfgManager, a.cacheKeepalive))
	apiGroup.POST("/channels/:id/cache-keepalive/stop", channels.StopCacheKeepalive(a.cfgManager, a.cacheKeepalive))
	a.registerWorker("指纹检测任务", channels.RegisterModelFingerprintRoutes(apiGroup, a.cfgManager))
	channelDeps := channels.ChannelRouteDeps{
		Cfg:       a.cfgManager,
		Scheduler: a.channelScheduler,
		MetricsByKind: func(kind scheduler.ChannelKind) *metrics.MetricsManager {
			return a.metricsByKind[kind]
		},
		LogStoreByKind: a.channelScheduler.GetChannelLogStore,
	}

	// 四个会话协议共享的公用图片理解池，带每条渠道自身的 kind。
	apiGroup.GET("/vision-channels", channels.GetVisionChannels(a.cfgManager))

	for _, kind := range scheduler.ChannelKinds() {
		opts := channels.ChannelRouteOptions{
			LoadBalance: true,
			Dashboard:   channels.GetChannelDashboard(a.cfgManager, a.channelScheduler, kind),
		}
		if kind == scheduler.ChannelKindMessages {
			opts.SchedulerStats = channels.GetSchedulerStats(a.channelScheduler)
		}
		channels.RegisterChannelRoutes(apiGroup, kind, channelDeps, opts)
	}
}

// setupProxyEndpoints 注册各协议代理端点。
func (a *app) setupProxyEndpoints(r *gin.Engine) {
	// 仅代理端点注入用量采集，管理操作不会形成用量记录。
	proxy := r.Group("")
	if a.usageLedger != nil {
		proxy.Use(func(c *gin.Context) {
			if a.usageLedger.Enabled() {
				c.Request = c.Request.WithContext(tokenusage.WithRecorder(c.Request.Context(), a.usageLedger.Record))
			}
			c.Next()
		})
	}
	// 代理端点 - Messages API
	proxy.POST("/v1/messages", messages.Handler(a.envCfg, a.cfgManager, a.channelScheduler, a.contentSafetyPipeline))
	r.POST("/v1/messages/count_tokens", messages.CountTokensHandler(a.envCfg, a.cfgManager, a.channelScheduler))

	// 模型目录只读取本地分组配置，按入口协议筛选并输出客户端所需格式。
	modelapi.RegisterRoutes(r, a.envCfg, a.cfgManager)

	// 代理端点 - Responses API
	proxy.POST("/v1/responses", responses.Handler(a.envCfg, a.cfgManager, a.sessionManager, a.channelScheduler, a.contentSafetyPipeline))
	proxy.POST("/v1/responses/compact", responses.CompactHandler(a.envCfg, a.cfgManager, a.sessionManager, a.channelScheduler, a.contentSafetyPipeline))

	// 代理端点 - Chat Completions API (OpenAI-compatible 原生协议)
	proxy.POST("/v1/chat/completions", chat.Handler(a.envCfg, a.cfgManager, a.channelScheduler, a.contentSafetyPipeline))
	// 代理端点 - Images API (OpenAI-compatible 独立协议)
	proxy.POST("/v1/images/generations", images.Handler(a.envCfg, a.cfgManager, a.channelScheduler, "/images/generations", a.contentSafetyPipeline))
	proxy.POST("/v1/images/edits", images.Handler(a.envCfg, a.cfgManager, a.channelScheduler, "/images/edits", a.contentSafetyPipeline))
	proxy.POST("/v1/images/variations", images.Handler(a.envCfg, a.cfgManager, a.channelScheduler, "/images/variations", a.contentSafetyPipeline))

	// 代理端点 - Embeddings API (OpenAI-compatible 独立协议)
	proxy.POST("/v1/embeddings", embeddings.Handler(a.envCfg, a.cfgManager, a.channelScheduler))
	// 代理端点 - Decisions API (独立协议)
	proxy.POST("/v1/decisions", decisions.Handler(a.envCfg, a.cfgManager, a.channelScheduler))

	// 代理端点 - Gemini API (原生协议)
	// 使用通配符捕获 model:action 格式，如 gemini-pro:generateContent
	// 路径格式：/v1beta/models/{model}:generateContent (Gemini 原生格式)
	proxy.POST("/v1beta/models/*modelAction", gemini.Handler(a.envCfg, a.cfgManager, a.channelScheduler, a.contentSafetyPipeline))
}

// setupHTTP 集中装配 HTTP 中间件、管理路由、代理入口与静态资源。
func (a *app) setupHTTP() *gin.Engine {
	// 设置 Gin 模式
	if a.envCfg.IsProduction() {
		gin.SetMode(gin.ReleaseMode)
	}

	// 创建路由器。QuietPollingLogs 由日志级别决定，FilteredLogger 据此过滤轮询访问日志。
	r := gin.New()
	r.Use(middleware.FilteredLogger(a.envCfg))
	r.Use(gin.Recovery())
	r.Use(func(c *gin.Context) {
		c.Request = c.Request.WithContext(intent.WithService(c.Request.Context(), a.intentService))
		c.Next()
	})

	// 配置 CORS
	r.Use(middleware.CORSMiddleware(a.envCfg))

	// Web UI 访问控制中间件
	r.Use(middleware.WebAuthMiddleware(a.envCfg, a.cfgManager))

	// 健康检查端点（固定路径 /health，与 Dockerfile HEALTHCHECK 保持一致）
	r.GET("/health", system.HealthCheck(a.envCfg, a.cfgManager))

	// 管理端点显式绑定管理鉴权，不依赖 WebAuthMiddleware 的路径判断。
	adminGroup := r.Group("/admin", middleware.AdminAuthMiddleware(a.envCfg))
	// 配置保存端点
	adminGroup.POST("/config/save", system.SaveConfigHandler(a.cfgManager))

	// 开发信息端点
	if a.envCfg.IsDevelopment() {
		adminGroup.GET("/dev/info", system.DevInfo(a.envCfg, a.cfgManager))
	}

	// Web 管理界面 API 路由（管理鉴权在路由组上显式绑定）
	apiGroup := r.Group("/api", middleware.AdminAuthMiddleware(a.envCfg))
	a.setupAdminAPI(apiGroup)

	// 各协议代理端点
	a.setupProxyEndpoints(r)

	// 静态文件服务 (嵌入的前端)
	if a.envCfg.EnableWebUI {
		system.ServeFrontend(r, apiproxy.FrontendFS)
	} else {
		// 纯 API 模式
		r.GET("/", func(c *gin.Context) {
			c.JSON(200, gin.H{
				"name":    "API Proxy",
				"mode":    "API Only",
				"version": Version,
				"endpoints": gin.H{
					"chat":       "/v1/chat/completions",
					"config":     "/admin/config/save",
					"decisions":  "/v1/decisions",
					"embeddings": "/v1/embeddings",
					"gemini":     "/v1beta/models/*modelAction",
					"health":     "/health",
					"images":     "/v1/images/generations",
					"messages":   "/v1/messages",
					"responses":  "/v1/responses",
				},
				"message": "Web界面已禁用，此服务器运行在纯API模式下",
			})
		})
	}

	return r
}
