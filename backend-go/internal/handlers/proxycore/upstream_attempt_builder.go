// 本文件提供 UpstreamAttempt 的构造收敛入口 NewAttemptBuilder：
// 由共享依赖（envCfg/cfgManager/channelScheduler）机械推导的字段只写一份
// （MetricsManager、AllowModelFailover、LogContext 的日志存储等），
// 各协议调用方只需显式声明真正的策略差异（NextAPIKey/BuildRequest/
// DeprioritizeKey/MarkURL*/HandleSuccess 与 URL 候选序）。
// NextAPIKey/BuildRequest/HandleSuccess 是必填策略，未设置会在尝试时以
// nil 调用 panic 暴露，不做静默兜底。
package proxycore

import (
	"github.com/BenedictKing/api-proxy/internal/config"
	"github.com/BenedictKing/api-proxy/internal/scheduler"
	"github.com/BenedictKing/api-proxy/internal/urlhealth"
	"github.com/gin-gonic/gin"
)

// AttemptBuilder 以链式 setter 收敛 UpstreamAttempt 的构造。
type AttemptBuilder struct {
	attempt UpstreamAttempt
}

// NewAttemptBuilder 预填所有可由共享依赖推导的字段。
func NewAttemptBuilder(
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
) *AttemptBuilder {
	return &AttemptBuilder{attempt: UpstreamAttempt{
		Context:            c,
		EnvConfig:          envCfg,
		ConfigManager:      cfgManager,
		ChannelScheduler:   channelScheduler,
		Kind:               kind,
		APIType:            apiType,
		MetricsManager:     channelScheduler.MetricsManager(kind),
		Upstream:           upstream,
		RequestedModel:     requestedModel,
		AllowModelFailover: cfgManager.GetFuzzyModeEnabled(),
		RequestBody:        requestBody,
		IsStream:           isStream,
		LogContext: AttemptLogContext{
			ChannelIndex:    channelIndex,
			Model:           requestedModel,
			ConversationID:  conversationID,
			LogStore:        channelScheduler.GetChannelLogStore(kind),
			RequestLogStore: channelScheduler.GetRequestLogStore(),
		},
	}}
}

// WithURLResults 设置本次尝试的 BaseURL 候选序。
func (b *AttemptBuilder) WithURLResults(results []urlhealth.URLLatencyResult) *AttemptBuilder {
	b.attempt.URLResults = results
	return b
}

// WithNextAPIKey 设置密钥轮换策略（必填）。
func (b *AttemptBuilder) WithNextAPIKey(fn NextAPIKeyFunc) *AttemptBuilder {
	b.attempt.NextAPIKey = fn
	return b
}

// WithBuildRequest 设置上游请求构建回调（必填）。
func (b *AttemptBuilder) WithBuildRequest(fn BuildRequestFunc) *AttemptBuilder {
	b.attempt.BuildRequest = fn
	return b
}

// WithDeprioritizeKey 设置 quota 失败密钥的降级策略；不设置则不降级。
func (b *AttemptBuilder) WithDeprioritizeKey(fn DeprioritizeKeyFunc) *AttemptBuilder {
	b.attempt.DeprioritizeKey = fn
	return b
}

// WithMarkURL 设置 URL 健康回写回调；单渠道不参与 URL 健康统计时不设置。
func (b *AttemptBuilder) WithMarkURL(onFailure func(url string), onSuccess func(url string)) *AttemptBuilder {
	b.attempt.MarkURLFailure = onFailure
	b.attempt.MarkURLSuccess = onSuccess
	return b
}

// WithHandleSuccess 设置成功响应处理回调（必填）。
func (b *AttemptBuilder) WithHandleSuccess(fn HandleSuccessFunc) *AttemptBuilder {
	b.attempt.HandleSuccess = fn
	return b
}

// WithAllowContentPolicyChannelFailover 开启内容审核错误时的跨渠道故障转移。
func (b *AttemptBuilder) WithAllowContentPolicyChannelFailover(allow bool) *AttemptBuilder {
	b.attempt.LogContext.AllowContentPolicyChannelFailover = allow
	return b
}

// Build 返回组装完成的 UpstreamAttempt。
func (b *AttemptBuilder) Build() UpstreamAttempt {
	return b.attempt
}
