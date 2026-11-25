package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"path/filepath"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	adminextensions "github.com/insistanan/api-proxy/internal/api/admin/extensions"
	adminsettings "github.com/insistanan/api-proxy/internal/api/admin/settings"
	"github.com/insistanan/api-proxy/internal/clients"
	"github.com/insistanan/api-proxy/internal/dialogue/conversation"
	"github.com/insistanan/api-proxy/internal/dialogue/routing/intent"
	"github.com/insistanan/api-proxy/internal/dialogue/session"
	"github.com/insistanan/api-proxy/internal/infra/config"
	"github.com/insistanan/api-proxy/internal/infra/httpclient"
	"github.com/insistanan/api-proxy/internal/management/forest"
	"github.com/insistanan/api-proxy/internal/orchestration/cachekeepalive"
	"github.com/insistanan/api-proxy/internal/orchestration/circuit"
	"github.com/insistanan/api-proxy/internal/orchestration/scheduler"
	"github.com/insistanan/api-proxy/internal/orchestration/urlhealth"
	"github.com/insistanan/api-proxy/internal/protocol/tokenusage"
	"github.com/insistanan/api-proxy/internal/proxycore"
	"github.com/insistanan/api-proxy/internal/safety/llmgate"
	"github.com/insistanan/api-proxy/internal/safety/pipeline"
	"github.com/insistanan/api-proxy/internal/safety/sensitive"
	"github.com/insistanan/api-proxy/internal/storage/logger"
	"github.com/insistanan/api-proxy/internal/storage/metrics"
	"github.com/insistanan/api-proxy/internal/storage/sqlitedb"
	"github.com/insistanan/api-proxy/internal/storage/usageledger"
)

// app 聚合一次进程运行的全部组件。
// 资源创建后立即登记；停机先停止后台生产者，再逆序释放存储。
type app struct {
	intentService         *intent.Service
	envCfg                *config.EnvConfig
	cfgManager            *config.ConfigManager
	logStore              *logger.Store
	requestLogStore       *metrics.RequestLogStore
	usageLedger           *usageledger.Store
	forest                *forest.Store
	blockedStore          *sensitive.BlockedStore
	gateStore             *sensitive.GateStore
	mappingStore          *sensitive.MappingStore
	cmdGate               *pipeline.CmdGate
	contentSafetyPipeline *pipeline.Pipeline
	sessionManager        *session.SessionManager
	conversationRegistry  *conversation.Registry
	metricsStore          *metrics.SQLiteStore
	metricsByKind         map[scheduler.ChannelKind]*metrics.MetricsManager
	channelScheduler      *scheduler.ChannelScheduler
	cacheKeepalive        *cachekeepalive.Manager
	profileManager        *metrics.ProfileManager
	piAgentAPI            *clients.PiAgentAPI
	skillsAPI             *clients.SkillsAPI
	skillTranslator       clients.SkillTranslator
	mcpAPI                *clients.MCPAPI
	extensionsGitAPI      *adminextensions.ExtensionsGitAPI
	disguiseSyncer        *adminsettings.DisguiseSyncer
	engine                *gin.Engine
	cleanups              []appCleanup
	workers               []appCleanup
	shutdownOnce          sync.Once
	shutdownErr           error
}

// newApp 构造并装配全部组件：基础设施 → 存储 → 调度器 → 路由。
// 只做装配，不启动监听；进程退出统一走 shutdown。
// 初始化中途失败也遵循生产者先于存储退出的依赖顺序。
func newApp() (_ *app, err error) {
	a := &app{}
	a.registerCleanup("上游 HTTP 连接池", func() error {
		httpclient.GetManager().CloseIdleConnections()
		return nil
	})
	defer func() {
		if err != nil {
			err = errors.Join(err, a.shutdown())
		}
	}()

	if err := a.initConfigAndLogs(); err != nil {
		return nil, err
	}
	a.initUsageJournal()
	if err := a.initStores(); err != nil {
		return nil, err
	}
	if err := a.initScheduling(); err != nil {
		return nil, err
	}
	gateCaller := &llmgate.Caller{
		EnvConfig:        a.envCfg,
		CfgManager:       a.cfgManager,
		ChannelScheduler: a.channelScheduler,
		RequestLogStore:  a.requestLogStore,
	}
	if err := a.initSafety(gateCaller); err != nil {
		return nil, err
	}
	a.cacheKeepalive = cachekeepalive.New(a.cfgManager, proxycore.CheckCacheKeepalivePrompt(a.contentSafetyPipeline), func(fact tokenusage.Fact) {
		if a.usageLedger != nil {
			a.usageLedger.Record(fact)
		}
	})
	a.channelScheduler.SetCacheKeepalive(a.cacheKeepalive)
	a.registerWorker("缓存保活", func() error { a.cacheKeepalive.Stop(); return nil })
	if err := a.initClients(gateCaller); err != nil {
		return nil, err
	}
	a.disguiseSyncer = adminsettings.StartDisguiseAutoSync(a.cfgManager)
	a.registerWorker("伪装版本自动同步", func() error {
		a.disguiseSyncer.Stop()
		return nil
	})
	a.engine = a.setupHTTP()

	return a, nil
}

