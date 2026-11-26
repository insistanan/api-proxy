package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"insistanan/api-proxy/internal/orchestration/upstreamcontrol"
)

// 不启动 watcher 或后台维护，确保失败后观察到的状态仅来自被测写操作。
func newTransactionTestManager(t *testing.T, kind string) *ConfigManager {
	t.Helper()
	cfg := Config{Settings: SettingsConfig{Gateway: DefaultGatewaySettings(), ContentSafety: DefaultContentSafetyConfig()}}
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
		failedKeysCache: map[string]*FailedKey{transactionFailureIdentity(): {Timestamp: time.Now(), FailureCount: 1}},
	}
	if err := cm.SaveConfig(); err != nil {
		t.Fatal(err)
	}
	return cm
}

func blockTransactionBackup(t *testing.T, cm *ConfigManager) {
	t.Helper()
	// 首次写入没有旧文件，无备份目录；用普通文件占位使下一次提交前备份失败。
	if err := os.WriteFile(filepath.Join(filepath.Dir(cm.configFile), "backups"), []byte("blocked"), 0o600); err != nil {
		t.Fatal(err)
	}
}

var allChannelKinds = []string{"messages", "responses", "gemini", "chat", "images", "embeddings", "decisions"}

func TestSettingsPatchPreservesConcurrentCategories(t *testing.T) {
	cm := newTransactionTestManager(t, "messages")
	network := NetworkSettings{UpstreamProxyURL: "http://127.0.0.1:8080", UpstreamProxyEnabled: true}
	gateway := json.RawMessage(`{"enableRequestLogs":false}`)
	start := make(chan struct{})
	results := make(chan error, 2)
	var workers sync.WaitGroup
	for _, patch := range []SettingsPatch{{Network: &network}, {Gateway: &gateway}} {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			results <- cm.PatchSettings(patch)
		}()
	}
	close(start)
	workers.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatal(err)
		}
	}
	settings := cm.GetSettings()
	if settings.Network != network || settings.Gateway.EnableRequestLogs {
		t.Fatal("并发分类保存丢失了其他分类的修改")
	}
}

func TestConfigSaveRejectsExternalEditBeforeWatcher(t *testing.T) {
	cm := newTransactionTestManager(t, "messages")
	before := cm.GetConfig()
	external := cloneConfig(before)
	external.Upstream[0].Name = "external"
	data, err := json.Marshal(external)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cm.configFile, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := cm.SaveConfig(); !errors.Is(err, ErrConfigConflict) {
		t.Fatalf("旧快照必须报告版本冲突，得到 %v", err)
	}
	actual, err := os.ReadFile(cm.configFile)
	if err != nil || !bytes.Equal(actual, data) || !reflect.DeepEqual(before, cm.GetConfig()) {
		t.Fatal("冲突后修改了磁盘或已发布快照")
	}
	if err := cm.reloadFromDisk(); err != nil {
		t.Fatal(err)
	}
	if cm.GetConfig().Upstream[0].Name != "external" {
		t.Fatal("外部修改未在重载后生效")
	}
}

func TestConfigRejectsNonObjectRoot(t *testing.T) {
	cm := newTransactionTestManager(t, "messages")
	for _, input := range []string{"null", "[]", "true", ""} {
		if _, _, err := cm.prepareCandidate([]byte(input)); err == nil {
			t.Errorf("接受了非对象根节点 %q", input)
		}
	}
}

func TestChannelWritesCheckIdentityAndTombstones(t *testing.T) {
	cm := newTransactionTestManager(t, "messages")
	commands := []func(string) error{
		func(id string) error { _, err := cm.RemoveChannel("messages", 0, id); return err },
		func(id string) error { return cm.SetChannelStatusFor("messages", 0, ChannelStatusSuspended, id) },
		func(id string) error { return cm.SetChannelPromotionFor("messages", 0, time.Minute, 1, id) },
		func(id string) error { return cm.AddChannelKey("messages", 0, "new", id) },
		func(id string) error { return cm.RemoveChannelKey("messages", 0, "k1", id) },
		func(id string) error { return cm.MoveChannelKeyTop("messages", 0, "k2", id) },
	}
	before := cm.GetConfig()
	for _, command := range commands {
		if err := command("stale-id"); !errors.Is(err, ErrChannelIdentityConflict) {
			t.Fatalf("旧身份未被拒绝: %v", err)
		}
	}
	if !reflect.DeepEqual(before, cm.GetConfig()) {
		t.Fatal("身份冲突改变了配置")
	}
	if _, err := cm.RemoveChannel("messages", 0, "first"); err != nil {
		t.Fatal(err)
	}
	for _, command := range commands {
		if err := command("first"); !errors.Is(err, ErrChannelNotFound) {
			t.Fatalf("墓碑仍可修改: %v", err)
		}
	}
	if err := cm.DuplicateChannel("messages", 0); !errors.Is(err, ErrChannelNotFound) {
		t.Fatalf("墓碑仍可复制: %v", err)
	}
}

