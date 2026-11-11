// app_routes.go 路由装配（R4-b 按初始化职责分文件）：
// 管理路由组（/api、/admin）与各协议代理端点的注册。
package main

import (
	"github.com/gin-gonic/gin"
	"github.com/insistanan/api-proxy/internal/core/channelcrud"
	"github.com/insistanan/api-proxy/internal/handlers"
	"github.com/insistanan/api-proxy/internal/handlers/chat"
	"github.com/insistanan/api-proxy/internal/handlers/decisions"
	"github.com/insistanan/api-proxy/internal/handlers/embeddings"
	"github.com/insistanan/api-proxy/internal/handlers/gemini"
	"github.com/insistanan/api-proxy/internal/handlers/images"
	"github.com/insistanan/api-proxy/internal/handlers/messages"
	"github.com/insistanan/api-proxy/internal/handlers/modelapi"
	"github.com/insistanan/api-proxy/internal/handlers/responses"
	"github.com/insistanan/api-proxy/internal/metrics"
	"github.com/insistanan/api-proxy/internal/scheduler"
)

// setupAdminAPI 注册 Web 管理界面 API 路由（/api 组）。
func (a *app) setupAdminAPI(apiGroup *gin.RouterGroup) {
	apiGroup.GET("/free-channels", handlers.GetFreeChannels(a.cfgManager))
	apiGroup.PUT("/free-channels/kilo", handlers.UpdateKiloFreeChannel(a.cfgManager))
	apiGroup.POST("/free-channels/kilo/models/sync", handlers.SyncKiloFreeModels(a.cfgManager))
	apiGroup.GET("/request-logs", handlers.GetRequestLogs(a.requestLogStore))
	apiGroup.GET("/model-availability", handlers.GetModelAvailability(a.logStore, a.cfgManager))
	apiGroup.GET("/system-logs", handlers.GetSystemLogs(a.logStore))
	apiGroup.GET("/system-logs/:requestId", handlers.GetSystemLog(a.logStore))
	apiGroup.GET("/log-conversations", handlers.GetLogConversations(a.logStore))
	apiGroup.GET("/log-conversation-requests", handlers.GetLogConversationRequests(a.logStore))
	// 会话注册表直接注入（R4-b：scheduler 退出服务定位器角色，
	// 装配层持有实例并显式传参）。
	apiGroup.GET("/blocked-logs", handlers.GetBlockedLogs(a.blockedStore, a.conversationRegistry))
	apiGroup.GET("/blocked-logs/:id", handlers.GetBlockedLog(a.blockedStore))
	apiGroup.DELETE("/blocked-logs/:id", handlers.DeleteBlockedLog(a.blockedStore))
	apiGroup.DELETE("/blocked-logs", handlers.ClearBlockedLogs(a.blockedStore))
	apiGroup.GET("/logs/dates", handlers.ListLogDates(a.logStore))
	apiGroup.POST("/logs/purge", handlers.PurgeLogs(a.logStore))

	// 危险命令 AI 闸门（待确认列表 / 用户决定 / 会话级白黑名单）
	apiGroup.GET("/security/gate/channels", handlers.GetGateChannels(a.cmdGate))
	apiGroup.GET("/security/gate/pending", handlers.GetGatePending(a.cmdGate))
	apiGroup.POST("/security/gate/pending/:id/decision", handlers.DecideGatePending(a.cmdGate))
	apiGroup.GET("/security/gate/decisions", handlers.ListGateDecisions(a.cmdGate))
	apiGroup.POST("/security/gate/decisions", handlers.AddGateDecision(a.cmdGate))
	apiGroup.DELETE("/security/gate/decisions/:conversationId", handlers.RemoveGateDecisions(a.cmdGate))

	a.setupChannelAdminAPI(apiGroup)

	// 本代理自身暴露的模型列表（客户端配置页按协议分组导入用，走 Web 鉴权而非代理鉴权）
	apiGroup.GET("/proxy-models", handlers.ProxyModelsHandler(a.cfgManager))

	// 性能报告只读取画像，观测和选渠的依赖在装配层分开。
	apiGroup.GET("/performance/report", handlers.GetChannelPerformanceReport(a.profileManager))

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
	apiGroup.GET("/settings/client-disguise", handlers.GetClientDisguise(a.cfgManager, a.disguiseSyncer))
	apiGroup.POST("/settings/client-disguise/sync", handlers.RefreshClientDisguise(a.disguiseSyncer))
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
	// 智能体客户端全局提示词文件读写（客户端配置页"全局提示词"卡片）
	apiGroup.GET("/settings/agent-prompt/:agent", handlers.GetAgentPrompt())
	apiGroup.PUT("/settings/agent-prompt/:agent", handlers.SaveAgentPrompt())
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
	// MCP 统一列表与智能体分发（沿用 apiGroup 的统一鉴权）
	apiGroup.GET("/mcp", a.mcpAPI.List())
	apiGroup.PUT("/mcp", a.mcpAPI.Save())
	apiGroup.POST("/mcp/toggle", a.mcpAPI.Toggle())
	a.assistantAPI.Register(apiGroup)
}

