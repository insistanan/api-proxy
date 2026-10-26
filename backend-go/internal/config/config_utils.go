package config

import (
	"errors"
	"fmt"
	"log"
	"maps"
	"sort"
	"strings"
	"time"

	"github.com/BenedictKing/api-proxy/internal/atomicfile"
)

// ============== 工具函数 ==============

const (
	ChannelStatusActive     = "active"
	ChannelStatusSuspended  = "suspended"
	ChannelStatusDisabled   = "disabled"
	ChannelStatusDeprecated = "deprecated"
	ChannelStatusDeleted    = "deleted"

	temporaryChannelTTL       = 24 * time.Hour
	deprecatedChannelTTL      = 72 * time.Hour
	channelLifecycleTick      = time.Hour
	deprecatedCleanupInterval = 12 * time.Hour
)

// deduplicateStrings 去重字符串切片，保持原始顺序
func deduplicateStrings(items []string) []string {
	if len(items) <= 1 {
		return items
	}
	seen := make(map[string]struct{}, len(items))
	result := make([]string, 0, len(items))
	for _, item := range items {
		if _, exists := seen[item]; !exists {
			seen[item] = struct{}{}
			result = append(result, item)
		}
	}
	return result
}

// deduplicateBaseURLs 去重 BaseURLs，忽略末尾 / 和 # 差异
func deduplicateBaseURLs(urls []string) []string {
	if len(urls) <= 1 {
		return urls
	}
	seen := make(map[string]struct{}, len(urls))
	result := make([]string, 0, len(urls))
	for _, url := range urls {
		normalized := strings.TrimRight(url, "/#")
		if _, exists := seen[normalized]; !exists {
			seen[normalized] = struct{}{}
			result = append(result, url)
		}
	}
	return result
}

// validateLoadBalanceStrategy 验证负载均衡策略
func validateLoadBalanceStrategy(strategy string) error {
	if strategy != "failover" {
		return &ConfigError{Message: "无效的负载均衡策略: " + strategy}
	}
	return nil
}

// ConfigError 配置错误
type ConfigError struct {
	Message string
}

func (e *ConfigError) Error() string {
	return e.Message
}

// IsConfigError 判断错误链中是否包含配置校验错误。
func IsConfigError(err error) bool {
	var target *ConfigError
	return errors.As(err, &target)
}

// IsCommittedError 判断配置提交错误是否已过提交点（文件已完成原子替换，
// 内存快照与磁盘一致）。调用方（管理 API、助手动作等）不得把该错误按
// "未提交"重试——一次新增会被重试成两次；应读取实际状态并向用户表达
// "已提交但收尾失败"（R3）。
func IsCommittedError(err error) bool {
	return atomicfile.IsCommitted(err)
}

// ============== 模型重定向 ==============

// redirectModelList 模型重定向（返回模型列表，支持多个备选）
// 返回：[]string 重定向后的模型列表（如果没有映射则返回包含原模型的列表）
//
// 模型名按客户端发来的原样参与匹配，不对任何形式的后缀做特殊处理：
// 需要区分同一别名的不同变体时，把完整名字（含变体标记）直接配成 ModelMapping 的键。
func redirectModelList(model string, upstream *UpstreamConfig) []string {
	_, targets := matchModelMapping(model, upstream)
	return targets
}

// matchModelMapping 按模型重定向规则匹配请求模型，返回命中的源模型键与目标模型列表。
// 未命中任何规则时源模型键为空串，目标列表为原模型（空模型返回 nil）。
// 与 ModelMappingChannels 的查找共用同一套匹配逻辑，保证"目标模型"与"目标渠道"
// 始终来自同一条规则。
func matchModelMapping(model string, upstream *UpstreamConfig) (string, []string) {
	model = strings.TrimSpace(model)
	// "*" 是任意源模型规则：配置后所有模型请求都使用它的目标模型列表。
	// 必须排在空模型早退之前——内部调用自动模式（空模型）下，只配 "*" 兜底
	// 映射的透传渠道也要能解析出模型，否则会被误判"解析不出模型"而整体跳过。
	if mapped, ok := upstream.ModelMapping["*"]; ok && len(mapped) > 0 {
		return "*", mapped
	}
	if model == "" {
		return "", nil
	}
	if len(upstream.ModelMapping) == 0 {
		return "", []string{model}
	}

	// 1. 精确匹配模型名
	if mapped, ok := upstream.ModelMapping[model]; ok && len(mapped) > 0 {
		return model, mapped
	}

	// 2. 模糊匹配：按源模型长度从长到短排序，确保最长匹配优先
	// 例如：同时配置 "codex" 和 "gpt-5.1-codex" 时，"gpt-5.1-codex" 应该先匹配
	type mapping struct {
		source string
		target []string
	}
	mappings := make([]mapping, 0, len(upstream.ModelMapping))
	for source, target := range upstream.ModelMapping {
		mappings = append(mappings, mapping{source, target})
	}
	// 按源模型长度降序排序；长度相同按字典序稳定处理，避免 Go map 遍历导致请求间结果漂移。
	sort.Slice(mappings, func(i, j int) bool {
		if len(mappings[i].source) != len(mappings[j].source) {
			return len(mappings[i].source) > len(mappings[j].source)
		}
		return mappings[i].source < mappings[j].source
	})

	for _, m := range mappings {
		if strings.Contains(model, m.source) || strings.Contains(m.source, model) {
			if len(m.target) > 0 {
				return m.source, m.target
			}
		}
	}

	return "", []string{model}
}