func (a *app) initConfigAndLogs() error {
	var err error
	// 初始化配置管理器
	a.envCfg, err = config.LoadEnvConfig()
	if err != nil {
		return fmt.Errorf("加载启动配置失败: %w", err)
	}

	// 先加载并迁移设置，日志路径和指标保留期必须在存储初始化前确定。
	a.cfgManager, err = config.NewConfigManager(".config/config.json")
	if err != nil {
		return fmt.Errorf("初始化配置管理器失败: %w", err)
	}
	a.registerWorker("配置管理器", a.cfgManager.Close)
	a.envCfg = a.envCfg.WithSettings(a.cfgManager)
	if err := a.cfgManager.EnsureFreeChannels(); err != nil {
		return fmt.Errorf("初始化免费分组失败: %w", err)
	}

	// 初始化日志系统；清理仍按配置生产者 → 日志存储的顺序执行。
	a.logStore, err = logger.Setup(&logger.Config{
		DBPath:  a.envCfg.LogDBPath,
		Console: a.envCfg.LogToConsole,
		PolicyProvider: func() logger.StoragePolicy {
			settings := a.cfgManager.GetGatewaySettings()
			return logger.StoragePolicy{RuntimeDays: settings.LogRuntimeRetentionDays, CallDays: settings.LogCallRetentionDays, BodyDays: settings.LogBodyRetentionDays, BudgetMB: settings.LogBudgetMB}
		},
	})
	if err != nil {
		return fmt.Errorf("初始化日志系统失败: %w", err)
	}
	a.registerCleanup("日志存储", a.logStore.Close)
	a.requestLogStore = metrics.NewRequestLogStore(a.logStore)
	return nil
}

func (a *app) initUsageJournal() {
	const ledgerPath = ".config/usage-ledger.db"
	ledger, ledgerErr := usageledger.Open(ledgerPath)
	if ledgerErr != nil {
		log.Printf("[UsageLedger-Open] 用量账本不可用: %v", ledgerErr)
		if err := usageledger.MarkUnavailable(ledgerPath, time.Now()); err != nil {
			log.Printf("[UsageLedger-Open] 保存用量停写标记失败: %v", err)
		}
	} else {
		a.usageLedger = ledger
		a.registerCleanup("用量账本", ledger.Close)
		forestStore, forestErr := forest.Open(".config/forest.db", ledger)
		if forestErr != nil {
			log.Printf("[Forest-Open] 个人森林不可用: %v", forestErr)
		} else {
			a.forest = forestStore
			a.registerWorker("个人森林", forestStore.Close)
		}
	}
}