// setupChannelAdminAPI 装配各协议渠道管理路由，沿用调用方路由组的鉴权。
func (a *app) setupChannelAdminAPI(apiGroup *gin.RouterGroup) {
	channelDeps := handlers.ChannelRouteDeps{
		Cfg:       a.cfgManager,
		Scheduler: a.channelScheduler,
		MetricsByKind: func(kind scheduler.ChannelKind) *metrics.MetricsManager {
			return a.metricsByKind[kind]
		},
		LogStoreByKind: a.channelScheduler.GetChannelLogStore,
	}

	// 每种协议只构造一次 handler 集合，负载均衡复用同一实例。
	for _, channel := range []struct {
		kind scheduler.ChannelKind
		crud *channelcrud.Handlers
	}{
		{scheduler.ChannelKindMessages, messages.Crud(a.cfgManager, a.channelScheduler)},
		{scheduler.ChannelKindResponses, responses.Crud(a.cfgManager, a.channelScheduler)},
		{scheduler.ChannelKindGemini, gemini.Crud(a.cfgManager, a.channelScheduler)},
		{scheduler.ChannelKindChat, chat.Crud(a.cfgManager, a.channelScheduler)},
		{scheduler.ChannelKindImages, images.Crud(a.cfgManager, a.channelScheduler)},
		{scheduler.ChannelKindEmbeddings, embeddings.Crud(a.cfgManager, a.channelScheduler)},
		{scheduler.ChannelKindDecisions, decisions.Crud(a.cfgManager, a.channelScheduler)},
	} {
		opts := handlers.ChannelRouteOptions{
			Crud:        channel.crud,
			LoadBalance: channel.crud.UpdateLoadBalance,
			Dashboard:   handlers.GetChannelDashboard(a.cfgManager, a.channelScheduler, channel.kind),
		}
		if channel.kind == scheduler.ChannelKindMessages {
			opts.SchedulerStats = handlers.GetSchedulerStats(a.channelScheduler)
		}
		handlers.RegisterChannelRoutes(apiGroup, channel.kind, channelDeps, opts)
	}
}

// setupProxyEndpoints 注册各协议代理端点。
func (a *app) setupProxyEndpoints(r *gin.Engine) {
	// 代理端点 - Messages API
	r.POST("/v1/messages", messages.Handler(a.envCfg, a.cfgManager, a.channelScheduler, a.contentSafetyPipeline))
	r.POST("/v1/messages/count_tokens", messages.CountTokensHandler(a.envCfg, a.cfgManager, a.channelScheduler))

	// 模型目录只读取本地分组配置，按入口协议筛选并输出客户端所需格式。
	modelapi.RegisterRoutes(r, a.envCfg, a.cfgManager)

	// 代理端点 - Responses API
	r.POST("/v1/responses", responses.Handler(a.envCfg, a.cfgManager, a.sessionManager, a.channelScheduler, a.contentSafetyPipeline))
	r.POST("/v1/responses/compact", responses.CompactHandler(a.envCfg, a.cfgManager, a.sessionManager, a.channelScheduler, a.contentSafetyPipeline))

	// 代理端点 - Chat Completions API (OpenAI-compatible 原生协议)
	r.POST("/v1/chat/completions", chat.Handler(a.envCfg, a.cfgManager, a.channelScheduler, a.contentSafetyPipeline))
	// 代理端点 - Images API (OpenAI-compatible 独立协议)
	r.POST("/v1/images/generations", images.Handler(a.envCfg, a.cfgManager, a.channelScheduler, "/images/generations", a.contentSafetyPipeline))
	r.POST("/v1/images/edits", images.Handler(a.envCfg, a.cfgManager, a.channelScheduler, "/images/edits", a.contentSafetyPipeline))
	r.POST("/v1/images/variations", images.Handler(a.envCfg, a.cfgManager, a.channelScheduler, "/images/variations", a.contentSafetyPipeline))

	// 代理端点 - Embeddings API (OpenAI-compatible 独立协议)
	r.POST("/v1/embeddings", embeddings.Handler(a.envCfg, a.cfgManager, a.channelScheduler))
	// 代理端点 - Decisions API (独立协议)
	r.POST("/v1/decisions", decisions.Handler(a.envCfg, a.cfgManager, a.channelScheduler))

	// 代理端点 - Gemini API (原生协议)
	// 使用通配符捕获 model:action 格式，如 gemini-pro:generateContent
	// 路径格式：/v1beta/models/{model}:generateContent (Gemini 原生格式)
	r.POST("/v1beta/models/*modelAction", gemini.Handler(a.envCfg, a.cfgManager, a.channelScheduler, a.contentSafetyPipeline))
}