// ResolveUpstreamModelTargets 解析模型的目标列表，并为每个目标给出显式指定的目标渠道稳定 ID。
// 返回的两个切片等长：channelIDs[i] 为空串表示该目标沿用本渠道。
// DefaultModel 生效时整体覆盖映射，此时不存在按目标指定渠道的语义，全部返回空串。
func ResolveUpstreamModelTargets(model string, upstream *UpstreamConfig) ([]string, []string) {
	if upstream == nil {
		return []string{strings.TrimSpace(model)}, []string{""}
	}
	if strings.TrimSpace(upstream.DefaultModel) != "" {
		return []string{strings.TrimSpace(upstream.DefaultModel)}, []string{""}
	}
	source, targets := matchModelMapping(model, upstream)
	channelIDs := make([]string, len(targets))
	if source == "" || len(upstream.ModelMappingChannels) == 0 {
		return targets, channelIDs
	}
	perSource := upstream.ModelMappingChannels[source]
	for i, target := range targets {
		channelIDs[i] = perSource[target]
	}
	return targets, channelIDs
}

// redirectModel 模型重定向（返回第一个匹配的模型）
func redirectModel(model string, upstream *UpstreamConfig) string {
	models := redirectModelList(model, upstream)
	if len(models) > 0 {
		return models[0]
	}
	return model
}

func ResolveUpstreamModel(model string, upstream *UpstreamConfig) string {
	model = strings.TrimSpace(model)

	if upstream == nil {
		return model
	}
	if strings.TrimSpace(upstream.DefaultModel) != "" {
		return strings.TrimSpace(upstream.DefaultModel)
	}

	return redirectModel(model, upstream)
}

// ResolveUpstreamModelList 解析上游模型列表（支持多个备选）
func ResolveUpstreamModelList(model string, upstream *UpstreamConfig) []string {
	model = strings.TrimSpace(model)

	if upstream == nil {
		return []string{model}
	}
	if strings.TrimSpace(upstream.DefaultModel) != "" {
		return []string{strings.TrimSpace(upstream.DefaultModel)}
	}

	return redirectModelList(model, upstream)
}

// ============== 渠道状态与优先级辅助函数 ==============

// GetChannelStatus 获取渠道状态（带默认值处理）
func GetChannelStatus(upstream *UpstreamConfig) string {
	if upstream.Status == "" {
		return "active"
	}
	return upstream.Status
}

// GetChannelPriority 获取渠道优先级（带默认值处理）
func GetChannelPriority(upstream *UpstreamConfig, index int) int {
	if upstream.Priority == 0 {
		return index + 1
	}
	return upstream.Priority
}

func IsChannelSchedulable(upstream *UpstreamConfig) bool {
	status := GetChannelStatus(upstream)
	return status != ChannelStatusDisabled && status != ChannelStatusDeprecated && status != ChannelStatusDeleted
}

// IsChannelInPromotion 检查渠道是否处于促销期
func IsChannelInPromotion(upstream *UpstreamConfig) bool {
	if upstream.PromotionUntil != nil && time.Now().Before(*upstream.PromotionUntil) {
		return true
	}
	if upstream.PromotionCount > 0 {
		return true
	}
	return false
}

func normalizeChannelStatus(status string) (string, error) {
	status = strings.ToLower(strings.TrimSpace(status))
	switch status {
	case "", ChannelStatusActive:
		return ChannelStatusActive, nil
	case ChannelStatusSuspended, ChannelStatusDisabled, ChannelStatusDeprecated, ChannelStatusDeleted:
		return status, nil
	default:
		return "", fmt.Errorf("无效的状态: %s (允许值: active, suspended, disabled, deprecated)", status)
	}
}

