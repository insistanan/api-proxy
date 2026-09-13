package main

import (
	"context"
	"embed"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/BenedictKing/api-proxy/internal/assistant"
	"github.com/BenedictKing/api-proxy/internal/circuit"
	"github.com/BenedictKing/api-proxy/internal/cli"
	"github.com/BenedictKing/api-proxy/internal/config"
	"github.com/BenedictKing/api-proxy/internal/conversation"
	"github.com/BenedictKing/api-proxy/internal/eval"
	"github.com/BenedictKing/api-proxy/internal/handlers"
	"github.com/BenedictKing/api-proxy/internal/handlers/chat"
	"github.com/BenedictKing/api-proxy/internal/handlers/gemini"
	"github.com/BenedictKing/api-proxy/internal/handlers/hooks"
	"github.com/BenedictKing/api-proxy/internal/handlers/images"
	"github.com/BenedictKing/api-proxy/internal/handlers/messages"
	"github.com/BenedictKing/api-proxy/internal/handlers/responses"
	"github.com/BenedictKing/api-proxy/internal/llmgate"
	"github.com/BenedictKing/api-proxy/internal/logger"
	"github.com/BenedictKing/api-proxy/internal/metrics"
	"github.com/BenedictKing/api-proxy/internal/middleware"
	"github.com/BenedictKing/api-proxy/internal/pricing"
	"github.com/BenedictKing/api-proxy/internal/scheduler"
	"github.com/BenedictKing/api-proxy/internal/sensitive"
	"github.com/BenedictKing/api-proxy/internal/session"
	"github.com/BenedictKing/api-proxy/internal/urlhealth"
	"github.com/gin-gonic/gin"
)

//go:embed all:frontend/dist
var frontendFS embed.FS

// app 聚合一次进程运行的全部组件。
// 字段顺序即构造依赖顺序；关闭顺序见 shutdown，两者不可随意调换。
type app struct {
	envCfg                *config.EnvConfig
	cfgManager            *config.ConfigManager
	logStore              *logger.Store
	requestLogStore       *metrics.RequestLogStore
	blockedStore          *sensitive.BlockedStore
	gateStore             *sensitive.GateStore
	mappingStore          *sensitive.MappingStore
	cmdGate               *hooks.CmdGate
	contentSafetyPipeline *hooks.Pipeline
	sessionManager        *session.SessionManager
	conversationRegistry  *conversation.Registry
	metricsStore          *metrics.SQLiteStore
	metricsByKind         map[scheduler.ChannelKind]*metrics.MetricsManager
	pricingStore          *pricing.Store
	channelScheduler      *scheduler.ChannelScheduler
	adaptiveScheduler     *scheduler.AdaptiveScheduler
	piAgentAPI            *handlers.PiAgentAPI
	skillsAPI             *handlers.SkillsAPI
	assistantService      *assistant.Service
	assistantAPI          *handlers.AssistantAPI
	evalService           *eval.Service
	engine                *gin.Engine
}

