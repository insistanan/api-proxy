package clients

import (
	"encoding/json"
	"fmt"

	"insistanan/api-proxy/internal/clients/filetxn"
)

func saveOpenCodeConfiguration(req saveOpenCodeConfigRequest) (string, string, error) {
	if req.Revision == nil {
		return "", "", filetxn.ErrRevisionRequired
	}
	openCodeConfigMu.Lock()
	defer openCodeConfigMu.Unlock()

	path := resolveOpenCodeConfigPath()
	root, raw, _, err := readOpenCodeConfig(path)
	if err != nil {
		return path, "", fmt.Errorf("读取 OpenCode 配置失败: %w", err)
	}
	if *req.Revision != sha256HexOrEmpty(raw) {
		return path, "", filetxn.ErrConflict
	}
	if _, exists := root["$schema"]; !exists {
		root["$schema"] = openCodeSchemaURL
	}
	root["provider"] = mergeOpenCodeProviders(objectValue(root["provider"]), req.Providers)
	data, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return path, "", fmt.Errorf("序列化 OpenCode 配置失败: %w", err)
	}
	data = append(data, '\n')
	if err := writeOpenCodeConfig(path, raw, data); err != nil {
		return path, "", err
	}
	return path, sha256HexOrEmpty(data), nil
}
