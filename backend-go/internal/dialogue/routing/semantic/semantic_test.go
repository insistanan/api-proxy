package semantic

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/insistanan/api-proxy/internal/infra/config"
	"github.com/stretchr/testify/require"
)

type testEmbedder struct {
	calls  [][]string
	fail   bool
	space  string
	during func()
}

func (e *testEmbedder) EmbeddingSpace(config.SemanticEmbeddingConfig) (string, error) {
	return e.space, nil
}
func (e *testEmbedder) EmbedSamples(_ context.Context, _ config.SemanticEmbeddingConfig, texts []string, _ string) ([][]float64, error) {
	e.calls = append(e.calls, append([]string(nil), texts...))
	if e.fail {
		return nil, errors.New("模拟向量服务失败")
	}
	if e.during != nil {
		fn := e.during
		e.during = nil
		fn()
	}
	out := make([][]float64, len(texts))
	for i, text := range texts {
		if strings.Contains(text, "翻译") {
			out[i] = []float64{1, 0}
		} else {
			out[i] = []float64{0, 1}
		}
	}
	return out, nil
}
func testProfile() config.SemanticEmbeddingConfig {
	return config.SemanticEmbeddingConfig{Source: "local", Model: "test", TimeoutMS: 1000}
}
func testDataset() Dataset {
	return Dataset{ID: "tasks", Name: "测试任务", Kind: "intent", Samples: []Sample{
		{ID: "one", Label: "translation", Text: "翻译邮件", Family: "mail", Split: "index", Approved: true, Source: "test"},
		{ID: "two", Label: "debug", Text: "排查错误", Family: "error", Split: "index", Approved: true, Source: "test"},
		{ID: "evaluation", Label: "translation", Text: "翻译书籍", Family: "book", Split: "eval", Approved: true, Source: "test"},
		{ID: "unapproved", Label: "translation", Text: "翻译机密", Family: "draft", Split: "index", Source: "test"},
	}}
}

func TestPersistentIndexAndIncrementalBuild(t *testing.T) {
	ctx := context.Background()
	embedder := &testEmbedder{space: "space1"}
	path := filepath.Join(t.TempDir(), "datasets.db")
	s, err := New(path, embedder)
	require.NoError(t, err)
	d, err := s.Save(ctx, testDataset())
	require.NoError(t, err)
	index, err := s.Build(ctx, d.ID, d.Revision, testProfile())
	require.NoError(t, err)
	require.Equal(t, 2, index.Count)
	require.Len(t, embedder.calls, 2, "本地逐条执行，评测与未批准样本不得进入索引")
	require.NoError(t, s.Close())
	s, err = New(path, embedder)
	require.NoError(t, err)
	defer s.Close()
	result, err := s.Search(ctx, []string{d.ID}, "intent", testProfile(), "翻译内容", "test")
	require.NoError(t, err)
	require.Equal(t, "translation", result.Hits[0].Sample.Label)
	require.Len(t, embedder.calls, 3, "重启仅计算查询向量")
	d.Samples[1].Text = "调试新问题"
	d, err = s.Save(ctx, d)
	require.NoError(t, err)
	index, err = s.Build(ctx, d.ID, d.Revision, testProfile())
	require.NoError(t, err)
	require.Equal(t, 1, index.Reused)
	require.Len(t, embedder.calls, 4)
	_, err = s.Save(ctx, testDataset())
	require.ErrorContains(t, err, "其他操作修改")
	evaluation, err := s.Evaluate(ctx, d.ID, testProfile(), 0.7, 0.1)
	require.NoError(t, err)
	require.Equal(t, 1, evaluation.Total)
	require.Equal(t, 1, evaluation.Correct)
}

func TestBuildFailureKeepsPublishedVersion(t *testing.T) {
	ctx := context.Background()
	embedder := &testEmbedder{space: "same"}
	s, err := New(filepath.Join(t.TempDir(), "data.db"), embedder)
	require.NoError(t, err)
	defer s.Close()
	d, err := s.Save(ctx, testDataset())
	require.NoError(t, err)
	_, err = s.Build(ctx, d.ID, d.Revision, testProfile())
	require.NoError(t, err)
	d.Samples[0].Text = "翻译新任务"
	d, err = s.Save(ctx, d)
	require.NoError(t, err)
	embedder.fail = true
	_, err = s.Build(ctx, d.ID, d.Revision, testProfile())
	require.ErrorContains(t, err, "旧索引仍可用")
	embedder.fail = false
	result, err := s.Search(ctx, []string{d.ID}, "intent", testProfile(), "翻译内容", "test")
	require.NoError(t, err)
	require.EqualValues(t, 1, result.Hits[0].Revision)
	embedder.space = "changed"
	_, err = s.Search(ctx, []string{d.ID}, "intent", testProfile(), "翻译", "test")
	require.ErrorContains(t, err, "模型已变化")
}

func TestConcurrentEditCannotPublishStaleBuild(t *testing.T) {
	ctx := context.Background()
	embedder := &testEmbedder{space: "same"}
	s, err := New(filepath.Join(t.TempDir(), "data.db"), embedder)
	require.NoError(t, err)
	defer s.Close()
	d, err := s.Save(ctx, testDataset())
	require.NoError(t, err)
	embedder.during = func() { d.Name = "已变化"; _, err := s.Save(ctx, d); require.NoError(t, err) }
	_, err = s.Build(ctx, d.ID, d.Revision, testProfile())
	require.ErrorContains(t, err, "样本已变化")
	_, err = s.Search(ctx, []string{d.ID}, "intent", testProfile(), "翻译", "test")
	require.ErrorContains(t, err, "尚未")
}

