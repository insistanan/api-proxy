package proxycore

import (
	"context"
	"github.com/insistanan/api-proxy/internal/storage/logbody"

	"github.com/gin-gonic/gin"
	"github.com/insistanan/api-proxy/internal/infra/config"
	"github.com/insistanan/api-proxy/internal/storage/logger"
)

func capturePolicy(ctx context.Context, env *config.EnvConfig) logger.CapturePolicy {
	if policy, ok := logger.CapturePolicyFromContext(ctx); ok {
		return policy
	}
	current := env.Current()
	mode, limit := current.LogCaptureMode, current.LogBodyLimitKB*1024
	// 手工构造的旧 EnvConfig 未携带新字段，保持历史上限和采集行为。
	if mode == "" {
		mode = "bounded"
	}
	if limit == 0 {
		limit = logbody.MaxBodyBytes
	}
	return logger.CapturePolicy{Request: current.EnableRequestLogs, Response: current.EnableResponseLogs, Mode: mode, BodyLimit: limit, Paused: logger.Default().BodyCapturePaused()}
}

func bindCapturePolicy(c *gin.Context, env *config.EnvConfig) logger.CapturePolicy {
	policy := capturePolicy(c.Request.Context(), env)
	c.Request = c.Request.WithContext(logger.ContextWithCapturePolicy(c.Request.Context(), policy))
	return policy
}

// RequestLogEnv 为旧日志消费方提供固定副本，防止正文绕过 metadata / budget 策略进入运行日志。
func RequestLogEnv(c *gin.Context, env *config.EnvConfig) *config.EnvConfig {
	current := env.Snapshot()
	policy := capturePolicy(c.Request.Context(), env)
	current.EnableRequestLogs = policy.BodyState(false) == ""
	current.EnableResponseLogs = policy.BodyState(true) == ""
	current.LogCaptureMode = policy.Mode
	current.LogBodyLimitKB = policy.BodyLimit / 1024
	return current
}
