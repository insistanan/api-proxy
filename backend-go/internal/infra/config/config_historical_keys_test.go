// 历史 API Key 迁移验收：历史 Key 只在配置文件里出现一次（迁移前），
// 之后由指标库按渠道稳定 ID 保管，配置保存不再回写。
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// fakeHistoricalKeyStore 用内存模拟指标库注册表，便于断言写入与清理行为。
type fakeHistoricalKeyStore struct {
	mu       sync.Mutex
	keys     map[string]map[string][]string
	failSave bool
	failLoad bool
}

func newFakeHistoricalKeyStore() *fakeHistoricalKeyStore {
	return &fakeHistoricalKeyStore{keys: make(map[string]map[string][]string)}
}

func (s *fakeHistoricalKeyStore) LoadHistoricalKeys(kind string) (map[string][]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failLoad {
		return nil, errors.New("模拟读取失败")
	}
	out := make(map[string][]string, len(s.keys[kind]))
	for channelID, keys := range s.keys[kind] {
		out[channelID] = append([]string(nil), keys...)
	}
	return out, nil
}

func (s *fakeHistoricalKeyStore) SaveHistoricalKeys(kind string, channelID string, keys []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failSave {
		return errors.New("模拟写入失败")
	}
	if s.keys[kind] == nil {
		s.keys[kind] = make(map[string][]string)
	}
	s.keys[kind][channelID] = append([]string(nil), keys...)
	return nil
}

func (s *fakeHistoricalKeyStore) DeleteHistoricalKeys(kind string, channelID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.keys[kind], channelID)
	return nil
}

func (s *fakeHistoricalKeyStore) snapshot(kind, channelID string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.keys[kind][channelID]...)
}

func readConfigFileForTest(t *testing.T, path string) Config {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var saved Config
	require.NoError(t, json.Unmarshal(data, &saved))
	return saved
}

func requireNoHistoricalKeysOnDisk(t *testing.T, path string) {
	t.Helper()
	saved := readConfigFileForTest(t, path)
	for _, set := range saved.channelSets() {
		for index := range *set.upstreams {
			require.Emptyf(t, (*set.upstreams)[index].HistoricalAPIKeys,
				"配置文件不应再保存历史 Key: %s[%d]", set.kind, index)
		}
	}
}

// 启动迁移：内存保留历史 Key，指标库收到注册项，配置文件被剥离。
func TestHistoricalKeysMigrateOutOfConfigFile(t *testing.T) {
	cm := newTransactionTestManager(t, "messages")
	seed := cloneConfig(cm.config)
	seed.Upstream[0].HistoricalAPIKeys = []string{"old-1", "old-2"}
	cm.config = seed

	store := newFakeHistoricalKeyStore()
	cm.SetHistoricalKeyStore(store)
	require.NoError(t, cm.SyncHistoricalKeys())

	require.Equal(t, []string{"old-1", "old-2"}, cm.GetConfig().Upstream[0].HistoricalAPIKeys)
	require.Equal(t, []string{"old-1", "old-2"}, store.snapshot("messages", "first"))
	requireNoHistoricalKeysOnDisk(t, cm.configFile)
}

// 换 Key 后：新增的旧 Key 同步到指标库，配置文件依旧不落历史 Key。
func TestHistoricalKeysSyncOnKeyRotation(t *testing.T) {
	cm := newTransactionTestManager(t, "messages")
	store := newFakeHistoricalKeyStore()
	cm.SetHistoricalKeyStore(store)
	require.NoError(t, cm.SyncHistoricalKeys())

	_, err := cm.UpdateChannel("messages", 0, UpstreamUpdate{APIKeys: []string{"k1"}})
	require.NoError(t, err)
	require.Equal(t, []string{"k2"}, cm.GetConfig().Upstream[0].HistoricalAPIKeys)
	require.Equal(t, []string{"k2"}, store.snapshot("messages", "first"))
	requireNoHistoricalKeysOnDisk(t, cm.configFile)
}

