package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	apiproxy "github.com/insistanan/api-proxy"
	"github.com/insistanan/api-proxy/internal/admin/assistant"
	"github.com/insistanan/api-proxy/internal/dialogue/conversation"
	"github.com/insistanan/api-proxy/internal/dialogue/intent"
	"github.com/insistanan/api-proxy/internal/dialogue/session"
	"github.com/insistanan/api-proxy/internal/handlers/admin"
	"github.com/insistanan/api-proxy/internal/handlers/clients"
	"github.com/insistanan/api-proxy/internal/handlers/hooks"
	"github.com/insistanan/api-proxy/internal/handlers/system"
	"github.com/insistanan/api-proxy/internal/infra/cli"
	"github.com/insistanan/api-proxy/internal/infra/config"
	"github.com/insistanan/api-proxy/internal/infra/middleware"
	"github.com/insistanan/api-proxy/internal/orchestration/circuit"
	"github.com/insistanan/api-proxy/internal/orchestration/scheduler"
	"github.com/insistanan/api-proxy/internal/orchestration/urlhealth"
	"github.com/insistanan/api-proxy/internal/safety/llmgate"
	"github.com/insistanan/api-proxy/internal/safety/sensitive"
	"github.com/insistanan/api-proxy/internal/storage/logger"
	"github.com/insistanan/api-proxy/internal/storage/metrics"
	"github.com/insistanan/api-proxy/internal/storage/sqlitedb"
)

// app 聚合一次进程运行的全部组件。
// 资源在创建成功后登记清理；启动失败与正常关闭均由同一登记表逆序释放。
type app struct {
	intentService         *intent.Service
	envCfg                *config.EnvConfig
	cfgManager            *config.ConfigManager
	logStore              *logger.Store
	requestLogStore       *metrics.RequestLogStore
	blockedStore          *sensitive.BlockedStore
	gateStore             *sensitive.GateStore
	mappingStore          *sensitive.MappingStore
	mappingWatchStop      chan struct{}
	mappingWatchDone      chan struct{}
	cmdGate               *hooks.CmdGate
	contentSafetyPipeline *hooks.Pipeline
	sessionManager        *session.SessionManager
	conversationRegistry  *conversation.Registry
	metricsStore          *metrics.SQLiteStore
	metricsByKind         map[scheduler.ChannelKind]*metrics.MetricsManager
	channelScheduler      *scheduler.ChannelScheduler
	profileManager        *metrics.ProfileManager
	piAgentAPI            *clients.PiAgentAPI
	skillsAPI             *clients.SkillsAPI
	mcpAPI                *clients.MCPAPI
	assistantService      *assistant.Service
	assistantAPI          *admin.AssistantAPI
	disguiseSyncer        *admin.DisguiseSyncer
	engine                *gin.Engine
	cleanups              []appCleanup
	shutdownOnce          sync.Once
	shutdownErr           error
}