func TestChannelIDsAreUniqueAcrossProtocols(t *testing.T) {
	cm := newTransactionTestManager(t, "messages")
	duplicate := *cm.GetConfig().Upstream[0].Clone()
	if _, err := cm.AddChannel("chat", duplicate); err == nil {
		t.Fatal("允许跨协议复用稳定 ID")
	}
	candidate := cm.GetConfig()
	candidate.ChatUpstream = []UpstreamConfig{duplicate}
	raw, err := json.Marshal(candidate)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := cm.prepareCandidate(raw); err == nil {
		t.Fatal("热重载静默改写了重复 ID")
	}
}

func TestKeyEditsPreserveExplicitSuspension(t *testing.T) {
	cm := newTransactionTestManager(t, "messages")
	if err := cm.RemoveChannelKey("messages", 1, "k3"); err != nil {
		t.Fatal(err)
	}
	if cm.GetConfig().Upstream[1].Status != ChannelStatusSuspended {
		t.Fatal("删除最后一个 Key 后没有立即暂停")
	}
	if err := cm.AddChannelKey("messages", 1, "new"); err != nil {
		t.Fatal(err)
	}
	if _, err := cm.UpdateChannel("messages", 1, UpstreamUpdate{APIKeys: []string{"replacement"}}); err != nil {
		t.Fatal(err)
	}
	if cm.GetConfig().Upstream[1].Status != ChannelStatusSuspended {
		t.Fatal("更换 Key 意外恢复了暂停状态")
	}
	if err := cm.reloadFromDisk(); err != nil {
		t.Fatal(err)
	}
}

func TestConfigSnapshotsAreIsolatedFromCallers(t *testing.T) {
	for _, kind := range allChannelKinds {
		t.Run("channels/"+kind, func(t *testing.T) {
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
		})
	}

	t.Run("unknown kind does not fall back to messages", func(t *testing.T) {
		cm := newTransactionTestManager(t, "messages")
		before := cm.GetConfig()
		if _, _, err := cm.GetChannelConfig("invalid"); err == nil {
			t.Fatal("未知协议必须报错，不能退回 Messages")
		}
		if err := cm.DuplicateChannel("messeges", 0); err == nil {
			t.Fatal("未知渠道类型必须报错")
		}
		if !reflect.DeepEqual(before, cm.GetConfig()) {
			t.Fatal("未知类型误写 Messages 配置")
		}
	})

	t.Run("settings slices", func(t *testing.T) {
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
	})
}

