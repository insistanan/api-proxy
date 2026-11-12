package semantic

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/insistanan/api-proxy/internal/config"
)

func (s *Service) Build(ctx context.Context, id string, revision int64, profile config.SemanticEmbeddingConfig) (IndexStatus, error) {
	if err := config.ValidateSemanticEmbedding(profile); err != nil {
		return IndexStatus{}, err
	}
	select {
	case s.buildSlot <- struct{}{}:
		defer func() { <-s.buildSlot }()
	default:
		return IndexStatus{}, fmt.Errorf("已有数据集正在构建或评测，请等待完成")
	}
	dataset, err := s.Get(id)
	if err != nil {
		return IndexStatus{}, err
	}
	if dataset.Revision != revision {
		return IndexStatus{}, fmt.Errorf("数据集版本已变化，请刷新后构建")
	}
	space, err := s.embedder.EmbeddingSpace(profile)
	if err != nil {
		return IndexStatus{}, err
	}
	index := &snapshot{IndexStatus: IndexStatus{DatasetID: id, Revision: revision, Profile: profile, Space: space}}
	for _, row := range dataset.Samples {
		if row.Approved && row.Split == "index" {
			index.Samples = append(index.Samples, row)
		}
	}
	if len(index.Samples) == 0 {
		return IndexStatus{}, fmt.Errorf("没有已批准的索引样本，请先审核样本")
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	cache := make(map[string][]float32)
	missing := []string{}
	for _, row := range index.Samples {
		text := profile.ExamplePrefix + row.Text
		key := vectorKey(space, text)
		if _, exists := cache[key]; exists {
			continue
		}
		var blob []byte
		err := s.db.QueryRowContext(ctx, `SELECT data FROM semantic_vectors WHERE id=?`, key).Scan(&blob)
		if errors.Is(err, sql.ErrNoRows) {
			cache[key] = nil
			missing = append(missing, text)
			continue
		}
		if err != nil {
			return IndexStatus{}, err
		}
		vector, err := decodeVector(blob)
		if err != nil {
			return IndexStatus{}, err
		}
		cache[key] = vector
	}
	batch := 100
	if profile.Source == "local" {
		batch = 1
	}
	for offset := 0; offset < len(missing); offset += batch {
		texts := missing[offset:min(offset+batch, len(missing))]
		batchCtx, batchCancel := context.WithTimeout(ctx, time.Duration(profile.TimeoutMS)*time.Millisecond)
		vectors, err := s.embedder.EmbedSamples(batchCtx, profile, texts, "dataset-build")
		batchCancel()
		if err != nil {
			return IndexStatus{}, fmt.Errorf("增量向量计算失败，旧索引仍可用: %w", err)
		}
		if len(vectors) != len(texts) {
			return IndexStatus{}, fmt.Errorf("向量数量与输入不一致")
		}
		if current, err := s.embedder.EmbeddingSpace(profile); err != nil || current != space {
			if err != nil {
				return IndexStatus{}, err
			}
			return IndexStatus{}, fmt.Errorf("计算期间模型或渠道变化，未缓存该批向量")
		}
		for i, text := range texts {
			vector, err := normalized(vectors[i])
			if err != nil {
				return IndexStatus{}, err
			}
			key := vectorKey(space, text)
			if _, err := s.db.ExecContext(ctx, `INSERT INTO semantic_vectors(id,data) VALUES(?,?) ON CONFLICT(id) DO UPDATE SET data=excluded.data`, key, encodeVector(vector)); err != nil {
				return IndexStatus{}, err
			}
			cache[key] = vector
		}
		// 缓存可再生成，按写入顺序淘汰；已发布快照单独存储，不受淘汰影响。
		if _, err := s.db.ExecContext(ctx, `DELETE FROM semantic_vectors WHERE rowid IN (SELECT rowid FROM semantic_vectors ORDER BY rowid DESC LIMIT -1 OFFSET ?)`, maxCachedVectors); err != nil {
			return IndexStatus{}, err
		}
	}
	for _, row := range index.Samples {
		vector := cache[vectorKey(space, profile.ExamplePrefix+row.Text)]
		if index.Dimension == 0 {
			index.Dimension = len(vector)
		}
		if len(vector) != index.Dimension || len(vector) == 0 {
			return IndexStatus{}, fmt.Errorf("索引构建期间向量维度发生变化")
		}
		index.Vectors = append(index.Vectors, vector)
	}
	currentSpace, err := s.embedder.EmbeddingSpace(profile)
	if err != nil {
		return IndexStatus{}, err
	}
	if currentSpace != space {
		return IndexStatus{}, fmt.Errorf("构建期间模型或渠道变化，未发布索引")
	}
	index.Count, index.Reused, index.BuiltAt = len(index.Samples), len(index.Samples)-len(missing), time.Now()
	if err := s.publish(ctx, index); err != nil {
		return IndexStatus{}, err
	}
	return index.IndexStatus, nil
}

func (s *Service) publish(ctx context.Context, index *snapshot) error {
	data, err := json.Marshal(index)
	if err != nil {
		return err
	}
	vectors := make([]byte, 0, index.Count*index.Dimension*4)
	for _, vector := range index.Vectors {
		vectors = append(vectors, encodeVector(vector)...)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.datasets[index.DatasetID].Revision != index.Revision {
		return fmt.Errorf("构建期间样本已变化，旧索引保持不变，请重新构建")
	}
	ctx, cancel := transactionContext(ctx)
	defer cancel()
	id := index.DatasetID + "/" + profileKey(index.Profile)
	var total int64
	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(length(data)+length(vectors)),0) FROM semantic_indexes WHERE id<>?`, id).Scan(&total); err != nil {
		return err
	}
	if total+int64(len(data)+len(vectors)) > maxIndexBytes {
		return fmt.Errorf("已发布索引超过 512 MiB，请先移除不再使用的模型索引")
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO semantic_indexes(id,data,vectors) VALUES(?,?,?) ON CONFLICT(id) DO UPDATE SET data=excluded.data,vectors=excluded.vectors`, id, data, vectors); err != nil {
		return fmt.Errorf("发布索引失败: %w", err)
	}
	delete(s.indexes, id) // 已开始的查询仍持有不可变旧快照；后续请求加载已提交版本。
	return nil
}

