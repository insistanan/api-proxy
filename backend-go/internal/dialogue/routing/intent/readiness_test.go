package intent

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"insistanan/api-proxy/internal/dialogue/routing/semantic"
	"insistanan/api-proxy/internal/infra/config"
)

func TestVectorReadinessIsSeparateFromPureConfigValidation(t *testing.T) {
	local, err := NewLocal(t.TempDir())
	require.NoError(t, err)
	s := &Service{Local: local}
	cfg := config.DefaultIntentRouting()
	cfg.Strategy, cfg.EmbeddingSource, cfg.EmbeddingModel = "auto", "local", "vector"
	require.NoError(t, s.ValidateIntentVector(cfg), "纯规则模式不需要向量")
	cfg.TaskMatcher = "semantic"
	require.NoError(t, config.ValidateIntentRouting(cfg, nil), "纯校验不读取运行状态")
	var required *semantic.VectorModelRequiredError
	require.ErrorAs(t, s.ValidateIntentVector(cfg), &required)
	require.True(t, filepath.IsAbs(required.InstallPath))
	require.Equal(t, DefaultPresetsURL, required.PresetsURL)
	require.ErrorAs(t, s.BuildIndex(context.Background(), cfg), &required, "导入原有例句之前就必须阻断")
	local.ready, local.manifest.ModelName = true, "vector"
	require.NoError(t, s.ValidateIntentVector(cfg))
	cfg.EmbeddingModel = "wrong"
	require.ErrorAs(t, s.ValidateIntentVector(cfg), &required)
}

func TestRemoteVectorReadinessRequiresUsableEmbeddingChannelAndModel(t *testing.T) {
	cm, err := config.NewConfigManager(filepath.Join(t.TempDir(), "config.json"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, cm.Close()) })
	_, err = cm.AddChannel("embeddings", config.UpstreamConfig{ID: "vector", Name: "vector", ServiceType: "openai", BaseURL: "https://example.invalid", APIKeys: []string{"test-key"}})
	require.NoError(t, err)
	s := &Service{Cfg: cm}
	for _, tc := range []struct {
		channel, model string
		ready          bool
	}{{"vector", "model", true}, {"vector", "", false}, {"missing", "model", false}} {
		err := s.ValidateVector(config.SemanticEmbeddingConfig{Source: "remote", Channel: tc.channel, Model: tc.model})
		if tc.ready {
			require.NoError(t, err)
		} else {
			var required *semantic.VectorModelRequiredError
			require.ErrorAs(t, err, &required)
		}
	}
}
