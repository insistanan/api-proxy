// 渠道密钥操作：更新、增删和顺序调整，沿用现有失败记录与日志语义。
package config

import (
	"fmt"
	"log"
	"strings"

	"github.com/insistanan/api-proxy/internal/utils"
)

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