// Search 始终只计算查询向量，不会在业务请求内构建样本索引。
func (s *Service) Search(ctx context.Context, ids []string, kind string, profile config.SemanticEmbeddingConfig, text, parent string) (Result, error) {
	return s.search(ctx, ids, kind, profile, text, parent, nil)
}

func (s *Service) SearchTool(ctx context.Context, ids []string, profile config.SemanticEmbeddingConfig, text, parent, tool string, labels []string) (Result, error) {
	return s.search(ctx, ids, "safety", profile, text, parent, func(row Sample) bool {
		return slices.Contains(labels, row.Label) && (row.Tool == "" || strings.EqualFold(row.Tool, tool))
	})
}

func (s *Service) search(ctx context.Context, ids []string, kind string, profile config.SemanticEmbeddingConfig, text, parent string, filter func(Sample) bool) (Result, error) {
	started := time.Now()
	if err := config.ValidateDatasetIDs(ids); err != nil {
		return Result{}, err
	}
	if err := config.ValidateSemanticEmbedding(profile); err != nil {
		return Result{}, err
	}
	if len(ids) == 0 || strings.TrimSpace(text) == "" || len(text) > 16000 {
		return Result{}, fmt.Errorf("检索需要数据集和不超过 16000 字节的文本")
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(profile.TimeoutMS)*time.Millisecond)
	defer cancel()
	space, err := s.embedder.EmbeddingSpace(profile)
	if err != nil {
		return Result{}, err
	}
	indexes := make([]*snapshot, 0, len(ids))
	versions := []string{space, profileKey(profile)}
	for _, id := range ids {
		if err := s.validateKind(id, kind); err != nil {
			return Result{}, err
		}
		index, err := s.published(ctx, id+"/"+profileKey(profile))
		if err != nil {
			return Result{}, fmt.Errorf("数据集 %s: %w", id, err)
		}
		if index.Space != space {
			return Result{}, fmt.Errorf("数据集 %s 的向量模型已变化，请重建索引", id)
		}
		indexes = append(indexes, index)
		versions = append(versions, fmt.Sprintf("%s:%d:%d", id, index.Revision, index.BuiltAt.UnixNano()))
	}
	result := Result{Hits: []Hit{}, Version: hash(strings.Join(versions, "\x00"))}
	if filter != nil && !slices.ContainsFunc(indexes, func(index *snapshot) bool { return slices.ContainsFunc(index.Samples, filter) }) {
		result.DurationMS = time.Since(started).Milliseconds()
		return result, nil
	}
	query, err := s.embedder.EmbedSamples(ctx, profile, []string{profile.QueryPrefix + text}, parent)
	if err != nil {
		return Result{}, err
	}
	if len(query) != 1 {
		return Result{}, fmt.Errorf("查询向量数量无效")
	}
	vector, err := normalized(query[0])
	if err != nil {
		return Result{}, err
	}
	if current, err := s.embedder.EmbeddingSpace(profile); err != nil || current != space {
		if err != nil {
			return Result{}, err
		}
		return Result{}, fmt.Errorf("查询期间向量空间发生变化")
	}
	// 每个类别分别保留最接近的正反例，避免全局 Top-K 把安全反例挤掉。
	best := map[string]Hit{}
	for _, index := range indexes {
		if len(vector) != index.Dimension {
			return Result{}, fmt.Errorf("查询与样本向量维度不一致")
		}
		for i, row := range index.Samples {
			if filter != nil && !filter(row) {
				continue
			}
			if err := ctx.Err(); err != nil {
				return Result{}, err
			}
			var score float64
			for j, v := range index.Vectors[i] {
				score += float64(v) * float64(vector[j])
			}
			score = max(-1, min(1, score))
			key := fmt.Sprintf("%s:%t", row.Label, row.Negative)
			if old, ok := best[key]; !ok || score > old.Score {
				best[key] = Hit{DatasetID: index.DatasetID, Revision: index.Revision, Sample: row, Score: score}
			}
		}
	}
	for _, hit := range best {
		result.Hits = append(result.Hits, hit)
	}
	sort.Slice(result.Hits, func(i, j int) bool {
		if result.Hits[i].Score == result.Hits[j].Score {
			return result.Hits[i].Sample.ID < result.Hits[j].Sample.ID
		}
		return result.Hits[i].Score > result.Hits[j].Score
	})
	result.DurationMS = time.Since(started).Milliseconds()
	return result, nil
}

