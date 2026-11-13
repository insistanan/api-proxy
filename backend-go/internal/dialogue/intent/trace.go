package intent

import (
	"context"
	"fmt"
	"regexp"
	"slices"

	"github.com/insistanan/api-proxy/internal/dialogue/semantic"
	"github.com/insistanan/api-proxy/internal/infra/config"
	"github.com/insistanan/api-proxy/internal/infra/utils"
	"github.com/insistanan/api-proxy/internal/safety/sensitive"
)

// DecisionTrace 保存当时的判断依据，不用当前配置重新推断历史决策。
type DecisionTrace struct {
	Input           string          `json:"input"`
	Truncated       bool            `json:"truncated"`
	Classified      bool            `json:"classified"`
	Strategy        string          `json:"strategy"`
	TaskSource      string          `json:"taskSource,omitempty"`
	TierSource      string          `json:"tierSource,omitempty"`
	TaskEvidence    string          `json:"taskEvidence,omitempty"`
	TierEvidence    string          `json:"tierEvidence,omitempty"`
	SuggestedPoolID string          `json:"suggestedPoolId,omitempty"`
	Selection       string          `json:"selection,omitempty"`
	Error           string          `json:"error,omitempty"`
	Groups          []GroupMatch    `json:"groups"`
	Semantic        []SemanticMatch `json:"semantic,omitempty"`
	Threshold       float64         `json:"threshold"`
	SwitchMargin    float64         `json:"switchMargin"`
	freeActive      bool
}

type GroupMatch struct {
	PoolID         string   `json:"poolId"`
	Name           string   `json:"name"`
	Tasks          []string `json:"tasks"`
	Tiers          []int    `json:"tiers"`
	Status         string   `json:"status"`
	Reason         string   `json:"reason"`
	Cost           float64  `json:"cost"`
	SelectionScore *float64 `json:"selectionScore,omitempty"`
	Good           float64  `json:"good,omitempty"`
	Bad            float64  `json:"bad,omitempty"`
}

type SemanticMatch struct {
	DatasetID string  `json:"datasetId"`
	Revision  int64   `json:"revision"`
	SampleID  string  `json:"sampleId"`
	Label     string  `json:"label"`
	Negative  bool    `json:"negative"`
	Score     float64 `json:"score"`
}

type traceKey struct{}

func traceFrom(ctx context.Context) *DecisionTrace {
	trace, _ := ctx.Value(traceKey{}).(*DecisionTrace)
	return trace
}

func safeTraceText(text string) string {
	text = utils.RedactSensitivePlaceholdersForLog(sensitive.RedactAuditText(text))
	bounded, _ := normalizeInput(text)
	return bounded
}

func (s *Service) freeEnabled() bool {
	if s.Cfg == nil {
		return false
	}
	cfg := s.Cfg.GetConfig().FreeChannels
	return cfg != nil && cfg.Kilo.Enabled
}

func (s *Service) newTrace(ctx context.Context, cfg config.IntentRoutingConfig, input Input) *DecisionTrace {
	trace := &DecisionTrace{Input: safeTraceText(input.Text), Truncated: input.Truncated, Strategy: cfg.Strategy, Groups: []GroupMatch{}, freeActive: s.freeEnabled()}
	if input.ToolContinuation {
		trace.Input, trace.Truncated = "", false
	}
	pools := []config.ChannelPool{}
	if s.Cfg != nil {
		pools = s.Cfg.GetChannelPools(protocolFrom(ctx))
	}
	// 单元分类调用可以没有渠道配置；仍保留实际参与的能力或映射快照。
	addPool := func(id string) {
		if !slices.ContainsFunc(pools, func(pool config.ChannelPool) bool { return pool.ID == id }) {
			pools = append(pools, config.ChannelPool{ID: id, Name: id})
		}
	}
	for _, group := range cfg.Groups {
		addPool(group.PoolID)
	}
	for _, route := range cfg.Routes {
		addPool(route.PoolID)
	}
	for _, pool := range pools {
		group := GroupMatch{PoolID: pool.ID, Name: pool.Name, Status: "skipped", Reason: "本轮未重新分类"}
		for _, capability := range cfg.Groups {
			if capability.PoolID == pool.ID {
				group.Tasks, group.Tiers, group.Cost = slices.Clone(capability.Tasks), slices.Clone(capability.Tiers), capability.Cost
			}
		}
		trace.Groups = append(trace.Groups, group)
	}
	return trace
}

