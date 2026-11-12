package intent

import (
	"context"
	"errors"
	"fmt"
	"math"
	"reflect"

	"github.com/insistanan/api-proxy/internal/config"
	"github.com/insistanan/api-proxy/internal/semantic"
)

func embeddingProfile(cfg config.IntentRoutingConfig) config.SemanticEmbeddingConfig {
	return config.SemanticEmbeddingConfig{Source: cfg.EmbeddingSource, Channel: cfg.EmbeddingChannel, Model: cfg.EmbeddingModel, ModelVersion: cfg.EmbeddingVersion, QueryPrefix: cfg.QueryPrefix, ExamplePrefix: cfg.ExamplePrefix, TimeoutMS: cfg.TimeoutMS}
}

func profileIntent(p config.SemanticEmbeddingConfig) config.IntentRoutingConfig {
	return config.IntentRoutingConfig{EmbeddingSource: p.Source, EmbeddingChannel: p.Channel, EmbeddingModel: p.Model, TimeoutMS: p.TimeoutMS}
}

func (s *Service) EmbeddingSpace(p config.SemanticEmbeddingConfig) (string, error) {
	space, err := s.embeddingSpace(profileIntent(p))
	if err != nil {
		return "", err
	}
	return digest("embedding-v1\x00" + space + "\x00" + p.ModelVersion), nil
}

func (s *Service) EmbedSamples(ctx context.Context, p config.SemanticEmbeddingConfig, texts []string, parent string) ([][]float64, error) {
	return s.embed(ctx, profileIntent(p), texts, parent)
}

func inlineDatasetID(ctx context.Context) string { return "inline-" + protocolFrom(ctx) }

// BuildIndex 将旧版协议内例句迁入独立数据集；正式数据集由类别映射复用。
func (s *Service) BuildIndex(ctx context.Context, cfg config.IntentRoutingConfig) error {
	ids := cfg.DatasetIDs
	if len(ids) == 0 {
		kind := protocolFrom(ctx)
		if !config.SupportsIntentRouting(kind) {
			return fmt.Errorf("构建例句索引需要明确入口协议")
		}
		d := semantic.Dataset{ID: inlineDatasetID(ctx), Name: kind + " 原有意图例句", Kind: "intent", Description: "由协议配置内例句迁入，后续可在数据集页面编辑。", Samples: []semantic.Sample{}}
		for _, route := range cfg.Routes {
			for _, example := range route.Examples {
				label := route.Label
				if label == "" {
					label = route.ID
				}
				d.Samples = append(d.Samples, semantic.Sample{ID: fmt.Sprintf("row_%d", len(d.Samples)+1), Label: label, Text: example, Split: "index", Family: route.ID, Approved: true, Source: "existing-intent-config"})
			}
		}
		if len(d.Samples) == 0 {
			return fmt.Errorf("请先选择数据集或填写例句")
		}
		current, err := s.Datasets.Get(d.ID)
		if err == nil {
			d.Revision = current.Revision
			if reflect.DeepEqual(current.Samples, d.Samples) {
				d = current
			} else {
				return fmt.Errorf("原例句已迁入数据集 %s；为保留独立编辑，请在路由中选择该数据集并到数据集页修改样本", d.ID)
			}
		} else if errors.Is(err, semantic.ErrNotFound) {
			d, err = s.Datasets.Save(ctx, d)
		}
		if err != nil {
			return err
		}
		ids = []string{d.ID}
	}
	for _, id := range ids {
		d, err := s.Datasets.Get(id)
		if err != nil {
			return err
		}
		if d.Kind != "intent" {
			return fmt.Errorf("意图路由只能使用意图数据集")
		}
		if _, err = s.Datasets.Build(ctx, id, d.Revision, embeddingProfile(cfg)); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) semantic(ctx context.Context, cfg config.IntentRoutingConfig, text, parent string) (Decision, error) {
	ids := cfg.DatasetIDs
	if len(ids) == 0 {
		ids = []string{inlineDatasetID(ctx)}
	}
	result, err := s.Datasets.Search(ctx, ids, "intent", embeddingProfile(cfg), text, parent)
	if err != nil {
		return Decision{}, err
	}
	best := Decision{Reason: "语义相似度或正反例差值未达到阈值", PoolScores: map[string]float64{}}
	// 切换收益需要同时比较当前分组；低于召回阈值的分数也必须保留。
	for _, hit := range result.Hits {
		if hit.Sample.Negative {
			continue
		}
		for _, route := range cfg.Routes {
			label := route.Label
			if label == "" {
				label = route.ID
			}
			if label == hit.Sample.Label {
				best.PoolScores[route.PoolID] = math.Max(best.PoolScores[route.PoolID], hit.Score)
			}
		}
	}
	for _, hit := range semantic.PositiveLabels(result, cfg.Threshold, cfg.SwitchMargin) {
		for _, route := range cfg.Routes {
			label := route.Label
			if label == "" {
				label = route.ID
			}
			if label != hit.Sample.Label {
				continue
			}
			best.PoolScores[route.PoolID] = math.Max(best.PoolScores[route.PoolID], hit.Score)
			if hit.Score > best.Score {
				scores := best.PoolScores
				best = routeDecision(route, hit.Score, fmt.Sprintf("数据集 %s r%d 命中 %s", hit.DatasetID, hit.Revision, hit.Sample.ID))
				best.PoolScores = scores
			}
		}
	}
	return best, nil
}
