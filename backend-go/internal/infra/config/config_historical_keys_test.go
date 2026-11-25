// 历史 API Key 提交验收：稳态由指标库保管，提交中断时由配置中的完整快照恢复。
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// fakeHistoricalKeyStore 用内存模拟指标库注册表，便于断言写入与清理行为。
type fakeHistoricalKeyStore struct {
	mu         sync.Mutex
	keys       map[string]map[string][]string
	failSave   bool
	failLoad   bool
	failDelete bool
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
	if s.failDelete {
		return errors.New("模拟删除失败")
	}
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

// 同步失败必须保留完整提交和明确错误，恢复后不能永久回退为双份存储。
func TestHistoricalKeys_ReportsPendingCommitAndRecovers(t *testing.T) {
	cm := newTransactionTestManager(t, "messages")
	seed := cloneConfig(cm.config)
	seed.Upstream[0].HistoricalAPIKeys = []string{"old-1"}
	cm.config = seed

	store := newFakeHistoricalKeyStore()
	store.failSave = true
	cm.SetHistoricalKeyStore(store)
	err := cm.SyncHistoricalKeys()
	require.Error(t, err)
	require.True(t, IsCommittedError(err))
	require.True(t, cm.historicalKeysReady)

	_, err = cm.UpdateChannel("messages", 0, UpstreamUpdate{APIKeys: []string{"k1"}})
	require.Error(t, err)
	require.True(t, IsCommittedError(err))
	saved := readConfigFileForTest(t, cm.configFile)
	require.True(t, saved.HistoricalKeysPending)
	require.Equal(t, []string{"old-1", "k2"}, saved.Upstream[0].HistoricalAPIKeys)
	store.failSave = false
	require.NoError(t, cm.SaveConfig())
	require.False(t, cm.GetConfig().HistoricalKeysPending)
	require.Equal(t, []string{"old-1", "k2"}, store.snapshot("messages", "first"))
	requireNoHistoricalKeysOnDisk(t, cm.configFile)
}

// 读取数据库失败时拒绝发布候选，不能报告重载成功却沿用部分旧数据。
func TestHistoricalKeys_RejectsReloadWhenStoreReadFails(t *testing.T) {
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

	require.Error(t, cm.reloadFromDisk())
	reloaded := cm.GetConfig()
	require.Equal(t, "first", reloaded.Upstream[0].Name)
	require.Equal(t, []string{"old-1"}, reloaded.Upstream[0].HistoricalAPIKeys)
}

func TestHistoricalKeys_ConfigFailureDoesNotDeleteDatabaseRecords(t *testing.T) {
	cm := newTransactionTestManager(t, "messages")
	cm.config.Upstream[0].HistoricalAPIKeys = []string{"old-1"}
	store := newFakeHistoricalKeyStore()
	cm.SetHistoricalKeyStore(store)
	require.NoError(t, cm.SyncHistoricalKeys())
	before := cm.GetConfig()
	backupDir := filepath.Join(filepath.Dir(cm.configFile), "backups")
	require.NoError(t, os.Rename(backupDir, backupDir+".saved"))
	blockTransactionBackup(t, cm)
	_, err := cm.RemoveChannel("messages", 0)
	require.Error(t, err)
	require.False(t, IsCommittedError(err))
	require.Equal(t, before, cm.GetConfig())
	require.Equal(t, []string{"old-1"}, store.snapshot("messages", "first"))
}

func TestHistoricalKeys_RestartCompletesPendingDeletion(t *testing.T) {
	cm := newTransactionTestManager(t, "messages")
	cm.config.Upstream[0].HistoricalAPIKeys = []string{"old-1"}
	store := newFakeHistoricalKeyStore()
	cm.SetHistoricalKeyStore(store)
	require.NoError(t, cm.SyncHistoricalKeys())
	store.failDelete = true
	_, err := cm.RemoveChannel("messages", 0)
	require.Error(t, err)
	require.True(t, IsCommittedError(err))
	require.True(t, readConfigFileForTest(t, cm.configFile).HistoricalKeysPending)
	require.Equal(t, []string{"old-1"}, store.snapshot("messages", "first"))
	// 不启动 watcher，直接模拟进程重建和装配阶段恢复。
	restarted := &ConfigManager{configFile: cm.configFile}
	require.NoError(t, restarted.loadConfig())
	store.failDelete = false
	restarted.SetHistoricalKeyStore(store)
	require.NoError(t, restarted.SyncHistoricalKeys())
	require.Equal(t, ChannelStatusDeleted, restarted.GetConfig().Upstream[0].Status)
	require.Empty(t, store.snapshot("messages", "first"))
	require.False(t, readConfigFileForTest(t, cm.configFile).HistoricalKeysPending)
}

// 稳态启动只从指标库补齐内存，不触发待同步提交或多余备份。
func TestHistoricalKeys_StartupReadsStoreWithoutRewritingConfig(t *testing.T) {
	cm := newTransactionTestManager(t, "messages")
	before, err := os.ReadFile(cm.configFile)
	require.NoError(t, err)
	store := newFakeHistoricalKeyStore()
	require.NoError(t, store.SaveHistoricalKeys("messages", "first", []string{"stored-1"}))
	cm.SetHistoricalKeyStore(store)
	require.NoError(t, cm.SyncHistoricalKeys())
	require.Equal(t, []string{"stored-1"}, cm.GetConfig().Upstream[0].HistoricalAPIKeys)
	requireNoHistoricalKeysOnDisk(t, cm.configFile)
	after, err := os.ReadFile(cm.configFile)
	require.NoError(t, err)
	require.Equal(t, before, after)
	_, err = os.Stat(filepath.Join(filepath.Dir(cm.configFile), "backups"))
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestHistoricalKeys_ReloadRecoversOrphanedDatabaseRecord(t *testing.T) {
	cm := newTransactionTestManager(t, "messages")
	store := newFakeHistoricalKeyStore()
	cm.SetHistoricalKeyStore(store)
	require.NoError(t, cm.SyncHistoricalKeys())
	// 数据库中的孤立注册项也需要先提交恢复标记，不能在加载时直接删除。
	require.NoError(t, store.SaveHistoricalKeys("messages", "orphan", []string{"old"}))
	store.failDelete = true
	raw, err := os.ReadFile(cm.configFile)
	require.NoError(t, err)
	updated := bytes.Replace(raw, []byte(`"name": "first"`), []byte(`"name": "renamed"`), 1)
	require.NotEqual(t, raw, updated)
	require.NoError(t, os.WriteFile(cm.configFile, updated, 0o600))
	err = cm.reloadFromDisk()
	require.Error(t, err)
	require.True(t, IsCommittedError(err))
	require.Equal(t, "renamed", cm.GetConfig().Upstream[0].Name)
	require.True(t, readConfigFileForTest(t, cm.configFile).HistoricalKeysPending)
	store.failDelete = false
	require.NoError(t, cm.SaveConfig())
	require.Empty(t, store.snapshot("messages", "orphan"))
	require.False(t, readConfigFileForTest(t, cm.configFile).HistoricalKeysPending)
}
