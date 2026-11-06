package config

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
)

const (
	DefaultChannelPoolID    = "default"
	FallbackChannelPoolName = "兜底分组"
)

// ChannelPool 是同一 API 类型内独立执行故障转移的渠道分组。
// ModelMatcher 为单个大小写无关的 contains 规则；default 分组固定作为兜底分组。
type ChannelPool struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	ModelMatcher string `json:"modelMatcher"`
	Priority     int    `json:"priority"`
}

type ChannelPoolUpdate struct {
	Name         *string `json:"name"`
	ModelMatcher *string `json:"modelMatcher"`
}

type ChannelPoolLayout struct {
	PoolID     string   `json:"poolId"`
	ChannelIDs []string `json:"channelIds"`
}

func defaultChannelPool() ChannelPool {
	return ChannelPool{ID: DefaultChannelPoolID, Name: FallbackChannelPoolName, ModelMatcher: "*", Priority: 1}
}

func normalizePoolMatcher(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func normalizeChannelPools(pools []ChannelPool) ([]ChannelPool, error) {
	result := make([]ChannelPool, 0, len(pools)+1)
	seenIDs := make(map[string]struct{}, len(pools)+1)
	seenNames := make(map[string]struct{}, len(pools)+1)
	seenMatchers := make(map[string]struct{}, len(pools)+1)
	hasDefault := false

	for _, pool := range pools {
		pool.ID = strings.TrimSpace(pool.ID)
		pool.Name = strings.TrimSpace(pool.Name)
		pool.ModelMatcher = normalizePoolMatcher(pool.ModelMatcher)
		if pool.ID == DefaultChannelPoolID {
			// default 是兼容旧配置的稳定 ID，其名称和规则固定为兜底语义。
			pool.Name = FallbackChannelPoolName
			pool.ModelMatcher = "*"
		} else if pool.ModelMatcher == "*" {
			return nil, fmt.Errorf("捕获规则 * 只能由兜底分组使用")
		}
		if pool.ID == "" || pool.Name == "" || pool.ModelMatcher == "" {
			return nil, fmt.Errorf("分组的名称和捕获规则不能为空")
		}
		if _, exists := seenIDs[pool.ID]; exists {
			return nil, fmt.Errorf("分组 ID 重复: %s", pool.ID)
		}
		nameKey := strings.ToLower(pool.Name)
		if _, exists := seenNames[nameKey]; exists {
			return nil, fmt.Errorf("分组名称重复: %s", pool.Name)
		}
		if _, exists := seenMatchers[pool.ModelMatcher]; exists {
			return nil, fmt.Errorf("分组捕获规则重复: %s", pool.ModelMatcher)
		}
		if pool.ID == DefaultChannelPoolID {
			hasDefault = true
		}
		seenIDs[pool.ID] = struct{}{}
		seenNames[nameKey] = struct{}{}
		seenMatchers[pool.ModelMatcher] = struct{}{}
		result = append(result, pool)
	}

	if !hasDefault {
		result = append(result, defaultChannelPool())
	}
	sort.SliceStable(result, func(i, j int) bool {
		if result[i].ID == DefaultChannelPoolID {
			return false
		}
		if result[j].ID == DefaultChannelPoolID {
			return true
		}
		if result[i].Priority != result[j].Priority {
			return result[i].Priority < result[j].Priority
		}
		return result[i].ID < result[j].ID
	})
	for index := range result {
		result[index].Priority = index + 1
	}
	return result, nil
}

// SelectChannelPoolRoute 返回本次请求的有序分组路由：最长规则命中的分组在前，
// 兜底分组在后；没有具体规则命中时仅返回兜底分组。
func SelectChannelPoolRoute(pools []ChannelPool, model string) ([]ChannelPool, error) {
	normalized, err := normalizeChannelPools(pools)
	if err != nil {
		return nil, err
	}
	model = strings.ToLower(strings.TrimSpace(model))
	var selected *ChannelPool
	var fallback *ChannelPool
	for index := range normalized {
		pool := &normalized[index]
		if pool.ID == DefaultChannelPoolID {
			fallback = pool
			continue
		}
		if !strings.Contains(model, pool.ModelMatcher) {
			continue
		}
		if selected == nil || len(pool.ModelMatcher) > len(selected.ModelMatcher) ||
			(len(pool.ModelMatcher) == len(selected.ModelMatcher) && pool.Priority < selected.Priority) {
			selected = pool
		}
	}
	if fallback == nil {
		return nil, fmt.Errorf("配置缺少兜底分组")
	}
	route := make([]ChannelPool, 0, 2)
	if selected != nil {
		route = append(route, *selected)
	}
	route = append(route, *fallback)
	return route, nil
}

// SelectChannelPool 返回本次请求首先进入的分组。
func SelectChannelPool(pools []ChannelPool, model string) (*ChannelPool, error) {
	route, err := SelectChannelPoolRoute(pools, model)
	if err != nil {
		return nil, err
	}
	selected := route[0]
	return &selected, nil
}

func newChannelPoolID() string {
	buf := make([]byte, 12)
	if _, err := rand.Read(buf); err == nil {
		return "pool_" + hex.EncodeToString(buf)
	}
	return "pool_fallback"
}

func (cm *ConfigManager) GetChannelPools(kind string) []ChannelPool {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	return append([]ChannelPool(nil), cm.channelPoolsLocked(kind)...)
}

func (cm *ConfigManager) channelPoolsLocked(kind string) []ChannelPool {
	cs, err := cm.config.channelSetFor(kind)
	if err != nil {
		return nil
	}
	return *cs.pools
}

func (cm *ConfigManager) CreateChannelPool(kind string, pool ChannelPool) (ChannelPool, error) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	candidate := cloneConfig(cm.config)
	cs, err := candidate.channelSetFor(kind)
	if err != nil {
		return ChannelPool{}, err
	}
	pool.ID = newChannelPoolID()
	pool.Priority = len(*cs.pools) + 1
	pools, err := normalizeChannelPools(append(*cs.pools, pool))
	if err != nil {
		return ChannelPool{}, err
	}
	*cs.pools = pools
	if err := cm.saveConfigLocked(candidate); err != nil {
		return ChannelPool{}, err
	}
	for _, item := range pools {
		if item.ID == pool.ID {
			return item, nil
		}
	}
	return ChannelPool{}, fmt.Errorf("创建分组失败")
}

