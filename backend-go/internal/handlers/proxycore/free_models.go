package proxycore

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/insistanan/api-proxy/internal/infra/config"
	"github.com/insistanan/api-proxy/internal/orchestration/availability"
)

const maxFreeModelAttempts = 3
const freeModelRetryWindow = time.Minute

type freeModelCandidate struct {
	model string
	state string
	rank  int
	score float64
}

func (a UpstreamAttempt) isFreeAttempt() bool {
	return a.Upstream != nil && a.Upstream.BuiltinProvider == "kilo" && config.IsFreeModel(a.RequestedModel)
}

// prepareFreeModels 对四个入口使用同一选模策略；入口协议的转换结果仍分别计入健康记录。
// 免费模型切换独立于普通渠道的 Fuzzy 开关，候选始终来自当前免费渠道快照。
func (a UpstreamAttempt) prepareFreeModels() (UpstreamAttempt, error) {
	now := time.Now()
	var aggregates []availability.Aggregate
	if a.LogContext.RequestLogStore != nil {
		ctx, cancel := context.WithTimeout(a.Context.Request.Context(), 2*time.Second)
		defer cancel()
		var err error
		aggregates, err = a.LogContext.RequestLogStore.ChannelAvailability(ctx, string(a.Kind), a.Upstream.ID, now)
		if err != nil {
			return a, fmt.Errorf("读取免费模型健康记录失败: %w", err)
		}
	} else {
		log.Printf("[%s-FreeModels] 未启用请求日志存储，免费模型按未知健康状态选择", a.APIType)
	}
	candidates := rankFreeModels(a.Upstream, a.RequestedModel, aggregates, now)
	if len(candidates) == 0 {
		return a, fmt.Errorf("免费池没有可尝试的模型：候选正在冷却或近期明确不支持，请稍后重试或同步模型")
	}
	if len(candidates) > maxFreeModelAttempts {
		candidates = candidates[:maxFreeModelAttempts]
	}
	models := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		models = append(models, candidate.model)
		log.Printf("[%s-FreeModels] 请求 %s，候选 %s，观测状态 %s", a.APIType, a.RequestedModel, candidate.model, candidate.state)
	}
	a.Upstream = a.Upstream.Clone()
	a.Upstream.ModelMapping = map[string][]string{a.RequestedModel: models}
	a.Upstream.ModelMappingChannels = nil
	a.AllowModelFailover = true
	return a, nil
}

func rankFreeModels(upstream *config.UpstreamConfig, requested string, aggregates []availability.Aggregate, now time.Time) []freeModelCandidate {
	byModel := make(map[string][]availability.Aggregate)
	for _, aggregate := range aggregates {
		byModel[aggregate.Model] = append(byModel[aggregate.Model], aggregate)
	}
	identity := upstream.RequestIdentity("").Fingerprint()
	identities := []availability.Candidate{{ID: identity}}
	explicit := ""
	if requested != config.FreeModelID {
		explicit = strings.TrimPrefix(requested, "free/")
	}
	var candidates []freeModelCandidate
	for _, model := range upstream.ModelMapping[config.FreeModelID] {
		row := availability.Summarize(upstream.ID, upstream.Name, model, identities, byModel[model], now)
		if row.State == "cooling" || row.State == "unsupported" && model != explicit {
			continue
		}
		rank := 1
		switch row.State {
		case "healthy":
			rank = 0
		case "degraded", "unsupported":
			rank = 2
		}
		// 平滑成功率避免把一次成功直接当作充分的健康证据；同分保留目录顺序。
		score := float64(row.Recent.Success+1) / float64(row.Recent.Eligible()+2)
		candidates = append(candidates, freeModelCandidate{model: model, state: row.State, rank: rank, score: score})
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		left, right := candidates[i], candidates[j]
		if (left.model == explicit) != (right.model == explicit) {
			return left.model == explicit
		}
		// 自动选模由本网关执行；Kilo 的自动入口保留为末位候选或显式请求目标。
		if (left.model == config.KiloAutoModelID) != (right.model == config.KiloAutoModelID) {
			return right.model == config.KiloAutoModelID
		}
		if left.rank != right.rank {
			return left.rank < right.rank
		}
		return left.score > right.score
	})
	return candidates
}
