package handlers

import (
	"github.com/BenedictKing/api-proxy/internal/config"
	"github.com/BenedictKing/api-proxy/internal/core/channelcrud"
	"github.com/BenedictKing/api-proxy/internal/metrics"
	"github.com/BenedictKing/api-proxy/internal/scheduler"
	"github.com/gin-gonic/gin"
)

// ChannelRouteDeps 渠道管理路由注册所需的共享依赖。
type ChannelRouteDeps struct {
	Cfg           *config.ConfigManager
	Scheduler     *scheduler.ChannelScheduler
	MetricsByKind func(kind scheduler.ChannelKind) *metrics.MetricsManager
	// LogStoreByKind 渠道日志存储（R4-b 装配层注入，替代调度器定位器）。
	LogStoreByKind func(kind scheduler.ChannelKind) *metrics.ChannelLogStore
}

// ChannelRouteOptions 各渠道类型的路由差异点。
// 大多数端点（CRUD / key 管理 / pools / reorder / status / promotion / metrics / ping）
// 对所有渠道类型完全一致，只有以下三处存在协议特有差异，用可选字段表达：
//   - LoadBalance：该渠道类型是否注册 /{kind}/loadbalance 端点
//   - Dashboard：该渠道类型的 dashboard 实现（messages 用通用版，gemini/chat/images 用协议包版）
//   - SchedulerStats：该渠道类型是否注册 /{kind}/channels/scheduler/stats 端点
type ChannelRouteOptions struct {
	Crud           *channelcrud.Handlers
	LoadBalance    gin.HandlerFunc
	Dashboard      gin.HandlerFunc
	SchedulerStats gin.HandlerFunc
}

// RegisterChannelRoutes 注册某一渠道类型的全部渠道管理 API 路由。
// 收敛前，messages/responses/gemini/chat/images 五段几乎逐字复制的路由注册
// （合计约 130 行）被本函数 + 5 次调用取代。新增渠道类型只需实现
// channelcrud.Ops + 导出一个 Crud 工厂，再调用本函数一次。
func RegisterChannelRoutes(api *gin.RouterGroup, kind scheduler.ChannelKind, deps ChannelRouteDeps, opts ChannelRouteOptions) {
	crud := opts.Crud
	p := "/" + string(kind)
	mm := deps.MetricsByKind(kind)

	// ===== 渠道 CRUD =====
	api.GET(p+"/channels", crud.GetUpstreams)
	api.POST(p+"/channels", crud.AddUpstream)
	api.PUT(p+"/channels/:id", crud.UpdateUpstream)
	api.DELETE(p+"/channels/:id", crud.DeleteUpstream)
	api.POST(p+"/channels/:id/keys", crud.AddApiKey)
	api.DELETE(p+"/channels/:id/keys/:apiKey", crud.DeleteApiKey)
	api.POST(p+"/channels/:id/keys/:apiKey/top", crud.MoveApiKeyToTop)
	api.POST(p+"/channels/:id/keys/:apiKey/bottom", crud.MoveApiKeyToBottom)

	// ===== 渠道池 =====
	kindName := string(kind)
	api.GET(p+"/pools", GetChannelPools(deps.Cfg, kindName))
	api.POST(p+"/pools", CreateChannelPool(deps.Cfg, kindName))
	api.PUT(p+"/pools/layout", SaveChannelPoolLayout(deps.Cfg, kindName))
	api.PUT(p+"/pools/:id", UpdateChannelPool(deps.Cfg, kindName))
	api.DELETE(p+"/pools/:id", DeleteChannelPool(deps.Cfg, kindName))

	// ===== 多渠道调度 API =====
	api.POST(p+"/channels/reorder", crud.ReorderChannels)
	api.POST(p+"/channels/tidy", TidyProblemChannels(deps.Cfg, kind))
	api.PATCH(p+"/channels/:id/status", crud.SetChannelStatus)
	api.POST(p+"/channels/:id/duplicate", DuplicateChannel(deps.Cfg, kind))
	api.POST(p+"/channels/:id/resume", ResumeChannelByKind(deps.Scheduler, kind))
	api.POST(p+"/channels/:id/promotion", crud.SetChannelPromotion)

	// 协议特有端点（可选）
	if opts.LoadBalance != nil {
		api.PUT(p+"/loadbalance", opts.LoadBalance)
	}
	if opts.Dashboard != nil {
		api.GET(p+"/channels/dashboard", opts.Dashboard)
	}
	if opts.SchedulerStats != nil {
		api.GET(p+"/channels/scheduler/stats", opts.SchedulerStats)
	}

	// ===== 模型映射目标渠道 =====
	api.GET(p+"/mapping-targets", ListMappingTargetChannels(deps.Cfg, deps.Scheduler, kind))
	api.GET(p+"/mapping-targets/:channelId/models", ListMappingTargetChannelModels(deps.Cfg, kind))

	// ===== 指标 =====
	api.GET(p+"/channels/metrics", GetChannelMetricsWithKind(mm, deps.Cfg, kind))
	api.GET(p+"/channels/metrics/history", GetChannelMetricsHistoryByKind(mm, deps.Cfg, kind))
	api.GET(p+"/channels/:id/keys/metrics/history", GetChannelKeyMetricsHistoryByKind(mm, deps.Cfg, kind))
	api.GET(p+"/channels/:id/logs", GetChannelLogs(deps.LogStoreByKind(kind), deps.Cfg, kind))
	api.GET(p+"/global/stats/history", GetGlobalStatsHistory(mm))

	// ===== Ping =====
	api.GET(p+"/ping/:id", crud.PingChannel)
	api.GET(p+"/ping", crud.PingAllChannels)
}
