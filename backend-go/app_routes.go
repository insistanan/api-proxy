// app_routes.go 路由装配（R4-b 按初始化职责分文件）：
// 管理路由组（/api、/admin）与五协议代理端点的注册。
package main

import (
	"github.com/BenedictKing/api-proxy/internal/handlers"
	"github.com/BenedictKing/api-proxy/internal/handlers/chat"
	"github.com/BenedictKing/api-proxy/internal/handlers/gemini"
	"github.com/BenedictKing/api-proxy/internal/handlers/images"
	"github.com/BenedictKing/api-proxy/internal/handlers/messages"
	"github.com/BenedictKing/api-proxy/internal/handlers/responses"
	"github.com/BenedictKing/api-proxy/internal/metrics"
	"github.com/BenedictKing/api-proxy/internal/scheduler"
	"github.com/gin-gonic/gin"
)

// setupAdminAPI 注册 Web 管理界面 API 路由（/api 组）。
func (a *app) setupAdminAPI(apiGroup *gin.RouterGroup) {
	apiGroup.GET("/request-logs", handlers.GetRequestLogs(a.requestLogStore))
	apiGroup.GET("/system-logs", handlers.GetSystemLogs(a.logStore))
	apiGroup.GET("/system-logs/:requestId", handlers.GetSystemLog(a.logStore))
	// 会话注册表直接注入（R4-b：scheduler 退出服务定位器角色，
	// 装配层持有实例并显式传参）。
	apiGroup.GET("/blocked-logs", handlers.GetBlockedLogs(a.blockedStore, a.conversationRegistry))
	apiGroup.GET("/blocked-logs/:id", handlers.GetBlockedLog(a.blockedStore))
	apiGroup.DELETE("/blocked-logs/:id", handlers.DeleteBlockedLog(a.blockedStore))
	apiGroup.DELETE("/blocked-logs", handlers.ClearBlockedLogs(a.blockedStore))
	apiGroup.GET("/logs/dates", handlers.ListLogDates(a.logStore))
	apiGroup.POST("/logs/purge", handlers.PurgeLogs(a.logStore))

	// 危险命令 AI 闸门（待确认列表 / 用户决定 / 会话级白黑名单）
	apiGroup.GET("/security/gate/pending", handlers.GetGatePending(a.cmdGate))
	apiGroup.POST("/security/gate/pending/:id/decision", handlers.DecideGatePending(a.cmdGate))
	apiGroup.GET("/security/gate/decisions", handlers.ListGateDecisions(a.cmdGate))
	apiGroup.POST("/security/gate/decisions", handlers.AddGateDecision(a.cmdGate))
	apiGroup.DELETE("/security/gate/decisions/:conversationId", handlers.RemoveGateDecisions(a.cmdGate))

	// 渠道管理 API 路由（messages/responses/gemini/chat/images 五组收敛为一次调用）
	// 各渠道类型的 CRUD / key 管理 / pools / reorder / status / promotion / metrics / ping
	// 全部由 handlers.RegisterChannelRoutes 统一注册，差异点通过 ChannelRouteOptions 表达。
	channelDeps := handlers.ChannelRouteDeps{
		Cfg:       a.cfgManager,
		Scheduler: a.channelScheduler,
		MetricsByKind: func(kind scheduler.ChannelKind) *metrics.MetricsManager {
			return a.metricsByKind[kind]
		},
		LogStoreByKind: a.channelScheduler.GetChannelLogStore,
	}

	// Messages 渠道管理
	handlers.RegisterChannelRoutes(apiGroup, scheduler.ChannelKindMessages, channelDeps, handlers.ChannelRouteOptions{
		Crud:           messages.Crud(a.cfgManager, a.channelScheduler),
		LoadBalance:    messages.UpdateLoadBalance(a.cfgManager),
		Dashboard:      handlers.GetChannelDashboard(a.cfgManager, a.channelScheduler, scheduler.ChannelKindMessages),
		SchedulerStats: handlers.GetSchedulerStats(a.channelScheduler),
	})

	// Responses 渠道管理
	handlers.RegisterChannelRoutes(apiGroup, scheduler.ChannelKindResponses, channelDeps, handlers.ChannelRouteOptions{
		Crud:        responses.Crud(a.cfgManager, a.channelScheduler),
		LoadBalance: responses.UpdateLoadBalance(a.cfgManager),
		Dashboard:   handlers.GetChannelDashboard(a.cfgManager, a.channelScheduler, scheduler.ChannelKindResponses),
	})

	// Gemini 渠道管理
	handlers.RegisterChannelRoutes(apiGroup, scheduler.ChannelKindGemini, channelDeps, handlers.ChannelRouteOptions{
		Crud:        gemini.Crud(a.cfgManager, a.channelScheduler),
		LoadBalance: gemini.UpdateLoadBalance(a.cfgManager),
		Dashboard:   handlers.GetChannelDashboard(a.cfgManager, a.channelScheduler, scheduler.ChannelKindGemini),
	})

	// Chat 渠道管理
	handlers.RegisterChannelRoutes(apiGroup, scheduler.ChannelKindChat, channelDeps, handlers.ChannelRouteOptions{
		Crud:        chat.Crud(a.cfgManager, a.channelScheduler),
		LoadBalance: chat.UpdateLoadBalance(a.cfgManager),
		Dashboard:   handlers.GetChannelDashboard(a.cfgManager, a.channelScheduler, scheduler.ChannelKindChat),
	})

	// Images 渠道管理
	handlers.RegisterChannelRoutes(apiGroup, scheduler.ChannelKindImages, channelDeps, handlers.ChannelRouteOptions{
		Crud:        images.Crud(a.cfgManager, a.channelScheduler),
		LoadBalance: images.UpdateLoadBalance(a.cfgManager),
		Dashboard:   handlers.GetChannelDashboard(a.cfgManager, a.channelScheduler, scheduler.ChannelKindImages),
	})

	// 本代理自身暴露的模型列表（客户端配置页按协议分组导入用，走 Web 鉴权而非代理鉴权）
	apiGroup.GET("/proxy-models", handlers.ProxyModelsHandler(a.cfgManager))

	// 渠道性能报告（自适应负载均衡）
	apiGroup.GET("/performance/report", func(c *gin.Context) {
		reports := a.adaptiveScheduler.GetChannelPerformanceReport()
		c.JSON(200, gin.H{
			"success": true,
			"data":    reports,
		})
	})

	// 渠道级熔断器（查询 / 手动重置）
	apiGroup.GET("/circuit-breakers", handlers.GetCircuitBreakers(a.channelScheduler))
	apiGroup.POST("/circuit-breakers/reset", handlers.ResetCircuitBreaker(a.channelScheduler))

	// 对话与路由覆盖
	apiGroup.GET("/conversations/route-options", handlers.GetConversationRouteOptions(a.cfgManager))
	// 会话注册表直接注入（R4-b：调度器退出服务定位器角色）
	apiGroup.GET("/conversations", handlers.ListConversations(a.conversationRegistry))
	apiGroup.DELETE("/conversations", handlers.DeleteAllConversations(a.conversationRegistry, a.sessionManager))
	apiGroup.GET("/conversations/:id", handlers.GetConversation(a.conversationRegistry))
	apiGroup.PUT("/conversations/:id/name", handlers.SetConversationName(a.conversationRegistry))
	apiGroup.DELETE("/conversations/:id", handlers.DeleteConversation(a.conversationRegistry, a.sessionManager))
	apiGroup.PUT("/conversations/:id/route", handlers.SetConversationRouteOverride(a.conversationRegistry, a.cfgManager))
	apiGroup.DELETE("/conversations/:id/route", handlers.ClearConversationRouteOverride(a.conversationRegistry))

	// 管理界面设置
	apiGroup.GET("/settings/fuzzy-mode", handlers.GetFuzzyMode(a.cfgManager))
	apiGroup.PUT("/settings/fuzzy-mode", handlers.SetFuzzyMode(a.cfgManager))
	apiGroup.GET("/settings", handlers.GetSettings(a.cfgManager))
	apiGroup.PUT("/settings", handlers.UpdateSettings(a.cfgManager))
	apiGroup.POST("/upstream/models", handlers.DiscoverUpstreamModels(a.cfgManager))
	apiGroup.GET("/settings/client-disguise", handlers.GetClientDisguise(a.cfgManager))
	apiGroup.PUT("/settings/client-disguise", handlers.SetClientDisguise(a.cfgManager))
	apiGroup.GET("/settings/opencode", handlers.GetOpenCodeConfig())
	apiGroup.PUT("/settings/opencode", handlers.SaveOpenCodeConfig())
	apiGroup.GET("/settings/claude-code", handlers.GetClaudeCodeSettings())
	apiGroup.PUT("/settings/claude-code", handlers.SaveClaudeCodeSettings())
	apiGroup.GET("/settings/codex", handlers.GetCodexSettings())
	apiGroup.PUT("/settings/codex", handlers.SaveCodexSettings())
	apiGroup.PUT("/settings/codex/model-catalog", handlers.SaveCodexModelCatalog())
	apiGroup.GET("/settings/dsh", handlers.GetDSHSettings())
	apiGroup.PUT("/settings/dsh", handlers.SaveDSHSettings())
	// CLI 工具版本检测（客户端配置页顶部横幅）
	apiGroup.GET("/tools/:tool/version", handlers.GetToolVersion())
	apiGroup.POST("/settings/ccs/pick-path", handlers.PickCcsPath(a.cfgManager))
	apiGroup.POST("/settings/ccs/import", handlers.ImportToCcs(a.cfgManager))

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
	apiGroup.DELETE("/skills", a.skillsAPI.Delete())
	apiGroup.POST("/skills/backup", a.skillsAPI.Backup())
	a.assistantAPI.Register(apiGroup)
	handlers.RegisterEvalRoutes(apiGroup, a.evalService)
}