// newApp 构造并装配全部组件：基础设施 → 存储 → 调度器 → 路由。
// 只做装配，不启动监听；进程退出统一走 shutdown。
func newApp() (*app, error) {
	a := &app{}
	var err error

	// 初始化配置管理器
	a.envCfg = config.NewEnvConfig()

	// 初始化日志系统（必须在其他初始化之前）
	a.logStore, err = logger.Setup(&logger.Config{
		DBPath:  a.envCfg.LogDBPath,
		Console: a.envCfg.LogToConsole,
	})
	if err != nil {
		return nil, fmt.Errorf("初始化日志系统失败: %w", err)
	}
	a.requestLogStore = metrics.NewRequestLogStore(a.logStore)

	a.cfgManager, err = config.NewConfigManager(".config/config.json")
	if err != nil {
		return nil, fmt.Errorf("初始化配置管理器失败: %w", err)
	}

	// 模型单价表：内置价格在代码里，用户改写与删除墓碑在 .config/pricing.json。
	// 加载失败一律中止启动，不降级成空表——空价表会让全部花费静默变 0，
	// 界面上看不出是"没配价"还是"配置坏了"。
	a.pricingStore, err = pricing.NewStore(pricing.DefaultFilePath)
	if err != nil {
		return nil, fmt.Errorf("初始化模型单价表失败: %w", err)
	}

	a.blockedStore, err = sensitive.NewBlockedStore(".config/blocked-logs.db")
	if err != nil {
		return nil, fmt.Errorf("初始化内容安全拦截记录存储失败: %w", err)
	}
	log.Printf("[ContentSafety-Init] 拦截记录存储已初始化")

	// 初始化会话管理器（Responses API 专用），与对话记录共用持久化数据库。
	a.sessionManager, err = session.NewPersistentSessionManager(
		".config/conversations.db",
		7*24*time.Hour, // 与对话记录一致，保留 7 天
		0,              // 不按消息数删除整段会话
		0,              // 不按 Token 数删除整段会话
	)
	if err != nil {
		return nil, fmt.Errorf("初始化 Responses 会话持久化存储失败: %w", err)
	}
	a.sessionManager.SetMaxSessions(5000) // 全局最多 5000 个活跃 session
	log.Printf("[Session-Init] 持久化会话管理器已初始化 (保留: 7天, 上限: 5000)")

	// 初始化指标持久化存储（可选）
	if a.envCfg.MetricsPersistenceEnabled {
		a.metricsStore, err = metrics.NewSQLiteStore(&metrics.SQLiteStoreConfig{
			DBPath:        ".config/metrics.db",
			RetentionDays: a.envCfg.MetricsRetentionDays,
		})
		if err != nil {
			log.Printf("[Metrics-Init] 警告: 初始化指标持久化存储失败: %v，将使用纯内存模式", err)
			a.metricsStore = nil
		}
	} else {
		log.Printf("[Metrics-Init] 指标持久化已禁用，使用纯内存模式")
	}

	// 初始化多渠道调度器（Messages、Responses、Gemini、Chat 和 Images 使用独立的指标管理器）
	// 收敛为按 ChannelKind 分组的 map，消除五组几乎相同的初始化代码
	metricsKinds := []scheduler.ChannelKind{
		scheduler.ChannelKindMessages,
		scheduler.ChannelKindResponses,
		scheduler.ChannelKindGemini,
		scheduler.ChannelKindChat,
		scheduler.ChannelKindImages,
	}
	a.metricsByKind = make(map[scheduler.ChannelKind]*metrics.MetricsManager, len(metricsKinds))
	for _, kind := range metricsKinds {
		// apiType 必须无条件传入：花费折算靠它判断 input_tokens 是否已含缓存
		// （pricing.IsCacheInclusiveAPIType），空串会被当成 messages 口径，
		// 于 responses/gemini/chat 上把缓存 Token 重复计一次输入费。
		// 这里先把可选的 metricsStore 收进接口变量：直接传 nil 的 *SQLiteStore
		// 会变成"非 nil 接口值"，AddRecord 里的 store != nil 判断随之失效。
		var store metrics.PersistenceStore
		if a.metricsStore != nil {
			store = a.metricsStore
		}
		a.metricsByKind[kind] = metrics.NewMetricsManagerWithPersistence(
			a.envCfg.MetricsWindowSize, a.envCfg.MetricsFailureThreshold, store, string(kind))
	}

	traceAffinityManager := session.NewTraceAffinityManager()
	a.conversationRegistry, err = conversation.NewPersistentRegistry(".config/conversations.db")
	if err != nil {
		return nil, fmt.Errorf("初始化对话持久化存储失败: %w", err)
	}

	// 初始化 URL 管理器（非阻塞，动态排序）
	urlManager := urlhealth.NewURLManager(30*time.Second, 3) // 30秒冷却期，连续3次失败后移到末尾
	log.Printf("[URLManager-Init] URL管理器已初始化 (冷却期: 30秒, 最大连续失败: 3)")

	a.channelScheduler = scheduler.NewChannelScheduler(
		a.cfgManager,
		a.metricsByKind[scheduler.ChannelKindMessages],
		a.metricsByKind[scheduler.ChannelKindResponses],
		a.metricsByKind[scheduler.ChannelKindGemini],
		a.metricsByKind[scheduler.ChannelKindChat],
		a.metricsByKind[scheduler.ChannelKindImages],
		traceAffinityManager,
		urlManager,
	)
	a.channelScheduler.SetRequestLogStore(a.requestLogStore)
	a.channelScheduler.SetConversationRegistry(a.conversationRegistry)
	log.Printf("[Scheduler-Init] 多渠道调度器已初始化 (失败率阈值: %.0f%%, 滑动窗口: %d)",
		a.metricsByKind[scheduler.ChannelKindMessages].GetFailureThreshold()*100,
		a.metricsByKind[scheduler.ChannelKindMessages].GetWindowSize())

	// 渠道级三态熔断器（internal/circuit）。CIRCUIT_ENABLED=false 一键关闭。
	circuitConfig := circuit.Config{
		FailureThreshold:   a.envCfg.CircuitFailureThreshold,
		ErrorRateThreshold: a.envCfg.CircuitErrorRateThreshold,
		MinRequests:        a.envCfg.CircuitMinRequests,
		Cooldown:           time.Duration(a.envCfg.CircuitCooldownSeconds) * time.Second,
		SuccessThreshold:   a.envCfg.CircuitProbeSuccessThreshold,
	}
	circuitManager := circuit.NewManager(circuitConfig, a.envCfg.CircuitEnabled)
	a.channelScheduler.SetCircuitManager(circuitManager)
	if a.envCfg.CircuitEnabled {
		log.Printf("[Circuit-Init] 渠道级熔断已启用 (连续失败阈值: %d, 错误率: %.0f%%, 最小样本: %d, 冷却: %ds, 半开成功阈值: %d)",
			circuitConfig.FailureThreshold, circuitConfig.ErrorRateThreshold*100, circuitConfig.MinRequests,
			a.envCfg.CircuitCooldownSeconds, circuitConfig.SuccessThreshold)
	} else {
		log.Printf("[Circuit-Init] 渠道级熔断已禁用 (CIRCUIT_ENABLED=false)")
	}

	// 初始化自适应负载均衡
	profileManager := metrics.NewProfileManager()
	a.adaptiveScheduler = scheduler.NewAdaptiveScheduler(profileManager)
	a.channelScheduler.SetProfileManager(profileManager)
	a.channelScheduler.SetAdaptiveScheduler(a.adaptiveScheduler)
	log.Println("[Main] 自适应负载均衡已启用")

	// 危险命令 AI 闸门：存储与拦截记录同库；遗留的 waiting 记录一律按拦截处理
	// （重启后内存等待者已不存在）。llmgate.Caller 供闸门与 AI 助手共用。
	a.gateStore, err = sensitive.NewGateStore(".config/blocked-logs.db")
	if err != nil {
		return nil, fmt.Errorf("初始化危险命令 AI 闸门存储失败: %w", err)
	}
	if expired, expireErr := a.gateStore.ExpireStalePending(context.Background()); expireErr == nil && expired > 0 {
		log.Printf("[Gate-Init] 已将 %d 条重启前遗留的待确认记录标记为过期（按拦截处理）", expired)
	}
	gateCaller := &llmgate.Caller{EnvConfig: a.envCfg, CfgManager: a.cfgManager, ChannelScheduler: a.channelScheduler}
	a.cmdGate = hooks.NewCmdGate(a.cfgManager, gateCaller, a.gateStore, a.blockedStore)
	// 内容安全管道注入闸门；未启用时保持"命中即拦截"的既有行为。
	a.contentSafetyPipeline = hooks.NewContentSafetyPipelineWithRecorder(a.cfgManager, a.blockedStore, a.cmdGate)
	log.Printf("[Gate-Init] 危险命令 AI 闸门已装配")

	// 可逆脱敏持久映射库（Veil ADR 0004 方案）：仅 sensitiveData.mode=mask 时注入。
	// 注入后占位符密钥从主密钥派生（同原文跨重启同占位符），映射 AES-256-GCM 加密落盘；
	// block 模式零落盘（既有行为）。mode 热切换由 watchMappingStoreMode 轮询跟随。
	a.mappingStore, err = sensitive.NewMappingStore(".config/redaction-mappings.db", ".config/master-key")
	if err != nil {
		return nil, fmt.Errorf("初始化可逆脱敏持久映射库失败: %w", err)
	}
	if a.cfgManager.GetSettings().ContentSafety.SensitiveData.Mode == config.ContentSafetyModeMask {
		sensitive.SetPersistentMappingStore(a.mappingStore)
		log.Printf("[SensitiveData-Init] 可逆脱敏持久映射库已启用（跨重启还原，AES-256-GCM 加密落盘）")
	} else {
		log.Printf("[SensitiveData-Init] 敏感数据处置为阻断模式，持久映射库未启用（零落盘）")
	}
	// 还原 miss 的审计回调：合法占位符形态但持久库查不到（如密钥已轮换）→ 原样下发 + 留痕。
	sensitive.SetPersistentMissCallback(func(placeholder string) {
		if _, recordErr := a.blockedStore.Record(context.Background(), sensitive.BlockedLog{
			APIType:       "messages",
			BlockType:     sensitive.BlockTypeRestoreMiss,
			RuleName:      "restore_miss",
			PromptSnippet: "mode=restore_miss source=persistent_store\n合法占位符形态但持久库未命中，已原样下发: " + placeholder,
		}); recordErr != nil {
			log.Printf("[SensitiveData-Restore] 写入还原未命中审计失败: %v", recordErr)
		}
	})
	go a.watchMappingStoreMode()

	// pi-agent 与 skills 管理 API（实例持有各自的互斥锁与存储）
	a.piAgentAPI, err = handlers.NewPiAgentAPI()
	if err != nil {
		return nil, fmt.Errorf("初始化 pi-agent 管理 API 失败: %w", err)
	}
	a.skillsAPI = handlers.NewSkillsAPI()

	// AI 配置助手（复用闸门的 llmgate.Caller：渠道优先级 messages → responses → chat → gemini）
	assistantStore, err := assistant.NewStore(".config/assistant.db")
	if err != nil {
		return nil, fmt.Errorf("初始化 AI 配置助手存储失败: %w", err)
	}
	a.assistantService, err = assistant.NewService(a.cfgManager, gateCaller, assistantStore)
	if err != nil {
		_ = assistantStore.Close()
		return nil, fmt.Errorf("初始化 AI 配置助手失败: %w", err)
	}
	a.assistantAPI = handlers.NewAssistantAPI(a.assistantService)
	log.Printf("[Assistant-Init] AI 配置助手已装配")

	a.evalService, err = eval.NewService(eval.DefaultDBPath, a.cfgManager, a.envCfg, a.channelScheduler)
	if err != nil {
		return nil, fmt.Errorf("初始化评测工作台失败: %w", err)
	}
	a.evalService.Start()

	// 从现有指标同步数据到性能画像
	syncConfig := a.cfgManager.GetConfig()
	kindToUpstreams := []struct {
		kind      scheduler.ChannelKind
		upstreams []config.UpstreamConfig
	}{
		{scheduler.ChannelKindMessages, syncConfig.Upstream},
		{scheduler.ChannelKindResponses, syncConfig.ResponsesUpstream},
		{scheduler.ChannelKindGemini, syncConfig.GeminiUpstream},
		{scheduler.ChannelKindChat, syncConfig.ChatUpstream},
		{scheduler.ChannelKindImages, syncConfig.ImagesUpstream},
	}
	for _, entry := range kindToUpstreams {
		for i := range entry.upstreams {
			syncUpstreamProfiles(a.metricsByKind[entry.kind], profileManager, &entry.upstreams[i], i)
		}
	}
	log.Println("[Main] 已从现有指标同步性能画像数据")

	// 设置 Gin 模式
	if a.envCfg.IsProduction() {
		gin.SetMode(gin.ReleaseMode)
	}

	// 创建路由器（使用自定义 Logger，根据 QUIET_POLLING_LOGS 配置过滤轮询日志）
	r := gin.New()
	r.Use(middleware.FilteredLogger(a.envCfg))
	r.Use(gin.Recovery())

	// 配置 CORS
	r.Use(middleware.CORSMiddleware(a.envCfg))

	// Web UI 访问控制中间件
	r.Use(middleware.WebAuthMiddleware(a.envCfg, a.cfgManager))

	// 健康检查端点（固定路径 /health，与 Dockerfile HEALTHCHECK 保持一致）
	r.GET("/health", handlers.HealthCheck(a.envCfg, a.cfgManager))

	// 配置保存端点
	r.POST("/admin/config/save", handlers.SaveConfigHandler(a.cfgManager))

	// 开发信息端点
	if a.envCfg.IsDevelopment() {
		r.GET("/admin/dev/info", handlers.DevInfo(a.envCfg, a.cfgManager))
	}

	// Web 管理界面 API 路由
	apiGroup := r.Group("/api")
	a.setupAdminAPI(apiGroup)

	// 五协议代理端点
	a.setupProxyEndpoints(r)

	// 静态文件服务 (嵌入的前端)
	if a.envCfg.EnableWebUI {
		handlers.ServeFrontend(r, frontendFS)
	} else {
		// 纯 API 模式
		r.GET("/", func(c *gin.Context) {
			c.JSON(200, gin.H{
				"name":    "API Proxy",
				"mode":    "API Only",
				"version": Version,
				"endpoints": gin.H{
					"chat":      "/v1/chat/completions",
					"config":    "/admin/config/save",
					"gemini":    "/v1beta/models/*modelAction",
					"health":    "/health",
					"images":    "/v1/images/generations",
					"messages":  "/v1/messages",
					"responses": "/v1/responses",
				},
				"message": "Web界面已禁用，此服务器运行在纯API模式下",
			})
		})
	}

	a.engine = r
	return a, nil
}

