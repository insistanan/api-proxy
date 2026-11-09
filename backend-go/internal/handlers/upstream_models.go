package handlers

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/insistanan/api-proxy/internal/config"
	"github.com/insistanan/api-proxy/internal/modelcatalog"
)

// DiscoverUpstreamModels 通过服务端网络栈获取指定渠道的模型列表。
func DiscoverUpstreamModels(cfgManager *config.ConfigManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			BaseURL            string   `json:"baseUrl" binding:"required"`
			BaseURLs           []string `json:"baseUrls"`
			APIKey             string   `json:"apiKey"`
			ServiceType        string   `json:"serviceType"`
			InsecureSkipVerify bool     `json:"insecureSkipVerify"`
			ProxyMode          string   `json:"proxyMode"`
			ProxyURL           string   `json:"proxyUrl"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "请提供有效的上游地址"})
			return
		}
		baseURL := strings.TrimSpace(req.BaseURL)
		apiKey := strings.TrimSpace(req.APIKey)
		if baseURL == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "上游地址不能为空"})
			return
		}

		upstream := config.UpstreamConfig{
			Name:               "模型发现",
			BaseURL:            baseURL,
			BaseURLs:           req.BaseURLs,
			ServiceType:        strings.TrimSpace(req.ServiceType),
			InsecureSkipVerify: req.InsecureSkipVerify,
			ProxyMode:          req.ProxyMode,
			ProxyURL:           req.ProxyURL,
		}
		response, err := modelcatalog.DiscoverUpstreamModels(c.Request.Context(), cfgManager, &upstream, apiKey)
		if err != nil {
			status := http.StatusBadGateway
			if config.IsConfigError(err) {
				status = http.StatusBadRequest
			}
			c.JSON(status, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, response)
	}
}
