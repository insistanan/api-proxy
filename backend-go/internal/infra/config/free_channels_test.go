package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFreeChannelsSharedConfigPersistsAndKeepsIndexes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	cm, err := NewConfigManager(path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, cm.Close()) })
	err = cm.AddUpstream(UpstreamConfig{Name: "普通渠道", BaseURL: "https://example.invalid", APIKeys: []string{"test"}})
	require.NoError(t, err)
	before := cm.GetConfig().Upstream[0].ID
	require.NoError(t, cm.EnsureFreeChannels())
	require.NoError(t, cm.EnsureFreeChannels())
	require.Equal(t, before, cm.GetConfig().Upstream[0].ID)
	require.NoError(t, cm.SetKiloEnabled(false))
	require.NoError(t, cm.SetKiloModels([]string{"vendor/free-model"}, "2026-11-12T00:00:00Z"))

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var saved Config
	require.NoError(t, json.Unmarshal(data, &saved))
	require.Equal(t, []string{"vendor/free-model"}, saved.FreeChannels.Kilo.Models)
	for _, kind := range []string{"messages", "responses", "chat", "gemini"} {
		cs, err := saved.channelSetFor(kind)
		require.NoError(t, err)
		ref := (*cs.upstreams)[len(*cs.upstreams)-1]
		require.Equal(t, "builtin-kilo-"+kind, ref.ID)
		require.Empty(t, ref.BaseURL, "磁盘仅保存引用")
		require.Empty(t, ref.APIKeys)
	}
	require.NoError(t, materializeFreeChannels(&saved))
	for _, kind := range []string{"messages", "responses", "chat", "gemini"} {
		cs, err := saved.channelSetFor(kind)
		require.NoError(t, err)
		up := (*cs.upstreams)[len(*cs.upstreams)-1]
		require.Equal(t, ChannelStatusDisabled, up.Status)
		require.Equal(t, KiloChatURL, up.BaseURL)
		require.True(t, up.UseFullURL)
		require.Equal(t, []string{""}, up.APIKeys)
		require.Equal(t, []string{"vendor/free-model"}, up.ModelMapping["free/vendor/free-model"])
		require.Equal(t, []string{"vendor/free-model"}, up.ModelMapping[FreeModelID])
		require.Equal(t, "openai", up.ServiceType)
	}
	// 真正从持久化配置重载也必须保留关闭状态。
	reloaded, err := NewConfigManager(path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, reloaded.Close()) })
	require.NoError(t, reloaded.EnsureFreeChannels())
	require.Equal(t, ChannelStatusDisabled, reloaded.GetConfig().ChatUpstream[0].Status)
	require.Equal(t, before, reloaded.GetConfig().Upstream[0].ID)
}

func TestFreeRequestsAndMutationsStayIsolated(t *testing.T) {
	cm, err := NewConfigManager(filepath.Join(t.TempDir(), "config.json"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, cm.Close()) })
	err = cm.AddUpstream(UpstreamConfig{Name: "普通渠道", BaseURL: "https://example.invalid", APIKeys: []string{"test"}})
	require.NoError(t, err)
	require.NoError(t, cm.EnsureFreeChannels())
	ordinary, builtin := 0, 1
	for _, kind := range []string{"messages", "responses", "chat", "gemini"} {
		require.NoError(t, cm.ValidateFreeRequest(kind, "free", nil))
		route, err := SelectChannelPoolRoute(cm.GetChannelPools(kind), "free")
		require.NoError(t, err)
		require.Len(t, route, 1)
		require.Equal(t, FreeChannelPoolID, route[0].ID)
	}
	require.Error(t, cm.ValidateFreeRequest("messages", "free", &ordinary))
	require.Error(t, cm.ValidateFreeRequest("images", "free", nil))
	require.NoError(t, cm.ValidateFreeRequest("messages", "free/kilo-auto/free", &builtin))
	require.Error(t, cm.ValidateFreeRequest("messages", "free/kilo-auto/free", &ordinary))
	require.Error(t, cm.ValidateFreeRequest("messages", "paid-model", &builtin))
	require.Error(t, cm.ValidateFreeRequest("messages", "free/unknown", nil))
	require.Error(t, cm.ValidateFreeRequest("images", "free/kilo-auto/free", nil))
	route, err := SelectChannelPoolRoute(cm.GetChannelPools("messages"), "free/kilo-auto/free")
	require.NoError(t, err)
	require.Len(t, route, 1)
	require.Equal(t, FreeChannelPoolID, route[0].ID)
	route, err = SelectChannelPoolRoute(cm.GetChannelPools("messages"), "prefix-free/model")
	require.NoError(t, err)
	require.Len(t, route, 1)
	require.Equal(t, DefaultChannelPoolID, route[0].ID)

	require.Error(t, cm.DuplicateChannel("messages", builtin))
	require.Error(t, cm.SetChannelStatus(builtin, ChannelStatusDisabled))
	require.Error(t, cm.AddAPIKey(builtin, "not-allowed"))
	require.Error(t, cm.DeleteChannelPool("messages", FreeChannelPoolID))
	require.Error(t, cm.SaveChannelPoolLayout("messages", []ChannelPoolLayout{
		{PoolID: DefaultChannelPoolID, ChannelIDs: []string{"builtin-kilo-messages"}},
		{PoolID: FreeChannelPoolID, ChannelIDs: []string{cm.GetConfig().Upstream[0].ID}},
	}))
	require.NoError(t, cm.SetKiloEnabled(false))
	require.Error(t, cm.ValidateFreeRequest("chat", "free", nil))
	require.Error(t, cm.ValidateFreeRequest("chat", "free/kilo-auto/free", nil))
	require.Error(t, cm.SetKiloModels(nil, ""))
	snapshot, err := cm.GetFreeChannels()
	require.NoError(t, err)
	require.Equal(t, []string{"kilo-auto/free"}, snapshot.Kilo.Models)
}

func TestFreeOnlyConfigDoesNotBecomeImplicitDefault(t *testing.T) {
	cfg := Config{FreeChannels: &FreeChannelsConfig{Kilo: KiloFreeConfig{Enabled: true, Models: []string{"kilo-auto/free"}}}}
	require.NoError(t, materializeFreeChannels(&cfg))
	_, _, err := getFirstActiveWithIndexForModel(cfg.Upstream, cfg.MessagePools, "Messages", "")
	require.Error(t, err)
	_, _, err = getFirstActiveWithIndexForModel(cfg.Upstream, cfg.MessagePools, "Messages", "ordinary")
	require.Error(t, err)
}
