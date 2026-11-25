package intent

import (
	"context"
	"testing"

	"github.com/insistanan/api-proxy/internal/dialogue/routing/semantic"
	"github.com/stretchr/testify/require"
)

func TestRoutingEvaluationUsesIndependentCasesAndRealClassification(t *testing.T) {
	s := intentTestService(t)
	ctx := context.Background()
	dataset, err := s.Datasets.Save(ctx, semantic.Dataset{ID: "eval", Name: "独立样本", Kind: "intent", Samples: []semantic.Sample{
		{ID: "translation", Label: "translation", Text: "翻译你好", Split: "eval", Family: "translation", Approved: true, Source: "test"},
		{ID: "unknown", Label: "unmatched", Text: "嗯，再看看", Split: "eval", Family: "unknown", Approved: true, Negative: true, Source: "test"},
		{ID: "unreviewed", Label: "writing", Text: "写一封邮件", Split: "eval", Family: "unreviewed", Source: "test"},
		{ID: "index", Label: "writing", Text: "润色公告", Split: "index", Family: "index", Approved: true, Source: "test"},
	}})
	require.NoError(t, err)
	cfg := automaticConfig()
	result, err := s.EvaluateRouting(ctx, "messages", cfg, dataset.ID)
	require.NoError(t, err)
	require.Equal(t, 2, result.Total)
	require.Equal(t, 2, result.Correct)
	require.Equal(t, 1, result.Unmatched)
	require.Zero(t, result.WrongRoutes)
	require.Zero(t, result.Failures)
	direct, err := s.Classify(WithProtocol(ctx, "messages"), cfg, Input{Text: "翻译你好"}, "test")
	require.NoError(t, err)
	require.Equal(t, direct.Task, result.Cases[0].Actual)
	require.Equal(t, direct.PoolID, result.Cases[0].PoolID)
	require.Equal(t, direct.Tier, result.Cases[0].Tier)
}

func TestCorrectionRequiresReviewAndPreservesExistingSplit(t *testing.T) {
	s := intentTestService(t)
	s.state.Events = []Event{{RequestID: "request", Protocol: "messages", Decision: Decision{Trace: &DecisionTrace{Input: "一个已经脱敏的任务"}}}}
	ctx := context.Background()
	id, err := s.CorrectTask(ctx, "messages", "request", "debugging")
	require.NoError(t, err)
	dataset, err := s.Datasets.Get(id)
	require.NoError(t, err)
	require.Len(t, dataset.Samples, 1)
	require.False(t, dataset.Samples[0].Approved)
	require.Equal(t, "eval", dataset.Samples[0].Split)
	dataset.Samples[0].Split, dataset.Samples[0].Approved = "index", true
	dataset, err = s.Datasets.Save(ctx, dataset)
	require.NoError(t, err)
	_, err = s.CorrectTask(ctx, "messages", "request", "unmatched")
	require.NoError(t, err)
	corrected, err := s.Datasets.Get(id)
	require.NoError(t, err)
	require.Len(t, corrected.Samples, 1)
	require.Equal(t, dataset.Samples[0].Family, corrected.Samples[0].Family)
	require.Equal(t, "index", corrected.Samples[0].Split)
	require.True(t, corrected.Samples[0].Negative)
	require.False(t, corrected.Samples[0].Approved)
	s.state.Events[0].Decision.Trace.Truncated = true
	_, err = s.CorrectTask(ctx, "messages", "request", "debugging")
	require.ErrorContains(t, err, "已截断")
}