// newApp 构造并装配全部组件：基础设施 → 存储 → 调度器 → 路由。
// 只做装配，不启动监听；进程退出统一走 shutdown。
// 初始化中途失败时按逆序清理已创建资源（方案 R4-b 第 6 步），
// 不把半初始化的资源泄漏给操作系统回收。
func newApp() (_ *app, err error) {
	a := &app{}
	defer func() {
		if err != nil {
			err = errors.Join(err, a.shutdown())
		}
	}()

	// 初始化配置管理器
	a.envCfg = config.NewEnvConfig()

	// 先加载并迁移设置，日志路径和指标保留期必须在存储初始化前确定。
	a.cfgManager, err = config.NewConfigManager(".config/config.json")
	if err != nil {
		return nil, fmt.Errorf("初始化配置管理器失败: %w", err)
	}
	a.envCfg = a.envCfg.WithSettings(a.cfgManager)
	if err := a.cfgManager.EnsureFreeChannels(); err != nil {
		return nil, errors.Join(fmt.Errorf("初始化免费分组失败: %w", err), a.cfgManager.Close())
	}

	// 初始化日志系统；清理仍按配置生产者 → 日志存储的顺序执行。
	a.logStore, err = logger.Setup(&logger.Config{
		DBPath:  a.envCfg.LogDBPath,
		Console: a.envCfg.LogToConsole,
	})
	if err != nil {
		return nil, errors.Join(fmt.Errorf("初始化日志系统失败: %w", err), a.cfgManager.Close())
	}
	a.registerCleanup("日志存储", a.logStore.Close)
	a.requestLogStore = metrics.NewRequestLogStore(a.logStore)

	a.registerCleanup("配置管理器", a.cfgManager.Close)

	blockedDB, err := sqlitedb.Open(".config/blocked-logs.db")
	if err != nil {
		return nil, err
	}
	a.registerCleanup("内容安全数据库", blockedDB.Close)
	a.blockedStore, err = sensitive.NewBlockedStoreWithDB(blockedDB)
	if err != nil {
		return nil, fmt.Errorf("初始化内容安全拦截记录存储失败: %w", err)
	}
	a.registerCleanup("拦截记录存储", a.blockedStore.Close)
	log.Printf("[ContentSafety-Init] 拦截记录存储已初始化")

	// 初始化会话管理器（Responses API 专用），与对话记录共用持久化数据库。
	conversationDB, err := sqlitedb.Open(".config/conversations.db")
	if err != nil {
		return nil, err
	}
	a.registerCleanup("对话数据库", conversationDB.Close)
	a.sessionManager, err = session.NewPersistentSessionManagerWithDB(
		conversationDB,
		7*24*time.Hour, // 与对话记录一致，保留 7 天
		0,              // 不按消息数删除整段会话
		0,              // 不按 Token 数删除整段会话
	)
	if err != nil {
		return nil, fmt.Errorf("初始化 Responses 会话持久化存储失败: %w", err)
	}
	a.registerCleanup("Responses 会话", func() error { a.sessionManager.Stop(); return nil })
	a.sessionManager.SetMaxSessions(5000) // 全局最多 5000 个活跃 session
	log.Printf("[Session-Init] 持久化会话管理器已初始化 (保留: 7天, 上限: 5000)")

	// 对话存储先于调度组件创建，关闭时必须等依赖它的生产者退出。
	a.conversationRegistry, err = conversation.NewPersistentRegistryWithDB(conversationDB)
	if err != nil {
		return nil, fmt.Errorf("初始化对话持久化存储失败: %w", err)
	}
	a.registerCleanup("对话注册表", func() error { a.conversationRegistry.Stop(); return nil })

	// 初始化指标持久化存储（可选）
	if a.envCfg.MetricsPersistenceEnabled {
		a.metricsStore, err = metrics.NewSQLiteStore(&metrics.SQLiteStoreConfig{
			DBPath:        ".config/metrics.db",
			RetentionDays: a.envCfg.MetricsRetentionDays,
			RetentionDaysProvider: func() int {
				return a.cfgManager.GetGatewaySettings().MetricsRetentionDays
			},
		})
		if err != nil {
			return nil, fmt.Errorf("初始化指标持久化存储失败: %w", err)
		}
		a.registerCleanup("指标存储", a.metricsStore.Close)
	} else {
		log.Printf("[Metrics-Init] 指标持久化已禁用，使用纯内存模式")
	}

	// 每类协议入口分别使用独立的指标管理器，共用渠道调度器。
	// 启用指标持久化时，各管理器使用同一份存储，并按入口类型区分记录。
	metricsKinds := []scheduler.ChannelKind{
		scheduler.ChannelKindMessages,
		scheduler.ChannelKindResponses,
		scheduler.ChannelKindGemini,
		scheduler.ChannelKindChat,
		scheduler.ChannelKindImages,
		scheduler.ChannelKindEmbeddings,
		scheduler.ChannelKindDecisions,
	}
	a.metricsByKind = make(map[scheduler.ChannelKind]*metrics.MetricsManager, len(metricsKinds))
	var traceAffinityManager *session.TraceAffinityManager
	// 构造调度器前由装配层持有组件，成功后由调度器聚合停止，只有一个清理入口。
	a.registerCleanup("调度与观测", func() error {
		if a.channelScheduler != nil {
			a.channelScheduler.Stop()
		} else {
			for _, mm := range a.metricsByKind {
				mm.Stop()
			}
			if traceAffinityManager != nil {
				traceAffinityManager.Stop()
			}
		}
		return nil
	})
	for _, kind := range metricsKinds {
		// apiType 必须无条件传入：空串会被当成 messages 口径，
		// 影响 Token 统计的缓存口径归属。
		// 这里先把可选的 metricsStore 收进接口变量：直接传 nil 的 *SQLiteStore
		// 会变成"非 nil 接口值"，AddRecord 里的 store != nil 判断随之失效。
		var store metrics.PersistenceStore
		if a.metricsStore != nil {
			store = a.metricsStore
		}
		a.metricsByKind[kind] = metrics.NewMetricsManagerWithPersistence(
			a.envCfg.MetricsWindowSize, a.envCfg.MetricsFailureThreshold, store, string(kind))
	}

	traceAffinityManager = session.NewTraceAffinityManager()

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
		a.metricsByKind[scheduler.ChannelKindEmbeddings],
		a.metricsByKind[scheduler.ChannelKindDecisions],
		traceAffinityManager,
		urlManager,
	)
	a.channelScheduler.SetRequestLogStore(a.requestLogStore)
	a.channelScheduler.SetConversationRegistry(a.conversationRegistry)
	log.Printf("[Scheduler-Init] 多渠道调度器已初始化 (失败率阈值: %.0f%%, 滑动窗口: %d)",
		a.metricsByKind[scheduler.ChannelKindMessages].GetFailureThreshold()*100,
		a.metricsByKind[scheduler.ChannelKindMessages].GetWindowSize())

	// 渠道级三态熔断器使用 EnvConfig 的内部默认策略，不从旧 CIRCUIT_* 环境变量读取。
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

	// 性能画像与渠道性能报告（/api/performance/report）：仅用于观测展示，
	// 不参与生产选渠——默认遵循配置顺序，健康促销在分组内临时优先。
	a.profileManager = metrics.NewProfileManager()
	a.channelScheduler.SetProfileManager(a.profileManager)
	log.Println("[Main] 渠道性能画像已启用（仅观测展示，默认按配置顺序选渠，健康促销在分组内临时优先）")

	// 危险命令 AI 闸门：存储与拦截记录同库；遗留的 waiting 记录一律按拦截处理
	// （重启后内存等待者已不存在）。llmgate.Caller 供闸门与 AI 助手共用。
	a.gateStore, err = sensitive.NewGateStoreWithDB(blockedDB)
	if err != nil {
		return nil, fmt.Errorf("初始化危险命令 AI 闸门存储失败: %w", err)
	}
	a.registerCleanup("命令闸门存储", a.gateStore.Close)
	if expired, expireErr := a.gateStore.ExpireStalePending(context.Background()); expireErr != nil {
		return nil, fmt.Errorf("清理重启前命令确认记录失败: %w", expireErr)
	} else if expired > 0 {
		log.Printf("[Gate-Init] 已将 %d 条重启前遗留的待确认记录标记为过期（按拦截处理）", expired)
	}
	gateCaller := &llmgate.Caller{
		EnvConfig:        a.envCfg,
		CfgManager:       a.cfgManager,
		ChannelScheduler: a.channelScheduler,
		RequestLogStore:  a.requestLogStore, // R4-b 装配层注入，不经调度器定位器
	}
	a.cmdGate = hooks.NewCmdGate(a.cfgManager, gateCaller, a.gateStore, a.blockedStore)
	localEmbedding, err := intent.NewLocal(".config/intent-embedding")
	if err != nil {
		return nil, err
	}
	a.intentService, err = intent.New(a.cfgManager, gateCaller, ".config/intent.db", localEmbedding)
	if err != nil {
		return nil, err
	}
	a.registerCleanup("意图会话存储", a.intentService.Close)
	a.registerCleanup("本地向量助手", localEmbedding.Stop)
	// 内容安全管道注入闸门；未启用时保持"命中即拦截"的既有行为。
	a.contentSafetyPipeline = hooks.NewContentSafetyPipelineWithSemantic(a.cfgManager, a.blockedStore, a.cmdGate, a.intentService.Datasets)
	log.Printf("[Gate-Init] 危险命令 AI 闸门已装配")

	// 持久映射库在启动时初始化，以便运行中切换脱敏模式（Veil ADR 0004）。
	// 只有 mask 模式会向请求注入映射库：占位符密钥从主密钥派生，
	// 映射以 AES-256-GCM 加密保存。block 模式不写入脱敏映射，拦截记录仍正常保存。
	a.mappingStore, err = sensitive.NewMappingStore(".config/redaction-mappings.db", ".config/master-key")
	if err != nil {
		return nil, fmt.Errorf("初始化可逆脱敏持久映射库失败: %w", err)
	}
	a.registerCleanup("持久映射存储", a.mappingStore.Close)
	if a.cfgManager.GetSettings().ContentSafety.SensitiveData.Mode == config.ContentSafetyModeMask {
		sensitive.SetPersistentMappingStore(a.mappingStore)
		log.Printf("[SensitiveData-Init] 可逆脱敏持久映射库已启用（跨重启还原，AES-256-GCM 加密落盘）")
	} else {
		log.Printf("[SensitiveData-Init] 敏感数据处置为阻断模式，持久映射库未启用（零落盘）")
	}
	// 还原 miss 的审计回调：合法占位符形态但持久库查不到（如密钥已轮换）→ 原样下发 + 留痕。
	sensitive.SetPersistentMissCallback(func(placeholder string) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if _, recordErr := a.blockedStore.Record(ctx, sensitive.BlockedLog{
			APIType:       "messages",
			BlockType:     sensitive.BlockTypeRestoreMiss,
			RuleName:      "restore_miss",
			PromptSnippet: "mode=restore_miss source=persistent_store\n合法占位符形态但持久库未命中，已原样下发: " + placeholder,
		}); recordErr != nil {
			log.Printf("[SensitiveData-Restore] 写入还原未命中审计失败: %v", recordErr)
		}
	})
	a.registerCleanup("脱敏全局绑定", func() error {
		sensitive.SetPersistentMissCallback(nil)
		sensitive.SetPersistentMappingStore(nil)
		return nil
	})

	// pi-agent 与 skills 管理 API（实例持有各自的互斥锁与存储）
	a.piAgentAPI, err = clients.NewPiAgentAPI()
	if err != nil {
		return nil, fmt.Errorf("初始化 pi-agent 管理 API 失败: %w", err)
	}
	a.skillsAPI = clients.NewSkillsAPI()

	// MCP 统一列表与智能体分发（列表含 env/headers 等可能的密钥，存放于 .config 下）
	a.mcpAPI = clients.NewMCPAPI(".config/mcp.json")

	// AI 配置助手（复用闸门的 llmgate.Caller：渠道优先级 messages → responses → chat → gemini）
	assistantStore, err := assistant.NewStore(".config/assistant.db")
	if err != nil {
		return nil, fmt.Errorf("初始化 AI 配置助手存储失败: %w", err)
	}
	a.registerCleanup("AI 配置助手存储", assistantStore.Close)
	a.assistantService, err = assistant.NewService(a.cfgManager, gateCaller, assistantStore)
	if err != nil {
		return nil, fmt.Errorf("初始化 AI 配置助手失败: %w", err)
	}
	a.assistantAPI = admin.NewAssistantAPI(a.assistantService)
	log.Printf("[Assistant-Init] AI 配置助手已装配")

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
		{scheduler.ChannelKindEmbeddings, syncConfig.EmbeddingsUpstream},
		{scheduler.ChannelKindDecisions, syncConfig.DecisionsUpstream},
	}
	for _, entry := range kindToUpstreams {
		for i := range entry.upstreams {
			syncUpstreamProfiles(a.metricsByKind[entry.kind], a.profileManager, &entry.upstreams[i], i)
		}
	}
	log.Println("[Main] 已从现有指标同步性能画像数据")

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

	// 管理端点显式绑定管理鉴权（R1：不依赖 WebAuthMiddleware 的路径判断）
	adminGroup := r.Group("/admin", middleware.AdminAuthMiddleware(a.envCfg))
	// 配置保存端点
	adminGroup.POST("/config/save", system.SaveConfigHandler(a.cfgManager))

	// 开发信息端点
	if a.envCfg.IsDevelopment() {
		adminGroup.GET("/dev/info", system.DevInfo(a.envCfg, a.cfgManager))
	}

	// Web 管理界面 API 路由（管理鉴权在路由组上显式绑定）
	a.disguiseSyncer = admin.StartDisguiseAutoSync(a.cfgManager)
	a.registerCleanup("伪装版本自动同步", func() error {
		a.disguiseSyncer.Stop()
		return nil
	})
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

	a.engine = r
	a.mappingWatchStop = make(chan struct{})
	a.mappingWatchDone = make(chan struct{})
	go a.watchMappingStoreMode()
	a.registerCleanup("脱敏后台跟随", func() error {
		close(a.mappingWatchStop)
		<-a.mappingWatchDone
		return nil
	})

	return a, nil
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
	system.SetVersionInfo(Version, BuildTime, GitCommit)

	if err := runServer(); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
}

// runServer 保证监听失败、收到信号和正常退出都先回收资源，再由 main 决定退出码。
func runServer() (err error) {
	application, err := newApp()
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, application.shutdown()) }()

	addr := fmt.Sprintf(":%d", application.envCfg.Port)
	application.logStartupInfo()
	srv := &http.Server{
		Addr:              addr,
		Handler:           application.engine,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	return serveUntilStopped(ctx, srv, 10*time.Second)
}
