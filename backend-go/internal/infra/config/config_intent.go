package config

import (
	"fmt"
	"math"
	"regexp"
	"slices"
	"strings"
)

// IntentRoutingConfig 按入口协议保存，路由目标始终是稳定的分组 ID。
type IntentRoutingConfig struct {
	TaskMatcher         string                  `json:"taskMatcher,omitempty"`
	Groups              []IntentGroupCapability `json:"groups,omitempty"`
	UseFeedback         bool                    `json:"useFeedback,omitempty"`
	DatasetIDs          []string                `json:"datasetIds,omitempty"`
	EmbeddingVersion    string                  `json:"embeddingVersion,omitempty"`
	SessionEpoch        uint64                  `json:"sessionEpoch,omitempty"`
	Enabled             bool                    `json:"enabled"`
	Strategy            string                  `json:"strategy"`
	Classifier          string                  `json:"classifier"`
	ClassifierChannel   string                  `json:"classifierChannel"`
	ClassifierModel     string                  `json:"classifierModel"`
	EmbeddingChannel    string                  `json:"embeddingChannel"`
	EmbeddingModel      string                  `json:"embeddingModel"`
	EmbeddingSource     string                  `json:"embeddingSource"`
	QueryPrefix         string                  `json:"queryPrefix"`
	ExamplePrefix       string                  `json:"examplePrefix"`
	Threshold           float64                 `json:"threshold"`
	TimeoutMS           int                     `json:"timeoutMs"`
	MinResidenceSeconds int                     `json:"minResidenceSeconds"`
	MinTurns            int                     `json:"minTurns"`
	SwitchMargin        float64                 `json:"switchMargin"`
	Unmatched           string                  `json:"unmatched"`
	FallbackPoolID      string                  `json:"fallbackPoolId"`
	FailurePolicy       string                  `json:"failurePolicy"`
	ObserveOnly         bool                    `json:"observeOnly"`
	Routes              []IntentRoute           `json:"routes"`
}

type IntentRoute struct {
	Label       string   `json:"label,omitempty"`
	ID          string   `json:"id"`
	PoolID      string   `json:"poolId"`
	Description string   `json:"description"`
	Patterns    []string `json:"patterns"`
	Examples    []string `json:"examples"`
	Tier        int      `json:"tier"`
	Cost        float64  `json:"cost"`
}

func SupportsIntentRouting(kind string) bool {
	return kind == "messages" || kind == "responses" || kind == "chat" || kind == "gemini"
}

func DefaultIntentRouting() IntentRoutingConfig {
	return IntentRoutingConfig{Strategy: "regex", Classifier: "heuristic", EmbeddingSource: "remote", Threshold: 0.75,
		TimeoutMS: 2000, MinResidenceSeconds: 300, MinTurns: 4, SwitchMargin: 0.15,
		Unmatched: "original", FailurePolicy: "error", ObserveOnly: true, Routes: []IntentRoute{}}
}

func cloneIntentRouting(source map[string]IntentRoutingConfig) map[string]IntentRoutingConfig {
	if source == nil {
		return nil
	}
	out := make(map[string]IntentRoutingConfig, len(source))
	for kind, value := range source {
		if value.Classifier == "systemone" {
			value.Classifier = "decisions"
		}
		value.Groups = slices.Clone(value.Groups)
		for i := range value.Groups {
			value.Groups[i].Tasks = slices.Clone(value.Groups[i].Tasks)
			value.Groups[i].Tiers = slices.Clone(value.Groups[i].Tiers)
		}
		value.DatasetIDs = slices.Clone(value.DatasetIDs)
		value.Routes = slices.Clone(value.Routes)
		if value.Routes == nil {
			value.Routes = []IntentRoute{}
		}
		for i := range value.Routes {
			value.Routes[i].Patterns = slices.Clone(value.Routes[i].Patterns)
			value.Routes[i].Examples = slices.Clone(value.Routes[i].Examples)
			if value.Routes[i].Patterns == nil {
				value.Routes[i].Patterns = []string{}
			}
			if value.Routes[i].Examples == nil {
				value.Routes[i].Examples = []string{}
			}
		}
		out[kind] = value
	}
	return out
}

