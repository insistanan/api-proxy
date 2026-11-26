package clients

import (
	"fmt"
	"strings"

	"insistanan/api-proxy/internal/clients/filetxn"
)

func saveCodexConfiguration(req saveCodexSettingsRequest) (string, string, error) {
	codexSettingsMu.Lock()
	defer codexSettingsMu.Unlock()

	configPath, authPath, _, err := resolveCodexPaths()
	if err != nil {
		return "", "", fmt.Errorf("解析 Codex 配置目录失败: %w", err)
	}
	_, configRaw, _, err := readCodexConfig(configPath)
	if err != nil {
		return configPath, authPath, fmt.Errorf("读取 Codex config.toml 失败: %w", err)
	}
	_, authRaw, _, err := readCodexAuth(authPath)
	if err != nil {
		return configPath, authPath, fmt.Errorf("读取 Codex auth.json 失败: %w", err)
	}
	if req.Revision == "" {
		return configPath, authPath, filetxn.ErrRevisionRequired
	}
	if req.Revision != filetxn.RevisionSet(configRaw, authRaw) {
		return configPath, authPath, filetxn.ErrConflict
	}
	updatedConfig, err := mergeCodexProviderBaseURL(configRaw, strings.TrimSpace(req.Provider), strings.TrimSpace(req.BaseURL))
	if err != nil {
		return configPath, authPath, fmt.Errorf("%w: %w", errClientEdit, err)
	}
	changes := []filetxn.Change{{Path: configPath, Previous: configRaw, Next: updatedConfig}}
	if req.APIKeyAction != "keep" {
		updatedAuth, err := mergeCodexAPIKey(authRaw, req)
		if err != nil {
			return configPath, authPath, fmt.Errorf("更新 Codex auth.json 失败: %w", err)
		}
		changes = append(changes, filetxn.Change{Path: authPath, Previous: authRaw, Next: updatedAuth})
	}
	return configPath, authPath, filetxn.Commit(changes...)
}
