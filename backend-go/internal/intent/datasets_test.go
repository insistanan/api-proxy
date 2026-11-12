package intent

import (
	"context"
	"math"
	"path/filepath"
	"testing"

	"github.com/insistanan/api-proxy/internal/config"
	"github.com/insistanan/api-proxy/internal/semantic"
	"github.com/stretchr/testify/require"
)

type routingDatasetEmbedder struct{}

func (routingDatasetEmbedder) EmbeddingSpace(config.SemanticEmbeddingConfig) (string, error) {
	return "test", nil
}
func (routingDatasetEmbedder) EmbedSamples(_ context.Context, _ config.SemanticEmbeddingConfig, texts []string, _ string) ([][]float64, error) {
	out := make([][]float64, len(texts))
	for i, text := range texts {
		x := 1.0
		if text == "current" {
			x = 0.74
		}
		if text == "candidate" {
			x = 0.8
		}
		out[i] = []float64{x, math.Sqrt(1 - x*x)}
	}
	return out, nil
}

func TestDatasetRoutingRetainsCurrentPoolScoreBelowThreshold(t *testing.T) {
	ctx := context.Background()
	datasets, err := semantic.New(filepath.Join(t.TempDir(), "data.db"), routingDatasetEmbedder{})
	require.NoError(t, err)
	defer datasets.Close()
	d, err := datasets.Save(ctx, semantic.Dataset{ID: "intent", Name: "意图", Kind: "intent", Samples: []semantic.Sample{
		{ID: "a", Label: "current", Text: "current", Split: "index", Family: "a", Source: "test", Approved: true},
		{ID: "b", Label: "candidate", Text: "candidate", Split: "index", Family: "b", Source: "test", Approved: true},
	}})
	require.NoError(t, err)
	cfg := config.DefaultIntentRouting()
	cfg.DatasetIDs = []string{d.ID}
	cfg.EmbeddingSource = "local"
	cfg.EmbeddingModel = "test"
	cfg.Threshold = 0.78
	cfg.Routes = []config.IntentRoute{{ID: "rule-a", Label: "current", PoolID: "group-a"}, {ID: "rule-b", Label: "candidate", PoolID: "group-b"}}
	_, err = datasets.Build(ctx, d.ID, d.Revision, embeddingProfile(cfg))
	require.NoError(t, err)
	s := &Service{Datasets: datasets}
	decision, err := s.semantic(ctx, cfg, "query", "test")
	require.NoError(t, err)
	require.Equal(t, "group-b", decision.PoolID)
	require.InDelta(t, 0.74, decision.PoolScores["group-a"], 0.00001)
	require.Less(t, decision.Score-decision.PoolScores["group-a"], cfg.SwitchMargin, "未过阈值的当前分数不能变成零而触发无收益切换")
}

func TestInlineMigrationDoesNotOverwriteIndependentDatasetEdits(t *testing.T) {
	ctx := WithProtocol(context.Background(), "messages")
	datasets, err := semantic.New(filepath.Join(t.TempDir(), "data.db"), routingDatasetEmbedder{})
	require.NoError(t, err)
	defer datasets.Close()
	s := &Service{Datasets: datasets}
	cfg := config.DefaultIntentRouting()
	cfg.EmbeddingSource = "local"
	cfg.EmbeddingModel = "test"
	cfg.Routes = []config.IntentRoute{{ID: "label", PoolID: "group", Examples: []string{"current"}}}
	require.NoError(t, s.BuildIndex(ctx, cfg))
	d, err := datasets.Get("inline-messages")
	require.NoError(t, err)
	d.Samples[0].Text = "独立编辑"
	d, err = datasets.Save(ctx, d)
	require.NoError(t, err)
	require.ErrorContains(t, s.BuildIndex(ctx, cfg), "保留独立编辑")
	d, err = datasets.Get(d.ID)
	require.NoError(t, err)
	require.Equal(t, "独立编辑", d.Samples[0].Text)
}