func (cm *ConfigManager) GetIntentRouting(kind string) IntentRoutingConfig {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	if value, ok := cloneIntentRouting(cm.config.IntentRouting)[kind]; ok {
		return value
	}
	return DefaultIntentRouting()
}

func (cm *ConfigManager) UpdateIntentRouting(kind string, value IntentRoutingConfig) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	candidate := cloneConfig(cm.config)
	previous := candidate.IntentRouting[kind]
	value.SessionEpoch = previous.SessionEpoch
	if previous.Enabled != value.Enabled {
		value.SessionEpoch++
	}
	if candidate.IntentRouting == nil {
		candidate.IntentRouting = make(map[string]IntentRoutingConfig)
	}
	candidate.IntentRouting[kind] = cloneIntentRouting(map[string]IntentRoutingConfig{kind: value})[kind]
	return cm.saveConfigLocked(candidate)
}

func validateIntentRouting(candidate *Config) error {
	for kind, value := range candidate.IntentRouting {
		if !SupportsIntentRouting(kind) {
			return fmt.Errorf("%s 不支持意图匹配", kind)
		}
		cs, err := candidate.channelSetFor(kind)
		if err != nil {
			return err
		}
		if err := ValidateIntentRouting(value, *cs.pools); err != nil {
			return fmt.Errorf("%s 意图匹配配置无效: %w", kind, err)
		}
		if value.EmbeddingChannel != "" && !slices.ContainsFunc(candidate.EmbeddingsUpstream, func(up UpstreamConfig) bool { return up.ID == value.EmbeddingChannel }) {
			return fmt.Errorf("%s 意图配置引用的向量渠道不存在，删除渠道前请解除引用", kind)
		}
		if value.ClassifierChannel != "" {
			found := false
			for _, group := range [][]UpstreamConfig{candidate.Upstream, candidate.ResponsesUpstream, candidate.ChatUpstream, candidate.GeminiUpstream, candidate.DecisionsUpstream} {
				if slices.ContainsFunc(group, func(up UpstreamConfig) bool { return up.ID == value.ClassifierChannel }) {
					found = true
				}
			}
			if !found {
				return fmt.Errorf("%s 意图配置引用的分类渠道不存在，删除渠道前请解除引用", kind)
			}
		}
	}
	return nil
}

