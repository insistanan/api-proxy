package piagent

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
)

// ModelSettingsView 是 settings.json 中可管理的默认模型字段。
type ModelSettingsView struct {
	DefaultProvider      string   `json:"defaultProvider"`
	DefaultModel         string   `json:"defaultModel"`
	DefaultThinkingLevel string   `json:"defaultThinkingLevel"`
	EnabledModels        []string `json:"enabledModels"`
}

// ReadModelSettings 读取 settings.json 中的默认模型字段。
func (s *Store) ReadModelSettings() (ModelSettingsView, map[string]json.RawMessage, []byte, bool, error) {
	path, err := s.settingsPath()
	if err != nil {
		return ModelSettingsView{}, nil, nil, false, err
	}
	root, raw, exists, err := ReadJSON(path)
	if err != nil {
		return ModelSettingsView{}, nil, raw, exists, err
	}
	if err := validateModelSettingsFields(root); err != nil {
		return ModelSettingsView{}, nil, raw, exists, err
	}
	view := ModelSettingsView{
		DefaultProvider:      stringField(root, "defaultProvider"),
		DefaultModel:         stringField(root, "defaultModel"),
		DefaultThinkingLevel: stringField(root, "defaultThinkingLevel"),
		EnabledModels:        stringSliceField(root, "enabledModels"),
	}
	return view, root, raw, exists, nil
}

// UpdateModelSettings 字段级合并默认模型设置，保留 theme / shellPath / packages 等未知字段。
func (s *Store) UpdateModelSettings(update ModelSettingsView, revision string) (WriteResult, error) {
	path, err := s.settingsPath()
	if err != nil {
		return WriteResult{}, err
	}
	root, raw, exists, err := ReadJSON(path)
	if err != nil {
		return WriteResult{}, err
	}
	if err := validateModelSettingsFields(root); err != nil {
		return WriteResult{}, err
	}
	if exists && revision == "" {
		return WriteResult{}, ErrRevisionRequired
	}
	if revision != "" && SHA256Hex(raw) != revision {
		return WriteResult{}, ErrRevisionMismatch
	}
	if strings.TrimSpace(update.DefaultThinkingLevel) != "" && !IsValidThinkingLevel(update.DefaultThinkingLevel) {
		return WriteResult{}, fmt.Errorf("无效的思考等级: %s", update.DefaultThinkingLevel)
	}
	setStringField(root, "defaultProvider", update.DefaultProvider)
	setStringField(root, "defaultModel", update.DefaultModel)
	setStringField(root, "defaultThinkingLevel", update.DefaultThinkingLevel)
	if update.EnabledModels == nil {
		delete(root, "enabledModels")
	} else {
		data, err := json.Marshal(update.EnabledModels)
		if err != nil {
			return WriteResult{}, err
		}
		root["enabledModels"] = json.RawMessage(data)
	}

	data, err := MarshalIndent(root)
	if err != nil {
		return WriteResult{}, err
	}
	return WriteFileAtomic(path, data, false, s.BackupDir, raw)
}

func (s *Store) settingsPath() (string, error) {
	dir, err := ResolveConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, string(FileSettings)), nil
}

func validateModelSettingsFields(root map[string]json.RawMessage) error {
	for _, key := range []string{"defaultProvider", "defaultModel", "defaultThinkingLevel", "enabledModels"} {
		raw, exists := root[key]
		if !exists {
			continue
		}
		if strings.TrimSpace(string(raw)) == "null" {
			return fmt.Errorf("%s 不能是 null", key)
		}
		var err error
		if key == "enabledModels" {
			var value []string
			err = json.Unmarshal(raw, &value)
		} else {
			var value string
			err = json.Unmarshal(raw, &value)
		}
		if err != nil {
			return fmt.Errorf("%s 类型无效: %w", key, err)
		}
	}
	return nil
}

func stringField(root map[string]json.RawMessage, key string) string {
	raw, exists := root[key]
	if !exists {
		return ""
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return ""
	}
	return value
}

func stringSliceField(root map[string]json.RawMessage, key string) []string {
	raw, exists := root[key]
	if !exists {
		return nil
	}
	var values []string
	if err := json.Unmarshal(raw, &values); err != nil {
		return nil
	}
	return values
}

func setStringField(root map[string]json.RawMessage, key, value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		delete(root, key)
		return
	}
	data, err := json.Marshal(value)
	if err != nil {
		return
	}
	root[key] = json.RawMessage(data)
}