type Evaluation struct {
	Total         int              `json:"total"`
	Correct       int              `json:"correct"`
	FalsePositive int              `json:"falsePositive"`
	FalseNegative int              `json:"falseNegative"`
	DurationMS    int64            `json:"durationMs"`
	Cases         []EvaluationCase `json:"cases"`
}

type EvaluationCase struct {
	ID       string  `json:"id"`
	Expected string  `json:"expected"`
	Actual   string  `json:"actual"`
	Score    float64 `json:"score"`
	Correct  bool    `json:"correct"`
}

func (s *Service) Evaluate(ctx context.Context, id string, profile config.SemanticEmbeddingConfig, threshold, margin float64) (Evaluation, error) {
	select {
	case s.buildSlot <- struct{}{}:
		defer func() { <-s.buildSlot }()
	default:
		return Evaluation{}, fmt.Errorf("已有构建或评测正在运行")
	}
	d, err := s.Get(id)
	if err != nil {
		return Evaluation{}, err
	}
	index, err := s.published(ctx, id+"/"+profileKey(profile))
	if err != nil {
		return Evaluation{}, err
	}
	if index.Revision != d.Revision {
		return Evaluation{}, fmt.Errorf("评测前请先发布当前版本索引")
	}
	start := time.Now()
	out := Evaluation{Cases: []EvaluationCase{}}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	for _, row := range d.Samples {
		if !row.Approved || row.Split != "eval" {
			continue
		}
		if out.Total >= 500 {
			return Evaluation{}, fmt.Errorf("一次最多评测 500 条样本")
		}
		result, err := s.Search(ctx, []string{id}, d.Kind, profile, row.Text, "dataset-eval")
		if err != nil {
			return Evaluation{}, err
		}
		actual, score := "", 0.0
		for _, hit := range result.Hits {
			if hit.Sample.Negative || hit.Score < threshold {
				continue
			}
			negative := -1.0
			for _, other := range result.Hits {
				if other.Sample.Label == hit.Sample.Label && other.Sample.Negative {
					negative = other.Score
				}
			}
			if d.Kind == "intent" && hit.Score-negative < margin {
				continue
			}
			actual, score = hit.Sample.Label, hit.Score
			break
		}
		expected := row.Label
		if row.Negative {
			expected = ""
		}
		correct := actual == expected
		out.Total++
		if correct {
			out.Correct++
		} else if row.Negative {
			out.FalsePositive++
		} else {
			out.FalseNegative++
		}
		out.Cases = append(out.Cases, EvaluationCase{ID: row.ID, Expected: expected, Actual: actual, Score: score, Correct: correct})
	}
	if out.Total == 0 {
		return Evaluation{}, fmt.Errorf("没有已批准的独立评测样本")
	}
	latest, err := s.Get(id)
	if err != nil {
		return Evaluation{}, err
	}
	if latest.Revision != d.Revision {
		return Evaluation{}, fmt.Errorf("评测期间数据集被修改，结果未发布")
	}
	out.DurationMS = time.Since(start).Milliseconds()
	return out, nil
}

func PositiveLabels(result Result, threshold, margin float64) []Hit {
	positive := []Hit{}
	for _, hit := range result.Hits {
		if hit.Sample.Negative || hit.Score < threshold {
			continue
		}
		negative := -1.0
		for _, other := range result.Hits {
			if other.Sample.Label == hit.Sample.Label && other.Sample.Negative {
				negative = other.Score
			}
		}
		if hit.Score-negative >= margin {
			positive = append(positive, hit)
		}
	}
	return slices.Clone(positive)
}
