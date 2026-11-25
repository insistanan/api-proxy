// 请求开始与结束独立于上游尝试记录，避免把处理中、本地拒绝和进程中断都标为缺段。
package proxycore

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"insistanan/api-proxy/internal/infra/config"
	"insistanan/api-proxy/internal/storage/logger"
)

const requestLifecycleKey = "__request_log_lifecycle"
const requestLifecycleStartKey = "__request_log_started_at"

func newRequestLifecycle(c *gin.Context, body []byte, start time.Time) *logger.RequestLifecycle {
	state := &logger.RequestLifecycle{
		Client:    c.Request.UserAgent(),
		BootID:    logger.Default().BootID(),
		StartedAt: start.UnixMilli(),
	}
	if body != nil {
		digest := sha256.Sum256(body)
		state.RequestHash = hex.EncodeToString(digest[:])
	}
	c.Set(requestLifecycleKey, state)
	c.Set(requestLifecycleStartKey, start)
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
	policy := bindCapturePolicy(c, env)
	snapshot := *state
	if policy.BodyState(false) != "" {
		snapshot.PromptPreview, snapshot.Context = "", nil
	}
	payload, err := json.Marshal(snapshot)
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
		if state.Status != "blocked" {
			state.Status = "failed"
		}
		state.StatusCode = c.Writer.Status()
		if state.ErrorType == "" {
			state.ErrorType = "local_response"
			state.ErrorMessage = fmt.Sprintf("网关返回 HTTP %d，未记录更具体的终止原因", c.Writer.Status())
		}
	} else if state.Status == "" {
		// 未写回任何响应且没有尝试结果，不能用 gin 的默认 200 冒充完成。
		if c.Writer.Written() {
			state.Status = "completed"
		} else {
			state.Status = "interrupted"
			state.ErrorType = "missing_response"
			state.ErrorMessage = "网关处理结束但没有交付响应或记录上游结果"
		}
	}
	if c.Writer.Written() {
		state.ClientStatusCode = c.Writer.Status()
		if state.StatusCode == 0 {
			state.StatusCode = state.ClientStatusCode
		}
	}
	state.EndedAt = time.Now().UnixMilli()
	if value, ok := c.Get(requestLifecycleStartKey); ok {
		if start, ok := value.(time.Time); ok {
			duration := time.Since(start).Milliseconds()
			state.DurationMs = &duration
		}
	}
	recordRequestLifecycle(c, env, apiType, logger.PhaseRequestEnd, state)
}
