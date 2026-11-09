// Package responses 提供 Responses API 的渠道管理
package responses

import (
	"net/http"
	"time"

	"github.com/insistanan/api-proxy/internal/config"
	"github.com/insistanan/api-proxy/internal/core/channelcrud"
	"github.com/insistanan/api-proxy/internal/httpclient"
	"github.com/insistanan/api-proxy/internal/scheduler"
)

// Crud 返回本协议专用的渠道管理 handler 集合。
// 路由注册见 handlers.RegisterChannelRoutes，它直接消费本函数返回的 *channelcrud.Handlers，
// 因此本包不再提供逐方法的 gin.HandlerFunc 包装。
func Crud(cfgManager *config.ConfigManager, sch *scheduler.ChannelScheduler) *channelcrud.Handlers {
	return channelcrud.New(channelcrud.Ops{
		Kind: scheduler.ChannelKindResponses,
		List: func() []config.UpstreamConfig {
			return cfgManager.GetConfig().ResponsesUpstream
		},
		LoadBalance: func() string {
			return cfgManager.GetConfig().ResponsesLoadBalance
		},
		GetClient: func(ch *config.UpstreamConfig, timeout time.Duration) (*http.Client, error) {
			return httpclient.GetManager().GetStandardClientForUpstream(timeout, cfgManager, ch)
		},
		Add:            cfgManager.AddResponsesUpstreamWithResult,
		Update:         cfgManager.UpdateResponsesUpstream,
		Remove:         cfgManager.RemoveResponsesUpstream,
		AddKey:         cfgManager.AddResponsesAPIKey,
		RemoveKey:      cfgManager.RemoveResponsesAPIKey,
		MoveKeyTop:     cfgManager.MoveResponsesAPIKeyToTop,
		MoveKeyBottom:  cfgManager.MoveResponsesAPIKeyToBottom,
		Reorder:        cfgManager.ReorderResponsesUpstreams,
		SetStatus:      cfgManager.SetResponsesChannelStatus,
		SetPromotion:   cfgManager.SetResponsesChannelPromotion,
		SetLoadBalance: cfgManager.SetResponsesLoadBalance,
	}, sch)
}
