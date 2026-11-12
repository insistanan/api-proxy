package middleware

import (
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/insistanan/api-proxy/internal/infra/config"
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

// FilteredLogger 根据 QuietPollingLogs 选择访问日志的输出方式。
// 开启时，指定前缀的 GET 请求、健康检查和 OPTIONS 预检不输出访问日志，
// 其余请求记录开始、结束、状态码和耗时；关闭时使用 gin.Logger() 的标准输出。
// 这个开关只影响访问日志，各业务组件的日志仍由调用方控制。
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

// isSilentRequest 判断静默模式下是否跳过访问日志；业务日志由各调用方控制。
func isSilentRequest(c *gin.Context, skipPrefixes []string) bool {
	// 静默模式下跳过 OPTIONS 预检与健康检查的访问日志。
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
