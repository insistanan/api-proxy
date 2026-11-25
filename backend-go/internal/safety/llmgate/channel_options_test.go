package llmgate

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"insistanan/api-proxy/internal/infra/config"
)

func TestChannelOptionsDeduplicateKiloAndPreserveExplicitIDs(t *testing.T) {
	cm, sch := newGateTestEnv(t)
	require.NoError(t, cm.EnsureFreeChannels())
	require.NoError(t, cm.SetKiloModels([]string{"kilo-auto/free"}, "2026-11-14T00:00:00Z"))
	require.NoError(t, cm.SetKiloEnabled(true))
	for _, kind := range []string{"messages", "decisions"} {
		_, err := cm.AddChannel(kind, config.UpstreamConfig{ID: "same-" + kind, Name: "同名", BaseURL: "https://example.invalid", ServiceType: "openai", APIKeys: []string{"secret-test-key"}, DefaultModel: "model"})
		require.NoError(t, err)
	}
	caller := &Caller{CfgManager: cm, ChannelScheduler: sch}
	options, err := caller.ChannelOptions(context.Background())
	require.NoError(t, err)
	kiloCount, namedCount := 0, 0
	for _, option := range options {
		if option.Builtin {
			kiloCount++
			require.Equal(t, "kilo", option.SharedKey)
			require.Equal(t, "builtin-kilo-messages", option.ID)
			require.Equal(t, "messages", option.Kind)
			require.NotEmpty(t, option.Models)
		}
		if option.Name == "同名" {
			namedCount++
			require.NotEmpty(t, option.Kind)
		}
	}
	require.Equal(t, 1, kiloCount)
	require.Equal(t, 2, namedCount)
	data, err := json.Marshal(options)
	require.NoError(t, err)
	require.NotContains(t, string(data), "secret-test-key")
	for _, kind := range []string{"messages", "responses", "chat", "gemini"} {
		selected, err := PickChannel(context.Background(), cm, sch, "builtin-kilo-"+kind, "free/kilo-auto/free", nil)
		require.NoError(t, err, "旧配置仍能通过原协议 ID 选渠")
		require.Equal(t, "builtin-kilo-"+kind, selected.Upstream.ID)
		if selected.Permit != nil {
			selected.Permit.Release()
		}
	}
}
