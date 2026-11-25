package intent

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"insistanan/api-proxy/internal/dialogue/routing/semantic"
	"insistanan/api-proxy/internal/infra/config"
)

func TestPreparationOwnsCancellationAndRejectsOverlap(t *testing.T) {
	s := &Service{}
	started := make(chan struct{})
	job, err := s.startPreparation("messages", func(ctx context.Context) (*config.IntentRoutingConfig, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	})
	require.NoError(t, err)
	require.True(t, job.Running)
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("准备任务未启动")
	}
	_, err = s.startPreparation("chat", nil)
	require.ErrorContains(t, err, "已有模型准备任务")
	s.CancelPreparation()
	status := s.PreparationStatus()
	require.False(t, status.Running)
	require.Equal(t, "cancelled", status.Phase)
	require.NotEmpty(t, status.Error)
	require.NoError(t, s.StopPreparation())
	_, err = s.startPreparation("chat", nil)
	require.Error(t, err, "停机后不可继续接受新任务")
}

func TestAdoptBuiltinPreservesEditsAndOnlyApprovesIntent(t *testing.T) {
	ctx := context.Background()
	datasets, err := semantic.New(filepath.Join(t.TempDir(), "data.db"), routingDatasetEmbedder{})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, datasets.Close()) })
	s := &Service{Datasets: datasets}
	require.NoError(t, s.adoptBuiltinIntents(ctx))
	dataset, err := datasets.Get(semantic.RecommendedIntentDatasetID)
	require.NoError(t, err)
	for _, sample := range dataset.Samples {
		require.True(t, sample.Approved)
	}
	require.NoError(t, s.adoptBuiltinIntents(ctx))
	unchanged, err := datasets.Get(dataset.ID)
	require.NoError(t, err)
	require.Equal(t, dataset.Revision, unchanged.Revision, "重复准备不制造新修订")
	safety, err := datasets.Get("starter-tools-v2")
	require.NoError(t, err)
	for _, sample := range safety.Samples {
		require.False(t, sample.Approved)
	}
	dataset.Samples[0].Text = "管理员编辑的独立句子"
	dataset, err = datasets.Save(ctx, dataset)
	require.NoError(t, err)
	require.ErrorContains(t, s.adoptBuiltinIntents(ctx), "不会覆盖样本")
	actual, err := datasets.Get(dataset.ID)
	require.NoError(t, err)
	require.Equal(t, dataset, actual)
}
