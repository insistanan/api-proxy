package config

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/insistanan/api-proxy/internal/orchestration/upstreamcontrol"
)

// AddedUpstream 记录新增渠道的稳定标识和当前位置。
type AddedUpstream struct {
	ID    string
	Index int
}

// MarkPromptCacheKeyUnsupported 记录渠道不支持 OpenAI prompt_cache_key。
// channelType 使用内部渠道类型：messages、responses、gemini、chat、images。
// 调用方已经从上游的明确参数校验错误中确认该能力，故需要立即持久化，避免后续请求重复探测。
func (cm *ConfigManager) MarkPromptCacheKeyUnsupported(channelType, upstreamID string) error {
	if strings.TrimSpace(upstreamID) == "" {
		return fmt.Errorf("无法记录缓存键能力：渠道 ID 为空")
	}

	cm.mu.Lock()
	defer cm.mu.Unlock()

	candidate := cloneConfig(cm.config)
	cs, err := candidate.channelSetFor(channelType)
	if err != nil {
		return err
	}
	upstreams := *cs.upstreams

	for index := range upstreams {
		if upstreams[index].ID != upstreamID {
			continue
		}
		if upstreams[index].DisablePromptCacheKey {
			return nil
		}

		upstreams[index].DisablePromptCacheKey = true
		if err := cm.saveConfigLocked(candidate); err != nil {
			return fmt.Errorf("保存渠道缓存键能力失败: %w", err)
		}
		log.Printf("[Config-ChannelCapability] 渠道 %s (%s) 不支持 prompt_cache_key，已自动禁用", upstreams[index].Name, upstreamID)
		return nil
	}

	return fmt.Errorf("未找到渠道 ID: %s", upstreamID)
}

// MarkReasoningContentRequired 记录渠道会拒绝缺少 reasoning_content 的 assistant 历史。
// 该能力只在收到上游明确的字段校验错误后启用；后续 Messages/Responses 请求
// 在转换阶段校验原始推理，无法恢复时明确拒绝，不能用占位思考续接。
func (cm *ConfigManager) MarkReasoningContentRequired(channelType, upstreamID string) error {
	if strings.TrimSpace(upstreamID) == "" {
		return fmt.Errorf("无法记录推理字段能力：渠道 ID 为空")
	}

	cm.mu.Lock()
	defer cm.mu.Unlock()

	candidate := cloneConfig(cm.config)
	cs, err := candidate.channelSetFor(channelType)
	if err != nil {
		return err
	}
	upstreams := *cs.upstreams

	for index := range upstreams {
		if upstreams[index].ID != upstreamID {
			continue
		}
		if upstreams[index].RequireReasoningContent {
			return nil
		}

		upstreams[index].RequireReasoningContent = true
		if err := cm.saveConfigLocked(candidate); err != nil {
			return fmt.Errorf("保存渠道推理字段能力失败: %w", err)
		}
		log.Printf("[Config-ChannelCapability] 渠道 %s (%s) 要求原始 reasoning_content，已自动启用严格历史校验", upstreams[index].Name, upstreamID)
		return nil
	}

	return fmt.Errorf("未找到渠道 ID: %s", upstreamID)
}

// getFirstActive 从上游列表中选择第一个 active 且可调度的渠道
func getFirstActive(upstreams []UpstreamConfig, label string) (*UpstreamConfig, error) {
	if len(upstreams) == 0 {
		return nil, fmt.Errorf("未配置任何%s渠道", label)
	}
	if index := bestUpstreamIndex(upstreams, true); index >= 0 {
		return &upstreams[index], nil
	}
	if index := bestUpstreamIndex(upstreams, false); index >= 0 {
		return &upstreams[index], nil
	}
	return nil, fmt.Errorf("没有可用的%s渠道", label)
}

// getFirstActiveWithIndex 从上游列表中选择第一个 active 且可调度的渠道（带索引）
func getFirstActiveWithIndex(upstreams []UpstreamConfig, label string) (*UpstreamConfig, int, error) {
	if len(upstreams) == 0 {
		return nil, -1, fmt.Errorf("未配置任何%s渠道", label)
	}
	if index := bestUpstreamIndex(upstreams, true); index >= 0 {
		return &upstreams[index], index, nil
	}
	if index := bestUpstreamIndex(upstreams, false); index >= 0 {
		return &upstreams[index], index, nil
	}
	return nil, -1, fmt.Errorf("没有可用的%s渠道", label)
}

func bestUpstreamIndex(upstreams []UpstreamConfig, activeOnly bool) int {
	bestIndex := -1
	bestPriority := 0
	for index := range upstreams {
		upstream := &upstreams[index]
		if !IsChannelSchedulable(upstream) || upstream.ExcludeFromConversation || upstream.BuiltinProvider != "" {
			continue
		}
		if activeOnly && GetChannelStatus(upstream) != ChannelStatusActive {
			continue
		}
		priority := GetChannelPriority(upstream, index)
		if bestIndex < 0 || priority < bestPriority || (priority == bestPriority && index < bestIndex) {
			bestIndex = index
			bestPriority = priority
		}
	}
	return bestIndex
}

// prepareNewUpstream 准备新增渠道的公共字段
func prepareNewUpstream(upstream *UpstreamConfig) {
	if upstream.MaxConcurrency == 0 {
		upstream.MaxConcurrency = upstreamcontrol.DefaultConcurrency
	}
	if strings.TrimSpace(upstream.ID) == "" {
		upstream.ID = newUpstreamID()
	}
	if upstream.Status == "" {
		upstream.Status = "active"
	}
	if strings.TrimSpace(upstream.PoolID) == "" {
		upstream.PoolID = DefaultChannelPoolID
	}
	prepareUpstreamLifecycle(upstream, time.Now())
	upstream.APIKeys = deduplicateStrings(upstream.APIKeys)
	upstream.BaseURLs = deduplicateBaseURLs(upstream.BaseURLs, upstream.UseFullURL)
	upstream.ModelMapping = normalizeModelMapping(upstream.ModelMapping)
	upstream.ModelMappingChannels = normalizeModelMappingChannels(upstream.ModelMappingChannels, upstream.ModelMapping)
	normalizeUpstreamProxyConfig(upstream)
}

