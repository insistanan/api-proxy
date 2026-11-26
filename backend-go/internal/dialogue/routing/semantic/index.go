package semantic

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"insistanan/api-proxy/internal/infra/config"
)

func (s *Service) Build(ctx context.Context, id string, revision int64, profile config.SemanticEmbeddingConfig) (IndexStatus, error) {
	if err := config.ValidateSemanticEmbedding(profile); err != nil {
		return IndexStatus{}, fmt.Errorf("%w: %w", ErrInvalidInput, err)
	}
	select {
	case s.buildSlot <- struct{}{}:
		defer func() { <-s.buildSlot }()
	default:
		return IndexStatus{}, fmt.Errorf("%w: 已有数据集正在构建或评测，请等待完成", ErrConflict)
	}
	dataset, err := s.Get(id)
	if err != nil {
		return IndexStatus{}, err
	}
	if dataset.Revision != revision {
		return IndexStatus{}, fmt.Errorf("%w: 数据集版本已变化，请刷新后构建", ErrConflict)
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
		return IndexStatus{}, fmt.Errorf("%w: 没有已批准的索引样本，请先审核样本", ErrConflict)
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
			return IndexStatus{}, fmt.Errorf("%w: 计算期间模型或渠道变化，未缓存该批向量", ErrConflict)
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
		return IndexStatus{}, fmt.Errorf("%w: 构建期间模型或渠道变化，未发布索引", ErrConflict)
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
		return fmt.Errorf("%w: 构建期间样本已变化，旧索引保持不变，请重新构建", ErrConflict)
	}
	ctx, cancel := transactionContext(ctx)
	defer cancel()
	id := index.DatasetID + "/" + profileKey(index.Profile)
	var total int64
	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(length(data)+length(vectors)),0) FROM semantic_indexes WHERE id<>?`, id).Scan(&total); err != nil {
		return err
	}
	if total+int64(len(data)+len(vectors)) > maxIndexBytes {
		return fmt.Errorf("%w: 已发布索引超过 512 MiB，请先移除不再使用的模型索引", ErrConflict)
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

// ValidateIndexes 在保存配置前检查模型身份，不计算查询向量或修改旧索引。
func (s *Service) ValidateIndexes(ctx context.Context, ids []string, kind string, profile config.SemanticEmbeddingConfig) error {
	if len(ids) == 0 {
		return fmt.Errorf("%w: 请先选择数据集并构建索引", ErrConflict)
	}
	space, err := s.embedder.EmbeddingSpace(profile)
	if err != nil {
		return err
	}
	return s.ValidateIndexesInSpace(ctx, ids, kind, profile, space)
}

// 调用方已固定配置快照时直接传入向量空间，避免在配置提交锁内重读配置。
func (s *Service) ValidateIndexesInSpace(ctx context.Context, ids []string, kind string, profile config.SemanticEmbeddingConfig, space string) error {
	if len(ids) == 0 {
		return fmt.Errorf("%w: 请先选择数据集并构建索引", ErrConflict)
	}
	for _, id := range ids {
		if err := s.validateKind(id, kind); err != nil {
			return err
		}
		index, err := s.published(ctx, id+"/"+profileKey(profile))
		if err != nil {
			return fmt.Errorf("数据集 %s 没有当前向量模型的可用索引，请重新构建: %w", id, err)
		}
		if index.Space != space {
			return fmt.Errorf("%w: 数据集 %s 的模型、渠道或模型映射已变化，请重新计算并发布索引", ErrConflict, id)
		}
	}
	return nil
}

func (s *Service) SearchTool(ctx context.Context, ids []string, profile config.SemanticEmbeddingConfig, text, parent, tool string, labels []string) (Result, error) {
	return s.search(ctx, ids, "safety", profile, text, parent, func(row Sample) bool {
		return slices.Contains(labels, row.Label) && (row.Tool == "" || strings.EqualFold(row.Tool, tool))
	})
}

func (s *Service) search(ctx context.Context, ids []string, kind string, profile config.SemanticEmbeddingConfig, text, parent string, filter func(Sample) bool) (Result, error) {
	started := time.Now()
	if err := config.ValidateDatasetIDs(ids); err != nil {
		return Result{}, fmt.Errorf("%w: %w", ErrInvalidInput, err)
	}
	if err := config.ValidateSemanticEmbedding(profile); err != nil {
		return Result{}, fmt.Errorf("%w: %w", ErrInvalidInput, err)
	}
	if len(ids) == 0 || strings.TrimSpace(text) == "" || len(text) > 16000 {
		return Result{}, fmt.Errorf("%w: 检索需要数据集和不超过 16000 字节的文本", ErrInvalidInput)
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
			return Result{}, fmt.Errorf("%w: 数据集 %s 的向量模型已变化，请重建索引", ErrConflict, id)
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
		return Result{}, fmt.Errorf("%w: 查询期间向量空间发生变化", ErrConflict)
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
	Mode          string           `json:"mode"`
	Total         int              `json:"total"`
	Correct       int              `json:"correct"`
	FalsePositive int              `json:"falsePositive"`
	FalseNegative int              `json:"falseNegative"`
	DurationMS    int64            `json:"durationMs"`
	Cases         []EvaluationCase `json:"cases"`
}

type EvaluationCase struct {
	BlockEligible bool    `json:"blockEligible"`
	ID            string  `json:"id"`
	Expected      string  `json:"expected"`
	Actual        string  `json:"actual"`
	Score         float64 `json:"score"`
	Correct       bool    `json:"correct"`
}

// ReserveEvaluation 让业务路由评测与数据集评测共用构建互斥，避免评测中途换索引。
func (s *Service) ReserveEvaluation() (func(), error) {
	select {
	case s.buildSlot <- struct{}{}:
		return func() { <-s.buildSlot }, nil
	default:
		return nil, fmt.Errorf("%w: 已有构建或评测正在运行", ErrConflict)
	}
}

func (s *Service) Evaluate(ctx context.Context, id string, profile config.SemanticEmbeddingConfig, threshold, margin float64) (Evaluation, error) {
	if err := config.ValidateSemanticEmbedding(profile); err != nil {
		return Evaluation{}, fmt.Errorf("%w: %w", ErrInvalidInput, err)
	}
	release, err := s.ReserveEvaluation()
	if err != nil {
		return Evaluation{}, err
	}
	defer release()
	d, err := s.Get(id)
	if err != nil {
		return Evaluation{}, err
	}
	index, err := s.published(ctx, id+"/"+profileKey(profile))
	if err != nil {
		return Evaluation{}, err
	}
	if index.Revision != d.Revision {
		return Evaluation{}, fmt.Errorf("%w: 评测前请先发布当前版本索引", ErrConflict)
	}
	start := time.Now()
	out := Evaluation{Mode: "intent", Cases: []EvaluationCase{}}
	if d.Kind == "safety" {
		out.Mode = "safety_retrieval"
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	for _, row := range d.Samples {
		if !row.Approved || row.Split != "eval" {
			continue
		}
		if out.Total >= 500 {
			return Evaluation{}, fmt.Errorf("%w: 一次最多评测 500 条样本", ErrInvalidInput)
		}
		// 安全评测使用样本携带的工具作用域；评测当前数据集全部标签，不假设线上启用配置。
		var filter func(Sample) bool
		if d.Kind == "safety" {
			filter = func(candidate Sample) bool {
				return candidate.Tool == "" || strings.EqualFold(candidate.Tool, row.Tool)
			}
		}
		result, err := s.search(ctx, []string{id}, d.Kind, profile, row.Text, "dataset-eval", filter)
		if err != nil {
			return Evaluation{}, err
		}
		actual, score := "", 0.0
		blockEligible := false
		if d.Kind == "intent" {
			if hit, matched := SelectIntent(result, threshold, margin); matched {
				actual, score = hit.Sample.Label, hit.Score
			}
		} else {
			matches, err := AssessSafety(result, row.Text, threshold, margin)
			if err != nil {
				return Evaluation{}, err
			}
			for index, match := range matches {
				if index == 0 {
					actual, score = match.Hit.Sample.Label, match.Hit.Score
				}
				blockEligible = blockEligible || match.BlockEligible
			}
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
		out.Cases = append(out.Cases, EvaluationCase{ID: row.ID, Expected: expected, Actual: actual, Score: score, Correct: correct, BlockEligible: blockEligible})
	}
	if out.Total == 0 {
		return Evaluation{}, fmt.Errorf("%w: 没有已批准的独立评测样本", ErrConflict)
	}
	latest, err := s.Get(id)
	if err != nil {
		return Evaluation{}, err
	}
	if latest.Revision != d.Revision {
		return Evaluation{}, fmt.Errorf("%w: 评测期间数据集被修改，结果未发布", ErrConflict)
	}
	out.DurationMS = time.Since(start).Milliseconds()
	return out, nil
}

type SafetyMatch struct {
	Hit           Hit
	NegativeScore float64
	BlockEligible bool
}

// AssessSafety 共用风险召回与确定性核验规则；低差值或未命中 pattern 的候选仍需审核，不能作为放行依据。
func AssessSafety(result Result, text string, threshold, margin float64) ([]SafetyMatch, error) {
	matches := []SafetyMatch{}
	for _, hit := range result.Hits {
		if hit.Sample.Negative || hit.Score < threshold {
			continue
		}
		negative := -1.0
		for _, other := range result.Hits {
			if other.Sample.Label == hit.Sample.Label && other.Sample.Negative {
				negative = max(negative, other.Score)
			}
		}
		eligible := false
		if hit.Sample.Pattern != "" && hit.Score-negative >= margin {
			pattern, err := regexp.Compile(hit.Sample.Pattern)
			if err != nil {
				return nil, fmt.Errorf("语义核验规则无效: %w", err)
			}
			eligible = pattern.MatchString(text)
		}
		matches = append(matches, SafetyMatch{Hit: hit, NegativeScore: negative, BlockEligible: eligible})
	}
	return matches, nil
}

// SelectIntent 是路由与检索评测共用的拒识规则。次佳类别即便低于阈值仍参与差值判断。
// 反例否决所属类别，unmatched 反例否决所有类别；反例不会被当作新的意图。
func SelectIntent(result Result, threshold, margin float64) (Hit, bool) {
	var best Hit
	bestScore, runnerUp := -2.0, -2.0
	for _, hit := range result.Hits {
		if hit.Sample.Negative {
			continue
		}
		if hit.Score > bestScore {
			if best.Sample.Label != hit.Sample.Label {
				runnerUp = bestScore
			}
			best, bestScore = hit, hit.Score
		} else if hit.Sample.Label != best.Sample.Label && hit.Score > runnerUp {
			runnerUp = hit.Score
		}
	}
	if best.Sample.Label == "" || bestScore < threshold || bestScore <= runnerUp || bestScore-runnerUp < margin {
		return best, false
	}
	for _, hit := range result.Hits {
		if hit.Sample.Negative && (hit.Sample.Label == best.Sample.Label || hit.Sample.Label == "unmatched") && (bestScore <= hit.Score || bestScore-hit.Score < margin) {
			return best, false
		}
	}
	return best, true
}
