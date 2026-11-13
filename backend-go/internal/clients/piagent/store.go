package piagent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Store 封装 pi-agent 三个配置文件的读取与安全写入。
type Store struct {
	// BackupDir 为写前备份目录；为空时不创建备份。
	BackupDir string
}

// NewStore 创建 Store，备份目录默认放在 api-proxy 的 .config/backups/pi-agent。
func NewStore(backupDir string) *Store {
	return &Store{BackupDir: backupDir}
}

// DefaultBackupDir 返回默认备份目录。
func DefaultBackupDir() (string, error) {
	if _, err := ResolveConfigDir(); err != nil {
		return "", err
	}
	workingDir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return filepath.Join(workingDir, ".config", "backups", "pi-agent"), nil
}

// LockAndCheck 获取管理锁并检测 pi-agent proper-lockfile。返回 release 函数。
func (s *Store) LockAndCheck() (release func(), err error) {
	dir, err := ResolveConfigDir()
	if err != nil {
		return nil, err
	}
	if err := checkPiAgentLocks(dir); err != nil {
		return nil, err
	}
	return acquireManagerLock(dir)
}

// ReadProviders 读取 models.json 中的 provider map。
func (s *Store) ReadProviders() (map[string]ProviderConfig, map[string]json.RawMessage, []byte, bool, error) {
	path, err := s.modelsPath()
	if err != nil {
		return nil, nil, nil, false, err
	}
	root, raw, exists, err := ReadJSON(path)
	if err != nil {
		return nil, nil, raw, exists, err
	}
	providers := make(map[string]ProviderConfig)
	rawProviders, err := objectRawValue(root[KeyProvider])
	if err != nil {
		return nil, nil, raw, exists, fmt.Errorf("providers 字段必须是 JSON 对象: %w", err)
	}
	for id, rawProvider := range rawProviders {
		provider, err := decodeProvider(rawProvider)
		if err != nil {
			return nil, nil, raw, exists, fmt.Errorf("provider %s 解析失败: %w", id, err)
		}
		providers[id] = provider
	}
	return providers, root, raw, exists, nil
}

// SaveProviders 整体替换 provider map，返回新 revision。
// revision 参数用于并发冲突检测：非空且与磁盘不符时返回 ErrRevisionMismatch。
func (s *Store) SaveProviders(providers map[string]ProviderConfig, revision string) (WriteResult, error) {
	path, err := s.modelsPath()
	if err != nil {
		return WriteResult{}, err
	}
	root, raw, exists, err := ReadJSON(path)
	if err != nil {
		return WriteResult{}, err
	}
	if exists && revision == "" {
		return WriteResult{}, ErrRevisionRequired
	}
	if revision != "" && SHA256Hex(raw) != revision {
		return WriteResult{}, ErrRevisionMismatch
	}

	rawProviders := make(map[string]json.RawMessage, len(providers))
	for id, provider := range providers {
		encoded, err := encodeProvider(provider)
		if err != nil {
			return WriteResult{}, fmt.Errorf("provider %s 序列化失败: %w", id, err)
		}
		rawProviders[id] = encoded
	}
	if len(rawProviders) == 0 {
		delete(root, KeyProvider)
	} else {
		providersBytes, err := json.Marshal(rawProviders)
		if err != nil {
			return WriteResult{}, err
		}
		root[KeyProvider] = json.RawMessage(providersBytes)
	}

	data, err := MarshalIndent(root)
	if err != nil {
		return WriteResult{}, err
	}
	return WriteFileAtomic(path, data, true, s.BackupDir)
}

// UpsertProvider 新增或更新单个 provider。
func (s *Store) UpsertProvider(id string, provider ProviderConfig, revision string) (WriteResult, error) {
	providers, _, raw, _, err := s.ReadProviders()
	if err != nil {
		return WriteResult{}, err
	}
	if revision != "" && SHA256Hex(raw) != revision {
		return WriteResult{}, ErrRevisionMismatch
	}
	providers[id] = provider
	return s.SaveProviders(providers, revision)
}

// DeleteProvider 删除单个 provider。
func (s *Store) DeleteProvider(id string, revision string) (WriteResult, error) {
	providers, _, raw, _, err := s.ReadProviders()
	if err != nil {
		return WriteResult{}, err
	}
	if revision != "" && SHA256Hex(raw) != revision {
		return WriteResult{}, ErrRevisionMismatch
	}
	if _, exists := providers[id]; !exists {
		return WriteResult{}, fmt.Errorf("provider 不存在: %s", id)
	}
	delete(providers, id)
	return s.SaveProviders(providers, revision)
}

// ListModels 列出 provider 下的模型 ID 列表。
func (s *Store) ListModels(providerID string) ([]ModelConfig, error) {
	providers, _, _, _, err := s.ReadProviders()
	if err != nil {
		return nil, err
	}
	provider, exists := providers[providerID]
	if !exists {
		return nil, fmt.Errorf("provider 不存在: %s", providerID)
	}
	return provider.Models, nil
}

func (s *Store) modelsPath() (string, error) {
	dir, err := ResolveConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, string(FileModels)), nil
}

func objectRawValue(raw json.RawMessage) (map[string]json.RawMessage, error) {
	if len(raw) == 0 {
		return make(map[string]json.RawMessage), nil
	}
	result := make(map[string]json.RawMessage)
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}
	return result, nil
}
