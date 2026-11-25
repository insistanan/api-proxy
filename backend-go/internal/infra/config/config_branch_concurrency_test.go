package config

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBranchConcurrency_JSONDefaultsAndValidation(t *testing.T) {
	var upstream UpstreamConfig
	require.NoError(t, json.Unmarshal([]byte(`{}`), &upstream))
	require.Equal(t, 3, upstream.MaxBranchConcurrency)
	var kilo KiloFreeConfig
	require.NoError(t, json.Unmarshal([]byte(`{"enabled":true}`), &kilo))
	require.Equal(t, 3, kilo.MaxBranchConcurrency)
	require.Error(t, json.Unmarshal([]byte(`{"MaxBranchConcurrency":null}`), &upstream))
	for _, value := range []string{"0", "-1", "1.5", "null", `"3"`, "true", "9007199254740992"} {
		t.Run(value, func(t *testing.T) {
			body := []byte(`{"maxBranchConcurrency":` + value + `}`)
			require.Error(t, json.Unmarshal(body, &UpstreamConfig{}))
			require.Error(t, json.Unmarshal(body, &UpstreamUpdate{}))
			require.Error(t, json.Unmarshal(body, &KiloFreeConfig{}))
		})
	}
}

func TestBranchConcurrency_UpdatePersistsForEveryProtocol(t *testing.T) {
	for _, kind := range ChannelKinds() {
		t.Run(kind, func(t *testing.T) {
			cm := newTransactionTestManager(t, kind)
			limit := 7
			_, err := cm.UpdateChannel(kind, 0, UpstreamUpdate{MaxBranchConcurrency: &limit})
			require.NoError(t, err)
			data, err := os.ReadFile(cm.configFile)
			require.NoError(t, err)
			var saved Config
			require.NoError(t, json.Unmarshal(data, &saved))
			set, err := saved.channelSetFor(kind)
			require.NoError(t, err)
			require.Equal(t, limit, (*set.upstreams)[0].MaxBranchConcurrency)
			require.Equal(t, limit, ChannelToDTO(&(*set.upstreams)[0], 0)["maxBranchConcurrency"])
			limit = 0
			_, err = cm.UpdateChannel(kind, 0, UpstreamUpdate{MaxBranchConcurrency: &limit})
			require.Error(t, err)
			current, _, err := cm.GetChannelConfig(kind)
			require.NoError(t, err)
			require.Equal(t, 7, current[0].MaxBranchConcurrency)
			cm = newTransactionTestManager(t, kind)
			blockTransactionBackup(t, cm)
			limit = 9
			_, err = cm.UpdateChannel(kind, 0, UpstreamUpdate{MaxBranchConcurrency: &limit})
			require.Error(t, err)
			current, _, err = cm.GetChannelConfig(kind)
			require.NoError(t, err)
			require.Equal(t, 3, current[0].MaxBranchConcurrency)
		})
	}
}
