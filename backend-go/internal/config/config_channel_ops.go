package config

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/BenedictKing/api-proxy/internal/utils"
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

	var upstreams []UpstreamConfig
	switch channelType {
	case "messages":
		upstreams = cm.config.Upstream
	case "responses":
		upstreams = cm.config.ResponsesUpstream
	case "gemini":
		upstreams = cm.config.GeminiUpstream
	case "chat":
		upstreams = cm.config.ChatUpstream
	case "images":
		upstreams = cm.config.ImagesUpstream
	default:
		return fmt.Errorf("不支持的渠道类型: %s", channelType)
	}

	for index := range upstreams {
		if upstreams[index].ID != upstreamID {
			continue
		}
		if upstreams[index].DisablePromptCacheKey {
			return nil
		}

		upstreams[index].DisablePromptCacheKey = true
		if err := cm.saveConfigLocked(cm.config); err != nil {
			upstreams[index].DisablePromptCacheKey = false
			return fmt.Errorf("保存渠道缓存键能力失败: %w", err)
		}
		log.Printf("[Config-ChannelCapability] 渠道 %s (%s) 不支持 prompt_cache_key，已自动禁用", upstreams[index].Name, upstreamID)
		return nil
	}

	return fmt.Errorf("未找到渠道 ID: %s", upstreamID)
}