func TestDatasetValidationPreventsEvaluationLeakage(t *testing.T) {
	d := testDataset()
	d.Samples[2].Family = "mail"
	require.ErrorContains(t, ValidateDataset(d), "整族划分")
	d = testDataset()
	d.Samples[2].Text = d.Samples[0].Text
	require.ErrorContains(t, ValidateDataset(d), "同一文本")
	for _, builtin := range BuiltinDatasets() {
		require.NoError(t, ValidateDataset(builtin))
		for _, row := range builtin.Samples {
			require.False(t, row.Approved)
		}
	}
}

func TestEmbeddingSpaceChangeCannotPoisonIncrementalCache(t *testing.T) {
	ctx := context.Background()
	e := &testEmbedder{space: "old"}
	s, err := New(filepath.Join(t.TempDir(), "data.db"), e)
	require.NoError(t, err)
	defer s.Close()
	d, err := s.Save(ctx, testDataset())
	require.NoError(t, err)
	e.during = func() { e.space = "new" }
	_, err = s.Build(ctx, d.ID, d.Revision, testProfile())
	require.ErrorContains(t, err, "未缓存")
	var count int
	require.NoError(t, s.db.QueryRow(`SELECT count(*) FROM semantic_vectors`).Scan(&count))
	require.Zero(t, count)
	e.space = "old"
	index, err := s.Build(ctx, d.ID, d.Revision, testProfile())
	require.NoError(t, err)
	require.Zero(t, index.Reused)
}

func TestCacheQuotaAndExplicitDeletion(t *testing.T) {
	ctx := context.Background()
	s, err := New(filepath.Join(t.TempDir(), "data.db"), &testEmbedder{space: "same"})
	require.NoError(t, err)
	defer s.Close()
	_, err = s.db.Exec(`WITH RECURSIVE sequence(n) AS (SELECT 1 UNION ALL SELECT n+1 FROM sequence WHERE n<?) INSERT INTO semantic_vectors(id,data) SELECT CAST(n AS TEXT),? FROM sequence`, maxCachedVectors, encodeVector([]float32{1, 0}))
	require.NoError(t, err)
	d, err := s.Save(ctx, testDataset())
	require.NoError(t, err)
	_, err = s.Build(ctx, d.ID, d.Revision, testProfile())
	require.NoError(t, err)
	var count int
	require.NoError(t, s.db.QueryRow(`SELECT count(*) FROM semantic_vectors`).Scan(&count))
	require.Equal(t, maxCachedVectors, count)
	require.Error(t, s.Delete(ctx, d.ID, d.Revision-1))
	indexes, err := s.List(ctx)
	require.NoError(t, err)
	stale := indexes[0].Indexes[0]
	stale.Revision--
	require.ErrorContains(t, s.DeleteIndex(ctx, d.ID, stale), "重新发布")
	require.NoError(t, s.DeleteIndex(ctx, d.ID, indexes[0].Indexes[0]))
	_, err = s.Search(ctx, []string{d.ID}, "intent", testProfile(), "翻译", "test")
	require.ErrorContains(t, err, "尚未")
	_, err = s.Build(ctx, d.ID, d.Revision, testProfile())
	require.NoError(t, err)
	require.NoError(t, s.Delete(ctx, d.ID, d.Revision))
	_, err = s.Get(d.ID)
	require.ErrorIs(t, err, ErrNotFound)
	require.NoError(t, s.db.QueryRow(`SELECT count(*) FROM semantic_indexes`).Scan(&count))
	require.Zero(t, count)
}

func TestDatasetBoundsAndToolFilterAvoidsUnnecessaryEmbedding(t *testing.T) {
	d := testDataset()
	d.Samples[0].Source = ""
	require.ErrorContains(t, ValidateDataset(d), "来源")
	d = testDataset()
	d.Samples = nil
	for i := 0; i < 300; i++ {
		d.Samples = append(d.Samples, Sample{ID: fmt.Sprintf("s%d", i), Label: "label", Text: strings.Repeat("x", 16000), Family: "family", Source: "test", Split: "index"})
	}
	require.ErrorContains(t, ValidateDataset(d), "4 MiB")
	e := &testEmbedder{space: "same"}
	s, err := New(filepath.Join(t.TempDir(), "data.db"), e)
	require.NoError(t, err)
	defer s.Close()
	d = testDataset()
	d.Kind = "safety"
	d.Samples = d.Samples[:1]
	d.Samples[0].Label = "destructive"
	d.Samples[0].Tool = "bash"
	d, err = s.Save(context.Background(), d)
	require.NoError(t, err)
	_, err = s.Build(context.Background(), d.ID, d.Revision, testProfile())
	require.NoError(t, err)
	before := len(e.calls)
	result, err := s.SearchTool(context.Background(), []string{d.ID}, testProfile(), "test", "test", "powershell", []string{"destructive"})
	require.NoError(t, err)
	require.Empty(t, result.Hits)
	require.Len(t, e.calls, before)
}