// 所有写操作在备份阶段失败时，内存配置、失败密钥缓存与磁盘文件都必须保持原样。
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
		{name: "delete-nonempty-pool", prepare: func(cm *ConfigManager, kind string) {
			cs, _ := cm.config.channelSetFor(kind)
			(*cs.upstreams)[0].PoolID = "empty"
		}, mutate: func(cm *ConfigManager, kind string) error { return cm.DeleteChannelPool(kind, "empty") }},
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
	for _, kind := range allChannelKinds {
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
				failure := *cm.failedKeysCache[transactionFailureIdentity()]
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
				if got := cm.failedKeysCache[transactionFailureIdentity()]; got == nil || *got != failure {
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
		{"fuzzy-protocol", func(cm *ConfigManager) error { return cm.SetFuzzyModeForProtocol("responses", true) }},
		{"claude-disguise", func(cm *ConfigManager) error { return cm.SetClientDisguiseEnabled("messages", true) }},
		{"codex-disguise", func(cm *ConfigManager) error { return cm.SetClientDisguiseEnabled("responses", true) }},
		{"network", func(cm *ConfigManager) error {
			return cm.UpdateNetworkSettings(NetworkSettings{UpstreamProxyEnabled: true, UpstreamProxyURL: "http://127.0.0.1:7897"})
		}},
		{"settings", func(cm *ConfigManager) error {
			settings := cm.GetSettings()
			settings.Network.UpstreamProxyEnabled = true
			return cm.UpdateSettings(settings)
		}},
		{"intent", func(cm *ConfigManager) error {
			value := DefaultIntentRouting()
			value.Enabled = true
			value.Routes = []IntentRoute{{ID: "task", PoolID: "default", Patterns: []string{"task"}}}
			return cm.UpdateIntentRouting("messages", value)
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

func TestInvalidPoolReloadKeepsPublishedSnapshot(t *testing.T) {
	cm := newTransactionTestManager(t, "messages")
	before := cm.GetConfig()
	for _, kind := range allChannelKinds {
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

// 删除占位只落盘身份字段，图片能力迁移必须跳过它们，否则每次启动都会
// 判定待迁移、写回、再被墓碑序列化剥离，形成启动日志与磁盘写入的循环。
func TestVisionMigrationSkipsDeletedTombstones(t *testing.T) {
	rawOld := []byte(`{
		"fuzzyModeEnabled": true,
		"settings": {"contentSafety": {}},
		"responsesUpstream": [
			{"id": "live", "name": "live", "status": "active", "priority": 1},
			{"id": "gone", "name": "gone", "status": "deleted", "priority": 2}
		]
	}`)
	var candidate Config
	if err := json.Unmarshal(rawOld, &candidate); err != nil {
		t.Fatal(err)
	}
	for _, set := range candidate.channelSets() {
		*set.loadBalance = "failover"
		*set.pools = []ChannelPool{defaultChannelPool()}
		for index := range *set.upstreams {
			(*set.upstreams)[index].PoolID = DefaultChannelPoolID
		}
	}

	needSave, err := applyConfigDefaultsTo(&candidate, rawOld)
	if err != nil {
		t.Fatal(err)
	}
	if !needSave {
		t.Fatal("在线渠道缺少图片能力字段时应迁移")
	}
	if !candidate.ResponsesUpstream[0].VisionCapable {
		t.Fatal("在线渠道应默认启用图片理解")
	}
	if candidate.ResponsesUpstream[1].VisionCapable {
		t.Fatal("删除占位不应写入图片能力")
	}

	// 第二次加载：墓碑仍缺字段（只落盘 4 字段），但迁移必须已经稳定，不能再次要求保存。
	saved, err := json.Marshal(candidate)
	if err != nil {
		t.Fatal(err)
	}
	again := cloneConfig(candidate)
	needSave, err = applyConfigDefaultsTo(&again, saved)
	if err != nil {
		t.Fatal(err)
	}
	if needSave {
		t.Fatal("第二次加载不应再次触发迁移写入")
	}
}

func TestPromotionConsumptionDoesNotRequireDiskWrite(t *testing.T) {
	cm := newTransactionTestManager(t, "messages")
	cm.config.Upstream[0].PromotionCount = 3
	blockTransactionBackup(t, cm)
	cm.ConsumePromotionCount(0, "messages")
	if cm.GetConfig().Upstream[0].PromotionCount != 2 {
		t.Fatal("促销次数应在内存中消费，不依赖配置备份")
	}
}

// 删除渠道先落盘墓碑再清理失败密钥；墓碑只保留身份字段，索引稳定。
func TestChannelRemovalCommitsTombstoneBeforeCleanup(t *testing.T) {
	for _, kind := range []string{"messages", "responses", "gemini", "chat", "images"} {
		t.Run(kind, func(t *testing.T) {
			cm := newTransactionTestManager(t, kind)
			removed, err := cm.RemoveChannel(kind, 0)
			if err != nil || removed == nil || removed.ID != "first" {
				t.Fatalf("删除失败: %v", err)
			}
			if cm.failedKeysCache[transactionFailureIdentity()] != nil {
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
			if kind != "messages" {
				return
			}
			var doc map[string]json.RawMessage
			if err := json.Unmarshal(raw, &doc); err != nil {
				t.Fatal(err)
			}
			var upstreams []map[string]json.RawMessage
			if err := json.Unmarshal(doc["upstream"], &upstreams); err != nil {
				t.Fatal(err)
			}
			var tombstone map[string]json.RawMessage
			for _, entry := range upstreams {
				var status string
				if err := json.Unmarshal(entry["status"], &status); err == nil && status == ChannelStatusDeleted {
					tombstone = entry
					break
				}
			}
			if tombstone == nil {
				t.Fatal("删除占位未落盘")
			}
			if len(tombstone) != 4 {
				t.Fatalf("墓碑字段应为 id/name/priority/status，实际 %d 个: %v", len(tombstone), tombstone)
			}
			for _, key := range []string{"id", "name", "priority", "status"} {
				if _, ok := tombstone[key]; !ok {
					t.Fatalf("墓碑缺少字段 %s", key)
				}
			}
		})
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

func transactionFailureIdentity() string {
	channel := UpstreamConfig{BaseURL: "https://first.example.com", ServiceType: "openai"}
	return channel.RequestIdentity("k1").Fingerprint()
}

func TestKeyFailuresUseActualUpstreamIdentity(t *testing.T) {
	cm := newTransactionTestManager(t, "messages")
	cm.keyRecoveryTime = time.Hour
	first := cm.GetConfig().Upstream[0]
	cm.MarkKeyAsFailed(&first, "k1", "test")
	key, err := cm.GetNextAPIKey(&first, nil, "test")
	if err != nil || key != "k2" {
		t.Fatalf("失败身份未被避开: key=%s err=%v", key, err)
	}
	other := first
	other.BaseURL = "https://other.example.com"
	key, err = cm.GetNextAPIKey(&other, nil, "test")
	if err != nil || key != "k1" {
		t.Fatalf("另一地址被错误隔离: key=%s err=%v", key, err)
	}
	other = first
	other.ServiceType = "gemini"
	other.upstreamProtocol = "gemini"
	key, err = cm.GetNextAPIKey(&other, nil, "test")
	if err != nil || key != "k1" {
		t.Fatalf("另一协议被错误隔离: key=%s err=%v", key, err)
	}
	other.APIKeys = []string{"k1"}
	if _, err = cm.GetNextAPIKey(&other, map[string]bool{"k1": true}, "test"); err == nil {
		t.Fatal("单 Key 必须遵守请求内排除")
	}
}