func (cm *ConfigManager) UpdateChannelPool(kind string, id string, update ChannelPoolUpdate) error {
	if id == DefaultChannelPoolID {
		return fmt.Errorf("兜底分组的名称和捕获规则不能修改")
	}
	cm.mu.Lock()
	defer cm.mu.Unlock()
	candidate := cloneConfig(cm.config)
	cs, err := candidate.channelSetFor(kind)
	if err != nil {
		return err
	}
	pools := *cs.pools
	for index := range pools {
		if pools[index].ID != id {
			continue
		}
		if update.Name != nil {
			pools[index].Name = *update.Name
		}
		if update.ModelMatcher != nil {
			pools[index].ModelMatcher = *update.ModelMatcher
		}
		normalized, err := normalizeChannelPools(pools)
		if err != nil {
			return err
		}
		*cs.pools = normalized
		return cm.saveConfigLocked(candidate)
	}
	return fmt.Errorf("分组不存在")
}

// SaveChannelPoolLayout 原子更新渠道归属与各分组内部的故障转移顺序。
func (cm *ConfigManager) SaveChannelPoolLayout(kind string, layout []ChannelPoolLayout) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	candidate := cloneConfig(cm.config)
	cs, err := candidate.channelSetFor(kind)
	if err != nil {
		return err
	}
	pools := *cs.pools
	upstreams := *cs.upstreams

	poolLayouts := make(map[string][]string, len(layout))
	seenChannels := make(map[string]struct{})
	for _, item := range layout {
		poolID := strings.TrimSpace(item.PoolID)
		if !poolExists(pools, poolID) {
			return fmt.Errorf("指定的分组不存在: %s", poolID)
		}
		if _, exists := poolLayouts[poolID]; exists {
			return fmt.Errorf("分组布局重复: %s", poolID)
		}
		poolLayouts[poolID] = append([]string(nil), item.ChannelIDs...)
		for _, rawID := range item.ChannelIDs {
			channelID := strings.TrimSpace(rawID)
			if channelID == "" {
				return fmt.Errorf("渠道 ID 不能为空")
			}
			if _, exists := seenChannels[channelID]; exists {
				return fmt.Errorf("渠道在布局中重复: %s", channelID)
			}
			seenChannels[channelID] = struct{}{}
		}
	}
	if len(poolLayouts) != len(pools) {
		return fmt.Errorf("分组布局不完整: 收到 %d 个分组，实际需要 %d 个", len(poolLayouts), len(pools))
	}
	for _, pool := range pools {
		if _, exists := poolLayouts[pool.ID]; !exists {
			return fmt.Errorf("分组布局缺少: %s", pool.Name)
		}
	}

	channelIndexes := make(map[string]int, len(upstreams))
	for index := range upstreams {
		channelIndexes[upstreams[index].ID] = index
		status := GetChannelStatus(&upstreams[index])
		if !upstreams[index].ExcludeFromConversation && (status == ChannelStatusActive || status == ChannelStatusSuspended) {
			if _, exists := seenChannels[upstreams[index].ID]; !exists {
				return fmt.Errorf("分组布局缺少渠道: %s", upstreams[index].Name)
			}
		}
	}
	for poolID, channelIDs := range poolLayouts {
		for _, channelID := range channelIDs {
			index, exists := channelIndexes[strings.TrimSpace(channelID)]
			if !exists {
				return fmt.Errorf("渠道不存在: %s", channelID)
			}
			if upstreams[index].ExcludeFromConversation {
				return fmt.Errorf("公用图片理解渠道不能加入模型路由分组: %s", upstreams[index].Name)
			}
			upstreams[index].PoolID = poolID
		}
	}

	for poolID, channelIDs := range poolLayouts {
		priority := 1
		for _, channelID := range channelIDs {
			index := channelIndexes[strings.TrimSpace(channelID)]
			upstreams[index].Priority = priority
			priority++
		}
		remaining := make([]int, 0)
		for index := range upstreams {
			if upstreams[index].PoolID != poolID || upstreams[index].ExcludeFromConversation {
				continue
			}
			if _, explicitlyOrdered := seenChannels[upstreams[index].ID]; !explicitlyOrdered {
				remaining = append(remaining, index)
			}
		}
		sort.SliceStable(remaining, func(i, j int) bool {
			left, right := remaining[i], remaining[j]
			leftPriority := GetChannelPriority(&upstreams[left], left)
			rightPriority := GetChannelPriority(&upstreams[right], right)
			if leftPriority != rightPriority {
				return leftPriority < rightPriority
			}
			return left < right
		})
		for _, index := range remaining {
			upstreams[index].Priority = priority
			priority++
		}
	}

	if err := validateAllVisionLayerConfigs(upstreams); err != nil {
		return err
	}
	return cm.saveConfigLocked(candidate)
}

