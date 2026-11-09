// 普通日志只在 debug 级别打印正文；结构化流量记录由发送层统一采集。
package proxycore

import (
	"log"
	"net/http"

	"github.com/insistanan/api-proxy/internal/config"
	"github.com/insistanan/api-proxy/internal/logger"
	"github.com/insistanan/api-proxy/internal/utils"
)

func LogResponseDetails(resp *http.Response, body []byte, envCfg *config.EnvConfig, apiType string) {
	if !envCfg.EnableResponseLogs || !envCfg.ShouldLog("debug") {
		return
	}
	log.Printf("[%s-Response] 响应头:\n%s", apiType, logger.MaskedHeaderJSON(resp.Header))
	var formatted string
	if envCfg.RawLogOutput {
		formatted = utils.FormatJSONBytesRaw(body)
	} else {
		formatted = utils.FormatJSONBytesForLog(body, 500)
	}
	log.Printf("[%s-Response] 响应体:\n%s", apiType, formatted)
}
