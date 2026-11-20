package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/insistanan/api-proxy/internal/orchestration/upstreamcontrol"
)

func TestDeleteChannelPoolDeletesAllMembersAtomically(t *testing.T) {
	for _, kind := range []string{"messages", "responses", "gemini", "chat", "images", "embeddings", "decisions"} {
		t.Run(kind, func(t *testing.T) {
			cm := newTransactionTestManager(t, kind)
			cs, err := cm.config.channelSetFor(kind)
			if err != nil {
				t.Fatal(err)
			}
			original := (*cs.upstreams)[1]
			for _, status := range []string{ChannelStatusActive, ChannelStatusSuspended, ChannelStatusDisabled, ChannelStatusDeprecated} {
				channel := (*cs.upstreams)[0]
				channel.ID = status
				channel.PoolID = "empty"
				channel.Status = status
				channel.ExcludeFromConversation = status == ChannelStatusDisabled
				*cs.upstreams = append(*cs.upstreams, channel)
			}
			if err := cm.SaveConfig(); err != nil {
				t.Fatal(err)
			}
			removed, err := cm.DeleteChannelPoolWithChannels(kind, "empty")
			if err != nil {
				t.Fatal(err)
			}
			if len(removed) != 4 {
				t.Fatalf("removed = %d, want 4", len(removed))
			}
			got, _, err := cm.GetChannelConfig(kind)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got[1], original) {
				t.Fatal("其他渠道发生变化")
			}
			for i, channel := range removed {
				if channel.Index != i+2 || got[channel.Index].Status != ChannelStatusDeleted || len(got[channel.Index].APIKeys) != 0 {
					t.Fatalf("渠道未删除或索引发生变化: %+v", channel)
				}
			}
			if poolExists(cm.GetChannelPools(kind), "empty") {
				t.Fatal("分组未删除")
			}
			data, err := os.ReadFile(cm.configFile)
			if err != nil {
				t.Fatal(err)
			}
			var persisted Config
			if err := json.Unmarshal(data, &persisted); err != nil {
				t.Fatal(err)
			}
			persistedSet, err := persisted.channelSetFor(kind)
			if err != nil {
				t.Fatal(err)
			}
			if poolExists(*persistedSet.pools, "empty") || (*persistedSet.upstreams)[2].Status != ChannelStatusDeleted {
				t.Fatal("删除未完整落盘")
			}
		})
	}
}

func TestDeleteChannelPoolDetachesExternalVisionReference(t *testing.T) {
	cm := newTransactionTestManager(t, "messages")
	cm.config.Upstream[0].PoolID = "empty"
	cm.config.ChatUpstream = []UpstreamConfig{{ID: "owner", Name: "owner", VisionCapable: true, VisionLayerEnabled: true, VisionLayerChannelID: "first", VisionLayerModel: "vision-model", MaxConcurrency: upstreamcontrol.DefaultConcurrency}}
	if err := cm.DeleteChannelPool("messages", "empty"); err != nil {
		t.Fatal(err)
	}
	owner := cm.config.ChatUpstream[0]
	if owner.VisionCapable || owner.VisionLayerEnabled || owner.VisionLayerChannelID != "" || owner.VisionLayerModel != "" {
		t.Fatalf("引用方仍保留图片理解配置: %+v", owner)
	}
}

func TestDeleteChannelPoolAllowsInternalVisionReferences(t *testing.T) {
	cm := newTransactionTestManager(t, "messages")
	for i := range cm.config.Upstream {
		cm.config.Upstream[i].PoolID = "empty"
	}
	cm.config.Upstream[0].VisionLayerEnabled = true
	cm.config.Upstream[0].VisionLayerChannelID = "second"
	if err := cm.DeleteChannelPool("messages", "empty"); err != nil {
		t.Fatal(err)
	}
	for _, channel := range cm.config.Upstream {
		if channel.Status != ChannelStatusDeleted {
			t.Fatal("同组渠道未全部删除")
		}
	}
}