func (a *app) initStores() error {
	blockedDB, err := sqlitedb.Open(".config/blocked-logs.db")
	if err != nil {
		return err
	}
	a.registerCleanup("内容安全数据库", blockedDB.Close)
	a.blockedStore, err = sensitive.NewBlockedStoreWithDB(blockedDB)
	if err != nil {
		return fmt.Errorf("初始化内容安全拦截记录存储失败: %w", err)
	}
	a.registerCleanup("拦截记录存储", a.blockedStore.Close)
	logInitf(a.envCfg, "[ContentSafety-Init] 拦截记录存储已初始化")

	// 初始化会话管理器（Responses API 专用），与对话记录共用持久化数据库。
	conversationDB, err := sqlitedb.Open(".config/conversations.db")
	if err != nil {
		return err
	}
	a.registerCleanup("对话数据库", conversationDB.Close)
	a.sessionManager, err = session.NewPersistentSessionManagerWithDB(
		conversationDB,
		7*24*time.Hour, // 与对话记录一致，保留 7 天
		0,              // 不按消息数删除整段会话
		0,              // 不按 Token 数删除整段会话
	)
	if err != nil {
		return fmt.Errorf("初始化 Responses 会话持久化存储失败: %w", err)
	}
	a.registerWorker("Responses 会话", func() error { a.sessionManager.Stop(); return nil })
	a.sessionManager.SetMaxSessions(5000) // 全局最多 5000 个活跃 session
	logInitf(a.envCfg, "[Session-Init] 持久化会话管理器已初始化 (保留: 7天, 上限: 5000)")

	// 对话存储先于调度组件创建，关闭时必须等依赖它的生产者退出。
	a.conversationRegistry, err = conversation.NewPersistentRegistryWithDB(conversationDB)
	if err != nil {
		return fmt.Errorf("初始化对话持久化存储失败: %w", err)
	}
	a.registerWorker("对话注册表", func() error { a.conversationRegistry.Stop(); return nil })

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
			return fmt.Errorf("初始化指标持久化存储失败: %w", err)
		}
		a.registerCleanup("指标存储", a.metricsStore.Close)
	} else {
		logInitf(a.envCfg, "[Metrics-Init] 指标持久化已禁用，使用纯内存模式")
	}

	// 历史 API Key 属运行时数据，跟随指标库保存；未启用持久化时继续留在配置文件。
	if a.metricsStore != nil {
		a.cfgManager.SetHistoricalKeyStore(a.metricsStore)
		if err := a.cfgManager.SyncHistoricalKeys(); err != nil {
			return fmt.Errorf("同步历史 API Key 到指标库失败: %w", err)
		}
	}

	// 危险命令 AI 闸门：存储与拦截记录同库；遗留的 waiting 记录一律按拦截处理
	// （重启后内存等待者已不存在）。llmgate.Caller 供闸门与意图识别共用。
	a.gateStore, err = sensitive.NewGateStoreWithDB(blockedDB)
	if err != nil {
		return fmt.Errorf("初始化危险命令 AI 闸门存储失败: %w", err)
	}
	a.registerCleanup("命令闸门存储", a.gateStore.Close)
	if expired, expireErr := a.gateStore.ExpireStalePending(context.Background()); expireErr != nil {
		return fmt.Errorf("清理重启前命令确认记录失败: %w", expireErr)
	} else if expired > 0 {
		log.Printf("[Gate-Init] 已将 %d 条重启前遗留的待确认记录标记为过期（按拦截处理）", expired)
	}
	return nil
}

func (a *app) initScheduling() error {
	// 每类协议入口分别使用独立的指标管理器，共用渠道调度器。
	// 启用指标持久化时，各管理器使用同一份存储，并按入口类型区分记录。
	metricsKinds := scheduler.ChannelKinds()
	a.metricsByKind = make(map[scheduler.ChannelKind]*metrics.MetricsManager, len(metricsKinds))
	// 先处理可选存储，避免将有类型的 nil 指针传入接口。
	var store metrics.PersistenceStore
	if a.metricsStore != nil {
		store = a.metricsStore
	}
	for _, kind := range metricsKinds {
		// apiType 必须无条件传入：空串会被当成 messages 口径，
		// 影响 Token 统计的缓存口径归属。
		a.metricsByKind[kind] = metrics.NewMetricsManagerWithPersistence(
			a.envCfg.MetricsWindowSize, a.envCfg.MetricsFailureThreshold, store, string(kind))
	}

	traceAffinityManager := session.NewTraceAffinityManager()

	// 初始化 URL 管理器（非阻塞，动态排序）
	urlManager := urlhealth.NewURLManager(30*time.Second, 3) // 30秒冷却期，连续3次失败后移到末尾
	logInitf(a.envCfg, "[URLManager-Init] URL管理器已初始化 (冷却期: 30秒, 最大连续失败: 3)")

	a.channelScheduler = scheduler.NewChannelScheduler(
		a.cfgManager,
		a.metricsByKind,
		traceAffinityManager,
		urlManager,
	)
	a.registerWorker("调度与观测", func() error { a.channelScheduler.Stop(); return nil })
	a.channelScheduler.SetRequestLogStore(a.requestLogStore)
	a.channelScheduler.SetConversationRegistry(a.conversationRegistry)
	logInitf(a.envCfg, "[Scheduler-Init] 多渠道调度器已初始化 (失败率阈值: %.0f%%, 滑动窗口: %d)",
		a.metricsByKind[scheduler.ChannelKindMessages].GetFailureThreshold()*100,
		a.metricsByKind[scheduler.ChannelKindMessages].GetWindowSize())

	// 渠道级三态熔断器以 circuit.DefaultConfig() 为唯一默认来源，不从旧 CIRCUIT_* 环境变量读取。
	circuitConfig := circuit.DefaultConfig()
	circuitManager := circuit.NewManager(circuitConfig, true)
	a.channelScheduler.SetCircuitManager(circuitManager)
	logInitf(a.envCfg, "[Circuit-Init] 渠道级熔断已启用 (连续失败阈值: %d, 错误率: %.0f%%, 最小样本: %d, 冷却: %ds, 半开成功阈值: %d)",
		circuitConfig.FailureThreshold, circuitConfig.ErrorRateThreshold*100, circuitConfig.MinRequests,
		int(circuitConfig.Cooldown/time.Second), circuitConfig.SuccessThreshold)

	// 性能画像与渠道性能报告（/api/performance/report）：仅用于观测展示，
	// 不参与生产选渠——默认遵循配置顺序，健康促销在分组内临时优先。
	a.profileManager = metrics.NewProfileManager()
	a.channelScheduler.SetProfileManager(a.profileManager)
	if err := a.channelScheduler.InitializeProfiles(); err != nil {
		return err
	}
	logInitf(a.envCfg, "[Main] 渠道性能画像已启用（仅观测展示，默认按配置顺序选渠，健康促销在分组内临时优先）")
	return nil
}

