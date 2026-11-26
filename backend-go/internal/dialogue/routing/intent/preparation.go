package intent

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"insistanan/api-proxy/internal/dialogue/routing/semantic"
	"insistanan/api-proxy/internal/infra/config"
)

// Preparation 属于服务生命周期；关闭浏览器不取消任务，停机先取消并等待再关闭存储。
type Preparation struct {
	ID        string                      `json:"id"`
	Protocol  string                      `json:"protocol"`
	Running   bool                        `json:"running"`
	Phase     string                      `json:"phase"`
	Error     string                      `json:"error"`
	Config    *config.IntentRoutingConfig `json:"config,omitempty"`
	Completed []string                    `json:"completed"`
	Committed bool                        `json:"committed"`
}

type preparationObserverKey struct{}

func recordPreparation(ctx context.Context, artifact string) {
	if observe, ok := ctx.Value(preparationObserverKey{}).(func(string)); ok {
		observe(artifact)
	}
}

func (s *Service) PreparationStatus() Preparation {
	s.preparationMu.Lock()
	defer s.preparationMu.Unlock()
	result := s.preparation
	result.Completed = append([]string{}, result.Completed...)
	if result.Config != nil {
		copy := *result.Config
		copy.DatasetIDs = slices.Clone(copy.DatasetIDs)
		result.Config = &copy
	}
	return result
}

func (s *Service) preparationPhase(phase string) {
	s.preparationMu.Lock()
	s.preparation.Phase = phase
	s.preparationMu.Unlock()
}