// 删除渠道：墓碑不再保留历史 Key，指标库注册项同步清理。
func TestHistoricalKeysRemovedWhenChannelDeleted(t *testing.T) {
	cm := newTransactionTestManager(t, "messages")
	seed := cloneConfig(cm.config)
	seed.Upstream[0].HistoricalAPIKeys = []string{"old-1"}
	cm.config = seed

	store := newFakeHistoricalKeyStore()
	cm.SetHistoricalKeyStore(store)
	require.NoError(t, cm.SyncHistoricalKeys())
	require.Equal(t, []string{"old-1"}, store.snapshot("messages", "first"))

	removed, err := cm.RemoveChannel("messages", 0)
	require.NoError(t, err)
	require.Equal(t, "first", removed.ID)
	require.Empty(t, store.snapshot("messages", "first"))
	requireNoHistoricalKeysOnDisk(t, cm.configFile)
}

// 指标库写入失败时必须降级为随配置保存，不能丢历史 Key。
func TestHistoricalKeysFallbackWhenStoreFails(t *testing.T) {
	cm := newTransactionTestManager(t, "messages")
	seed := cloneConfig(cm.config)
	seed.Upstream[0].HistoricalAPIKeys = []string{"old-1"}
	cm.config = seed

	store := newFakeHistoricalKeyStore()
	store.failSave = true
	cm.SetHistoricalKeyStore(store)
	require.Error(t, cm.SyncHistoricalKeys())
	require.False(t, cm.historicalKeysReady)

	_, err := cm.UpdateChannel("messages", 0, UpstreamUpdate{APIKeys: []string{"k1"}})
	require.NoError(t, err)
	saved := readConfigFileForTest(t, cm.configFile)
	require.Equal(t, []string{"old-1", "k2"}, saved.Upstream[0].HistoricalAPIKeys)
}

// 指标库读取失败时按渠道 ID 沿用当前快照，且不阻断配置重载。
func TestHistoricalKeysReloadFallsBackToSnapshot(t *testing.T) {
	cm := newTransactionTestManager(t, "messages")
	seed := cloneConfig(cm.config)
	seed.Settings.Gateway = DefaultGatewaySettings()
	seed.Upstream[0].HistoricalAPIKeys = []string{"old-1"}
	cm.config = seed
	require.NoError(t, cm.SaveConfig())

	store := newFakeHistoricalKeyStore()
	cm.SetHistoricalKeyStore(store)
	require.NoError(t, cm.SyncHistoricalKeys())
	require.True(t, cm.historicalKeysReady)
	store.failLoad = true

	// 只改渠道名，保持其余磁盘内容原样，避免引入无关的迁移校验噪声。
	raw, err := os.ReadFile(cm.configFile)
	require.NoError(t, err)
	updated := bytes.Replace(raw, []byte(`"name": "first"`), []byte(`"name": "renamed"`), 1)
	require.NotEqual(t, raw, updated, "测试配置未包含预期渠道名")
	require.NoError(t, os.WriteFile(cm.configFile, updated, 0o600))

	require.NoError(t, cm.reloadFromDisk())
	reloaded := cm.GetConfig()
	require.Equal(t, "renamed", reloaded.Upstream[0].Name)
	require.Equal(t, []string{"old-1"}, reloaded.Upstream[0].HistoricalAPIKeys)
}

// 重载时配置文件没变：只从指标库补齐内存，不触发多余回写。
func TestHistoricalKeysReloadMergesFromStore(t *testing.T) {
	cm := newTransactionTestManager(t, "messages")
	store := newFakeHistoricalKeyStore()
	require.NoError(t, store.SaveHistoricalKeys("messages", "first", []string{"stored-1"}))
	cm.SetHistoricalKeyStore(store)
	require.NoError(t, cm.SyncHistoricalKeys())
	require.Equal(t, []string{"stored-1"}, cm.GetConfig().Upstream[0].HistoricalAPIKeys)
	requireNoHistoricalKeysOnDisk(t, cm.configFile)
}