func ValidateIntentRouting(value IntentRoutingConfig, pools []ChannelPool) error {
	if err := ValidateDatasetIDs(value.DatasetIDs); err != nil {
		return err
	}
	if len(value.EmbeddingVersion) > 128 {
		return fmt.Errorf("向量模型版本不能超过 128 字节")
	}
	if !slices.Contains([]string{"auto", "regex", "semantic", "complexity", "adaptive"}, value.Strategy) {
		return fmt.Errorf("不支持的策略 %q", value.Strategy)
	}
	if !slices.Contains([]string{"heuristic", "llm", "decisions", "systemone", "hybrid"}, value.Classifier) {
		return fmt.Errorf("不支持的分类器 %q", value.Classifier)
	}
	if !slices.Contains([]string{"remote", "local"}, value.EmbeddingSource) {
		return fmt.Errorf("向量来源必须为 remote 或 local")
	}
	if !slices.Contains([]string{"original", "error", "fallback"}, value.Unmatched) {
		return fmt.Errorf("未命中处理无效")
	}
	if !slices.Contains([]string{"error", "keep"}, value.FailurePolicy) {
		return fmt.Errorf("分类失败处理无效")
	}
	if value.TimeoutMS < 100 || value.TimeoutMS > 30000 {
		return fmt.Errorf("判断超时须为 100–30000 毫秒")
	}
	if value.MinResidenceSeconds < 30 || value.MinResidenceSeconds > 86400 || value.MinTurns < 1 || value.MinTurns > 100 {
		return fmt.Errorf("最短驻留须为 30–86400 秒，最少轮次须为 1–100")
	}
	for _, n := range []float64{value.Threshold, value.SwitchMargin} {
		if math.IsNaN(n) || math.IsInf(n, 0) || n < 0 || n > 1 {
			return fmt.Errorf("阈值和切换收益须在 0–1 之间")
		}
	}
	validPool := func(id string) bool {
		return slices.ContainsFunc(pools, func(p ChannelPool) bool { return p.ID == id })
	}
	if err := validateIntentGroups(value, validPool); err != nil {
		return err
	}
	if value.Unmatched == "fallback" && !validPool(value.FallbackPoolID) {
		return fmt.Errorf("未命中目标分组不存在")
	}
	if len(value.Routes) > 100 || (value.Enabled && value.Strategy != "auto" && len(value.Routes) == 0) {
		return fmt.Errorf("启用时需配置 1–100 条规则")
	}
	if len(value.QueryPrefix) > 512 || len(value.ExamplePrefix) > 512 {
		return fmt.Errorf("向量输入前缀不能超过 512 字节")
	}
	ids := make(map[string]bool)
	examples := 0
	for _, route := range value.Routes {
		if route.Label != "" && !semanticID.MatchString(route.Label) {
			return fmt.Errorf("数据集类别标识无效: %q", route.Label)
		}
		if !regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`).MatchString(route.ID) || ids[route.ID] || route.ID == "unmatched" {
			return fmt.Errorf("规则 ID 无效、重复或使用保留名 unmatched: %q", route.ID)
		}
		ids[route.ID] = true
		if !validPool(route.PoolID) {
			return fmt.Errorf("规则 %s 引用的分组 %q 不存在，删除分组前请解除引用", route.ID, route.PoolID)
		}
		if route.Tier < 0 || route.Tier > 2 || route.Cost < 0 || math.IsNaN(route.Cost) || math.IsInf(route.Cost, 0) {
			return fmt.Errorf("规则 %s 的等级或成本无效", route.ID)
		}
		if len(route.Description) > 2000 || len(route.Patterns) > 32 {
			return fmt.Errorf("规则 %s 的描述或正则数量超限", route.ID)
		}
		for _, pattern := range route.Patterns {
			if len(pattern) > 2048 || strings.TrimSpace(pattern) == "" {
				return fmt.Errorf("规则 %s 的正则为空或过长", route.ID)
			}
			if _, err := regexp.Compile(pattern); err != nil {
				return fmt.Errorf("规则 %s 正则无效: %w", route.ID, err)
			}
		}
		for _, example := range route.Examples {
			if strings.TrimSpace(example) == "" || len(example) > 4096 {
				return fmt.Errorf("规则 %s 的例句为空或过长", route.ID)
			}
		}
		examples += len(route.Examples)
	}
	if examples > 1000 {
		return fmt.Errorf("例句总数不能超过 1000")
	}
	if value.Enabled && value.Strategy == "semantic" && ((examples == 0 && len(value.DatasetIDs) == 0) || strings.TrimSpace(value.EmbeddingModel) == "") {
		return fmt.Errorf("语义匹配需要例句和向量模型")
	}
	if value.Enabled && value.Strategy == "semantic" && value.EmbeddingSource == "remote" && value.EmbeddingChannel == "" {
		return fmt.Errorf("请选择辅助 Embeddings 渠道")
	}
	if value.Enabled && (value.Strategy == "auto" || value.Strategy == "complexity" || value.Strategy == "adaptive") && value.Classifier != "heuristic" && strings.TrimSpace(value.ClassifierModel) == "" {
		return fmt.Errorf("远程分类需要指定分类模型")
	}
	return nil
}
