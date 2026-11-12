package config

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/insistanan/api-proxy/internal/orchestration/upstreamcontrol"
)

// 不启动 watcher 或后台维护，确保失败后观察到的状态仅来自被测写操作。
func newTransactionTestManager(t *testing.T, kind string) *ConfigManager {
	t.Helper()
	cfg := Config{Settings: SettingsConfig{ContentSafety: DefaultContentSafetyConfig()}}
	cs, err := cfg.channelSetFor(kind)
	if err != nil {
		t.Fatal(err)
	}
	*cs.loadBalance = "failover"
	*cs.pools = []ChannelPool{defaultChannelPool(), {ID: "empty", Name: "空分组", ModelMatcher: "empty", Priority: 1}}
	*cs.upstreams = []UpstreamConfig{
		{ID: "first", PoolID: DefaultChannelPoolID, Name: "first", BaseURL: "https://first.example.com", APIKeys: []string{"k1", "k2"}, ServiceType: "openai", Status: ChannelStatusActive, Priority: 1, MaxConcurrency: upstreamcontrol.DefaultConcurrency},
		{ID: "second", PoolID: DefaultChannelPoolID, Name: "second", BaseURL: "https://second.example.com", APIKeys: []string{"k3"}, ServiceType: "openai", Status: ChannelStatusActive, Priority: 2, MaxConcurrency: upstreamcontrol.DefaultConcurrency},
	}
	cm := &ConfigManager{
		config: cfg, configFile: filepath.Join(t.TempDir(), "config.json"),
		failedKeysCache: map[string]*FailedKey{"k1": {Timestamp: time.Now(), FailureCount: 1}},
	}
	if err := cm.SaveConfig(); err != nil {
		t.Fatal(err)
	}
	return cm
}

func TestGetChannelConfigReturnsIsolatedSnapshot(t *testing.T) {
	for _, kind := range []string{"messages", "responses", "gemini", "chat", "images", "embeddings", "decisions"} {
		t.Run(kind, func(t *testing.T) {
			cm := newTransactionTestManager(t, kind)
			list, strategy, err := cm.GetChannelConfig(kind)
			if err != nil {
				t.Fatal(err)
			}
			if len(list) != 2 || strategy != "failover" {
				t.Fatalf("读取了错误的渠道集合: list=%+v strategy=%s", list, strategy)
			}
			list[0].Name = "outside"
			list[0].APIKeys[0] = "outside"
			again, _, err := cm.GetChannelConfig(kind)
			if err != nil {
				t.Fatal(err)
			}
			if again[0].Name != "first" || again[0].APIKeys[0] != "k1" {
				t.Fatalf("返回值修改污染了配置快照: %+v", again[0])
			}
			if _, _, err := cm.GetChannelConfig("invalid"); err == nil {
				t.Fatal("未知协议必须报错，不能退回 Messages")
			}
		})
	}
}

