package config

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestConfigValidation_LoadAndSaveRejectSameInvalidSnapshot(t *testing.T) {
	cases := []struct {
		name   string
		kind   string
		mutate func(*Config, *UpstreamConfig)
	}{
		{name: "图片渠道协议", kind: "images", mutate: func(_ *Config, up *UpstreamConfig) { up.ServiceType = "claude" }},
		{name: "图片主模型缺失", kind: "images", mutate: func(_ *Config, up *UpstreamConfig) { up.ServiceType = "responses" }},
		{name: "向量维度为负", kind: "embeddings", mutate: func(_ *Config, up *UpstreamConfig) { up.EmbeddingsDimensions = -1 }},
		{name: "决策渠道协议", kind: "decisions", mutate: func(_ *Config, up *UpstreamConfig) { up.ServiceType = "gemini" }},
		{name: "图片理解悬空引用", kind: "messages", mutate: func(_ *Config, up *UpstreamConfig) {
			up.VisionLayerEnabled, up.VisionLayerChannelID = true, "missing"
		}},
		{name: "模型映射悬空引用", kind: "messages", mutate: func(_ *Config, up *UpstreamConfig) {
			up.ModelMapping = map[string][]string{"source": {"target"}}
			up.ModelMappingChannels = map[string]map[string]string{"source": {"target": "missing"}}
		}},
		{name: "提示词版本不存在", kind: "messages", mutate: func(cfg *Config, _ *UpstreamConfig) {
			cfg.Settings.Prompts.ActiveVersion = "missing"
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			manager := newTransactionTestManager(t, tc.kind)
			before := manager.GetConfig()
			disk, err := os.ReadFile(manager.configFile)
			require.NoError(t, err)
			candidate := cloneConfig(before)
			set, err := candidate.channelSetFor(tc.kind)
			require.NoError(t, err)
			tc.mutate(&candidate, &(*set.upstreams)[0])
			raw, err := json.Marshal(candidate)
			require.NoError(t, err)
			_, _, err = manager.prepareCandidate(raw)
			require.Error(t, err)
			require.Error(t, manager.saveConfigLocked(candidate))
			require.Equal(t, before, manager.GetConfig())
			after, err := os.ReadFile(manager.configFile)
			require.NoError(t, err)
			require.Equal(t, disk, after)
		})
	}
}