func prepareUpstreamLifecycle(upstream *UpstreamConfig, now time.Time) {
	if upstream == nil {
		return
	}
	if upstream.Status == "" {
		upstream.Status = ChannelStatusActive
	}
	if upstream.Status == ChannelStatusDeprecated {
		upstream.Temporary = false
		upstream.TemporaryUntil = nil
		if upstream.DeprecatedAt == nil {
			deprecatedAt := now
			upstream.DeprecatedAt = &deprecatedAt
		}
		return
	}
	if upstream.Status == ChannelStatusDeleted {
		upstream.Temporary = false
		upstream.TemporaryUntil = nil
		upstream.PromotionUntil = nil
		upstream.PromotionCount = 0
		return
	}
	if upstream.Temporary {
		if upstream.TemporaryUntil == nil {
			until := now.Add(temporaryChannelTTL)
			upstream.TemporaryUntil = &until
		}
	} else {
		upstream.TemporaryUntil = nil
	}
	if upstream.Status != ChannelStatusDeprecated {
		upstream.DeprecatedAt = nil
	}
}

func setUpstreamStatus(upstream *UpstreamConfig, status string, now time.Time) error {
	normalized, err := normalizeChannelStatus(status)
	if err != nil {
		return err
	}
	upstream.Status = normalized
	if normalized == ChannelStatusDeprecated {
		upstream.Temporary = false
		upstream.TemporaryUntil = nil
		if upstream.DeprecatedAt == nil {
			deprecatedAt := now
			upstream.DeprecatedAt = &deprecatedAt
		}
	} else {
		upstream.DeprecatedAt = nil
	}
	if normalized == ChannelStatusSuspended || normalized == ChannelStatusDeprecated || normalized == ChannelStatusDeleted {
		upstream.PromotionUntil = nil
		upstream.PromotionCount = 0
	}
	prepareUpstreamLifecycle(upstream, now)
	return nil
}

func reorderProblemChannelsStable(upstreams []UpstreamConfig) bool {
	ordered := make([]int, len(upstreams))
	for index := range upstreams {
		ordered[index] = index
	}
	sort.SliceStable(ordered, func(i, j int) bool {
		left, right := ordered[i], ordered[j]
		leftScope := upstreamPriorityScope(&upstreams[left])
		rightScope := upstreamPriorityScope(&upstreams[right])
		if leftScope != rightScope {
			return leftScope < rightScope
		}
		leftPool := channelStatusPool(&upstreams[left])
		rightPool := channelStatusPool(&upstreams[right])
		if leftPool != rightPool {
			return leftPool < rightPool
		}
		leftPriority := GetChannelPriority(&upstreams[left], left)
		rightPriority := GetChannelPriority(&upstreams[right], right)
		if leftPriority != rightPriority {
			return leftPriority < rightPriority
		}
		return left < right
	})
	changed := false
	poolPriorities := make(map[string]int)
	for _, index := range ordered {
		scope := upstreamPriorityScope(&upstreams[index])
		poolPriorities[scope]++
		wanted := poolPriorities[scope]
		if upstreams[index].Priority != wanted {
			changed = true
			upstreams[index].Priority = wanted
		}
	}
	return changed
}

func insertClonedUpstream(upstreams []UpstreamConfig, index int, now time.Time) ([]UpstreamConfig, error) {
	if index < 0 || index >= len(upstreams) {
		return nil, fmt.Errorf("无效的上游索引: %d", index)
	}
	clone := *upstreams[index].Clone()
	clone.ID = newUpstreamID()
	clone.Name = clone.Name + " - 副本"
	clone.PromotionUntil = nil
	clone.PromotionCount = 0
	if clone.Status == ChannelStatusDeprecated {
		clone.Status = ChannelStatusDisabled
		clone.DeprecatedAt = nil
	}
	prepareUpstreamLifecycle(&clone, now)

	normalizeUpstreamPriorities(upstreams)
	targetPriority := GetChannelPriority(&upstreams[index], index)
	clone.Priority = targetPriority + 1
	next := append([]UpstreamConfig(nil), upstreams...)
	for i := range next {
		if upstreamPriorityScope(&next[i]) == upstreamPriorityScope(&clone) && next[i].Priority > targetPriority {
			next[i].Priority++
		}
	}
	next = append(next, clone)
	return next, nil
}

func sameTimePtr(a *time.Time, b *time.Time) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Equal(*b)
}

func lifecycleMetadataChanged(before UpstreamConfig, after UpstreamConfig) bool {
	return before.Status != after.Status ||
		before.Temporary != after.Temporary ||
		!sameTimePtr(before.TemporaryUntil, after.TemporaryUntil) ||
		!sameTimePtr(before.DeprecatedAt, after.DeprecatedAt)
}

