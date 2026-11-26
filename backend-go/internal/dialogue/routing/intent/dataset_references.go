package intent

import (
	"context"
	"fmt"
	"slices"
	"time"

	"insistanan/api-proxy/internal/dialogue/routing/semantic"
	"insistanan/api-proxy/internal/infra/config"
)

func (s *Service) validateDatasetReferences(previous, snapshot config.Config) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	oldRefs := config.DatasetReferences(previous)
	for _, ref := range config.DatasetReferences(snapshot) {
		// 资源删除与配置发布已串行化。未变更的引用不重复探测本地助手，
		// 避免助手短暂离线阻止无关设置保存；新引用和上游身份变化仍完整验证。
		unchanged := false
		for _, old := range oldRefs {
			if old.Name != ref.Name || old.Kind != ref.Kind || old.Profile != ref.Profile || !slices.Equal(old.IDs, ref.IDs) {
				continue
			}
			if ref.Profile.Source == "local" {
				unchanged = true
				break
			}
			oldSpace, oldErr := s.embeddingSpaceIn(previous, profileIntent(old.Profile))
			newSpace, newErr := s.embeddingSpaceIn(snapshot, profileIntent(ref.Profile))
			unchanged = oldErr == nil && newErr == nil && oldSpace == newSpace
			break
		}
		if unchanged {
			continue
		}
		space, err := s.embeddingSpaceIn(snapshot, profileIntent(ref.Profile))
		if err != nil {
			return fmt.Errorf("%s: %w", ref.Name, err)
		}
		space = digest("embedding-v1\x00" + space + "\x00" + ref.Profile.ModelVersion)
		if err := s.Datasets.ValidateIndexesInSpace(ctx, ref.IDs, ref.Kind, ref.Profile, space); err != nil {
			return fmt.Errorf("%s: %w", ref.Name, err)
		}
	}
	return nil
}

func (s *Service) deleteDatasetResource(id string, profile *config.SemanticEmbeddingConfig, remove func() error) error {
	return s.Cfg.WithResourceSnapshot(func(snapshot config.Config) error {
		for _, ref := range config.DatasetReferences(snapshot) {
			if !slices.Contains(ref.IDs, id) {
				continue
			}
			if profile != nil {
				requested, referenced := *profile, ref.Profile
				requested.TimeoutMS, referenced.TimeoutMS = 0, 0
				if requested != referenced {
					continue
				}
			}
			return fmt.Errorf("%w: 数据集 %s 正被%s使用，请先关闭功能或解除引用", semantic.ErrConflict, id, ref.Name)
		}
		return remove()
	})
}

func (s *Service) DeleteDataset(ctx context.Context, id string, revision int64) error {
	return s.deleteDatasetResource(id, nil, func() error { return s.Datasets.Delete(ctx, id, revision) })
}

func (s *Service) DeleteDatasetIndex(ctx context.Context, id string, expected semantic.IndexStatus) error {
	return s.deleteDatasetResource(id, &expected.Profile, func() error { return s.Datasets.DeleteIndex(ctx, id, expected) })
}