// MarkReasoningContentRequired 记录渠道会拒绝缺少 reasoning_content 的 assistant 历史。
// 该能力只在收到上游明确的字段校验错误后启用；后续 Messages 请求会在转换阶段补齐
// 已被客户端隐藏或删除的历史推理字段，避免每轮都先收到一次 400。
func (cm *ConfigManager) MarkReasoningContentRequired(channelType, upstreamID string) error {
	if strings.TrimSpace(upstreamID) == "" {
		return fmt.Errorf("无法记录推理字段能力：渠道 ID 为空")
	}

	cm.mu.Lock()
	defer cm.mu.Unlock()

	var upstreams []UpstreamConfig
	switch channelType {
	case "messages":
		upstreams = cm.config.Upstream
	case "responses":
		upstreams = cm.config.ResponsesUpstream
	case "gemini":
		upstreams = cm.config.GeminiUpstream
	case "chat":
		upstreams = cm.config.ChatUpstream
	case "images":
		upstreams = cm.config.ImagesUpstream
	default:
		return fmt.Errorf("不支持的渠道类型: %s", channelType)
	}

	for index := range upstreams {
		if upstreams[index].ID != upstreamID {
			continue
		}
		if upstreams[index].RequireReasoningContent {
			return nil
		}

		upstreams[index].RequireReasoningContent = true
		if err := cm.saveConfigLocked(cm.config); err != nil {
			upstreams[index].RequireReasoningContent = false
			return fmt.Errorf("保存渠道推理字段能力失败: %w", err)
		}
		log.Printf("[Config-ChannelCapability] 渠道 %s (%s) 要求补全 reasoning_content，已自动启用兼容模式", upstreams[index].Name, upstreamID)
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
		if !IsChannelSchedulable(upstream) || upstream.ExcludeFromConversation {
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
	upstream.BaseURLs = deduplicateBaseURLs(upstream.BaseURLs)
	upstream.ModelMapping = normalizeModelMapping(upstream.ModelMapping)
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
	return candidate, result, nil
}

func cloneUpstreamList(upstreams []UpstreamConfig) []UpstreamConfig {
	cloned := make([]UpstreamConfig, len(upstreams))
	for index := range upstreams {
		cloned[index] = *upstreams[index].Clone()
	}
	return cloned
}

// applyCommonUpdates 应用通用的 UpstreamUpdate 字段到上游配置
// 返回 shouldResetMetrics 标识是否需要重置指标
func applyCommonUpdates(upstream *UpstreamConfig, index int, updates UpstreamUpdate, label string) (bool, error) {
	if updates.VisionCapable != nil && *updates.VisionCapable &&
		updates.VisionLayerEnabled != nil && *updates.VisionLayerEnabled {
		return false, fmt.Errorf("渠道不能同时标记为支持图片理解并启用图片理解层")
	}
	shouldResetMetrics := false

	if updates.Name != nil {
		upstream.Name = *updates.Name
	}
	if updates.PoolID != nil {
		upstream.PoolID = strings.TrimSpace(*updates.PoolID)
	}
	if updates.BaseURL != nil {
		upstream.BaseURL = *updates.BaseURL
		if updates.BaseURLs == nil {
			upstream.BaseURLs = nil
		}
	}
	if updates.BaseURLs != nil {
		upstream.BaseURLs = deduplicateBaseURLs(updates.BaseURLs)
	}
	if updates.ServiceType != nil {
		upstream.ServiceType = *updates.ServiceType
	}
	if updates.Description != nil {
		upstream.Description = *updates.Description
	}
	if updates.Website != nil {
		upstream.Website = *updates.Website
	}
	if updates.APIKeys != nil {
		shouldResetMetrics = applyAPIKeysUpdate(upstream, index, updates.APIKeys, label)
	}
	if updates.ModelMapping != nil {
		upstream.ModelMapping = normalizeModelMapping(updates.ModelMapping)
	}
	if updates.DefaultModel != nil {
		upstream.DefaultModel = strings.TrimSpace(*updates.DefaultModel)
	}
	if updates.InsecureSkipVerify != nil {
		upstream.InsecureSkipVerify = *updates.InsecureSkipVerify
	}
	if updates.ProxyMode != nil {
		upstream.ProxyMode = *updates.ProxyMode
	}
	if updates.ProxyURL != nil {
		upstream.ProxyURL = *updates.ProxyURL
	}
	normalizeUpstreamProxyConfig(upstream)
	if err := validateUpstreamProxyConfig(upstream); err != nil {
		return false, err
	}
	if updates.Priority != nil {
		upstream.Priority = *updates.Priority
	}
	if updates.Status != nil {
		if err := setUpstreamStatus(upstream, *updates.Status, time.Now()); err != nil {
			return false, err
		}
	}
	if updates.PromotionUntil != nil {
		upstream.PromotionUntil = updates.PromotionUntil
	}
	if updates.PromotionCount != nil {
		upstream.PromotionCount = *updates.PromotionCount
	}
	if updates.LowQuality != nil {
		upstream.LowQuality = *updates.LowQuality
	}
	if updates.VisionCapable != nil {
		upstream.VisionCapable = *updates.VisionCapable
		if *updates.VisionCapable {
			upstream.VisionLayerEnabled = false
			upstream.VisionLayerChannelID = ""
			upstream.VisionLayerModel = ""
		}
	}
	if updates.ExcludeFromConversation != nil {
		upstream.ExcludeFromConversation = *updates.ExcludeFromConversation
	}
	if updates.VisionLayerEnabled != nil {
		upstream.VisionLayerEnabled = *updates.VisionLayerEnabled
		if *updates.VisionLayerEnabled {
			upstream.VisionCapable = false
		} else {
			upstream.VisionLayerChannelID = ""
			upstream.VisionLayerModel = ""
		}
	}
	if updates.VisionLayerChannelID != nil {
		upstream.VisionLayerChannelID = strings.TrimSpace(*updates.VisionLayerChannelID)
	}
	if updates.VisionLayerModel != nil {
		upstream.VisionLayerModel = strings.TrimSpace(*updates.VisionLayerModel)
	}
	if updates.Temporary != nil {
		upstream.Temporary = *updates.Temporary
	}
	if updates.TemporaryUntil != nil {
		upstream.TemporaryUntil = updates.TemporaryUntil
	}
	if updates.DeprecatedAt != nil {
		upstream.DeprecatedAt = updates.DeprecatedAt
	}
	prepareUpstreamLifecycle(upstream, time.Now())
	if updates.InjectDummyThoughtSignature != nil {
		upstream.InjectDummyThoughtSignature = *updates.InjectDummyThoughtSignature
	}
	if updates.StripThoughtSignature != nil {
		upstream.StripThoughtSignature = *updates.StripThoughtSignature
	}
	if updates.IncludeHistoryThinking != nil {
		upstream.IncludeHistoryThinking = *updates.IncludeHistoryThinking
	}
	if updates.DisablePromptCacheInjection != nil {
		upstream.DisablePromptCacheInjection = *updates.DisablePromptCacheInjection
	}
	if updates.DisablePromptCacheKey != nil {
		upstream.DisablePromptCacheKey = *updates.DisablePromptCacheKey
	}
	if updates.RequireReasoningContent != nil {
		upstream.RequireReasoningContent = *updates.RequireReasoningContent
	}
	if updates.EnablePreviousResponseID != nil {
		upstream.EnablePreviousResponseID = *updates.EnablePreviousResponseID
	}
	return shouldResetMetrics, nil
}

// applyCommonUpdatesToList 应用渠道更新，并在状态池发生变化时同步调整优先级。
func applyCommonUpdatesToList(upstreams []UpstreamConfig, pools []ChannelPool, index int, updates UpstreamUpdate, label string) (bool, error) {
	original := upstreams[index].Clone()
	previousPool := channelStatusPool(&upstreams[index])
	shouldResetMetrics, err := applyCommonUpdates(&upstreams[index], index, updates, label)
	if err != nil {
		upstreams[index] = *original
		return false, err
	}
	if err := ValidateChannelPoolAssignment(pools, upstreams[index].PoolID); err != nil {
		upstreams[index] = *original
		return false, err
	}
	if err := validateAllVisionLayerConfigs(upstreams); err != nil {
		upstreams[index] = *original
		return false, err
	}
	if previousPool != channelStatusPool(&upstreams[index]) {
		moveChannelToStatusPoolTail(upstreams, index)
	}
	return shouldResetMetrics, nil
}

func validateAllVisionLayerConfigs(upstreams []UpstreamConfig) error {
	for index := range upstreams {
		if GetChannelStatus(&upstreams[index]) == ChannelStatusDeleted {
			continue
		}
		if err := validateVisionLayerConfig(upstreams, index); err != nil {
			return fmt.Errorf("渠道 %q 配置无效: %w", upstreams[index].Name, err)
		}
	}
	return nil
}

func validateVisionLayerConfig(upstreams []UpstreamConfig, ownerIndex int) error {
	if ownerIndex < 0 || ownerIndex >= len(upstreams) {
		return fmt.Errorf("图片理解层所属渠道索引无效: %d", ownerIndex)
	}
	owner := &upstreams[ownerIndex]
	if owner.ExcludeFromConversation && !owner.VisionCapable {
		return fmt.Errorf("不参与对话的渠道必须标记为支持图片理解")
	}
	if owner.VisionCapable && owner.VisionLayerEnabled {
		return fmt.Errorf("渠道不能同时标记为支持图片理解并启用图片理解层")
	}
	if !owner.VisionLayerEnabled {
		return nil
	}
	targetID := strings.TrimSpace(owner.VisionLayerChannelID)
	if targetID == "" {
		return fmt.Errorf("启用图片理解层后必须选择图片理解渠道")
	}
	for targetIndex := range upstreams {
		target := &upstreams[targetIndex]
		if target.ID != targetID {
			continue
		}
		if targetIndex == ownerIndex {
			return fmt.Errorf("图片理解渠道不能选择当前渠道自身")
		}
		if !target.VisionCapable {
			return fmt.Errorf("所选渠道 %q 未标记为支持图片理解", target.Name)
		}
		if !target.ExcludeFromConversation && target.PoolID != owner.PoolID {
			return fmt.Errorf("图片理解渠道必须位于当前分组或公用纯图片理解池")
		}
		return nil
	}
	return fmt.Errorf("图片理解渠道 %q 不存在", targetID)
}

// applyAPIKeysUpdate 处理 API Keys 更新逻辑（历史记录、自动激活）
func applyAPIKeysUpdate(upstream *UpstreamConfig, index int, newAPIKeys []string, label string) bool {
	shouldResetMetrics := false
	newKeys := make(map[string]bool)
	for _, key := range newAPIKeys {
		newKeys[key] = true
	}

	for _, key := range upstream.APIKeys {
		if !newKeys[key] {
			alreadyInHistory := false
			for _, hk := range upstream.HistoricalAPIKeys {
				if hk == key {
					alreadyInHistory = true
					break
				}
			}
			if !alreadyInHistory {
				upstream.HistoricalAPIKeys = append(upstream.HistoricalAPIKeys, key)
				log.Printf("[Config-Upstream] %s渠道 [%d] %s: Key %s 已移入历史列表", label, index, upstream.Name, utils.MaskAPIKey(key))
			}
		}
	}

	var newHistoricalKeys []string
	for _, hk := range upstream.HistoricalAPIKeys {
		if !newKeys[hk] {
			newHistoricalKeys = append(newHistoricalKeys, hk)
		} else {
			log.Printf("[Config-Upstream] %s渠道 [%d] %s: Key %s 已从历史列表恢复", label, index, upstream.Name, utils.MaskAPIKey(hk))
		}
	}
	upstream.HistoricalAPIKeys = newHistoricalKeys

	if len(upstream.APIKeys) == 1 && len(newAPIKeys) == 1 && upstream.APIKeys[0] != newAPIKeys[0] {
		shouldResetMetrics = true
		if upstream.Status == "suspended" {
			upstream.Status = "active"
			log.Printf("[Config-Upstream] %s渠道 [%d] %s 已从暂停状态自动激活（单 key 更换）", label, index, upstream.Name)
		}
	}
	upstream.APIKeys = deduplicateStrings(newAPIKeys)
	return shouldResetMetrics
}

// removeFromSlice 将渠道替换为删除占位，保留索引稳定性。
// 渠道索引被会话路由、日志和 URL 健康缓存引用，物理删除会使这些状态错绑到后续渠道。
func removeFromSlice(upstreams []UpstreamConfig, index int, label string) ([]UpstreamConfig, *UpstreamConfig, error) {
	if index < 0 || index >= len(upstreams) {
		return upstreams, nil, fmt.Errorf("无效的%s上游索引: %d", label, index)
	}
	if GetChannelStatus(&upstreams[index]) == ChannelStatusDeleted {
		return upstreams, nil, fmt.Errorf("无效的%s上游索引: %d（已删除）", label, index)
	}
	removed := upstreams[index]
	for ownerIndex := range upstreams {
		if ownerIndex == index || !upstreams[ownerIndex].VisionLayerEnabled {
			continue
		}
		if strings.TrimSpace(upstreams[ownerIndex].VisionLayerChannelID) == removed.ID {
			return upstreams, nil, fmt.Errorf("渠道 %q 正被 %q 的图片理解层使用，无法删除", removed.Name, upstreams[ownerIndex].Name)
		}
	}
	upstreams[index] = UpstreamConfig{
		ID:       removed.ID,
		Name:     removed.Name,
		Priority: removed.Priority,
		Status:   ChannelStatusDeleted,
	}
	moveChannelToStatusPoolTail(upstreams, index)
	return upstreams, &removed, nil
}

// addAPIKeyOp 向指定渠道添加 API 密钥（调用方必须持锁）
func addAPIKeyOp(upstreams []UpstreamConfig, index int, apiKey string, label string) error {
	if index < 0 || index >= len(upstreams) {
		return fmt.Errorf("无效的上游索引: %d", index)
	}
	for _, key := range upstreams[index].APIKeys {
		if key == apiKey {
			return &ConfigError{Message: "API密钥已存在"}
		}
	}
	upstreams[index].APIKeys = append(upstreams[index].APIKeys, apiKey)

	var newHistoricalKeys []string
	for _, hk := range upstreams[index].HistoricalAPIKeys {
		if hk != apiKey {
			newHistoricalKeys = append(newHistoricalKeys, hk)
		} else {
			log.Printf("[%s-Key] 上游 [%d] %s: Key %s 已从历史列表恢复", label, index, upstreams[index].Name, utils.MaskAPIKey(hk))
		}
	}
	upstreams[index].HistoricalAPIKeys = newHistoricalKeys

	log.Printf("[%s-Key] 已添加API密钥到上游 [%d] %s", label, index, upstreams[index].Name)
	return nil
}

// removeAPIKeyOp 从指定渠道删除 API 密钥（调用方必须持锁）
func removeAPIKeyOp(upstreams []UpstreamConfig, index int, apiKey string, label string) error {
	if index < 0 || index >= len(upstreams) {
		return fmt.Errorf("无效的上游索引: %d", index)
	}
	keys := upstreams[index].APIKeys
	found := false
	for i, key := range keys {
		if key == apiKey {
			upstreams[index].APIKeys = append(keys[:i], keys[i+1:]...)
			found = true
			break
		}
	}
	if !found {
		return &ConfigError{Message: "API密钥不存在"}
	}

	alreadyInHistory := false
	for _, hk := range upstreams[index].HistoricalAPIKeys {
		if hk == apiKey {
			alreadyInHistory = true
			break
		}
	}
	if !alreadyInHistory {
		upstreams[index].HistoricalAPIKeys = append(upstreams[index].HistoricalAPIKeys, apiKey)
		log.Printf("[%s-Key] 上游 [%d] %s: Key %s 已移入历史列表", label, index, upstreams[index].Name, utils.MaskAPIKey(apiKey))
	}

	log.Printf("[%s-Key] 已从上游 [%d] %s 删除API密钥", label, index, upstreams[index].Name)
	return nil
}

// moveKeyToTopOp 将密钥移到列表顶部（调用方必须持锁）
func moveKeyToTopOp(upstreams []UpstreamConfig, upstreamIndex int, apiKey string) error {
	if upstreamIndex < 0 || upstreamIndex >= len(upstreams) {
		return fmt.Errorf("无效的上游索引: %d", upstreamIndex)
	}
	upstream := &upstreams[upstreamIndex]
	index := -1
	for i, key := range upstream.APIKeys {
		if key == apiKey {
			index = i
			break
		}
	}
	if index <= 0 {
		return nil
	}
	upstream.APIKeys = append([]string{apiKey}, append(upstream.APIKeys[:index], upstream.APIKeys[index+1:]...)...)
	return nil
}

// moveKeyToBottomOp 将密钥移到列表底部（调用方必须持锁）
func moveKeyToBottomOp(upstreams []UpstreamConfig, upstreamIndex int, apiKey string) error {
	if upstreamIndex < 0 || upstreamIndex >= len(upstreams) {
		return fmt.Errorf("无效的上游索引: %d", upstreamIndex)
	}
	upstream := &upstreams[upstreamIndex]
	index := -1
	for i, key := range upstream.APIKeys {
		if key == apiKey {
			index = i
			break
		}
	}
	if index == -1 || index == len(upstream.APIKeys)-1 {
		return nil
	}
	upstream.APIKeys = append(upstream.APIKeys[:index], upstream.APIKeys[index+1:]...)
	upstream.APIKeys = append(upstream.APIKeys, apiKey)
	return nil
}

// reorderOp 重新排序渠道优先级（调用方必须持锁）
func reorderOp(upstreams []UpstreamConfig, order []int, label string) error {
	if len(order) == 0 {
		return fmt.Errorf("排序数组不能为空")
	}
	seen := make(map[int]bool)
	for _, idx := range order {
		if idx < 0 || idx >= len(upstreams) {
			return fmt.Errorf("无效的渠道索引: %d", idx)
		}
		if seen[idx] {
			return fmt.Errorf("重复的渠道索引: %d", idx)
		}
		seen[idx] = true
	}
	// UI 仅传入故障转移序列。将未提交渠道按当前顺序补齐，保证全量优先级连续且唯一。
	allOrder := make([]int, 0, len(upstreams))
	allOrder = append(allOrder, order...)
	remaining := make([]int, 0, len(upstreams)-len(order))
	for index := range upstreams {
		if !seen[index] {
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
	allOrder = append(allOrder, remaining...)
	poolPriorities := make(map[string]int)
	for _, idx := range allOrder {
		scope := upstreamPriorityScope(&upstreams[idx])
		poolPriorities[scope]++
		upstreams[idx].Priority = poolPriorities[scope]
	}
	log.Printf("[Config-Reorder] 已更新 %s 渠道优先级顺序 (%d 个显式渠道，%d 个总渠道)", label, len(order), len(allOrder))
	return nil
}

func moveChannelToStatusPoolTail(upstreams []UpstreamConfig, targetIndex int) {
	ordered := make([]int, 0, len(upstreams)-1)
	for index := range upstreams {
		if index != targetIndex && upstreamPriorityScope(&upstreams[index]) == upstreamPriorityScope(&upstreams[targetIndex]) {
			ordered = append(ordered, index)
		}
	}
	sort.SliceStable(ordered, func(i, j int) bool {
		left, right := ordered[i], ordered[j]
		leftPriority := GetChannelPriority(&upstreams[left], left)
		rightPriority := GetChannelPriority(&upstreams[right], right)
		if leftPriority != rightPriority {
			return leftPriority < rightPriority
		}
		return left < right
	})

	active := make([]int, 0, len(ordered)+1)
	suspended := make([]int, 0, len(ordered)+1)
	inactive := make([]int, 0, len(ordered)+1)
	for _, index := range ordered {
		switch channelStatusPool(&upstreams[index]) {
		case 0:
			active = append(active, index)
		case 1:
			suspended = append(suspended, index)
		default:
			inactive = append(inactive, index)
		}
	}
	switch channelStatusPool(&upstreams[targetIndex]) {
	case 0:
		active = append(active, targetIndex)
	case 1:
		suspended = append(suspended, targetIndex)
	default:
		inactive = append(inactive, targetIndex)
	}
	ordered = append(active, suspended...)
	ordered = append(ordered, inactive...)
	for priority, index := range ordered {
		upstreams[index].Priority = priority + 1
	}
}

func channelStatusPool(upstream *UpstreamConfig) int {
	switch GetChannelStatus(upstream) {
	case ChannelStatusSuspended:
		return 1
	case ChannelStatusDisabled, ChannelStatusDeprecated, ChannelStatusDeleted:
		return 2
	default:
		return 0
	}
}

// setStatusOp 设置渠道状态（调用方必须持锁）
func setStatusOp(upstreams []UpstreamConfig, index int, status string, label string) error {
	if index < 0 || index >= len(upstreams) {
		return fmt.Errorf("无效的上游索引: %d", index)
	}
	previousPool := channelStatusPool(&upstreams[index])
	if err := setUpstreamStatus(&upstreams[index], status, time.Now()); err != nil {
		return err
	}
	if previousPool != channelStatusPool(&upstreams[index]) {
		moveChannelToStatusPoolTail(upstreams, index)
	}
	finalStatus := upstreams[index].Status
	if finalStatus == "suspended" || finalStatus == "deprecated" {
		log.Printf("[Config-Status] 已清除渠道 [%d] %s 的促销期", index, upstreams[index].Name)
	}
	log.Printf("[Config-Status] 已设置 %s 渠道 [%d] %s 状态为: %s", label, index, upstreams[index].Name, finalStatus)
	return nil
}

// setPromotionOp 设置渠道促销期（调用方必须持锁）
func setPromotionOp(upstreams []UpstreamConfig, index int, duration time.Duration, count int, label string) error {
	if index < 0 || index >= len(upstreams) {
		return fmt.Errorf("无效的上游索引: %d", index)
	}
	upstream := &upstreams[index]
	if duration > 0 {
		until := time.Now().Add(duration)
		upstream.PromotionUntil = &until
		upstream.PromotionCount = 0
		log.Printf("[Config-Promotion] 已设置 %s 渠道 [%d] %s 促销期: %v", label, index, upstream.Name, duration)
	} else if count > 0 {
		upstream.PromotionUntil = nil
		upstream.PromotionCount = count
		log.Printf("[Config-Promotion] 已设置 %s 渠道 [%d] %s 促销次数: %d", label, index, upstream.Name, count)
	} else {
		upstream.PromotionUntil = nil
		upstream.PromotionCount = 0
		log.Printf("[Config-Promotion] 已清除 %s 渠道 [%d] %s 的促销期", label, index, upstream.Name)
	}
	return nil
}

// consumePromotionOp 消费促销计数（调用方必须持锁），返回是否需要保存
func consumePromotionOp(upstreams []UpstreamConfig, index int, label string) bool {
	if index < 0 || index >= len(upstreams) {
		return false
	}
	upstream := &upstreams[index]
	if upstream.PromotionCount <= 0 {
		return false
	}
	upstream.PromotionCount--
	if upstream.PromotionCount == 0 {
		upstream.PromotionUntil = nil
		log.Printf("[Config-Promotion] %s渠道 [%d] %s 促销次数已用完，自动清除促销状态", label, index, upstream.Name)
	}
	return true
}

// getPromotedOp 获取处于促销期的渠道索引
func getPromotedOp(upstreams []UpstreamConfig) (int, bool) {
	for i := range upstreams {
		if IsChannelInPromotion(&upstreams[i]) {
			return i, true
		}
	}
	return -1, false
}
