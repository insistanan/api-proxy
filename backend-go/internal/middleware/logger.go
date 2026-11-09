package middleware

import (
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/insistanan/api-proxy/internal/config"
)

// 默认跳过日志的路径前缀（仅 GET 请求）
var defaultSkipPrefixes = []string{
	"/api/messages/channels",
	"/api/responses/channels",
	"/api/gemini/channels",
	"/api/messages/global/stats",
	"/api/responses/global/stats",
	"/api/gemini/global/stats",
}

// FilteredLogger 创建一个可过滤路径的 Logger 中间件。
// 静默模式（QUIET_POLLING_LOGS=true）下按"请求块"打印：
//
//	┌──────── POST /v1/messages
//	[Auth-Success] ... / [Messages-Scheduler] ... 等业务日志
//	└──────── POST /v1/messages · 状态 200 · 耗时 5.23s
//
// 其中轮询端点、健康检查、OPTIONS 预检完全静默，避免刷屏。
// 非静默模式保持 gin.Logger() 标准访问日志。
// POST/PUT/DELETE 等管理操作始终记录日志以保留审计跟踪。
func FilteredLogger(envCfg *config.EnvConfig, skipPrefixes ...string) gin.HandlerFunc {
	if !envCfg.QuietPollingLogs {
		return gin.Logger()
	}

	if len(skipPrefixes) == 0 {
		skipPrefixes = defaultSkipPrefixes
	}

	return func(c *gin.Context) {
		if isSilentRequest(c, skipPrefixes) {
			c.Next()
			return
		}

		start := time.Now()
		log.Printf("┌──────── %s %s", c.Request.Method, c.Request.URL.Path)
		c.Next()
		log.Printf("└──────── %s %s · 状态 %d · 耗时 %s",
			c.Request.Method, c.Request.URL.Path, c.Writer.Status(), time.Since(start).Round(time.Millisecond))
	}
}

// isSilentRequest 判断静默模式下该请求是否完全不打印日志
func isSilentRequest(c *gin.Context, skipPrefixes []string) bool {
	// OPTIONS 预检与健康检查是纯噪音，始终静默
	if c.Request.Method == http.MethodOptions || c.Request.URL.Path == "/health" {
		return true
	}

	if c.Request.Method != http.MethodGet {
		return false
	}

	path := c.Request.URL.Path
	for _, prefix := range skipPrefixes {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}
