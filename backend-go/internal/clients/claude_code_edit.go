package clients

import (
	"encoding/json"
	"fmt"

	"insistanan/api-proxy/internal/clients/filetxn"
)

// saveClaudeCodeConfiguration 在同一读取快照上合并字段并提交，HTTP 层只处理请求和响应。
func saveClaudeCodeConfiguration(req saveClaudeCodeSettingsRequest) (string, error) {
	claudeCodeSettingsMu.Lock()
	defer claudeCodeSettingsMu.Unlock()

	path := resolveClaudeCodeSettingsPath()
	root, raw, _, err := readClaudeCodeSettings(path)
	if err != nil {
		return path, fmt.Errorf("读取 Claude Code 配置失败: %w", err)
	}
	if err := filetxn.CheckRevision(req.Revision, raw); err != nil {
		return path, err
	}
	env := objectValue(root["env"])
	setOptionalString(env, "ANTHROPIC_BASE_URL", req.BaseURL)
	mergeClaudeCodeCredential(env, req)
	mergeClaudeCodeModelDefaults(env, req.ModelDefaults)
	if len(env) == 0 {
		delete(root, "env")
	} else {
		root["env"] = env
	}
	data, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return path, fmt.Errorf("序列化 Claude Code 配置失败: %w", err)
	}
	return path, writeClaudeCodeSettings(path, raw, append(data, '\n'))
}