// setupProxyEndpoints 注册五协议代理端点。
func (a *app) setupProxyEndpoints(r *gin.Engine) {
	// 代理端点 - Messages API
	r.POST("/v1/messages", messages.Handler(a.envCfg, a.cfgManager, a.channelScheduler, a.contentSafetyPipeline))
	r.POST("/v1/messages/count_tokens", messages.CountTokensHandler(a.envCfg, a.cfgManager, a.channelScheduler))

	// 代理端点 - Models API（转发到上游）
	r.GET("/v1/models", messages.ModelsHandler(a.envCfg, a.cfgManager, a.channelScheduler))
	r.GET("/v1/models/:model", messages.ModelsDetailHandler(a.envCfg, a.cfgManager, a.channelScheduler))

	// 代理端点 - Responses API
	r.POST("/v1/responses", responses.Handler(a.envCfg, a.cfgManager, a.sessionManager, a.channelScheduler, a.contentSafetyPipeline))
	r.POST("/v1/responses/compact", responses.CompactHandler(a.envCfg, a.cfgManager, a.sessionManager, a.channelScheduler, a.contentSafetyPipeline))

	// 代理端点 - Chat Completions API (OpenAI-compatible 原生协议)
	r.POST("/v1/chat/completions", chat.Handler(a.envCfg, a.cfgManager, a.channelScheduler, a.contentSafetyPipeline))
	// 代理端点 - Images API (OpenAI-compatible 独立协议)
	r.POST("/v1/images/generations", images.Handler(a.envCfg, a.cfgManager, a.channelScheduler, "/images/generations", a.contentSafetyPipeline))
	r.POST("/v1/images/edits", images.Handler(a.envCfg, a.cfgManager, a.channelScheduler, "/images/edits", a.contentSafetyPipeline))
	r.POST("/v1/images/variations", images.Handler(a.envCfg, a.cfgManager, a.channelScheduler, "/images/variations", a.contentSafetyPipeline))

	// 代理端点 - Gemini API (原生协议)
	// 使用通配符捕获 model:action 格式，如 gemini-pro:generateContent
	// 路径格式：/v1beta/models/{model}:generateContent (Gemini 原生格式)
	r.POST("/v1beta/models/*modelAction", gemini.Handler(a.envCfg, a.cfgManager, a.channelScheduler, a.contentSafetyPipeline))
}
