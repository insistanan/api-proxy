package scheduler

import (
	"sync"

	"github.com/BenedictKing/api-proxy/internal/circuit"
	"github.com/BenedictKing/api-proxy/internal/config"
	"github.com/BenedictKing/api-proxy/internal/conversation"
	"github.com/BenedictKing/api-proxy/internal/metrics"
	"github.com/BenedictKing/api-proxy/internal/session"
	"github.com/BenedictKing/api-proxy/internal/urlhealth"
)

// ChannelScheduler 多渠道调度器
type ChannelScheduler struct {
	mu                   sync.RWMutex
	configManager        *config.ConfigManager
	metricsManagers      map[ChannelKind]*metrics.MetricsManager // 各渠道类型对应的指标管理器
	channelLogStores     map[ChannelKind]*metrics.ChannelLogStore
	requestLogStore      *metrics.RequestLogStore
	traceAffinity        *session.TraceAffinityManager
	baseURLAffinity      *session.BaseURLAffinityManager
	conversationRegistry *conversation.Registry
	urlManager           *urlhealth.URLManager   // URL 管理器（非阻塞，动态排序）
	profileManager       *metrics.ProfileManager // 性能画像管理器
	circuitManager       *circuit.Manager        // 渠道级三态熔断器（nil 表示未启用）
	// inFlightByKind 记录选渠后、真正发出上游请求前的在途预留。
	// 用于许可生命周期和在途观测，不参与渠道排序。
	inFlightByKind map[ChannelKind]map[int]int64

	// stopOnce 保证聚合停止只执行一次，下属组件也支持并发停止并等待任务退出。
	stopOnce sync.Once
}

// ChannelKind 标识调度器所处理的渠道类型
// 注意：这里的 kind 与 upstream.ServiceType（openai/claude/gemini）不同，
// kind 对应的是本代理对外暴露的一等公民入口：messages / responses / gemini / chat / images。
type ChannelKind string

const (
	ChannelKindMessages  ChannelKind = "messages"
	ChannelKindResponses ChannelKind = "responses"
	ChannelKindGemini    ChannelKind = "gemini"
	ChannelKindChat      ChannelKind = "chat"
	ChannelKindImages    ChannelKind = "images"
)

// NewChannelScheduler 创建多渠道调度器
func NewChannelScheduler(
	cfgManager *config.ConfigManager,
	messagesMetrics *metrics.MetricsManager,
	responsesMetrics *metrics.MetricsManager,
	geminiMetrics *metrics.MetricsManager,
	chatMetrics *metrics.MetricsManager,
	imagesMetrics *metrics.MetricsManager,
	traceAffinity *session.TraceAffinityManager,
	urlMgr *urlhealth.URLManager,
) *ChannelScheduler {
	return &ChannelScheduler{
		configManager: cfgManager,
		metricsManagers: map[ChannelKind]*metrics.MetricsManager{
			ChannelKindMessages:  messagesMetrics,
			ChannelKindResponses: responsesMetrics,
			ChannelKindGemini:    geminiMetrics,
			ChannelKindChat:      chatMetrics,
			ChannelKindImages:    imagesMetrics,
		},
		channelLogStores: map[ChannelKind]*metrics.ChannelLogStore{
			ChannelKindMessages:  metrics.NewChannelLogStore(),
			ChannelKindResponses: metrics.NewChannelLogStore(),
			ChannelKindGemini:    metrics.NewChannelLogStore(),
			ChannelKindChat:      metrics.NewChannelLogStore(),
			ChannelKindImages:    metrics.NewChannelLogStore(),
		},
		traceAffinity:   traceAffinity,
		baseURLAffinity: session.NewBaseURLAffinityManager(),
		urlManager:      urlMgr,
		inFlightByKind:  make(map[ChannelKind]map[int]int64),
	}
}

// SetCircuitManager 注入渠道级熔断器管理器（internal/circuit）。
// 由 main.go 在构造 scheduler 后调用；nil 表示不启用渠道级熔断。
func (s *ChannelScheduler) SetCircuitManager(manager *circuit.Manager) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.circuitManager = manager
}

// CircuitManager 返回渠道级熔断器管理器（可能为 nil；nil 表示未启用）。
func (s *ChannelScheduler) CircuitManager() *circuit.Manager {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.circuitManager
}

// getMetricsManager 根据类型获取对应的指标管理器
func (s *ChannelScheduler) getMetricsManager(kind ChannelKind) *metrics.MetricsManager {
	return s.metricsManagers[kind]
}

// Stop 停止调度器聚合的后台组件（各 MetricsManager 清理循环、Trace/BaseURL
// 亲和清理、性能画像更新），消除进程关闭后残留的 ticker goroutine。
// 幂等，可安全多次调用；必须在 main 关闭持久化存储（metricsStore 等）之前
// 调用，保证"先停数据生产者、再关存储"的顺序。
// 不含 conversationRegistry / sessionManager 等由 main 直接创建并 defer
// 停止的组件；URLManager 为纯内存计算，无后台循环。
func (s *ChannelScheduler) Stop() {
	if s == nil {
		return
	}
	s.stopOnce.Do(func() {
		for _, mm := range s.metricsManagers {
			if mm != nil {
				mm.Stop()
			}
		}
		if s.traceAffinity != nil {
			s.traceAffinity.Stop()
		}
		if s.baseURLAffinity != nil {
			s.baseURLAffinity.Stop()
		}
		if s.profileManager != nil {
			s.profileManager.Stop()
		}
	})
}

func (s *ChannelScheduler) GetChannelLogStore(kind ChannelKind) *metrics.ChannelLogStore {
	return s.channelLogStores[kind]
}

func (s *ChannelScheduler) SetRequestLogStore(store *metrics.RequestLogStore) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requestLogStore = store
}

func (s *ChannelScheduler) GetRequestLogStore() *metrics.RequestLogStore {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.requestLogStore
}

func (s *ChannelScheduler) SetConversationRegistry(registry *conversation.Registry) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.conversationRegistry = registry
}

func (s *ChannelScheduler) GetConversationRegistry() *conversation.Registry {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.conversationRegistry
}

// SetProfileManager 设置性能画像管理器
func (s *ChannelScheduler) SetProfileManager(pm *metrics.ProfileManager) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.profileManager = pm
}

// GetProfileManager 获取性能画像管理器
func (s *ChannelScheduler) GetProfileManager() *metrics.ProfileManager {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.profileManager
}

// MetricsManager 按渠道类型返回对应的指标管理器，是外部取用指标管理器的唯一入口。
// 五协议各有一份 MetricsManager 实例，调用方一律传 kind，不要在调用侧按 kind 分支。
func (s *ChannelScheduler) MetricsManager(kind ChannelKind) *metrics.MetricsManager {
	return s.getMetricsManager(kind)
}

// GetTraceAffinityManager 获取 Trace 亲和性管理器
func (s *ChannelScheduler) GetTraceAffinityManager() *session.TraceAffinityManager {
	return s.traceAffinity
}

func kindSchedulerLogPrefix(kind ChannelKind) string {
	switch kind {
	case ChannelKindResponses:
		return "Scheduler-Responses"
	case ChannelKindGemini:
		return "Scheduler-Gemini"
	case ChannelKindChat:
		return "Scheduler-Chat"
	case ChannelKindImages:
		return "Scheduler-Images"
	default:
		return "Scheduler-Unknown"
	}
}
