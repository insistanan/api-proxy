// 普通日志在 debug 级别输出正文定位信息；正文与头部统一由结构化流量采集管理保留期。
package proxycore

import (
	"context"
	"log"
	"net/http"

	"insistanan/api-proxy/internal/infra/config"
	"insistanan/api-proxy/internal/storage/logger"
)

func LogResponseDetails(resp *http.Response, body []byte, envCfg *config.EnvConfig, apiType string) {
	ctx := context.Background()
	if resp.Request != nil {
		ctx = resp.Request.Context()
	}
	policy := capturePolicy(ctx, envCfg)
	if policy.BodyState(true) != "" || !envCfg.ShouldLog("debug") {
		return
	}
	log.Printf("[%s-Response] requestId=%s attemptId=%s 状态=%d 正文=%d 字节；正文与响应头见结构化日志", apiType, logger.RequestIDFromContext(ctx), logger.AttemptIDFromContext(ctx), resp.StatusCode, len(body))
}