func applyLifecycleToUpstreams(upstreams []UpstreamConfig, now time.Time, cleanupDeprecated bool, label string) ([]UpstreamConfig, bool) {
	changed := false
	next := make([]UpstreamConfig, 0, len(upstreams))
	poolChanged := make([]int, 0)
	for i := range upstreams {
		upstream := upstreams[i]
		before := upstream
		previousPool := channelStatusPool(&before)
		prepareUpstreamLifecycle(&upstream, now)
		if lifecycleMetadataChanged(before, upstream) {
			changed = true
		}
		if upstream.Temporary && upstream.TemporaryUntil != nil && !now.Before(*upstream.TemporaryUntil) {
			upstream.Status = ChannelStatusDeprecated
			upstream.Temporary = false
			upstream.TemporaryUntil = nil
			deprecatedAt := now
			upstream.DeprecatedAt = &deprecatedAt
			changed = true
			log.Printf("[Config-Lifecycle] %s 渠道 [%d] %s 临时期结束，已移入弃用池", label, i, upstream.Name)
		}
		if cleanupDeprecated && upstream.Status == ChannelStatusDeprecated && upstream.DeprecatedAt != nil && now.Sub(*upstream.DeprecatedAt) > deprecatedChannelTTL {
			upstream.Status = ChannelStatusDeleted
			upstream.Temporary = false
			upstream.TemporaryUntil = nil
			upstream.PromotionUntil = nil
			upstream.PromotionCount = 0
			changed = true
			log.Printf("[Config-Lifecycle] %s 渠道 [%d] %s 已在弃用池超过 3 天，自动清理为删除占位", label, i, upstream.Name)
		}
		next = append(next, upstream)
		if previousPool != channelStatusPool(&upstream) {
			poolChanged = append(poolChanged, i)
		}
	}
	for _, index := range poolChanged {
		moveChannelToStatusPoolTail(next, index)
	}
	return next, changed
}

// ============== UpstreamConfig 方法 ==============

// Clone 深拷贝 UpstreamConfig（用于避免并发修改问题）
// 在多 BaseURL failover 场景下，需要临时修改 BaseURL 字段，
// 使用深拷贝可避免并发请求之间的竞态条件
func (u *UpstreamConfig) Clone() *UpstreamConfig {
	cloned := *u // 浅拷贝

	// 深拷贝切片字段
	if u.BaseURLs != nil {
		cloned.BaseURLs = make([]string, len(u.BaseURLs))
		copy(cloned.BaseURLs, u.BaseURLs)
	}
	if u.APIKeys != nil {
		cloned.APIKeys = make([]string, len(u.APIKeys))
		copy(cloned.APIKeys, u.APIKeys)
	}
	if u.HistoricalAPIKeys != nil {
		cloned.HistoricalAPIKeys = make([]string, len(u.HistoricalAPIKeys))
		copy(cloned.HistoricalAPIKeys, u.HistoricalAPIKeys)
	}
	if u.ModelMapping != nil {
		cloned.ModelMapping = make(map[string][]string, len(u.ModelMapping))
		for k, v := range u.ModelMapping {
			cloned.ModelMapping[k] = make([]string, len(v))
			copy(cloned.ModelMapping[k], v)
		}
	}
	if u.ModelMappingChannels != nil {
		cloned.ModelMappingChannels = make(map[string]map[string]string, len(u.ModelMappingChannels))
		for source, targets := range u.ModelMappingChannels {
			cloned.ModelMappingChannels[source] = maps.Clone(targets)
		}
	}
	if u.ImagesExtraParams != nil {
		cloned.ImagesExtraParams = maps.Clone(u.ImagesExtraParams)
	}
	if u.PromotionUntil != nil {
		t := *u.PromotionUntil
		cloned.PromotionUntil = &t
	}
	if u.TemporaryUntil != nil {
		t := *u.TemporaryUntil
		cloned.TemporaryUntil = &t
	}
	if u.DeprecatedAt != nil {
		t := *u.DeprecatedAt
		cloned.DeprecatedAt = &t
	}
	return &cloned
}

// GetEffectiveBaseURL 获取当前应使用的 BaseURL（纯 failover 模式）
// 优先使用 BaseURL 字段（支持调用方临时覆盖），否则从 BaseURLs 数组获取
func (u *UpstreamConfig) GetEffectiveBaseURL() string {
	// 优先使用 BaseURL（可能被调用方临时设置用于指定本次请求的 URL）
	if u.BaseURL != "" {
		return u.BaseURL
	}

	// 回退到 BaseURLs 数组
	if len(u.BaseURLs) > 0 {
		return u.BaseURLs[0]
	}

	return ""
}

// GetAllBaseURLs 获取所有 BaseURL（用于延迟测试）
func (u *UpstreamConfig) GetAllBaseURLs() []string {
	if len(u.BaseURLs) > 0 {
		return u.BaseURLs
	}
	if u.BaseURL != "" {
		return []string{u.BaseURL}
	}
	return nil
}
