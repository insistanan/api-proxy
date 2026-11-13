package config

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestAutomaticGroupCapabilitiesAreIndependentAndValidated(t *testing.T) {
	cm := newTransactionTestManager(t, "messages")
	cfg := DefaultIntentRouting()
	cfg.Enabled = true
	cfg.Strategy = "auto"
	require.ErrorContains(t, cm.UpdateIntentRouting("messages", cfg), "分组能力")
	cfg.Groups = []IntentGroupCapability{{PoolID: "empty", Tasks: []string{"translation"}, Tiers: []int{0, 1}}}
	require.NoError(t, cm.UpdateIntentRouting("messages", cfg))
	cfg.Groups[0].Tasks[0] = "writing"
	copy := cm.GetIntentRouting("messages")
	copy.Groups[0].Tiers[0] = 2
	stored := cm.GetIntentRouting("messages")
	require.Equal(t, "translation", stored.Groups[0].Tasks[0])
	require.Equal(t, 0, stored.Groups[0].Tiers[0])
	require.Empty(t, cm.GetIntentRouting("chat").Groups)
	require.Error(t, cm.DeleteChannelPool("messages", "empty"))
	stored.Groups[0].Tiers = []int{3}
	require.Error(t, cm.UpdateIntentRouting("messages", stored))
}

func TestIntentConfigProtocolIsolationAndReferences(t *testing.T) {
	cm := newTransactionTestManager(t, "messages")
	value := DefaultIntentRouting()
	value.Enabled = true
	value.Routes = []IntentRoute{{ID: "coding", PoolID: "empty", Patterns: []string{"代码"}, Examples: []string{"写代码"}}}
	if err := cm.UpdateIntentRouting("messages", value); err != nil {
		t.Fatal(err)
	}
	if cm.GetIntentRouting("chat").Enabled {
		t.Fatal("Messages 开关污染了 Chat")
	}
	value.Routes[0].Patterns[0] = "外部修改"
	copy := cm.GetIntentRouting("messages")
	copy.Routes[0].Examples[0] = "读取后修改"
	if got := cm.GetIntentRouting("messages"); got.Routes[0].Patterns[0] != "代码" || got.Routes[0].Examples[0] != "写代码" {
		t.Fatal("意图配置未深拷贝")
	}
	if err := cm.DeleteChannelPool("messages", "empty"); err == nil {
		t.Fatal("被引用的分组应阻止删除")
	}
	name := "重命名的组"
	if err := cm.UpdateChannelPool("messages", "empty", ChannelPoolUpdate{Name: &name}); err != nil {
		t.Fatal(err)
	}
	if cm.GetIntentRouting("messages").Routes[0].PoolID != "empty" {
		t.Fatal("重命名改变了分组引用")
	}
	for _, kind := range []string{"images", "embeddings", "decisions"} {
		if err := cm.UpdateIntentRouting(kind, DefaultIntentRouting()); err == nil {
			t.Fatalf("不应接受 %s 意图配置", kind)
		}
	}
}

func TestFreeGroupCanDeclareCapabilitiesForEverySupportedProtocol(t *testing.T) {
	cm := newTransactionTestManager(t, "messages")
	require.NoError(t, cm.EnsureFreeChannels())
	for _, kind := range []string{"messages", "responses", "chat", "gemini"} {
		cfg := DefaultIntentRouting()
		cfg.Enabled, cfg.Strategy = true, "auto"
		cfg.Groups = []IntentGroupCapability{{PoolID: FreeChannelPoolID, Tasks: []string{"architecture"}, Tiers: []int{0, 1, 2}}}
		require.NoError(t, cm.UpdateIntentRouting(kind, cfg))
		require.Equal(t, cfg.Groups, cm.GetIntentRouting(kind).Groups)
		cfg.Unmatched, cfg.FallbackPoolID = "fallback", FreeChannelPoolID
		require.NoError(t, cm.UpdateIntentRouting(kind, cfg))
		cfg.Strategy = "regex"
		cfg.Routes = []IntentRoute{{ID: "free-task", PoolID: FreeChannelPoolID, Patterns: []string{"任务"}}}
		require.NoError(t, cm.UpdateIntentRouting(kind, cfg))
	}
}

func TestSystemOneIntentConfigNormalizesToDecisions(t *testing.T) {
	cm := newTransactionTestManager(t, "messages")
	cfg := DefaultIntentRouting()
	cfg.Classifier = "systemone"
	require.NoError(t, cm.UpdateIntentRouting("messages", cfg))
	require.Equal(t, "decisions", cm.GetIntentRouting("messages").Classifier)
}

func TestIntentConfigSaveFailureKeepsOldSnapshot(t *testing.T) {
	cm := newTransactionTestManager(t, "messages")
	blockTransactionBackup(t, cm)
	value := DefaultIntentRouting()
	value.Enabled = true
	value.Routes = []IntentRoute{{ID: "task", PoolID: "default", Patterns: []string{"task"}}}
	if err := cm.UpdateIntentRouting("messages", value); err == nil {
		t.Fatal("应返回保存错误")
	}
	if cm.GetIntentRouting("messages").Enabled {
		t.Fatal("未提交的新配置污染了旧快照")
	}
}