func (a *app) initSafety(gateCaller *llmgate.Caller) error {
	a.cmdGate = pipeline.NewCmdGate(a.cfgManager, gateCaller, a.gateStore, a.blockedStore)
	localEmbedding, err := intent.NewLocal(".config/intent-embedding")
	if err != nil {
		return err
	}
	a.registerWorker("本地向量助手", localEmbedding.Stop)
	a.intentService, err = intent.New(a.cfgManager, gateCaller, ".config/intent.db", localEmbedding)
	if err != nil {
		return err
	}
	a.registerCleanup("意图会话存储", a.intentService.Close)

	// 持久映射库在启动时初始化，是否使用由每次请求的配置快照决定。
	// 只有 mask 模式会向请求注入映射库：占位符密钥从主密钥派生，
	// 映射以 AES-256-GCM 加密保存。block 模式不写入脱敏映射，拦截记录仍正常保存。
	a.mappingStore, err = sensitive.NewMappingStore(".config/redaction-mappings.db", ".config/master-key")
	if err != nil {
		return fmt.Errorf("初始化可逆脱敏持久映射库失败: %w", err)
	}
	a.registerCleanup("持久映射存储", a.mappingStore.Close)
	a.contentSafetyPipeline = pipeline.NewContentSafetyPipelineWithSemantic(a.cfgManager, a.blockedStore, a.cmdGate, a.intentService.Datasets, a.mappingStore)
	a.registerWorker("安全历史清理", sensitive.StartHistoryRetention(a.blockedStore, a.gateStore))
	return nil
}

func (a *app) initClients(gateCaller *llmgate.Caller) error {
	var err error
	// pi-agent 与 skills 管理 API（实例持有各自的互斥锁与存储）
	a.piAgentAPI, err = clients.NewPiAgentAPI()
	if err != nil {
		return fmt.Errorf("初始化 pi-agent 管理 API 失败: %w", err)
	}
	skillsDirFn := func() (string, error) {
		return config.ResolveSkillsDir(a.cfgManager.GetSettings().Extensions.SkillsDir)
	}
	a.skillsAPI = clients.NewSkillsAPI(skillsDirFn)
	a.registerWorker("技能翻译任务", a.skillsAPI.CloseSkillTranslations)
	a.skillTranslator = clients.NewSkillTranslator(gateCaller)

	// MCP 统一列表与智能体分发（存放在项目 Skills 目录下 mcp.json，便于与 Skills 一起 Git 管理）
	a.mcpAPI = clients.NewDynamicMCPAPI(func() (string, error) {
		dir, err := skillsDirFn()
		if err != nil {
			return "", err
		}
		return filepath.Join(dir, "mcp.json"), nil
	})
	if report, syncErr := a.skillsAPI.SyncOnStartup(); syncErr != nil {
		log.Printf("[Skills-Sync] 启动同步项目 Skills 到 Agent 失败: %v", syncErr)
	} else if report.Synced > 0 || report.Removed > 0 || len(report.Errors) > 0 {
		logInitf(a.envCfg, "[Skills-Sync] 启动同步完成: synced=%d removed=%d skipped=%d errors=%d", report.Synced, report.Removed, report.Skipped, len(report.Errors))
	}
	if syncErr := a.mcpAPI.SyncOnStartup(".config/mcp.json"); syncErr != nil {
		log.Printf("[MCP-Sync] 启动分发 MCP 到 Agent 失败: %v", syncErr)
	}
	a.extensionsGitAPI = adminextensions.NewExtensionsGitAPI(a.cfgManager, a.skillsAPI, a.mcpAPI)
	return nil
}
