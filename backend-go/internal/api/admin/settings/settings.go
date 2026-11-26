// Package settings 提供系统设置和客户端伪装管理接口。
package settings

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"

	adminshared "insistanan/api-proxy/internal/api/admin/shared"
	"insistanan/api-proxy/internal/api/common"
	"insistanan/api-proxy/internal/clients"
	"insistanan/api-proxy/internal/clients/extensions"
	"insistanan/api-proxy/internal/infra/config"
	"insistanan/api-proxy/internal/infra/upstreamreq"
	"insistanan/api-proxy/internal/storage/atomicfile"
)

// GetSettings 获取可由管理界面维护的全局设置。
func GetSettings(cfgManager *config.ConfigManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.JSON(http.StatusOK, struct {
			config.SettingsConfig
			ConfigReload config.ConfigReloadStatus `json:"configReload"`
		}{cfgManager.GetSettings(), cfgManager.GetReloadStatus()})
	}
}

// UpdateSettings 更新管理界面开放的全局设置。未提交的设置分类保持原值。
func UpdateSettings(cfgManager *config.ConfigManager, skillsAPI *clients.SkillsAPI, mcpAPI *clients.MCPAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		var patch config.SettingsPatch
		if err := c.ShouldBindJSON(&patch); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "无效的设置参数"})
			return
		}

		save := func() error { return cfgManager.PatchSettings(patch) }
		var err error
		if patch.Extensions == nil {
			err = save()
		} else {
			err = adminshared.WithExtensionsLocks(skillsAPI, mcpAPI, func() error {
				previous := cfgManager.GetSettings().Extensions
				next := config.NormalizeExtensionsSettings(*patch.Extensions)
				if err := config.ValidateExtensionsSettings(next); err != nil {
					return err
				}
				oldDir, err := config.ResolveSkillsDir(previous.SkillsDir)
				if err != nil {
					return err
				}
				newDir, err := config.ResolveSkillsDir(next.SkillsDir)
				if err != nil {
					return err
				}
				patch.ExpectedExtensions = &previous
				if config.SamePath(oldDir, newDir) {
					return save()
				}

				migrated, rollback, err := extensions.MigrateSkillsDir(oldDir, newDir)
				if err != nil {
					return err
				}
				saveErr := save()
				if saveErr != nil && !config.IsCommittedError(saveErr) {
					if migrated && rollback != nil {
						if rollbackErr := rollback(); rollbackErr != nil {
							return errors.Join(saveErr, fmt.Errorf("恢复 Skills 目录失败: %w", rollbackErr))
						}
					}
					return saveErr
				}
				// 配置已提交时必须完成迁移收尾，即使目录同步发出了告警。
				if migrated {
					if err := extensions.FinalizeSkillsMigration(oldDir, newDir); err != nil {
						return errors.Join(saveErr, &atomicfile.CommittedError{Phase: atomicfile.PhaseDirSync, Err: err})
					}
				}
				return saveErr
			})
		}
		if err != nil {
			common.WriteSaveError(c, "保存全局设置失败", err)
			return
		}
		c.JSON(http.StatusOK, cfgManager.GetSettings())
	}
}

// GetFuzzyMode 获取 Fuzzy 模式状态
func GetFuzzyMode(cfgManager *config.ConfigManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		cfg := cfgManager.GetConfig()
		modes := make(map[string]bool)
		for _, protocol := range []string{"messages", "responses", "gemini", "chat", "images", "embeddings", "decisions"} {
			enabled, exists := cfg.FuzzyModeByProtocol[protocol]
			if !exists {
				enabled = cfg.FuzzyModeEnabled
			}
			modes[protocol] = enabled
		}
		c.JSON(200, gin.H{
			"fuzzyModeEnabled":    cfg.FuzzyModeEnabled,
			"fuzzyModeByProtocol": modes,
		})
	}
}

// SetFuzzyMode 设置 Fuzzy 模式状态
func SetFuzzyMode(cfgManager *config.ConfigManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			Enabled  *bool   `json:"enabled"`
			Protocol *string `json:"protocol"`
		}
		if err := c.ShouldBindJSON(&req); err != nil || req.Enabled == nil {
			c.JSON(400, gin.H{"error": "必须提供 enabled 布尔值"})
			return
		}

		var err error
		if req.Protocol == nil {
			err = cfgManager.SetFuzzyModeEnabled(*req.Enabled)
		} else {
			switch *req.Protocol {
			case "messages", "responses", "gemini", "chat", "images", "embeddings", "decisions":
				err = cfgManager.SetFuzzyModeForProtocol(*req.Protocol, *req.Enabled)
			default:
				c.JSON(400, gin.H{"error": "不支持的渠道协议"})
				return
			}
		}
		if err != nil {
			common.WriteSaveError(c, "保存自动故障转移设置失败", err)
			return
		}

		c.JSON(200, gin.H{
			"success":          true,
			"fuzzyModeEnabled": *req.Enabled,
		})
	}
}

// GetClientDisguise 获取 Messages 与 Responses 的客户端伪装状态及生效版本。
func GetClientDisguise(cfgManager *config.ConfigManager, syncer *DisguiseSyncer) gin.HandlerFunc {
	return func(c *gin.Context) {
		if syncer == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "版本同步任务未初始化"})
			return
		}
		effClaude, effCodex, latClaude, latCodex := upstreamreq.GetEffectiveDisguiseVersions()
		status := syncer.Status()
		c.JSON(http.StatusOK, gin.H{
			"claudeCodeDisguiseEnabled": cfgManager.GetClaudeCodeDisguiseEnabled(),
			"codexDisguiseEnabled":      cfgManager.GetCodexDisguiseEnabled(),
			"effectiveClaudeVersion":    effClaude,
			"effectiveCodexVersion":     effCodex,
			"latestClaudeVersion":       latClaude,
			"latestCodexVersion":        latCodex,
			"sync":                      status,
		})
	}
}

// RefreshClientDisguise 排队查询并立即返回，不将保存设置挂在外部网络上。
func RefreshClientDisguise(syncer *DisguiseSyncer) gin.HandlerFunc {
	return func(c *gin.Context) {
		if syncer == nil || !syncer.RequestSync() {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "版本同步任务未运行或正在停止"})
			return
		}
		c.JSON(http.StatusAccepted, gin.H{"queued": true})
	}
}

// SetClientDisguise 设置指定协议的客户端伪装状态。
func SetClientDisguise(cfgManager *config.ConfigManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			Protocol string `json:"protocol" binding:"required"`
			Enabled  *bool  `json:"enabled"`
		}

		if err := c.ShouldBindJSON(&req); err != nil || req.Enabled == nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "无效的请求参数"})
			return
		}

		if req.Protocol != "messages" && req.Protocol != "responses" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "protocol 仅支持 messages 或 responses"})
			return
		}

		if err := cfgManager.SetClientDisguiseEnabled(req.Protocol, *req.Enabled); err != nil {
			common.WriteSaveError(c, "保存客户端伪装设置失败", err)
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"protocol": req.Protocol,
			"enabled":  req.Enabled,
		})
	}
}