func blockTransactionBackup(t *testing.T, cm *ConfigManager) {
	t.Helper()
	// 首次写入没有旧文件，无备份目录；用普通文件占位使下一次提交前备份失败。
	if err := os.WriteFile(filepath.Join(filepath.Dir(cm.configFile), "backups"), []byte("blocked"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestChannelTransactionsKeepSnapshotOnSaveFailure(t *testing.T) {
	name := "changed"
	ops := []struct {
		name    string
		prepare func(*ConfigManager, string)
		mutate  func(*ConfigManager, string) error
	}{
		{name: "add", mutate: func(cm *ConfigManager, kind string) error {
			_, err := cm.AddChannel(kind, UpstreamConfig{Name: "new", BaseURL: "https://new.example.com", APIKeys: []string{"new-key"}, ServiceType: "openai"})
			return err
		}},
		{name: "update", mutate: func(cm *ConfigManager, kind string) error {
			_, err := cm.UpdateChannel(kind, 0, UpstreamUpdate{Name: &name})
			return err
		}},
		{name: "remove", mutate: func(cm *ConfigManager, kind string) error {
			_, err := cm.RemoveChannel(kind, 0)
			return err
		}},
		{name: "add-key", mutate: func(cm *ConfigManager, kind string) error { return cm.AddChannelKey(kind, 0, "new-key") }},
		{name: "remove-key", mutate: func(cm *ConfigManager, kind string) error { return cm.RemoveChannelKey(kind, 0, "k1") }},
		{name: "move-key-top", mutate: func(cm *ConfigManager, kind string) error { return cm.MoveChannelKeyTop(kind, 0, "k2") }},
		{name: "move-key-bottom", mutate: func(cm *ConfigManager, kind string) error { return cm.MoveAPIKeyToBottomForKind(kind, 0, "k1") }},
		{name: "reorder", mutate: func(cm *ConfigManager, kind string) error { return cm.ReorderChannels(kind, []int{1, 0}) }},
		{name: "status", mutate: func(cm *ConfigManager, kind string) error {
			return cm.SetChannelStatusFor(kind, 0, ChannelStatusSuspended)
		}},
		{name: "promotion", mutate: func(cm *ConfigManager, kind string) error { return cm.SetChannelPromotionFor(kind, 0, time.Minute, 0) }},
		{name: "duplicate", mutate: func(cm *ConfigManager, kind string) error { return cm.DuplicateChannel(kind, 0) }},
		{name: "tidy", prepare: func(cm *ConfigManager, kind string) {
			cs, _ := cm.config.channelSetFor(kind)
			(*cs.upstreams)[0].Priority = 5
		}, mutate: func(cm *ConfigManager, kind string) error { return cm.TidyProblemChannels(kind) }},
		{name: "cache-key-capability", mutate: func(cm *ConfigManager, kind string) error { return cm.MarkPromptCacheKeyUnsupported(kind, "first") }},
		{name: "reasoning-capability", mutate: func(cm *ConfigManager, kind string) error { return cm.MarkReasoningContentRequired(kind, "first") }},
		{name: "create-pool", mutate: func(cm *ConfigManager, kind string) error {
			_, err := cm.CreateChannelPool(kind, ChannelPool{Name: "new", ModelMatcher: "new"})
			return err
		}},
		{name: "update-pool", mutate: func(cm *ConfigManager, kind string) error {
			return cm.UpdateChannelPool(kind, "empty", ChannelPoolUpdate{Name: &name})
		}},
		{name: "delete-pool", mutate: func(cm *ConfigManager, kind string) error { return cm.DeleteChannelPool(kind, "empty") }},
		{name: "pool-layout", mutate: func(cm *ConfigManager, kind string) error {
			return cm.SaveChannelPoolLayout(kind, []ChannelPoolLayout{{PoolID: DefaultChannelPoolID, ChannelIDs: []string{"second"}}, {PoolID: "empty", ChannelIDs: []string{"first"}}})
		}},
		{name: "lifecycle", prepare: func(cm *ConfigManager, kind string) {
			cs, _ := cm.config.channelSetFor(kind)
			expired := time.Now().Add(-time.Hour)
			(*cs.upstreams)[0].Temporary = true
			(*cs.upstreams)[0].TemporaryUntil = &expired
		}, mutate: func(cm *ConfigManager, _ string) error { return cm.runChannelLifecycle(time.Now(), true) }},
	}
	for _, kind := range []string{"messages", "responses", "gemini", "chat", "images", "embeddings", "decisions"} {
		for _, op := range ops {
			t.Run(kind+"/"+op.name, func(t *testing.T) {
				cm := newTransactionTestManager(t, kind)
				if op.prepare != nil {
					op.prepare(cm, kind)
					data, err := json.MarshalIndent(cm.config, "", "  ")
					if err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(cm.configFile, data, 0o600); err != nil {
						t.Fatal(err)
					}
					cm.persistedDigest = configDigest(data)
				}
				before := cm.GetConfig()
				failure := *cm.failedKeysCache["k1"]
				diskBefore, err := os.ReadFile(cm.configFile)
				if err != nil {
					t.Fatal(err)
				}
				blockTransactionBackup(t, cm)
				err = op.mutate(cm, kind)
				if err == nil || IsCommittedError(err) {
					t.Fatalf("备份失败应返回未提交错误: %v", err)
				}
				if !strings.Contains(err.Error(), "备份当前配置失败") {
					t.Fatalf("写操作没有到达备份阶段: %v", err)
				}
				if !reflect.DeepEqual(before, cm.GetConfig()) {
					t.Fatal("提交前失败污染当前配置")
				}
				if got := cm.failedKeysCache["k1"]; got == nil || *got != failure {
					t.Fatal("提交前失败提前清理失败密钥")
				}
				diskAfter, err := os.ReadFile(cm.configFile)
				if err != nil || !bytes.Equal(diskBefore, diskAfter) {
					t.Fatalf("提交前失败改变磁盘配置: %v", err)
				}
			})
		}
	}
}

func TestSettingsTransactionsKeepSnapshotOnSaveFailure(t *testing.T) {
	ops := []struct {
		name   string
		mutate func(*ConfigManager) error
	}{
		{"fuzzy", func(cm *ConfigManager) error { return cm.SetFuzzyModeEnabled(true) }},
		{"claude-disguise", func(cm *ConfigManager) error { return cm.SetClientDisguiseEnabled("messages", true) }},
		{"codex-disguise", func(cm *ConfigManager) error { return cm.SetClientDisguiseEnabled("responses", true) }},
		{"network", func(cm *ConfigManager) error {
			return cm.UpdateNetworkSettings(NetworkSettings{UpstreamProxyEnabled: true, UpstreamProxyURL: "http://127.0.0.1:7897"})
		}},
		{"settings", func(cm *ConfigManager) error {
			settings := cm.GetSettings()
			settings.Integration.CCSwitchPath = "changed"
			return cm.UpdateSettings(settings)
		}},
	}
	for _, op := range ops {
		t.Run(op.name, func(t *testing.T) {
			cm := newTransactionTestManager(t, "messages")
			before := cm.GetConfig()
			blockTransactionBackup(t, cm)
			if err := op.mutate(cm); err == nil || IsCommittedError(err) {
				t.Fatalf("应返回未提交错误: %v", err)
			}
			if !reflect.DeepEqual(before, cm.GetConfig()) {
				t.Fatal("设置保存失败污染当前配置")
			}
		})
	}
}

func TestConfigSnapshotIsolatesSettingsSlices(t *testing.T) {
	cm := newTransactionTestManager(t, "messages")
	cm.config.Settings.ContentSafety.Allowlist.Entries = []string{"original"}
	cm.config.Settings.ContentSafety.CustomRules.Rules = []CustomRuleItem{{Name: "original"}}
	cm.config.Settings.ContentSafety.DangerousCmd.Semantic = &SemanticSafetyConfig{DatasetIDs: []string{"original"}}
	cm.config.Settings.Prompts.Entries = []PromptEntry{{Version: "original"}}
	before := cm.GetConfig()
	snapshot := cm.GetConfig()
	snapshot.Settings.ContentSafety.Allowlist.Entries[0] = "changed"
	snapshot.Settings.ContentSafety.CustomRules.Rules[0].Name = "changed"
	snapshot.Settings.Prompts.Entries[0].Version = "changed"
	snapshot.Upstream[0].APIKeys[0] = "changed"
	safety := cm.GetContentSafetyConfig()
	safety.Allowlist.Entries[0] = "changed"
	safety.CustomRules.Rules[0].Name = "changed"
	safety.DangerousCmd.Semantic.DatasetIDs[0] = "changed"
	if !reflect.DeepEqual(before, cm.GetConfig()) {
		t.Fatal("修改读取快照污染运行配置")
	}
}

func TestInvalidPoolReloadKeepsPublishedSnapshot(t *testing.T) {
	cm := newTransactionTestManager(t, "messages")
	before := cm.GetConfig()
	for _, kind := range []string{"messages", "responses", "gemini", "chat", "images", "embeddings", "decisions"} {
		t.Run(kind, func(t *testing.T) {
			candidate := cm.GetConfig()
			set, err := candidate.channelSetFor(kind)
			if err != nil {
				t.Fatal(err)
			}
			// 普通分组不能占用兜底分组的 * 规则。
			*set.pools = append(*set.pools, ChannelPool{ID: "broken", Name: "broken", ModelMatcher: "*"})
			data, err := json.Marshal(candidate)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(cm.configFile, data, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := cm.reloadFromDisk(); err == nil {
				t.Fatal("无效分组必须阻止候选发布")
			}
			if !reflect.DeepEqual(before, cm.GetConfig()) {
				t.Fatal("分组校验失败污染当前配置")
			}
		})
	}
}

func TestConfigClonePreservesNilAndEmptySlices(t *testing.T) {
	for _, original := range []Config{{}, {Upstream: []UpstreamConfig{}, MessagePools: []ChannelPool{}, Settings: SettingsConfig{Prompts: PromptSettings{Entries: []PromptEntry{}}}}} {
		if !reflect.DeepEqual(original, cloneConfig(original)) {
			t.Fatal("配置副本改变 nil 与空切片语义")
		}
	}
}

func TestPromotionConsumptionKeepsCountOnSaveFailure(t *testing.T) {
	cm := newTransactionTestManager(t, "messages")
	cm.config.Upstream[0].PromotionCount = 3
	blockTransactionBackup(t, cm)
	cm.ConsumePromotionCount(0, "messages")
	if cm.GetConfig().Upstream[0].PromotionCount != 3 {
		t.Fatal("促销次数保存失败污染当前配置")
	}
}

func TestUnknownChannelKindDoesNotMutateMessages(t *testing.T) {
	cm := newTransactionTestManager(t, "messages")
	before := cm.GetConfig()
	if err := cm.DuplicateChannel("messeges", 0); err == nil {
		t.Fatal("未知渠道类型必须报错")
	}
	if !reflect.DeepEqual(before, cm.GetConfig()) {
		t.Fatal("未知类型误写 Messages 配置")
	}
}

func TestMigrationSaveFailureKeepsPublishedSnapshot(t *testing.T) {
	cm := newTransactionTestManager(t, "messages")
	before := cm.GetConfig()
	blockTransactionBackup(t, cm)
	// 旧格式必须补全默认设置并回写；失败时不应提前发布迁移候选。
	if err := os.WriteFile(cm.configFile, []byte(`{"upstream":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := cm.reloadFromDisk(); err == nil {
		t.Fatal("迁移回写应失败")
	}
	if !reflect.DeepEqual(before, cm.GetConfig()) {
		t.Fatal("迁移回写失败提前发布候选")
	}
}

func TestChannelRemovalCommitsBeforeCleanup(t *testing.T) {
	for _, kind := range []string{"messages", "responses", "gemini", "chat", "images"} {
		t.Run(kind, func(t *testing.T) {
			cm := newTransactionTestManager(t, kind)
			removed, err := cm.RemoveChannel(kind, 0)
			if err != nil || removed == nil || removed.ID != "first" {
				t.Fatalf("删除失败: %v", err)
			}
			if cm.failedKeysCache["k1"] != nil {
				t.Fatal("提交成功未清理失败密钥")
			}
			raw, err := os.ReadFile(cm.configFile)
			if err != nil {
				t.Fatal(err)
			}
			var persisted Config
			if err := json.Unmarshal(raw, &persisted); err != nil {
				t.Fatal(err)
			}
			cs, err := persisted.channelSetFor(kind)
			if err != nil || (*cs.upstreams)[0].Status != ChannelStatusDeleted {
				t.Fatalf("删除结果未落盘: %v", err)
			}
		})
	}
}
