package decisions

import (
	"net/http"
	"time"

	"github.com/insistanan/api-proxy/internal/admin/channelcrud"
	"github.com/insistanan/api-proxy/internal/infra/config"
	"github.com/insistanan/api-proxy/internal/infra/httpclient"
	"github.com/insistanan/api-proxy/internal/orchestration/scheduler"
)

// Crud 返回 Decisions 协议专用的渠道管理 handler 集合。
func Crud(cfgManager *config.ConfigManager, sch *scheduler.ChannelScheduler) *channelcrud.Handlers {
	return channelcrud.New(channelcrud.Ops{
		Kind: scheduler.ChannelKindDecisions,
		List: func() []config.UpstreamConfig {
			return cfgManager.GetConfig().DecisionsUpstream
		},
		LoadBalance: func() string {
			return cfgManager.GetConfig().DecisionsLoadBalance
		},
		GetClient: func(ch *config.UpstreamConfig, timeout time.Duration) (*http.Client, error) {
			return httpclient.GetManager().GetStandardClientForUpstream(timeout, cfgManager, ch)
		},
		Add:            cfgManager.AddDecisionsUpstreamWithResult,
		Update:         cfgManager.UpdateDecisionsUpstream,
		Remove:         cfgManager.RemoveDecisionsUpstream,
		AddKey:         cfgManager.AddDecisionsAPIKey,
		RemoveKey:      cfgManager.RemoveDecisionsAPIKey,
		MoveKeyTop:     cfgManager.MoveDecisionsAPIKeyToTop,
		MoveKeyBottom:  cfgManager.MoveDecisionsAPIKeyToBottom,
		Reorder:        cfgManager.ReorderDecisionsUpstreams,
		SetStatus:      cfgManager.SetDecisionsChannelStatus,
		SetPromotion:   cfgManager.SetDecisionsChannelPromotion,
		SetLoadBalance: cfgManager.SetDecisionsLoadBalance,
	}, sch)
}
