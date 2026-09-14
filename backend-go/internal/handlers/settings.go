// Package handlers 提供 HTTP 处理器
package handlers

import (
	"net/http"
	"strings"

	"github.com/BenedictKing/api-proxy/internal/config"
	"github.com/gin-gonic/gin"
)

// GetSettings 获取可由管理界面维护的全局设置。
func GetSettings(cfgManager *config.ConfigManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.JSON(http.StatusOK, cfgManager.GetSettings())
	}
}

// UpdateSettings 更新管理界面开放的全局设置。未提交的设置分类保持原值。
func UpdateSettings(cfgManager *config.ConfigManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			Network       *config.NetworkSettings     `json:"network"`
			ContentSafety *config.ContentSafetyConfig `json:"contentSafety"`
			Integration   *config.IntegrationSettings `json:"integration"`
			Assistant     *config.AssistantSettings   `json:"assistant"`
			Prompts       *config.PromptSettings      `json:"prompts"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "无效的设置参数"})
			return
		}
		if req.Network == nil && req.ContentSafety == nil && req.Integration == nil && req.Assistant == nil && req.Prompts == nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "至少需要提交一个设置分类"})
			return
		}

		settings := cfgManager.GetSettings()
		if req.Network != nil {
			req.Network.UpstreamProxyURL = strings.TrimSpace(req.Network.UpstreamProxyURL)
			if err := config.ValidateProxyURL(req.Network.UpstreamProxyURL); err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
				return
			}
			settings.Network = *req.Network
		}
		if req.ContentSafety != nil {
			if err := config.ValidateContentSafetyConfig(*req.ContentSafety); err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
				return
			}
			settings.ContentSafety = *req.ContentSafety
		}
		if req.Integration != nil {
			req.Integration.CCSwitchPath = strings.TrimSpace(req.Integration.CCSwitchPath)
			settings.Integration = *req.Integration
		}
		if req.Assistant != nil {
			req.Assistant.Model = strings.TrimSpace(req.Assistant.Model)
			req.Assistant.ChannelID = strings.TrimSpace(req.Assistant.ChannelID)
			settings.Assistant = *req.Assistant
		}
		if req.Prompts != nil {
			// 提交 prompts 分类时才归一化（trim + 刷新 UpdatedAt）；
			// 其他分类单独保存时保持 prompts 原快照，不误刷新时间戳。
			// previous 传当前快照：未变化的条目保留原 UpdatedAt。
			settings.Prompts = config.NormalizePromptSettings(*req.Prompts, settings.Prompts)
		}

		if err := cfgManager.UpdateSettings(settings); err != nil {
			respondSaveError(c, "保存全局设置失败", err)
			return
		}
		c.JSON(http.StatusOK, cfgManager.GetSettings())
	}
}

// respondSaveError 统一响应设置保存失败（R3-c）：已过提交点的错误携带
// code/committed，前端不得按未提交重试（应刷新查看实际状态）。
func respondSaveError(c *gin.Context, message string, err error) {
	body := gin.H{"error": message, "detail": err.Error()}
	if config.IsCommittedError(err) {
		body["code"] = "CONFIG_COMMITTED"
		body["committed"] = true
		body["error"] = message + "（已写入，请刷新查看实际状态）"
	}
	c.JSON(http.StatusInternalServerError, body)
}

// GetFuzzyMode 获取 Fuzzy 模式状态
func GetFuzzyMode(cfgManager *config.ConfigManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.JSON(200, gin.H{
			"fuzzyModeEnabled": cfgManager.GetFuzzyModeEnabled(),
		})
	}
}

// SetFuzzyMode 设置 Fuzzy 模式状态
func SetFuzzyMode(cfgManager *config.ConfigManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			Enabled bool `json:"enabled"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(400, gin.H{"error": "Invalid request body"})
			return
		}

		if err := cfgManager.SetFuzzyModeEnabled(req.Enabled); err != nil {
			c.JSON(500, gin.H{"error": "Failed to save config"})
			return
		}

		c.JSON(200, gin.H{
			"success":          true,
			"fuzzyModeEnabled": req.Enabled,
		})
	}
}

// GetClientDisguise 获取 Messages 与 Responses 的客户端伪装状态。
func GetClientDisguise(cfgManager *config.ConfigManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"claudeCodeDisguiseEnabled": cfgManager.GetClaudeCodeDisguiseEnabled(),
			"codexDisguiseEnabled":      cfgManager.GetCodexDisguiseEnabled(),
		})
	}
}

// SetClientDisguise 设置指定协议的客户端伪装状态。
func SetClientDisguise(cfgManager *config.ConfigManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			Protocol string `json:"protocol" binding:"required"`
			Enabled  bool   `json:"enabled"`
		}

		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "无效的请求参数"})
			return
		}

		if req.Protocol != "messages" && req.Protocol != "responses" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "protocol 仅支持 messages 或 responses"})
			return
		}

		if err := cfgManager.SetClientDisguiseEnabled(req.Protocol, req.Enabled); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "保存客户端伪装设置失败"})
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"protocol": req.Protocol,
			"enabled":  req.Enabled,
		})
	}
}
