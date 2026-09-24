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
	"github.com/BenedictKing/api-proxy/internal/handlers/hooks"
	"github.com/BenedictKing/api-proxy/internal/llmgate"
	"github.com/BenedictKing/api-proxy/internal/logger"
	"github.com/BenedictKing/api-proxy/internal/metrics"
	"github.com/BenedictKing/api-proxy/internal/middleware"
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
	mappingWatchStop      chan struct{}
	mappingWatchDone      chan struct{}
	cmdGate               *hooks.CmdGate
	contentSafetyPipeline *hooks.Pipeline
	sessionManager        *session.SessionManager
	conversationRegistry  *conversation.Registry
	metricsStore          *metrics.SQLiteStore
	metricsByKind         map[scheduler.ChannelKind]*metrics.MetricsManager
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
// 初始化中途失败时按逆序清理已创建资源（方案 R4-b 第 6 步），
// 不把半初始化的资源泄漏给操作系统回收。
func newApp() (*app, error) {
	a := &app{}
	var err error

	// cleanups 收集已创建资源的清理动作；失败时逆序执行。
	var cleanups []func()
	defer func() {
		if err != nil {
			for i := len(cleanups) - 1; i >= 0; i-- {
				cleanups[i]()
			}
		}
	}()
	registerCleanup := func(fn func()) {
		cleanups = append(cleanups, fn)
	}

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
	registerCleanup(func() { _ = a.logStore.Close() })
	a.requestLogStore = metrics.NewRequestLogStore(a.logStore)

	a.cfgManager, err = config.NewConfigManager(".config/config.json")
	if err != nil {
		return nil, fmt.Errorf("初始化配置管理器失败: %w", err)
	}
	registerCleanup(func() { _ = a.cfgManager.Close() })

	a.blockedStore, err = sensitive.NewBlockedStore(".config/blocked-logs.db")
	if err != nil {
		return nil, fmt.Errorf("初始化内容安全拦截记录存储失败: %w", err)
	}
	registerCleanup(func() { _ = a.blockedStore.Close() })
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
	registerCleanup(a.sessionManager.Stop)
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

	traceAffinityManager := session.NewTraceAffinityManager()
	a.conversationRegistry, err = conversation.NewPersistentRegistry(".config/conversations.db")
	if err != nil {
		return nil, fmt.Errorf("初始化对话持久化存储失败: %w", err)
	}
	registerCleanup(a.conversationRegistry.Stop)

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
	// 触发条件只有"连续失败达到阈值"：错误率不再参与熔断判定（失败率仅供展示）。
	circuitConfig := circuit.Config{
		FailureThreshold: a.envCfg.CircuitFailureThreshold,
		Cooldown:         time.Duration(a.envCfg.CircuitCooldownSeconds) * time.Second,
		SuccessThreshold: a.envCfg.CircuitProbeSuccessThreshold,
	}
	circuitManager := circuit.NewManager(circuitConfig, a.envCfg.CircuitEnabled)
	a.channelScheduler.SetCircuitManager(circuitManager)
	if a.envCfg.CircuitEnabled {
		log.Printf("[Circuit-Init] 渠道级熔断已启用 (连续失败阈值: %d, 冷却: %ds, 半开成功阈值: %d)",
			circuitConfig.FailureThreshold, a.envCfg.CircuitCooldownSeconds, circuitConfig.SuccessThreshold)
	} else {
		log.Printf("[Circuit-Init] 渠道级熔断已禁用 (CIRCUIT_ENABLED=false)")
	}

	// 性能画像与渠道性能报告（/api/performance/report）：仅用于观测展示，
	// 不参与生产选渠——生产选渠严格按配置顺序（R6：修正历史误导日志）。
	profileManager := metrics.NewProfileManager()
	a.adaptiveScheduler = scheduler.NewAdaptiveScheduler(profileManager)
	a.channelScheduler.SetProfileManager(profileManager)
	a.channelScheduler.SetAdaptiveScheduler(a.adaptiveScheduler)
	log.Println("[Main] 渠道性能画像已启用（仅观测展示，选渠按配置顺序）")

	// 危险命令 AI 闸门：存储与拦截记录同库；遗留的 waiting 记录一律按拦截处理
	// （重启后内存等待者已不存在）。llmgate.Caller 供闸门与 AI 助手共用。
	a.gateStore, err = sensitive.NewGateStore(".config/blocked-logs.db")
	if err != nil {
		return nil, fmt.Errorf("初始化危险命令 AI 闸门存储失败: %w", err)
	}
	registerCleanup(func() { _ = a.gateStore.Close() })
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
	registerCleanup(func() { _ = a.mappingStore.Close() })
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
	registerCleanup(func() { _ = a.assistantService.Close() })
	a.assistantAPI = handlers.NewAssistantAPI(a.assistantService)
	log.Printf("[Assistant-Init] AI 配置助手已装配")

	a.evalService, err = eval.NewService(eval.DefaultDBPath, a.cfgManager, a.envCfg, a.channelScheduler)
	if err != nil {
		return nil, fmt.Errorf("初始化评测工作台失败: %w", err)
	}
	a.evalService.Start()
	registerCleanup(a.evalService.Stop)

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

	// 管理端点显式绑定管理鉴权（R1：不依赖 WebAuthMiddleware 的路径判断）
	adminGroup := r.Group("/admin", middleware.AdminAuthMiddleware(a.envCfg))
	// 配置保存端点
	adminGroup.POST("/config/save", handlers.SaveConfigHandler(a.cfgManager))

	// 开发信息端点
	if a.envCfg.IsDevelopment() {
		adminGroup.GET("/dev/info", handlers.DevInfo(a.envCfg, a.cfgManager))
	}

	// Web 管理界面 API 路由（管理鉴权在路由组上显式绑定）
	apiGroup := r.Group("/api", middleware.AdminAuthMiddleware(a.envCfg))
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
	a.mappingWatchStop = make(chan struct{})
	a.mappingWatchDone = make(chan struct{})
	go a.watchMappingStoreMode()
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
