package embeddings

import (
	"net/http"
	"time"

	"github.com/insistanan/api-proxy/internal/admin/channelcrud"
	"github.com/insistanan/api-proxy/internal/infra/config"
	"github.com/insistanan/api-proxy/internal/infra/httpclient"
	"github.com/insistanan/api-proxy/internal/orchestration/scheduler"
)

// Crud 返回 Embeddings 协议专用的渠道管理 handler 集合。
func Crud(cfgManager *config.ConfigManager, sch *scheduler.ChannelScheduler) *channelcrud.Handlers {
	return channelcrud.New(channelcrud.Ops{
		Kind: scheduler.ChannelKindEmbeddings,
		List: func() []config.UpstreamConfig {
			return cfgManager.GetConfig().EmbeddingsUpstream
		},
		LoadBalance: func() string {
			return cfgManager.GetConfig().EmbeddingsLoadBalance
		},
		GetClient: func(ch *config.UpstreamConfig, timeout time.Duration) (*http.Client, error) {
			return httpclient.GetManager().GetStandardClientForUpstream(timeout, cfgManager, ch)
		},
		Add:            cfgManager.AddEmbeddingsUpstreamWithResult,
		Update:         cfgManager.UpdateEmbeddingsUpstream,
		Remove:         cfgManager.RemoveEmbeddingsUpstream,
		AddKey:         cfgManager.AddEmbeddingsAPIKey,
		RemoveKey:      cfgManager.RemoveEmbeddingsAPIKey,
		MoveKeyTop:     cfgManager.MoveEmbeddingsAPIKeyToTop,
		MoveKeyBottom:  cfgManager.MoveEmbeddingsAPIKeyToBottom,
		Reorder:        cfgManager.ReorderEmbeddingsUpstreams,
		SetStatus:      cfgManager.SetEmbeddingsChannelStatus,
		SetPromotion:   cfgManager.SetEmbeddingsChannelPromotion,
		SetLoadBalance: cfgManager.SetEmbeddingsLoadBalance,
	}, sch)
}