func (s *Service) startPreparation(kind string, run func(context.Context) (*config.IntentRoutingConfig, error)) (Preparation, error) {
	s.preparationMu.Lock()
	defer s.preparationMu.Unlock()
	if s.preparationClosing || s.preparationStopping || s.preparation.Running {
		return Preparation{}, fmt.Errorf("已有模型准备任务正在执行或服务正在关闭，请等待完成")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	done := make(chan struct{})
	s.preparationCancel, s.preparationDone = cancel, done
	s.preparation = Preparation{ID: uuid.NewString(), Protocol: kind, Running: true, Phase: "queued", Completed: []string{}}
	ctx = context.WithValue(ctx, preparationObserverKey{}, func(artifact string) {
		s.preparationMu.Lock()
		defer s.preparationMu.Unlock()
		if !slices.Contains(s.preparation.Completed, artifact) {
			s.preparation.Completed = append(s.preparation.Completed, artifact)
		}
		s.preparation.Committed = true
	})
	go func() {
		defer close(done)
		defer cancel()
		result, err := run(ctx)
		if err == nil {
			err = ctx.Err()
		}
		s.preparationMu.Lock()
		defer s.preparationMu.Unlock()
		s.preparation.Running = false
		s.preparation.Config = result
		s.preparation.Phase = "ready"
		if err != nil {
			s.preparation.Config = nil
			s.preparation.Phase, s.preparation.Error = "failed", err.Error()
			if errors.Is(err, context.Canceled) {
				s.preparation.Phase = "cancelled"
			}
			if len(s.preparation.Completed) > 0 {
				s.preparation.Error += "；已生效产物：" + strings.Join(s.preparation.Completed, "、") + "。请核对后重试。"
				if kind != "" {
					s.preparation.Error += "路由配置尚未应用。"
				}
			}
		}
	}()
	return s.preparation, nil
}

func (s *Service) CancelPreparation(taskID string) (Preparation, error) {
	s.preparationMu.Lock()
	defer s.preparationMu.Unlock()
	if taskID == "" || s.preparation.ID != taskID {
		return Preparation{}, fmt.Errorf("准备任务已变化，请刷新后重试")
	}
	if s.preparation.Running {
		s.preparationCancel()
		s.preparation.Phase = "cancelling"
	}
	result := s.preparation
	result.Completed = slices.Clone(result.Completed)
	return result, nil
}

func (s *Service) StopPreparation() error {
	s.preparationMu.Lock()
	s.preparationClosing = true
	cancel, done := s.preparationCancel, s.preparationDone
	s.preparationMu.Unlock()
	if cancel != nil {
		cancel()
		<-done
	}
	return nil
}

// StopLocal 不把停止运行时解释为取消未知代次的准备任务。
func (s *Service) StopLocal() error {
	s.preparationMu.Lock()
	if s.preparation.Running || s.preparationStopping || s.preparationClosing {
		s.preparationMu.Unlock()
		return fmt.Errorf("请先取消当前准备任务并等待结束，再停止本地助手")
	}
	s.preparationStopping = true
	s.preparationMu.Unlock()
	defer func() {
		s.preparationMu.Lock()
		s.preparationStopping = false
		s.preparationMu.Unlock()
	}()
	return s.Local.Stop()
}

func (s *Service) LocalStatus() LocalStatus {
	status := s.Local.Status()
	job := s.PreparationStatus()
	status.PreparationID = job.ID
	if job.Running && !status.Busy {
		status.Phase = job.Phase
	}
	status.Busy = status.Busy || job.Running
	if job.Error != "" {
		status.Error = job.Error
	}
	return status
}

func (s *Service) StartLocalAction(action, preset string) (Preparation, error) {
	if action != "start" && action != "install" {
		return Preparation{}, fmt.Errorf("不支持的本地模型操作")
	}
	return s.startPreparation("", func(ctx context.Context) (*config.IntentRoutingConfig, error) {
		s.preparationPhase("preparing_model")
		if action == "install" {
			return nil, s.Local.InstallPreset(ctx, preset)
		}
		return nil, s.Local.Start(ctx)
	})
}

func (s *Service) PrepareIntent(kind string, cfg config.IntentRoutingConfig, preset string, adoptBuiltin bool) (Preparation, error) {
	if !config.SupportsIntentRouting(kind) || !config.IntentUsesVector(cfg) {
		return Preparation{}, fmt.Errorf("当前协议与识别方式不需要向量准备")
	}
	return s.startPreparation(kind, func(ctx context.Context) (*config.IntentRoutingConfig, error) {
		ctx = WithProtocol(ctx, kind)
		if cfg.EmbeddingSource == "local" {
			s.preparationPhase("preparing_model")
			status := s.Local.Status()
			if !status.Ready {
				if status.Installed {
					present, err := s.Local.installedFilesPresent(status.Manifest)
					if err != nil {
						return nil, err
					}
					if !present || status.Error != "" {
						if err := s.Local.Install(ctx, status.Manifest); err != nil {
							return nil, err
						}
					}
					if err := s.Local.Start(ctx); err != nil {
						return nil, err
					}
				} else {
					if preset == "" {
						catalog := s.Local.Presets(ctx)
						if catalog.Error != "" {
							return nil, fmt.Errorf("推荐清单不可用：%s", catalog.Error)
						}
						if len(catalog.Presets) == 0 {
							return nil, fmt.Errorf("当前平台没有推荐运行时，请选择远程 Embeddings 渠道")
						}
						preset = catalog.Presets[0].ID
					}
					if err := s.Local.InstallPreset(ctx, preset); err != nil {
						return nil, err
					}
				}
			}
			model := s.Local.Status().Manifest.ModelName
			if cfg.EmbeddingModel != model || cfg.EmbeddingChannel != "" || cfg.EmbeddingVersion != "" {
				cfg.QueryPrefix, cfg.ExamplePrefix = "", ""
			}
			cfg.EmbeddingModel = model
			cfg.EmbeddingChannel, cfg.EmbeddingVersion = "", ""
		}
		if err := s.ValidateIntentVector(cfg); err != nil {
			return nil, err
		}
		if len(cfg.DatasetIDs) == 0 && cfg.RecognitionMode == "" && cfg.Strategy == "semantic" && slices.ContainsFunc(cfg.Routes, func(route config.IntentRoute) bool { return len(route.Examples) > 0 }) {
			s.preparationPhase("indexing")
			if err := s.BuildIndex(ctx, cfg); err != nil {
				return nil, err
			}
			cfg.DatasetIDs = []string{inlineDatasetID(ctx)}
			return &cfg, s.ValidateIntentIndexes(ctx, cfg)
		}
		if len(cfg.DatasetIDs) == 0 {
			if !adoptBuiltin {
				return nil, fmt.Errorf("请选择数据集，或明确采用内置意图样本")
			}
			s.preparationPhase("preparing_samples")
			if err := s.adoptBuiltinIntents(ctx); err != nil {
				return nil, err
			}
			recordPreparation(ctx, "数据集："+semantic.RecommendedIntentDatasetID)
			cfg.DatasetIDs = []string{semantic.RecommendedIntentDatasetID}
		}
		s.preparationPhase("indexing")
		if err := s.BuildIndex(ctx, cfg); err != nil {
			return nil, err
		}
		if err := s.ValidateIntentIndexes(ctx, cfg); err != nil {
			return nil, err
		}
		return &cfg, nil
	})
}

func (s *Service) adoptBuiltinIntents(ctx context.Context) error {
	var template semantic.Dataset
	for _, dataset := range semantic.BuiltinDatasets() {
		if dataset.ID == semantic.RecommendedIntentDatasetID {
			template = dataset
		}
	}
	if template.ID == "" {
		return fmt.Errorf("内置意图样本未随程序提供")
	}
	current, err := s.Datasets.Get(template.ID)
	if err == nil {
		rows := slices.Clone(current.Samples)
		for i := range rows {
			rows[i].Approved = false
		}
		if !reflect.DeepEqual(rows, template.Samples) {
			return fmt.Errorf("内置意图数据集已经编辑，请选择该数据集并审核自己的改动；准备任务不会覆盖样本")
		}
		template = current
		template.Samples = slices.Clone(current.Samples)
	} else if !errors.Is(err, semantic.ErrNotFound) {
		return err
	}
	for i := range template.Samples {
		template.Samples[i].Approved = true
	}
	if err == nil && reflect.DeepEqual(template.Samples, current.Samples) {
		return nil
	}
	_, err = s.Datasets.Save(ctx, template)
	return err
}

// 仅恢复已保存配置实际依赖的助手，不下载新资源，也不自动批准或更换数据集。
func (s *Service) RestoreLocal() error {
	needed := false
	for _, kind := range []string{"messages", "responses", "chat", "gemini"} {
		cfg := s.Cfg.GetIntentRouting(kind)
		needed = needed || cfg.Enabled && config.IntentUsesVector(cfg) && cfg.EmbeddingSource == "local"
	}
	safety := s.Cfg.GetContentSafetyConfig()
	if safety.Enabled && safety.DangerousCmd.Enabled && safety.DangerousCmd.Semantic != nil {
		needed = needed || safety.DangerousCmd.Semantic.Enabled && safety.DangerousCmd.Semantic.Embedding.Source == "local"
	}
	if !needed {
		return nil
	}
	_, err := s.StartLocalAction("start", "")
	return err
}