// finishTrace 只解释本次已经执行的条件检查，不额外调用分类器或重新计算反馈分数。
func (s *Service) finishTrace(trace *DecisionTrace, cfg config.IntentRoutingConfig, decision Decision, err error) {
	trace.SuggestedPoolID = decision.PoolID
	if err != nil {
		trace.Error = safeTraceText(err.Error())
	}
	for i := range trace.Groups {
		group := &trace.Groups[i]
		switch {
		case group.PoolID == config.FreeChannelPoolID && !trace.freeActive:
			group.Status, group.Reason = "disabled", "免费服务已停用或未初始化"
		case !trace.Classified:
			continue
		case err != nil:
			group.Status, group.Reason = "skipped", "分类失败，未完成选组"
		case cfg.Strategy == "auto":
			switch {
			case len(group.Tasks) == 0 || len(group.Tiers) == 0:
				group.Status, group.Reason = "unconfigured", "尚未配置任务类型和难度"
			case decision.Task == "":
				group.Status, group.Reason = "skipped", "任务类型未确定"
			case !slices.Contains(group.Tasks, decision.Task):
				group.Status, group.Reason = "excluded", "未声明适合该任务类型"
			case decision.Tier < 0:
				group.Status, group.Reason = "skipped", "任务类型满足，但难度未确定"
			case !slices.Contains(group.Tiers, decision.Tier):
				group.Status, group.Reason = "excluded", "未声明可处理该难度"
			default:
				group.Status, group.Reason = "candidate", "任务类型与难度均满足"
			}
		default:
			group.Status, group.Reason = "excluded", "本次映射规则未选择该组"
			for _, route := range cfg.Routes {
				if route.PoolID != group.PoolID {
					continue
				}
				if cfg.Strategy == "semantic" {
					if score, ok := decision.PoolScores[group.PoolID]; ok {
						group.Reason = fmt.Sprintf("召回相似度 %.4f；命中还需满足阈值与正反例差值", score)
					}
				} else if (cfg.Strategy == "complexity" || cfg.Strategy == "adaptive") && decision.Tier >= 0 && route.Tier == decision.Tier {
					group.Status, group.Reason = "candidate", "难度满足映射条件"
				}
			}
		}
		if group.PoolID == decision.PoolID && err == nil {
			group.Status, group.Reason = "selected", decision.Reason
		}
	}
}

func recordSemanticTrace(ctx context.Context, cfg config.IntentRoutingConfig, result semantic.Result) {
	trace := traceFrom(ctx)
	if trace == nil {
		return
	}
	trace.TaskSource = "semantic"
	trace.Threshold, trace.SwitchMargin = cfg.Threshold, cfg.SwitchMargin
	for _, hit := range result.Hits {
		if len(trace.Semantic) >= 8 {
			break
		}
		trace.Semantic = append(trace.Semantic, SemanticMatch{DatasetID: hit.DatasetID, Revision: hit.Revision, SampleID: hit.Sample.ID, Label: hit.Sample.Label, Negative: hit.Sample.Negative, Score: hit.Score})
	}
}

func recordTierEvidence(ctx context.Context, task, text string, tier int, known bool) {
	trace := traceFrom(ctx)
	if trace == nil {
		return
	}
	trace.TierSource = "heuristic"
	if !known {
		trace.TierEvidence = "内置规则未获得明确难度信号"
		return
	}
	patterns := []*regexp.Regexp{easySignals, mediumSignals, hardSignals}
	if tier >= 0 && tier < len(patterns) {
		if match := patterns[tier].FindString(text); match != "" {
			trace.TierEvidence = "内置难度规则命中：" + safeTraceText(match)
			return
		}
	}
	if task != "" {
		trace.TierEvidence = "依据已识别任务的内置难度规则"
	}
}
