package config

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFuzzyModeProtocolIsolationAndPersistence(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		cm := newTransactionTestManager(t, "messages")
		cm.config.Settings.Gateway = DefaultGatewaySettings()
		require.NoError(t, cm.SetFuzzyModeEnabled(legacy))
		for _, protocol := range []string{"messages", "responses", "gemini", "chat", "images", "embeddings", "decisions"} {
			require.Equal(t, legacy, cm.GetFuzzyModeEnabledForProtocol(protocol))
			require.NoError(t, cm.SetFuzzyModeForProtocol(protocol, !legacy))
			require.Equal(t, !legacy, cm.GetFuzzyModeEnabledForProtocol(protocol))
			require.NoError(t, cm.SetFuzzyModeForProtocol(protocol, legacy))
		}
		require.NoError(t, cm.SetFuzzyModeForProtocol("messages", !legacy))
		require.Equal(t, legacy, cm.GetFuzzyModeEnabledForProtocol("responses"))
		snapshot := cm.GetConfig()
		snapshot.FuzzyModeByProtocol["messages"] = legacy
		require.Equal(t, !legacy, cm.GetFuzzyModeEnabledForProtocol("messages"), "读取副本不能污染已发布快照")
		data, err := os.ReadFile(cm.configFile)
		require.NoError(t, err)
		loaded, _, err := cm.prepareCandidate(data)
		require.NoError(t, err)
		reloaded := &ConfigManager{config: loaded}
		require.Equal(t, !legacy, reloaded.GetFuzzyModeEnabledForProtocol("messages"))
		require.Equal(t, legacy, reloaded.GetFuzzyModeEnabledForProtocol("responses"))
	}
}

func TestFuzzyModeSaveFailureKeepsProtocolSnapshot(t *testing.T) {
	cm := newTransactionTestManager(t, "messages")
	cm.config.FuzzyModeByProtocol = map[string]bool{"messages": true, "responses": false}
	before := cm.GetConfig()
	blockTransactionBackup(t, cm)
	require.Error(t, cm.SetFuzzyModeForProtocol("responses", true))
	require.Equal(t, before, cm.GetConfig())
}

func TestFuzzyModeRejectsUnknownProtocols(t *testing.T) {
	cm := newTransactionTestManager(t, "messages")
	before := cm.GetConfig()
	for _, protocol := range []string{"", "messsages", "Messages"} {
		require.Error(t, cm.SetFuzzyModeForProtocol(protocol, true))
	}
	require.Equal(t, before, cm.GetConfig())
	before.FuzzyModeByProtocol = map[string]bool{"typo": true}
	data, err := json.Marshal(before)
	require.NoError(t, err)
	_, _, err = cm.prepareCandidate(data)
	require.ErrorContains(t, err, "故障转移配置无效")
}