func TestDeleteNonEmptyChannelPoolKeepsSnapshotOnSaveFailure(t *testing.T) {
	cm := newTransactionTestManager(t, "messages")
	cm.config.Upstream[0].PoolID = "empty"
	before := cloneConfig(cm.config)
	data, err := os.ReadFile(cm.configFile)
	if err != nil {
		t.Fatal(err)
	}
	blockTransactionBackup(t, cm)
	if err := cm.DeleteChannelPool("messages", "empty"); err == nil {
		t.Fatal("预期备份失败")
	}
	if !reflect.DeepEqual(before, cm.config) {
		t.Fatal("写入失败后内存快照发生变化")
	}
	after, err := os.ReadFile(cm.configFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string(after) {
		t.Fatal("写入失败后磁盘配置发生变化")
	}
}

func TestSelectChannelPoolUsesLongestMatcher(t *testing.T) {
	pools := []ChannelPool{
		defaultChannelPool(),
		{ID: "claude", Name: "Claude", ModelMatcher: "claude", Priority: 2},
		{ID: "sonnet", Name: "Sonnet", ModelMatcher: "claude-sonnet", Priority: 3},
	}

	selected, err := SelectChannelPool(pools, "CLAUDE-SONNET-4")
	if err != nil {
		t.Fatalf("SelectChannelPool() error = %v", err)
	}
	if selected.ID != "sonnet" {
		t.Fatalf("SelectChannelPool() ID = %q, want sonnet", selected.ID)
	}
}

func TestSelectChannelPoolRouteReturnsMatchedThenFallback(t *testing.T) {
	pools := []ChannelPool{
		{ID: "gpt", Name: "GPT", ModelMatcher: "gpt", Priority: 1},
		defaultChannelPool(),
	}

	route, err := SelectChannelPoolRoute(pools, "gpt-5.4")
	if err != nil {
		t.Fatalf("SelectChannelPoolRoute() error = %v", err)
	}
	if len(route) != 2 || route[0].ID != "gpt" || route[1].ID != DefaultChannelPoolID {
		t.Fatalf("SelectChannelPoolRoute() = %#v", route)
	}

	route, err = SelectChannelPoolRoute(pools, "claude-sonnet")
	if err != nil {
		t.Fatalf("SelectChannelPoolRoute() fallback error = %v", err)
	}
	if len(route) != 1 || route[0].ID != DefaultChannelPoolID {
		t.Fatalf("SelectChannelPoolRoute() fallback = %#v", route)
	}
}

func TestNormalizeChannelPoolsRejectsWildcardOutsideFallbackGroup(t *testing.T) {
	pools := []ChannelPool{
		defaultChannelPool(),
		{ID: "other", Name: "Other", ModelMatcher: "*", Priority: 2},
	}
	if _, err := normalizeChannelPools(pools); err == nil {
		t.Fatal("normalizeChannelPools() error = nil")
	}
}

func TestNormalizeUpstreamPrioritiesScopesByPool(t *testing.T) {
	upstreams := []UpstreamConfig{
		{ID: "default-2", PoolID: DefaultChannelPoolID, Priority: 2},
		{ID: "default-1", PoolID: DefaultChannelPoolID, Priority: 1},
		{ID: "gpt-2", PoolID: "gpt", Priority: 2},
		{ID: "gpt-1", PoolID: "gpt", Priority: 1},
		{ID: "vision", PoolID: "gpt", Priority: 9, ExcludeFromConversation: true},
	}

	normalizeUpstreamPriorities(upstreams)
	if upstreams[0].Priority != 2 || upstreams[1].Priority != 1 {
		t.Fatalf("default pool priorities = [%d, %d], want [2, 1]", upstreams[0].Priority, upstreams[1].Priority)
	}
	if upstreams[2].Priority != 2 || upstreams[3].Priority != 1 {
		t.Fatalf("gpt pool priorities = [%d, %d], want [2, 1]", upstreams[2].Priority, upstreams[3].Priority)
	}
	if upstreams[4].Priority != 1 {
		t.Fatalf("public vision priority = %d, want 1", upstreams[4].Priority)
	}
}

func TestSaveChannelPoolLayoutMovesAndReordersChannels(t *testing.T) {
	manager := &ConfigManager{
		configFile: filepath.Join(t.TempDir(), "config.json"),
		config: Config{
			MessagePools: []ChannelPool{
				{ID: "gpt", Name: "GPT", ModelMatcher: "gpt", Priority: 1},
				defaultChannelPool(),
			},
			Upstream: []UpstreamConfig{
				{ID: "a", Name: "A", PoolID: DefaultChannelPoolID, Status: ChannelStatusActive, Priority: 1, MaxConcurrency: upstreamcontrol.DefaultConcurrency},
				{ID: "b", Name: "B", PoolID: DefaultChannelPoolID, Status: ChannelStatusActive, Priority: 2, MaxConcurrency: upstreamcontrol.DefaultConcurrency},
				{ID: "c", Name: "C", PoolID: "gpt", Status: ChannelStatusSuspended, Priority: 1, MaxConcurrency: upstreamcontrol.DefaultConcurrency},
				{ID: "disabled", Name: "Disabled", PoolID: "gpt", Status: ChannelStatusDisabled, Priority: 2, MaxConcurrency: upstreamcontrol.DefaultConcurrency},
			},
		},
	}

	err := manager.SaveChannelPoolLayout("messages", []ChannelPoolLayout{
		{PoolID: "gpt", ChannelIDs: []string{"a", "c"}},
		{PoolID: DefaultChannelPoolID, ChannelIDs: []string{"b"}},
	})
	if err != nil {
		t.Fatalf("SaveChannelPoolLayout() error = %v", err)
	}

	got := manager.config.Upstream
	if got[0].PoolID != "gpt" || got[0].Priority != 1 {
		t.Fatalf("moved channel = %#v, want pool gpt priority 1", got[0])
	}
	if got[2].PoolID != "gpt" || got[2].Priority != 2 {
		t.Fatalf("gpt tail channel = %#v, want priority 2", got[2])
	}
	if got[3].Priority != 3 {
		t.Fatalf("disabled channel priority = %d, want 3", got[3].Priority)
	}
	if got[1].PoolID != DefaultChannelPoolID || got[1].Priority != 1 {
		t.Fatalf("default channel = %#v, want default priority 1", got[1])
	}
}

func TestEnsurePoolsAndAssignmentsMigratesLegacyWildcardGroup(t *testing.T) {
	pools := []ChannelPool{
		{ID: DefaultChannelPoolID, Name: "GPT", ModelMatcher: "gpt", Priority: 1},
		{ID: "legacy-fallback", Name: "旧兜底", ModelMatcher: "*", Priority: 2},
	}
	upstreams := []UpstreamConfig{
		{Name: "gpt-channel", PoolID: DefaultChannelPoolID},
		{Name: "fallback-channel", PoolID: "legacy-fallback"},
	}

	normalized, changed, err := ensurePoolsAndAssignments(pools, upstreams)
	if err != nil {
		t.Fatalf("ensurePoolsAndAssignments() error = %v", err)
	}
	if !changed {
		t.Fatal("ensurePoolsAndAssignments() changed = false, want true")
	}
	if normalized[0].ID != "legacy-fallback" || normalized[0].Name != "GPT" || normalized[0].ModelMatcher != "gpt" {
		t.Fatalf("migrated matched group = %#v", normalized[0])
	}
	if normalized[1].ID != DefaultChannelPoolID || normalized[1].Name != FallbackChannelPoolName || normalized[1].ModelMatcher != "*" {
		t.Fatalf("migrated fallback group = %#v", normalized[1])
	}
	if upstreams[0].PoolID != "legacy-fallback" || upstreams[1].PoolID != DefaultChannelPoolID {
		t.Fatalf("migrated assignments = %#v", upstreams)
	}
}

func TestValidateAllVisionLayerConfigsChecksDependents(t *testing.T) {
	upstreams := []UpstreamConfig{
		{
			ID:                      "text",
			Name:                    "text",
			PoolID:                  "pool-a",
			VisionLayerEnabled:      true,
			VisionLayerChannelID:    "vision",
			ExcludeFromConversation: false,
		},
		{
			ID:            "vision",
			Name:          "vision",
			PoolID:        "pool-b",
			VisionCapable: true,
			Status:        ChannelStatusActive,
		},
	}

	cfg := Config{Upstream: upstreams}

	if err := validateAllVisionLayerConfigs(&cfg, "messages", cfg.Upstream); err == nil {
		t.Fatal("validateAllVisionLayerConfigs() error = nil")
	}

	upstreams[1].ExcludeFromConversation = true
	cfg.Upstream = upstreams
	if err := validateAllVisionLayerConfigs(&cfg, "messages", cfg.Upstream); err != nil {
		t.Fatalf("public vision channel should be valid: %v", err)
	}
}

func TestRemoveFromSliceDetachesVisionDependency(t *testing.T) {
	upstreams := []UpstreamConfig{
		{ID: "text", Name: "text", VisionLayerEnabled: true, VisionLayerChannelID: "vision"},
		{ID: "vision", Name: "vision", VisionCapable: true},
	}

	if _, _, err := removeFromSlice(upstreams, 1, "Messages"); err != nil {
		t.Fatal(err)
	}
	if upstreams[0].VisionLayerEnabled || upstreams[0].VisionLayerChannelID != "" {
		t.Fatal("引用方的图片理解层未清理")
	}
}

// TestMoveAPIKeyToBottomForKindWritesCorrectPool 特征测试：多渠道模式密钥降级
// 必须按实际 kind 写池（回归锚点：历史 bug 曾用硬编码 "messages" 的
// MoveAPIKeyToBottom，导致 chat/gemini/images 的降级写错渠道池）。
func TestMoveAPIKeyToBottomForKindWritesCorrectPool(t *testing.T) {
	dir := t.TempDir()
	configFile := filepath.Join(dir, "config.json")
	if err := os.WriteFile(configFile, []byte(`{
		"chatUpstream": [{
			"name": "chat-ch",
			"baseUrl": "https://chat.example.com",
			"apiKeys": ["key-a", "key-b"],
			"status": "active"
		}],
		"upstream": [{
			"name": "msg-ch",
			"baseUrl": "https://msg.example.com",
			"apiKeys": ["msg-key"],
			"status": "active"
		}]
	}`), 0644); err != nil {
		t.Fatalf("写入配置失败: %v", err)
	}

	cm, err := NewConfigManager(configFile)
	if err != nil {
		t.Fatalf("NewConfigManager() error = %v", err)
	}
	defer cm.Close()

	if err := cm.MoveAPIKeyToBottomForKind("chat", 0, "key-a"); err != nil {
		t.Fatalf("MoveAPIKeyToBottomForKind() error = %v", err)
	}

	cfg := cm.GetConfig()
	if got, want := cfg.ChatUpstream[0].APIKeys[0], "key-b"; got != want {
		t.Fatalf("chat 池降级后首位应为 %q，得到 %q", want, got)
	}
	if got, want := len(cfg.Upstream[0].APIKeys), 1; got != want || cfg.Upstream[0].APIKeys[0] != "msg-key" {
		t.Fatalf("messages 池不应被跨池污染: %v", cfg.Upstream[0].APIKeys)
	}
}
