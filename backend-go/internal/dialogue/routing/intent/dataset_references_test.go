package intent

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"insistanan/api-proxy/internal/dialogue/routing/semantic"
	"insistanan/api-proxy/internal/infra/config"
)

func TestEnabledDatasetReferencePreventsDeletionAndUnchangedSaveNeedsNoLocalHelper(t *testing.T) {
	ctx := context.Background()
	manager, err := config.NewConfigManager(filepath.Join(t.TempDir(), "config.json"))
	require.NoError(t, err)
	defer manager.Close()
	datasets, err := semantic.New(filepath.Join(t.TempDir(), "datasets.db"), routingDatasetEmbedder{})
	require.NoError(t, err)
	defer datasets.Close()
	d, err := datasets.Save(ctx, semantic.Dataset{ID: "dataset", Name: "data", Kind: "intent", Samples: []semantic.Sample{{ID: "a", Label: "coding", Text: "代码", Split: "index", Family: "a", Source: "test", Approved: true}}})
	require.NoError(t, err)
	cfg := config.DefaultIntentRouting()
	cfg.Enabled = true
	cfg.Strategy = "semantic"
	cfg.EmbeddingSource = "local"
	cfg.EmbeddingModel = "test"
	cfg.DatasetIDs = []string{d.ID}
	cfg.Routes = []config.IntentRoute{{ID: "coding", Label: "coding", PoolID: config.DefaultChannelPoolID}}
	require.NoError(t, manager.UpdateIntentRouting("messages", cfg))
	s := &Service{Cfg: manager, Datasets: datasets}
	manager.SetResourceValidator(s.validateDatasetReferences)
	require.ErrorIs(t, s.DeleteDataset(ctx, d.ID, d.Revision), semantic.ErrConflict)
	profile := embeddingProfile(cfg)
	index, err := datasets.Build(ctx, d.ID, d.Revision, profile)
	require.NoError(t, err)
	require.ErrorIs(t, s.DeleteDatasetIndex(ctx, d.ID, index), semantic.ErrConflict)
	require.NoError(t, manager.SetFuzzyModeEnabled(false), "未变更引用不能因本地助手未装配阻止无关保存")
	cfg.Enabled = false
	require.NoError(t, manager.UpdateIntentRouting("messages", cfg))
	require.NoError(t, s.DeleteDataset(ctx, d.ID, d.Revision))
	cfg.Enabled = true
	require.Error(t, manager.UpdateIntentRouting("messages", cfg), "删除后不能重新启用失效资源")
}

type referenceDatasetEmbedder struct {
	routingDatasetEmbedder
	service *Service
}

func (e referenceDatasetEmbedder) EmbeddingSpace(profile config.SemanticEmbeddingConfig) (string, error) {
	return e.service.EmbeddingSpace(profile)
}

func TestDatasetDeletionAndConfigPublicationCannotCross(t *testing.T) {
	for _, saveFirst := range []bool{true, false} {
		t.Run(map[bool]string{true: "先保存引用", false: "先删除资源"}[saveFirst], func(t *testing.T) {
			ctx := context.Background()
			manager, err := config.NewConfigManager(filepath.Join(t.TempDir(), "config.json"))
			require.NoError(t, err)
			defer manager.Close()
			_, err = manager.AddChannel("embeddings", config.UpstreamConfig{ID: "embedding", Name: "embedding", BaseURL: "https://embedding.example", APIKeys: []string{"key"}})
			require.NoError(t, err)
			s := &Service{Cfg: manager}
			datasets, err := semantic.New(filepath.Join(t.TempDir(), "datasets.db"), referenceDatasetEmbedder{service: s})
			require.NoError(t, err)
			defer datasets.Close()
			s.Datasets = datasets
			d, err := datasets.Save(ctx, semantic.Dataset{ID: "dataset", Name: "data", Kind: "intent", Samples: []semantic.Sample{{ID: "a", Label: "coding", Text: "代码", Split: "index", Family: "a", Source: "test", Approved: true}}})
			require.NoError(t, err)
			cfg := config.DefaultIntentRouting()
			cfg.Enabled, cfg.Strategy, cfg.EmbeddingSource, cfg.EmbeddingModel, cfg.EmbeddingChannel = true, "semantic", "remote", "test", "embedding"
			cfg.DatasetIDs = []string{d.ID}
			cfg.Routes = []config.IntentRoute{{ID: "coding", Label: "coding", PoolID: config.DefaultChannelPoolID}}
			_, err = datasets.Build(ctx, d.ID, d.Revision, embeddingProfile(cfg))
			require.NoError(t, err)
			entered, release := make(chan struct{}), make(chan struct{})
			saved, deleted := make(chan error, 1), make(chan error, 1)
			manager.SetResourceValidator(func(previous, candidate config.Config) error {
				err := s.validateDatasetReferences(previous, candidate)
				if saveFirst {
					close(entered)
					<-release
				}
				return err
			})
			save := func() { saved <- manager.UpdateIntentRouting("messages", cfg) }
			if saveFirst {
				go save()
				<-entered
				go func() { deleted <- s.DeleteDataset(ctx, d.ID, d.Revision) }()
			} else {
				go func() {
					deleted <- s.deleteDatasetResource(d.ID, nil, func() error {
						close(entered)
						<-release
						return datasets.Delete(ctx, d.ID, d.Revision)
					})
				}()
				<-entered
				go save()
			}
			close(release)
			saveErr, deleteErr := <-saved, <-deleted
			if saveFirst {
				require.NoError(t, saveErr)
				require.ErrorIs(t, deleteErr, semantic.ErrConflict)
			} else {
				require.NoError(t, deleteErr)
				require.ErrorIs(t, saveErr, semantic.ErrNotFound)
			}
			require.Equal(t, saveFirst, manager.GetIntentRouting("messages").Enabled)
		})
	}
}
