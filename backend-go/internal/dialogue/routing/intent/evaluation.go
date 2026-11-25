package intent

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/insistanan/api-proxy/internal/dialogue/routing/semantic"
	"github.com/insistanan/api-proxy/internal/infra/config"
)

type RoutingEvaluationCase struct {
	ID         string `json:"id"`
	Expected   string `json:"expected"`
	Actual     string `json:"actual"`
	PoolID     string `json:"poolId"`
	Tier       int    `json:"tier"`
	Correct    bool   `json:"correct"`
	Error      string `json:"error,omitempty"`
	DurationMS int64  `json:"durationMs"`
}

type RoutingEvaluation struct {
	Total       int                     `json:"total"`
	Correct     int                     `json:"correct"`
	Unmatched   int                     `json:"unmatched"`
	WrongRoutes int                     `json:"wrongRoutes"`
	Failures    int                     `json:"failures"`
	DurationMS  int64                   `json:"durationMs"`
	Cases       []RoutingEvaluationCase `json:"cases"`
}

// EvaluateRouting 复用实际分类与分组选择，独立评测新任务，不模拟会话驻留或上游交付。
func (s *Service) EvaluateRouting(ctx context.Context, kind string, cfg config.IntentRoutingConfig, datasetID string) (RoutingEvaluation, error) {
	if s.Datasets == nil {
		return RoutingEvaluation{}, fmt.Errorf("意图匹配未绑定数据集服务")
	}
	release, err := s.Datasets.ReserveEvaluation()
	if err != nil {
		return RoutingEvaluation{}, err
	}
	defer release()
	dataset, err := s.Datasets.Get(datasetID)
	if err != nil {
		return RoutingEvaluation{}, err
	}
	if dataset.Kind != "intent" {
		return RoutingEvaluation{}, fmt.Errorf("路由评测只能使用意图数据集")
	}
	space := ""
	if config.IntentUsesVector(cfg) {
		space, err = s.EmbeddingSpace(embeddingProfile(cfg))
		if err != nil {
			return RoutingEvaluation{}, err
		}
	}
	ctx, cancel := context.WithTimeout(WithProtocol(ctx, kind), 10*time.Minute)
	defer cancel()
	started := time.Now()
	out := RoutingEvaluation{Cases: []RoutingEvaluationCase{}}
	for _, row := range dataset.Samples {
		if !row.Approved || row.Split != "eval" {
			continue
		}
		if out.Total >= 500 {
			return RoutingEvaluation{}, fmt.Errorf("一次最多评测 500 条样本")
		}
		if err := ctx.Err(); err != nil {
			return RoutingEvaluation{}, err
		}
		decision, classifyErr := s.Classify(ctx, cfg, Input{Text: row.Text}, "intent-evaluation")
		if err := ctx.Err(); err != nil {
			return RoutingEvaluation{}, err
		}
		expected := row.Label
		if row.Negative {
			expected = ""
		}
		item := RoutingEvaluationCase{ID: row.ID, Expected: expected, Actual: decision.Task, PoolID: decision.PoolID, Tier: decision.Tier, DurationMS: decision.DurationMS}
		if classifyErr != nil {
			item.Error = safeTraceText(classifyErr.Error())
			out.Failures++
		} else {
			item.Correct = expected == decision.Task
			if item.Correct {
				out.Correct++
			}
			if decision.PoolID == "" {
				out.Unmatched++
			}
			if decision.PoolID != "" && !item.Correct {
				out.WrongRoutes++
			}
		}
		out.Total++
		out.Cases = append(out.Cases, item)
	}
	if out.Total == 0 {
		return RoutingEvaluation{}, fmt.Errorf("没有已批准的独立评测样本")
	}
	current, err := s.Datasets.Get(dataset.ID)
	if err != nil {
		return RoutingEvaluation{}, err
	}
	if current.Revision != dataset.Revision {
		return RoutingEvaluation{}, fmt.Errorf("评测期间样本发生变化，请重试")
	}
	if space != "" {
		currentSpace, err := s.EmbeddingSpace(embeddingProfile(cfg))
		if err != nil {
			return RoutingEvaluation{}, err
		}
		if currentSpace != space {
			return RoutingEvaluation{}, fmt.Errorf("评测期间向量模型发生变化，请重试")
		}
	}
	out.DurationMS = time.Since(started).Milliseconds()
	return out, nil
}

// CorrectTask 只保存经过日志脱敏的输入为待审核样本，不自动改变线上索引。
func (s *Service) CorrectTask(ctx context.Context, kind, requestID, label string) (string, error) {
	cfg := s.Cfg.GetIntentRouting(kind)
	allowed := label == "unmatched" || slices.ContainsFunc(config.IntentTasks(), func(task config.IntentTask) bool { return task.ID == label })
	for _, group := range cfg.Groups {
		allowed = allowed || slices.Contains(group.Tasks, label)
	}
	if !allowed {
		return "", fmt.Errorf("请选择已声明的任务类别")
	}
	s.mu.Lock()
	var input string
	for _, event := range s.state.Events {
		if event.Protocol == kind && event.RequestID == requestID && !event.Control && event.Decision.Trace != nil && !event.Decision.Trace.Truncated {
			input = event.Decision.Trace.Input
			break
		}
	}
	s.mu.Unlock()
	if strings.TrimSpace(input) == "" {
		return "", fmt.Errorf("记录不存在、输入为空或已截断，无法建立可靠的纠正样本")
	}
	id := "routing-corrections-" + kind
	if len(cfg.DatasetIDs) == 1 {
		id = cfg.DatasetIDs[0]
	}
	dataset, err := s.Datasets.Get(id)
	if errors.Is(err, semantic.ErrNotFound) {
		dataset = semantic.Dataset{ID: id, Name: kind + " 意图纠正", Kind: "intent", Description: "来自人工纠正的脱敏输入，默认待审核评测样本。批准或调整集合后重新构建索引。", Samples: []semantic.Sample{}}
	} else if err != nil {
		return "", err
	}
	if dataset.Kind != "intent" {
		return "", fmt.Errorf("纠正目标必须是意图数据集")
	}
	sample := semantic.Sample{ID: "correction_" + digest(input)[:24], Label: label, Text: input, Negative: label == "unmatched", Split: "eval", Family: "correction_" + digest(input)[:24], Source: "manual-routing-feedback"}
	found := false
	for i, row := range dataset.Samples {
		if strings.TrimSpace(row.Text) == strings.TrimSpace(input) {
			sample.ID, sample.Family, sample.Split = row.ID, row.Family, row.Split
			dataset.Samples[i] = sample
			found = true
			break
		}
	}
	if !found {
		dataset.Samples = append(dataset.Samples, sample)
	}
	_, err = s.Datasets.Save(ctx, dataset)
	if err != nil {
		return "", err
	}
	return dataset.ID, nil
}