func (cm *ConfigManager) DeleteChannelPool(kind string, id string) error {
	if id == DefaultChannelPoolID {
		return fmt.Errorf("兜底分组不能删除")
	}
	cm.mu.Lock()
	defer cm.mu.Unlock()
	candidate := cloneConfig(cm.config)
	cs, err := candidate.channelSetFor(kind)
	if err != nil {
		return err
	}
	for _, upstream := range *cs.upstreams {
		if upstream.PoolID == id {
			return fmt.Errorf("分组仍包含渠道，无法删除")
		}
	}
	pools := *cs.pools
	filtered := make([]ChannelPool, 0, len(pools))
	found := false
	for _, pool := range pools {
		if pool.ID == id {
			found = true
			continue
		}
		filtered = append(filtered, pool)
	}
	if !found {
		return fmt.Errorf("分组不存在")
	}
	normalized, err := normalizeChannelPools(filtered)
	if err != nil {
		return err
	}
	*cs.pools = normalized
	return cm.saveConfigLocked(candidate)
}

func ensurePoolsAndAssignments(pools []ChannelPool, upstreams []UpstreamConfig) ([]ChannelPool, bool, error) {
	workingPools, assignmentSwap, migrated := migrateLegacyFallbackGroup(pools)
	normalized, err := normalizeChannelPools(workingPools)
	if err != nil {
		return nil, false, err
	}
	changed := migrated || !channelPoolsEqual(normalized, pools)
	if assignmentSwap != nil {
		for index := range upstreams {
			switch upstreams[index].PoolID {
			case "", assignmentSwap.legacyDefaultID:
				upstreams[index].PoolID = assignmentSwap.legacyWildcardID
			case assignmentSwap.legacyWildcardID:
				upstreams[index].PoolID = assignmentSwap.legacyDefaultID
			}
		}
	}
	validIDs := make(map[string]struct{}, len(normalized))
	for _, pool := range normalized {
		validIDs[pool.ID] = struct{}{}
	}
	for index := range upstreams {
		if _, exists := validIDs[upstreams[index].PoolID]; !exists {
			upstreams[index].PoolID = DefaultChannelPoolID
			changed = true
		}
	}
	return normalized, changed, nil
}

