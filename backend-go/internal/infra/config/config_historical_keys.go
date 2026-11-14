// 历史 Key 迁移：换 Key 后仍需聚合统计的旧 Key 属运行时数据。
// 它们不再写入配置文件，改由指标库按渠道稳定 ID 保存。
package config

import (
	"log"
	"strings"
)

// HistoricalKeyStore 按协议与渠道稳定 ID 保存历史 API Key。
type HistoricalKeyStore interface {
	LoadHistoricalKeys(kind string) (map[string][]string, error)
	SaveHistoricalKeys(kind string, channelID string, keys []string) error
	DeleteHistoricalKeys(kind string, channelID string) error
}

// SetHistoricalKeyStore 由装配层注入指标库。
// 指标持久化关闭时不注入，历史 Key 继续随配置保存，避免无库可放时丢数据。
func (cm *ConfigManager) SetHistoricalKeyStore(store HistoricalKeyStore) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	cm.historicalKeyStore = store
}

// SyncHistoricalKeys 启动迁移：把配置中的历史 Key 写入指标库，并用指标库补齐全量渠道。
// 配置文件里残留的历史 Key 会在本次或下次保存时剔除。
func (cm *ConfigManager) SyncHistoricalKeys() error {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	if cm.historicalKeyStore == nil {
		return nil
	}

	candidate := cloneConfig(cm.config)
	dirty := false
	if err := cm.mergeHistoricalKeysLocked(&candidate, &dirty); err != nil {
		return err
	}
	cm.historicalKeysReady = true
	if dirty {
		return cm.saveConfigLocked(candidate)
	}
	cm.publishConfigLocked(candidate)
	return nil
}

// mergeHistoricalKeysLocked 用指标库补齐候选配置的历史 Key。
// dirty 表示配置文件中仍带历史 Key，需要保存一次把它们从文件里剔除。
func (cm *ConfigManager) mergeHistoricalKeysLocked(candidate *Config, dirty *bool) error {
	store := cm.historicalKeyStore
	if store == nil {
		return nil
	}
	for _, set := range candidate.channelSets() {
		stored, err := store.LoadHistoricalKeys(set.kind)
		if err != nil {
			return err
		}
		for index := range *set.upstreams {
			channel := &(*set.upstreams)[index]
			channelID := strings.TrimSpace(channel.ID)
			if channelID == "" {
				continue
			}
			if GetChannelStatus(channel) == ChannelStatusDeleted {
				// 删除占位不再需要历史 Key，清掉可能遗留的注册项。
				if len(stored[channelID]) > 0 {
					if err := store.DeleteHistoricalKeys(set.kind, channelID); err != nil {
						return err
					}
				}
				continue
			}
			if len(channel.HistoricalAPIKeys) > 0 {
				*dirty = true
			}
			merged := mergeHistoricalKeyList(channel.HistoricalAPIKeys, stored[channelID])
			if !equalHistoricalKeyList(merged, stored[channelID]) {
				if err := store.SaveHistoricalKeys(set.kind, channelID, merged); err != nil {
					return err
				}
			}
			channel.HistoricalAPIKeys = merged
		}
	}
	return nil
}

// syncHistoricalKeysToStoreLocked 在配置保存成功后，把发生变化的渠道历史 Key 同步到指标库。
// 只处理有差异的渠道，避免每次保存都重写全部注册项。
func (cm *ConfigManager) syncHistoricalKeysToStoreLocked(next Config) error {
	store := cm.historicalKeyStore
	if store == nil {
		return nil
	}
	before := historicalKeysByChannelID(cm.config, true)
	after := historicalKeysByChannelID(next, false)
	for kind, afterChannels := range after {
		for channelID, keys := range afterChannels {
			if equalHistoricalKeyList(keys, before[kind][channelID]) {
				continue
			}
			if err := store.SaveHistoricalKeys(kind, channelID, keys); err != nil {
				return err
			}
		}
		// before 有、after 没有：渠道被删除或历史 Key 被清空，需要清理注册项。
		for channelID, keys := range before[kind] {
			if _, exists := afterChannels[channelID]; exists || len(keys) == 0 {
				continue
			}
			if err := store.DeleteHistoricalKeys(kind, channelID); err != nil {
				return err
			}
		}
	}
	return nil
}

// historicalKeysByChannelID 汇总各协议渠道的历史 Key。
// includeDeleted 用于保存前快照：删除占位在 after 中缺席，从而触发注册项清理。
func historicalKeysByChannelID(cfg Config, includeDeleted bool) map[string]map[string][]string {
	result := make(map[string]map[string][]string)
	for _, set := range cfg.channelSets() {
		channels := make(map[string][]string)
		for index := range *set.upstreams {
			channel := &(*set.upstreams)[index]
			if !includeDeleted && GetChannelStatus(channel) == ChannelStatusDeleted {
				continue
			}
			channelID := strings.TrimSpace(channel.ID)
			if channelID == "" || len(channel.HistoricalAPIKeys) == 0 {
				continue
			}
			channels[channelID] = append([]string(nil), channel.HistoricalAPIKeys...)
		}
		result[set.kind] = channels
	}
	return result
}

// clearHistoricalKeys 清空历史 Key 后再写盘；内存快照仍保留，供统计聚合使用。
func clearHistoricalKeys(cfg *Config) {
	for _, set := range cfg.channelSets() {
		for index := range *set.upstreams {
			(*set.upstreams)[index].HistoricalAPIKeys = nil
		}
	}
}

// carryHistoricalKeysByID 在指标库读取失败时，按渠道 ID 沿用当前快照的历史 Key。
func carryHistoricalKeysByID(dst *Config, src Config) {
	byKind := historicalKeysByChannelID(src, false)
	for _, set := range dst.channelSets() {
		for index := range *set.upstreams {
			channel := &(*set.upstreams)[index]
			if keys := byKind[set.kind][strings.TrimSpace(channel.ID)]; len(keys) > 0 {
				channel.HistoricalAPIKeys = append([]string(nil), keys...)
			}
		}
	}
}

func mergeHistoricalKeyList(primary, extra []string) []string {
	merged := make([]string, 0, len(primary)+len(extra))
	seen := make(map[string]bool, len(primary)+len(extra))
	for _, group := range [][]string{primary, extra} {
		for _, key := range group {
			if key == "" || seen[key] {
				continue
			}
			seen[key] = true
			merged = append(merged, key)
		}
	}
	return merged
}

func equalHistoricalKeyList(left, right []string) bool {
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

// logHistoricalKeyFallback 统一记录迁移失败后的降级行为，避免静默丢失。
func logHistoricalKeyFallback(err error) {
	log.Printf("[Config-HistoricalKeys] 历史 Key 同步指标库失败，后续保存将恢复写入配置文件: %v", err)
}
