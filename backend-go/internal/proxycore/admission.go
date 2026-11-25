package proxycore

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/insistanan/api-proxy/internal/orchestration/scheduler"
	"github.com/insistanan/api-proxy/internal/orchestration/upstreamcontrol"
)

func isLocalAdmissionError(err error) bool {
	return upstreamcontrol.IsLocalError(err) || errors.Is(err, scheduler.ErrBranchCapacity)
}

// 本地容量不足只排除当前 URL 的 Key，不更新持久化失败或渠道健康指标。
// 全请求预算耗尽必须穿透所有轮转层，不能继续尝试其他组合。
func skipLocalAdmissionCandidate(err error, state *upstreamFailoverState, retryState *candidateRetryState, apiKey string) bool {
	if !upstreamcontrol.IsLocalError(err) || errors.Is(err, upstreamcontrol.ErrBudget) {
		return false
	}
	state.lastError = err
	state.markNeutralTermination()
	retryState.MarkKeyFailed(apiKey)
	return true
}

func localAdmissionResult(c *gin.Context, err error, allowFailover bool) UpstreamAttemptResult {
	result := newUpstreamAttemptResult(false, "", 0, nil, nil, err).withHealth(channelHealthNeutral)
	if errors.Is(err, upstreamcontrol.ErrBudget) {
		allowFailover = false
		result.AbortFailover = true
	}
	if !allowFailover {
		code := "local_capacity_error"
		if errors.Is(err, scheduler.ErrBranchCapacity) {
			code = "channel_branch_capacity_exceeded"
		}
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{"type": "local_capacity_error", "code": code, "message": err.Error()}})
		result.Handled = true
	}
	return result
}
