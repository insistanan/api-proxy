package intent

import (
	"context"
	"fmt"
	"strings"

	"insistanan/api-proxy/internal/dialogue/routing/semantic"
	"insistanan/api-proxy/internal/infra/config"
)

func (s *Service) ValidateVector(p config.SemanticEmbeddingConfig) error {
	status := LocalStatus{PresetsURL: DefaultPresetsURL}
	if s.Local != nil {
		status = s.Local.Status()
	}
	required := func(message string) error {
		return &semantic.VectorModelRequiredError{Message: message, InstallPath: status.InstallPath, PresetsURL: status.PresetsURL}
	}
	switch p.Source {
	case "local":
		if !status.Ready {
			return required("本地向量模型尚未就绪，请下载、启动并预热，或选择已有 Embeddings 渠道")
		}
		if p.Model != status.Manifest.ModelName {
			return required("向量模型名称与本地助手不一致，请选择已安装模型 " + status.Manifest.ModelName)
		}
	case "remote":
		if strings.TrimSpace(p.Model) == "" {
			return required("请选择 Embeddings 渠道并填写向量模型名称")
		}
		if s.Cfg == nil {
			return required("向量渠道配置尚未装配")
		}
		if _, _, err := s.embeddingUpstream(profileIntent(p)); err != nil {
			return required(fmt.Sprintf("%s；请选择已启用且已配置密钥的 Embeddings 渠道，或安装本地向量模型", err))
		}
	default:
		return fmt.Errorf("向量来源必须为 remote 或 local")
	}
	return nil
}

func (s *Service) ValidateIntentIndexes(ctx context.Context, cfg config.IntentRoutingConfig) error {
	if !config.IntentUsesVector(cfg) {
		return nil
	}
	if s.Datasets == nil {
		return fmt.Errorf("意图匹配未绑定数据集服务")
	}
	ids := cfg.DatasetIDs
	if len(ids) == 0 && cfg.RecognitionMode == "" {
		ids = []string{inlineDatasetID(ctx)}
	}
	return s.Datasets.ValidateIndexes(ctx, ids, "intent", embeddingProfile(cfg))
}

func (s *Service) ValidateIntentVector(cfg config.IntentRoutingConfig) error {
	if config.IntentUsesVector(cfg) {
		return s.ValidateVector(embeddingProfile(cfg))
	}
	return nil
}