func normalizeModelMapping(mapping map[string][]string) map[string][]string {
	if mapping == nil {
		return nil
	}

	normalized := make(map[string][]string, len(mapping))
	rawSources := make([]string, 0, len(mapping))
	for source := range mapping {
		rawSources = append(rawSources, source)
	}
	sort.Strings(rawSources)
	for _, rawSource := range rawSources {
		source := strings.TrimSpace(rawSource)
		if source == "" {
			continue
		}
		targets := mapping[rawSource]
		seen := make(map[string]struct{}, len(targets)+len(normalized[source]))
		for _, target := range normalized[source] {
			seen[target] = struct{}{}
		}
		for _, target := range targets {
			target = strings.TrimSpace(target)
			if target == "" {
				continue
			}
			if _, exists := seen[target]; exists {
				continue
			}
			seen[target] = struct{}{}
			normalized[source] = append(normalized[source], target)
		}
		if len(normalized[source]) == 0 {
			delete(normalized, source)
		}
	}
	return normalized
}

func newUpstreamID() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err == nil {
		return "ch_" + hex.EncodeToString(buf)
	}
	// crypto/rand 在受支持的平台上不应失败；这里保留一个可追踪的降级值，避免新增渠道没有身份标识。
	return fmt.Sprintf("ch_%d", time.Now().UnixNano())
}

func upstreamPriorityScope(upstream *UpstreamConfig) string {
	if upstream.ExcludeFromConversation {
		return "\x00public-vision"
	}
	poolID := strings.TrimSpace(upstream.PoolID)
	if poolID == "" {
		return DefaultChannelPoolID
	}
	return poolID
}

// normalizeUpstreamPriorities 为每个渠道分组分别建立连续优先级。
func normalizeUpstreamPriorities(upstreams []UpstreamConfig) bool {
	if len(upstreams) == 0 {
		return false
	}

	order := make([]int, len(upstreams))
	for i := range upstreams {
		order[i] = i
	}
	sort.SliceStable(order, func(i, j int) bool {
		left, right := order[i], order[j]
		leftPriority := upstreams[left].Priority
		rightPriority := upstreams[right].Priority
		if leftPriority <= 0 {
			leftPriority = left + 1
		}
		if rightPriority <= 0 {
			rightPriority = right + 1
		}
		if leftPriority != rightPriority {
			return leftPriority < rightPriority
		}
		return left < right
	})

	changed := false
	poolPriorities := make(map[string]int)
	for _, index := range order {
		scope := upstreamPriorityScope(&upstreams[index])
		poolPriorities[scope]++
		wanted := poolPriorities[scope]
		if upstreams[index].Priority != wanted {
			upstreams[index].Priority = wanted
			changed = true
		}
	}
	return changed
}

func ensureUpstreamIDs(upstreams []UpstreamConfig) bool {
	seen := make(map[string]struct{}, len(upstreams))
	changed := false
	for i := range upstreams {
		id := strings.TrimSpace(upstreams[i].ID)
		if id == "" {
			id = newUpstreamID()
		} else if _, exists := seen[id]; exists {
			id = newUpstreamID()
		}
		if upstreams[i].ID != id {
			upstreams[i].ID = id
			changed = true
		}
		seen[id] = struct{}{}
	}
	return changed
}

// addUpstreamOp 新增渠道但不改变已有渠道的数组位置。
// 位置索引仍被会话、日志和 URL 健康状态使用，新增只能通过优先级置顶。
func addUpstreamOp(upstreams []UpstreamConfig, upstream UpstreamConfig) ([]UpstreamConfig, AddedUpstream) {
	prepareNewUpstream(&upstream)
	normalizeUpstreamPriorities(upstreams)
	for i := range upstreams {
		if upstreamPriorityScope(&upstreams[i]) == upstreamPriorityScope(&upstream) {
			upstreams[i].Priority++
		}
	}
	upstream.Priority = 1
	upstreams = append(upstreams, upstream)
	return upstreams, AddedUpstream{ID: upstream.ID, Index: len(upstreams) - 1}
}

func addValidatedUpstreamOp(upstreams []UpstreamConfig, pools []ChannelPool, upstream UpstreamConfig) ([]UpstreamConfig, AddedUpstream, error) {
	candidate := cloneUpstreamList(upstreams)
	candidate, result := addUpstreamOp(candidate, upstream)
	if err := ValidateChannelPoolAssignment(pools, candidate[result.Index].PoolID); err != nil {
		return nil, AddedUpstream{}, err
	}
	if err := validateUpstreamProxyConfig(&candidate[result.Index]); err != nil {
		return nil, AddedUpstream{}, err
	}
	if err := validateAllVisionLayerConfigs(candidate); err != nil {
		return nil, AddedUpstream{}, err
	}
	if err := validateAllModelMappingChannels(candidate); err != nil {
		return nil, AddedUpstream{}, err
	}
	return candidate, result, nil
}

func cloneUpstreamList(upstreams []UpstreamConfig) []UpstreamConfig {
	if upstreams == nil {
		return nil
	}
	cloned := make([]UpstreamConfig, len(upstreams))
	for index := range upstreams {
		cloned[index] = *upstreams[index].Clone()
	}
	return cloned
}
