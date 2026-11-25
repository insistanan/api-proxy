package intent

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"insistanan/api-proxy/internal/dialogue/routing/semantic"
	"insistanan/api-proxy/internal/infra/config"
)

func TestRecognition_BuiltinMatchSkipsVectorAndAssistant(t *testing.T) {
	cfg := automaticConfig()
	cfg.RecognitionMode = "hybrid"
	cfg.HybridMatchers = []string{"builtin", "semantic", "assistant"}
	cfg.Classifier = "llm"
	// 没有装配两个远程调用器；规则可靠命中后不应访问它们。
	decision, err := (&Service{}).Classify(context.Background(), cfg, Input{Text: "实现一个分页接口"}, "test")
	require.NoError(t, err)
	require.Equal(t, "coding", decision.PoolID)
	require.Equal(t, []RecognitionAttempt{{Stage: "builtin", Matched: true, Task: "code_generation"}}, decision.Trace.Stages)
}

func TestRecognition_VectorMatchUsesAllDifficultyGroupAndSkipsAssistant(t *testing.T) {
	ctx := context.Background()
	datasets, err := semantic.New(filepath.Join(t.TempDir(), "datasets.db"), routingDatasetEmbedder{})
	require.NoError(t, err)
	defer datasets.Close()
	dataset, err := datasets.Save(ctx, semantic.Dataset{ID: "intent", Name: "意图", Kind: "intent", Samples: []semantic.Sample{{ID: "sample", Label: "debugging", Text: "candidate", Split: "index", Family: "family", Source: "test", Approved: true}}})
	require.NoError(t, err)
	cfg := automaticConfig()
	cfg.RecognitionMode, cfg.Classifier = "hybrid", "llm"
	cfg.HybridMatchers = []string{"builtin", "semantic", "assistant"}
	cfg.DatasetIDs, cfg.EmbeddingSource, cfg.EmbeddingModel = []string{dataset.ID}, "local", "test"
	cfg.Groups = []config.IntentGroupCapability{{PoolID: "coding", Tasks: []string{"debugging"}, Tiers: []int{0, 1, 2}}}
	_, err = datasets.Build(ctx, dataset.ID, dataset.Revision, embeddingProfile(cfg))
	require.NoError(t, err)
	decision, err := (&Service{Datasets: datasets}).Classify(ctx, cfg, Input{Text: "query"}, "test")
	require.NoError(t, err)
	require.Equal(t, "coding", decision.PoolID)
	require.Equal(t, -1, decision.Tier, "未推断出难度时不能补成简单任务")
	require.Equal(t, []RecognitionAttempt{{Stage: "builtin", Matched: false}, {Stage: "semantic", Matched: true, Task: "debugging"}}, decision.Trace.Stages)
}

func TestRecognition_VectorFailureDoesNotSilentlyTryAssistant(t *testing.T) {
	cfg := automaticConfig()
	cfg.RecognitionMode, cfg.Classifier = "hybrid", "llm"
	cfg.HybridMatchers = []string{"semantic", "assistant"}
	_, err := (&Service{}).Classify(context.Background(), cfg, Input{Text: "query"}, "test")
	require.ErrorContains(t, err, "数据集服务")
}