type legacyFallbackAssignmentSwap struct {
	legacyDefaultID  string
	legacyWildcardID string
}

// migrateLegacyFallbackGroup 兼容旧版允许“default 使用具体规则、其他分组使用 *”的配置。
// 通过交换两组的名称、规则和渠道归属，将稳定 ID default 恢复为兜底分组，
// 同时保持迁移前各模型实际命中的渠道集合不变。
func migrateLegacyFallbackGroup(pools []ChannelPool) ([]ChannelPool, *legacyFallbackAssignmentSwap, bool) {
	working := append([]ChannelPool(nil), pools...)
	defaultIndex := -1
	wildcardIndex := -1
	for index := range working {
		if strings.TrimSpace(working[index].ID) == DefaultChannelPoolID {
			defaultIndex = index
		}
		if strings.TrimSpace(working[index].ID) != DefaultChannelPoolID && normalizePoolMatcher(working[index].ModelMatcher) == "*" {
			wildcardIndex = index
		}
	}
	if defaultIndex < 0 || wildcardIndex < 0 || normalizePoolMatcher(working[defaultIndex].ModelMatcher) == "*" {
		return working, nil, false
	}

	legacyDefaultName := working[defaultIndex].Name
	legacyDefaultMatcher := working[defaultIndex].ModelMatcher
	working[defaultIndex].Name = FallbackChannelPoolName
	working[defaultIndex].ModelMatcher = "*"
	working[wildcardIndex].Name = legacyDefaultName
	working[wildcardIndex].ModelMatcher = legacyDefaultMatcher

	return working, &legacyFallbackAssignmentSwap{
		legacyDefaultID:  DefaultChannelPoolID,
		legacyWildcardID: strings.TrimSpace(working[wildcardIndex].ID),
	}, true
}

func channelPoolsEqual(left []ChannelPool, right []ChannelPool) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func getFirstActiveWithIndexForModel(upstreams []UpstreamConfig, pools []ChannelPool, label string, model string) (*UpstreamConfig, int, error) {
	route, err := SelectChannelPoolRoute(pools, model)
	if err != nil {
		return nil, -1, err
	}
	for _, pool := range route {
		bestIndex := -1
		bestPriority := 0
		for index := range upstreams {
			upstream := &upstreams[index]
			poolID := strings.TrimSpace(upstream.PoolID)
			if poolID == "" {
				poolID = DefaultChannelPoolID
			}
			if poolID != pool.ID || !IsChannelSchedulable(upstream) || upstream.ExcludeFromConversation {
				continue
			}
			if GetChannelStatus(upstream) != ChannelStatusActive {
				continue
			}
			priority := GetChannelPriority(upstream, index)
			if bestIndex < 0 || priority < bestPriority || (priority == bestPriority && index < bestIndex) {
				bestIndex = index
				bestPriority = priority
			}
		}
		if bestIndex >= 0 {
			return &upstreams[bestIndex], bestIndex, nil
		}
	}
	return nil, -1, fmt.Errorf("命中分组及兜底分组均没有可用的%s渠道", label)
}

func poolExists(pools []ChannelPool, id string) bool {
	for _, pool := range pools {
		if pool.ID == id {
			return true
		}
	}
	return false
}

// ValidateChannelPoolAssignment 确保渠道只能归属当前 API 类型中存在的分组。
func ValidateChannelPoolAssignment(pools []ChannelPool, poolID string) error {
	if !poolExists(pools, strings.TrimSpace(poolID)) {
		return fmt.Errorf("指定的分组不存在")
	}
	return nil
}
