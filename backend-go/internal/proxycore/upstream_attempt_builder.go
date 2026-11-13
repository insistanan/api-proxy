// 上游尝试的公共初始化；协议差异直接填写 UpstreamAttempt 字段。
package proxycore

import (
	"time"

	"github.com/gin-gonic/gin"
	"github.com/insistanan/api-proxy/internal/infra/config"
	"github.com/insistanan/api-proxy/internal/orchestration/scheduler"
	"github.com/insistanan/api-proxy/internal/orchestration/upstreamcontrol"
)

// NewUpstreamAttempt 初始化共享依赖、请求预算与日志上下文。
func NewUpstreamAttempt(
	c *gin.Context,
	envCfg *config.EnvConfig,
	cfgManager *config.ConfigManager,
	channelScheduler *scheduler.ChannelScheduler,
	kind scheduler.ChannelKind,
	apiType string,
	upstream *config.UpstreamConfig,
	requestedModel string,
	requestBody []byte,
	isStream bool,
	channelIndex int,
	conversationID string,
) UpstreamAttempt {
	c.Request = c.Request.WithContext(upstreamcontrol.WithScope(c.Request.Context()))
	return UpstreamAttempt{
		Context:            c,
		EnvConfig:          envCfg,
		ConfigManager:      cfgManager,
		ChannelScheduler:   channelScheduler,
		Kind:               kind,
		APIType:            apiType,
		MetricsManager:     channelScheduler.MetricsManager(kind),
		Upstream:           upstream,
		RequestedModel:     requestedModel,
		AllowModelFailover: cfgManager.GetFuzzyModeEnabledForProtocol(string(kind)),
		RequestBody:        requestBody,
		IsStream:           isStream,
		RequestTimeout:     time.Duration(envCfg.Current().RequestTimeout) * time.Millisecond,
		LogContext: AttemptLogContext{
			ChannelIndex:    channelIndex,
			Model:           requestedModel,
			ConversationID:  conversationID,
			LogStore:        channelScheduler.GetChannelLogStore(kind),
			RequestLogStore: channelScheduler.GetRequestLogStore(),
		},
	}
}
