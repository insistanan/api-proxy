// 请求开始与结束独立于上游尝试记录，避免把处理中、本地拒绝和进程中断都标为缺段。
package proxycore

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/insistanan/api-proxy/internal/infra/config"
	"github.com/insistanan/api-proxy/internal/storage/logger"
)

const requestLifecycleKey = "__request_log_lifecycle"

func newRequestLifecycle(c *gin.Context, body []byte, start time.Time) *logger.RequestLifecycle {
	digest := sha256.Sum256(body)
	state := &logger.RequestLifecycle{
		RequestHash: hex.EncodeToString(digest[:]),
		Client:      c.Request.UserAgent(),
		BootID:      logger.Default().BootID(),
		StartedAt:   start.UnixMilli(),
	}
	c.Set(requestLifecycleKey, state)
	return state
}

func requestLifecycle(c *gin.Context) *logger.RequestLifecycle {
	if c == nil {
		return nil
	}
	value, _ := c.Get(requestLifecycleKey)
	state, _ := value.(*logger.RequestLifecycle)
	return state
}

func recordRequestLifecycle(c *gin.Context, env *config.EnvConfig, apiType, phase string, state *logger.RequestLifecycle) {
	if !env.Current().EnableRequestLogs && !env.Current().EnableResponseLogs {
		return
	}
	payload, err := json.Marshal(state)
	if err != nil {
		log.Printf("[Request-Log] 编码请求生命周期失败: %v", err)
		return
	}
	logger.RecordTraffic(logger.TrafficLog{
		RequestID: RequestLogID(c), Phase: phase, APIType: apiType,
		StatusCode: state.StatusCode, Body: string(payload), Stream: state.Stream,
	})
}

func finishRequestLifecycle(c *gin.Context, env *config.EnvConfig, apiType string, state *logger.RequestLifecycle) {
	if err := c.Request.Context().Err(); err != nil && state.Status != "completed" {
		state.Status = "cancelled"
		state.ErrorType = "client_cancelled"
		state.ErrorMessage = err.Error()
	} else if c.Writer.Status() >= http.StatusBadRequest {
		state.Status = "failed"
		state.StatusCode = c.Writer.Status()
		if state.ErrorType == "" {
			state.ErrorType = "local_response"
		}
	} else if state.Status == "" {
		// 未写回任何响应且没有尝试结果，不能用 gin 的默认 200 冒充完成。
		if c.Writer.Written() {
			state.Status = "completed"
		} else {
			state.Status = "interrupted"
		}
	}
	recordRequestLifecycle(c, env, apiType, logger.PhaseRequestEnd, state)
}
