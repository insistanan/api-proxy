package pipeline

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/insistanan/api-proxy/internal/infra/config"
	"github.com/insistanan/api-proxy/internal/safety/llmgate"
	"github.com/stretchr/testify/require"
)

func TestGateSharedOptionRetainsSavedKiloAliasWithoutDuplicates(t *testing.T) {
	cm, err := config.NewConfigManager(filepath.Join(t.TempDir(), "config.json"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, cm.Close()) })
	require.NoError(t, cm.EnsureFreeChannels())
	require.NoError(t, cm.SetKiloEnabled(true))
	caller := &llmgate.Caller{CfgManager: cm}
	gate := NewCmdGate(cm, caller, nil, nil)
	for _, selected := range []string{"", "builtin-kilo-responses", "builtin-kilo-chat", "builtin-kilo-gemini"} {
		settings := cm.GetSettings()
		settings.ContentSafety.DangerousCmd.AIGate.ChannelID = selected
		require.NoError(t, cm.UpdateSettings(settings))
		options, err := gate.ChannelOptions(context.Background())
		require.NoError(t, err)
		require.Len(t, options, 1)
		if selected == "" {
			require.Equal(t, "builtin-kilo-messages", options[0].ID)
		} else {
			require.Equal(t, selected, options[0].ID)
		}
		canonical, err := caller.ChannelOptions(context.Background())
		require.NoError(t, err)
		require.Equal(t, "builtin-kilo-messages", canonical[0].ID, "通用候选优先级始终确定")
		require.Equal(t, selected, cm.GetContentSafetyConfig().DangerousCmd.AIGate.ChannelID, "兼容显示不改写配置")
	}
}
