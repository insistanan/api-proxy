package config

import (
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCurrentSchemaRejectsBrokenChannelOnLoadAndSave(t *testing.T) {
	for _, mutate := range []func(*UpstreamConfig){
		func(u *UpstreamConfig) { u.ID = "" },
		func(u *UpstreamConfig) { u.ServiceType = "typo" },
		func(u *UpstreamConfig) { u.Status = "typo" },
		func(u *UpstreamConfig) { u.Status = "" },
		func(u *UpstreamConfig) { u.APIKeys = []string{" "} },
		func(u *UpstreamConfig) { u.BaseURL = "file:///private" },
		func(u *UpstreamConfig) { u.BaseURLs = []string{"https://valid.example", "invalid"} },
	} {
		cm := newTransactionTestManager(t, "messages")
		candidate := cm.GetConfig()
		mutate(&candidate.Upstream[0])
		raw, err := json.Marshal(candidate)
		require.NoError(t, err)
		_, _, err = cm.prepareCandidate(raw)
		require.Error(t, err)
		require.Error(t, cm.saveConfigLocked(candidate))
	}
	for _, raw := range []string{`{"unknown":true}`, `{"schemaVersion":2}`, `{"schemaVersion":1,"loadBalance":"random"}`} {
		cm := newTransactionTestManager(t, "messages")
		_, _, err := cm.prepareCandidate([]byte(raw))
		require.Error(t, err)
	}
	var patch SettingsPatch
	require.Error(t, json.Unmarshal([]byte(`{"network":{"typo":true}}`), &patch))
	var update UpstreamUpdate
	require.Error(t, json.Unmarshal([]byte(`{"typo":true}`), &update))
}

func TestLifecycleBlockedReferenceDoesNotBlockOtherChannels(t *testing.T) {
	cm := newTransactionTestManager(t, "messages")
	now := time.Now()
	old := now.Add(-4 * 24 * time.Hour)
	for i := range cm.config.Upstream {
		cm.config.Upstream[i].Status = ChannelStatusDeprecated
		cm.config.Upstream[i].DeprecatedAt = &old
	}
	owner := *cm.config.Upstream[0].Clone()
	owner.ID, owner.Name, owner.Status, owner.DeprecatedAt = "owner", "owner", ChannelStatusActive, nil
	owner.ModelMapping = map[string][]string{"source": {"target"}}
	owner.ModelMappingChannels = map[string]map[string]string{"source": {"target": "first"}}
	cm.config.Upstream = append(cm.config.Upstream, owner)
	require.NoError(t, cm.SaveConfig())
	require.ErrorContains(t, cm.runChannelLifecycle(now, true), "first")
	result := cm.GetConfig()
	require.Equal(t, ChannelStatusDeprecated, result.Upstream[0].Status)
	require.Equal(t, ChannelStatusDeleted, result.Upstream[1].Status)
	require.Equal(t, ChannelStatusActive, result.Upstream[2].Status)
}

func TestAnonymousCredentialRemainsRetryableAcrossRequests(t *testing.T) {
	cm := newTransactionTestManager(t, "messages")
	upstream := UpstreamConfig{BaseURL: "https://anonymous.example", ServiceType: "openai", APIKeys: []string{""}}
	cm.MarkKeyAsFailed(&upstream, "", "test")
	key, err := cm.GetNextAPIKey(&upstream, nil, "test")
	require.NoError(t, err)
	require.Empty(t, key)
	_, err = cm.GetNextAPIKey(&upstream, map[string]bool{"": true}, "test")
	require.Error(t, err, "本次请求已失败的匿名凭据也必须排除")
}

func TestLifecycle_ResourceReferenceDoesNotBlockOtherChannels(t *testing.T) {
	cm := newTransactionTestManager(t, "embeddings")
	now := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	expired := now.Add(-time.Minute)
	for i := range cm.config.EmbeddingsUpstream {
		cm.config.EmbeddingsUpstream[i].Temporary = true
		cm.config.EmbeddingsUpstream[i].TemporaryUntil = &expired
	}
	require.NoError(t, cm.SaveConfig())
	blocked := errors.New("向量渠道未启用")
	cm.SetResourceValidator(func(previous, candidate Config) error {
		if candidate.EmbeddingsUpstream[0].Status != ChannelStatusActive {
			return blocked
		}
		return nil
	})
	err := cm.runChannelLifecycle(now, false)
	require.ErrorIs(t, err, blocked)
	require.ErrorContains(t, err, "embeddings/first")
	result := cm.GetConfig()
	require.Equal(t, ChannelStatusActive, result.EmbeddingsUpstream[0].Status)
	require.True(t, result.EmbeddingsUpstream[0].Temporary)
	require.Equal(t, ChannelStatusDeprecated, result.EmbeddingsUpstream[1].Status)
	require.False(t, result.EmbeddingsUpstream[1].Temporary)
	data, err := os.ReadFile(cm.configFile)
	require.NoError(t, err)
	var persisted Config
	require.NoError(t, json.Unmarshal(data, &persisted))
	require.Len(t, persisted.EmbeddingsUpstream, len(result.EmbeddingsUpstream))
	for i, expected := range result.EmbeddingsUpstream {
		actual := persisted.EmbeddingsUpstream[i]
		require.Equal(t, expected.ID, actual.ID)
		require.Equal(t, expected.Status, actual.Status)
		require.Equal(t, expected.Temporary, actual.Temporary)
		require.Equal(t, expected.TemporaryUntil, actual.TemporaryUntil)
		require.Equal(t, expected.DeprecatedAt, actual.DeprecatedAt)
	}
}
