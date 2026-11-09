// Package messages 提供 Claude Messages API 的渠道管理
package messages

import (
	"net/http"
	"time"

	"github.com/insistanan/api-proxy/internal/config"
	"github.com/insistanan/api-proxy/internal/core/channelcrud"
	"github.com/insistanan/api-proxy/internal/httpclient"
	"github.com/insistanan/api-proxy/internal/scheduler"
)

// Crud 返回本协议专用的渠道管理 handler 集合。
// 渠道 CRUD / key 管理 / reorder / status / promotion / 负载均衡 / Ping 的实现
// 全部收敛在 core/channelcrud，本包只提供"绑定了 Messages 切片与 ChannelKind"的操作集合。
// 路由注册见 handlers.RegisterChannelRoutes，它直接消费本函数返回的 *channelcrud.Handlers，
// 因此本包不再提供逐方法的 gin.HandlerFunc 包装。
func Crud(cfgManager *config.ConfigManager, sch *scheduler.ChannelScheduler) *channelcrud.Handlers {
	return channelcrud.New(channelcrud.Ops{
		Kind: scheduler.ChannelKindMessages,
		List: func() []config.UpstreamConfig {
			return cfgManager.GetConfig().Upstream
		},
		LoadBalance: func() string {
			return cfgManager.GetConfig().LoadBalance
		},
		GetClient: func(ch *config.UpstreamConfig, timeout time.Duration) (*http.Client, error) {
			return httpclient.GetManager().GetStandardClientForUpstream(timeout, cfgManager, ch)
		},
		Add:            cfgManager.AddUpstreamWithResult,
		Update:         cfgManager.UpdateUpstream,
		Remove:         cfgManager.RemoveUpstream,
		AddKey:         cfgManager.AddAPIKey,
		RemoveKey:      cfgManager.RemoveAPIKey,
		MoveKeyTop:     cfgManager.MoveAPIKeyToTop,
		MoveKeyBottom:  cfgManager.MoveAPIKeyToBottom,
		Reorder:        cfgManager.ReorderUpstreams,
		SetStatus:      cfgManager.SetChannelStatus,
		SetPromotion:   cfgManager.SetChannelPromotion,
		SetLoadBalance: cfgManager.SetLoadBalance,
	}, sch)
}