// setupAdminAPI 注册 Web 管理界面 API 路由（/api 组）。
func (a *app) setupAdminAPI(apiGroup *gin.RouterGroup) {
	apiGroup.GET("/request-logs", handlers.GetRequestLogs(a.requestLogStore, a.pricingStore))
	apiGroup.GET("/system-logs", handlers.GetSystemLogs(a.logStore))
	apiGroup.GET("/system-logs/:requestId", handlers.GetSystemLog(a.logStore))
	apiGroup.GET("/blocked-logs", handlers.GetBlockedLogs(a.blockedStore))
	apiGroup.GET("/blocked-logs/:id", handlers.GetBlockedLog(a.blockedStore))
	apiGroup.DELETE("/blocked-logs/:id", handlers.DeleteBlockedLog(a.blockedStore))
	apiGroup.DELETE("/blocked-logs", handlers.ClearBlockedLogs(a.blockedStore))

	// 危险命令 AI 闸门（待确认列表 / 用户决定 / 会话级白黑名单）
	apiGroup.GET("/security/gate/pending", handlers.GetGatePending(a.cmdGate))
	apiGroup.POST("/security/gate/pending/:id/decision", handlers.DecideGatePending(a.cmdGate))
	apiGroup.GET("/security/gate/decisions", handlers.ListGateDecisions(a.cmdGate))
	apiGroup.DELETE("/security/gate/decisions/:conversationId", handlers.RemoveGateDecisions(a.cmdGate))

	// 模型单价表（内置价 + 用户改写），花费统计的单价来源
	apiGroup.GET("/pricing/models", handlers.GetModelPricing(a.pricingStore))
	apiGroup.POST("/pricing/models", handlers.UpsertModelPricing(a.pricingStore))
	apiGroup.DELETE("/pricing/models", handlers.DeleteModelPricing(a.pricingStore))

	// 渠道管理 API 路由（messages/responses/gemini/chat/images 五组收敛为一次调用）
	// 各渠道类型的 CRUD / key 管理 / pools / reorder / status / promotion / metrics / ping
	// 全部由 handlers.RegisterChannelRoutes 统一注册，差异点通过 ChannelRouteOptions 表达。
	channelDeps := handlers.ChannelRouteDeps{
		Cfg:       a.cfgManager,
		Scheduler: a.channelScheduler,
		MetricsByKind: func(kind scheduler.ChannelKind) *metrics.MetricsManager {
			return a.metricsByKind[kind]
		},
		Prices: a.pricingStore,
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
	apiGroup.GET("/conversations", handlers.ListConversations(a.channelScheduler))
	apiGroup.DELETE("/conversations", handlers.DeleteAllConversations(a.channelScheduler, a.sessionManager))
	apiGroup.GET("/conversations/:id", handlers.GetConversation(a.channelScheduler))
	apiGroup.PUT("/conversations/:id/name", handlers.SetConversationName(a.channelScheduler))
	apiGroup.DELETE("/conversations/:id", handlers.DeleteConversation(a.channelScheduler, a.sessionManager))
	apiGroup.PUT("/conversations/:id/route", handlers.SetConversationRouteOverride(a.channelScheduler, a.cfgManager))
	apiGroup.DELETE("/conversations/:id/route", handlers.ClearConversationRouteOverride(a.channelScheduler))

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

// logStartupInfo 输出启动横幅与服务地址信息。
func (a *app) logStartupInfo() {
	fmt.Printf("\n[Server-Startup] API代理服务器已启动\n")
	fmt.Printf("[Server-Info] 版本: %s\n", Version)
	if BuildTime != "unknown" {
		fmt.Printf("[Server-Info] 构建时间: %s\n", BuildTime)
	}
	if GitCommit != "unknown" {
		fmt.Printf("[Server-Info] Git提交: %s\n", GitCommit)
	}
	fmt.Printf("[Server-Info] 管理界面: http://localhost:%d\n", a.envCfg.Port)
	fmt.Printf("[Server-Info] API 地址: http://localhost:%d/v1\n", a.envCfg.Port)
	fmt.Printf("[Server-Info] Messages: POST /v1/messages\n")
	fmt.Printf("[Server-Info] Responses: POST /v1/responses\n")
	fmt.Printf("[Server-Info] OpenAI Chat: POST /v1/chat/completions\n")
	fmt.Printf("[Server-Info] OpenAI Images: POST /v1/images/{generations|edits|variations}\n")
	fmt.Printf("[Server-Info] Gemini API: POST /v1beta/models/{model}:generateContent\n")
	fmt.Printf("[Server-Info] Gemini API: POST /v1beta/models/{model}:streamGenerateContent\n")
	fmt.Printf("[Server-Info] 健康检查: GET /health\n")
	fmt.Printf("[Server-Info] 环境: %s\n", a.envCfg.Env)
	// 检查是否使用默认密码，给予提示
	if a.envCfg.ProxyAccessKey == "your-proxy-access-key" {
		fmt.Printf("[Server-Warn] 访问密钥: your-proxy-access-key (默认值，建议通过 .env 文件修改)\n")
	}
	fmt.Printf("\n")
}

// watchMappingStoreMode 跟随 sensitiveData.mode 的热切换：
// mask → 注入持久库（新占位符从主密钥派生）；mask 之外 → 撤销注入（停止新写入，
// 还原路径仍可通过已注入的内存映射工作，进程重启后彻底回到零落盘）。
func (a *app) watchMappingStoreMode() {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		mode := a.cfgManager.GetSettings().ContentSafety.SensitiveData.Mode
		enabled := sensitive.PersistentMappingStore() != nil
		want := mode == config.ContentSafetyModeMask
		if want == enabled {
			continue
		}
		if want {
			sensitive.SetPersistentMappingStore(a.mappingStore)
			log.Printf("[SensitiveData-Watch] 已切换到可逆脱敏，持久映射库启用")
		} else {
			sensitive.SetPersistentMappingStore(nil)
			log.Printf("[SensitiveData-Watch] 已离开可逆脱敏，持久映射库停用（停止新写入）")
		}
	}
}

// shutdown 按依赖逆序停止后台组件并关闭持久化存储。
// 顺序约束：必须先停调度器（数据生产者）再关指标存储，
// 避免关闭期间后台循环与存储 Close 产生写并发。
func (a *app) shutdown() {
	a.channelScheduler.Stop()

	if a.evalService != nil {
		a.evalService.Stop()
	}

	if a.metricsStore != nil {
		if err := a.metricsStore.Close(); err != nil {
			log.Printf("[Metrics-Shutdown] 警告: 关闭指标存储时发生错误: %v", err)
		} else {
			log.Println("[Metrics-Shutdown] 指标存储已安全关闭")
		}
	}

	a.conversationRegistry.Stop()
	a.sessionManager.Stop()
	if a.assistantService != nil {
		if err := a.assistantService.Close(); err != nil {
			log.Printf("[Assistant-Shutdown] 关闭 AI 配置助手存储失败: %v", err)
		}
	}
	if err := a.blockedStore.Close(); err != nil {
		log.Printf("[ContentSafety-Shutdown] 关闭拦截记录存储失败: %v", err)
	}
	if a.gateStore != nil {
		if err := a.gateStore.Close(); err != nil {
			log.Printf("[Gate-Shutdown] 关闭闸门存储失败: %v", err)
		}
	}
	if a.mappingStore != nil {
		if err := a.mappingStore.Close(); err != nil {
			log.Printf("[SensitiveData-Shutdown] 关闭持久映射库失败: %v", err)
		}
	}
	a.cfgManager.Close()
	a.requestLogStore.Close()
	if err := a.logStore.Close(); err != nil {
		fmt.Fprintf(os.Stderr, "[Logger-Shutdown] 关闭日志存储失败: %v\n", err)
	}
}

func main() {
	loadedEnvFiles := config.LoadDotEnv()
	if len(loadedEnvFiles) == 0 {
		log.Println("没有找到 .env 文件，使用环境变量或默认值")
	} else {
		log.Printf("[Env-Load] 已加载: %s", strings.Join(loadedEnvFiles, ", "))
	}

	if len(os.Args) > 1 && os.Args[1] == "logs" {
		if err := cli.RunLogs(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
			os.Exit(1)
		}
		return
	}

	// 设置版本信息到 handlers 包
	handlers.SetVersionInfo(Version, BuildTime, GitCommit)

	application, err := newApp()
	if err != nil {
		log.Fatalf("%v", err)
	}

	addr := fmt.Sprintf(":%d", application.envCfg.Port)
	application.logStartupInfo()

	// 创建 HTTP 服务器
	srv := &http.Server{
		Addr:              addr,
		Handler:           application.engine,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	// 用于传递关闭结果
	shutdownDone := make(chan struct{})

	// 优雅关闭：监听系统信号
	go func() {
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
		<-sigChan
		signal.Stop(sigChan) // 停止信号监听，避免资源泄漏

		log.Println("[Server-Shutdown] 收到关闭信号，正在优雅关闭服务器...")

		// 创建超时上下文
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		if err := srv.Shutdown(ctx); err != nil {
			log.Printf("[Server-Shutdown] 警告: 服务器关闭时发生错误: %v", err)
		} else {
			log.Println("[Server-Shutdown] 服务器已安全关闭")
		}

		application.shutdown()
		close(shutdownDone)
	}()

	// 启动服务器（阻塞直到关闭）
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("服务器启动失败: %v", err)
	}

	// 等待关闭完成（带超时保护，避免死锁）
	select {
	case <-shutdownDone:
		// 正常关闭完成
	case <-time.After(15 * time.Second):
		log.Println("[Server-Shutdown] 警告: 等待关闭超时")
	}
}

func syncUpstreamProfiles(mm *metrics.MetricsManager, pm *metrics.ProfileManager, upstream *config.UpstreamConfig, channelIdx int) {
	if mm == nil || pm == nil || upstream == nil || len(upstream.APIKeys) == 0 {
		return
	}

	models := make(map[string]struct{})
	if model := strings.TrimSpace(upstream.DefaultModel); model != "" {
		models[model] = struct{}{}
	}
	for sourceModel, targetModels := range upstream.ModelMapping {
		if source := strings.TrimSpace(sourceModel); source != "" && source != "*" {
			models[source] = struct{}{}
		}
		for _, targetModel := range targetModels {
			if target := strings.TrimSpace(targetModel); target != "" {
				models[target] = struct{}{}
			}
		}
	}
	if len(models) == 0 {
		return
	}

	for _, baseURL := range upstream.GetAllBaseURLs() {
		for model := range models {
			mm.SyncToProfile(pm, baseURL, upstream.APIKeys, model, channelIdx)
		}
	}
}
