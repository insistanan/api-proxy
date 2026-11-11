package handlers

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/insistanan/api-proxy/internal/config"
	"github.com/insistanan/api-proxy/internal/httpclient"
)

func GetFreeChannels(cfg *config.ConfigManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		snapshot, err := cfg.GetFreeChannels()
		if err != nil {
			c.JSON(503, gin.H{"error": err.Error()})
			return
		}
		c.JSON(200, snapshot)
	}
}

func UpdateKiloFreeChannel(cfg *config.ConfigManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		var input struct {
			Enabled *bool `json:"enabled"`
		}
		if err := c.ShouldBindJSON(&input); err != nil || input.Enabled == nil {
			c.JSON(400, gin.H{"error": "必须提供 enabled 布尔值"})
			return
		}
		if err := cfg.SetKiloEnabled(*input.Enabled); err != nil {
			writeFreeConfigError(c, err)
			return
		}
		GetFreeChannels(cfg)(c)
	}
}

func writeFreeConfigError(c *gin.Context, err error) {
	c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error(), "committed": config.IsCommittedError(err)})
}

// 仅由管理页手动触发，读取模型目录，不向每个模型发送推理探测。
func SyncKiloFreeModels(cfg *config.ConfigManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		proxyURL, err := cfg.ResolveUpstreamProxyURL(&config.UpstreamConfig{})
		if err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
		}
		client, err := httpclient.GetManager().GetStandardClient(20*time.Second, false, proxyURL)
		if err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
		}
		request, err := http.NewRequestWithContext(c.Request.Context(), http.MethodGet, config.KiloModelsURL, nil)
		if err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
		}
		request.Header.Set("Accept", "application/json")
		response, err := client.Do(request)
		if err != nil {
			c.JSON(502, gin.H{"error": "读取 Kilo 模型目录失败：" + err.Error()})
			return
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			c.JSON(502, gin.H{"error": fmt.Sprintf("Kilo 模型目录返回 HTTP %d", response.StatusCode)})
			return
		}
		const maxBody = 4 << 20
		body, err := io.ReadAll(io.LimitReader(response.Body, maxBody+1))
		if err != nil || len(body) > maxBody {
			c.JSON(502, gin.H{"error": "Kilo 模型目录读取失败或超出大小限制"})
			return
		}
		models, err := parseKiloFreeModels(body)
		if err != nil {
			c.JSON(502, gin.H{"error": err.Error()})
			return
		}
		if err := cfg.SetKiloModels(models, time.Now().UTC().Format(time.RFC3339)); err != nil {
			writeFreeConfigError(c, err)
			return
		}
		GetFreeChannels(cfg)(c)
	}
}

func parseKiloFreeModels(body []byte) ([]string, error) {
	var payload struct {
		Data []struct {
			ID     string `json:"id"`
			IsFree bool   `json:"isFree"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("Kilo 模型目录格式无效：%w", err)
	}
	models := make([]string, 0)
	seen := make(map[string]bool)
	for _, model := range payload.Data {
		if !model.IsFree {
			continue
		}
		if model.ID == "" || strings.TrimSpace(model.ID) != model.ID || strings.ContainsAny(model.ID, "\r\n\t") {
			return nil, fmt.Errorf("Kilo 免费模型 ID 格式无效")
		}
		if !seen[model.ID] {
			models = append(models, model.ID)
			seen[model.ID] = true
		}
	}
	if len(models) == 0 {
		return nil, fmt.Errorf("Kilo 目录没有明确标为免费的模型，保留原清单")
	}
	sort.Strings(models)
	return models, nil
}
