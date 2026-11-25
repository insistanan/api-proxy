// 历史 Key 迁移：换 Key 后仍需聚合统计的旧 Key 属运行时数据。
// 稳态由指标库按渠道稳定 ID 保存；提交期间用配置中的待同步快照保证可恢复。
package config

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/insistanan/api-proxy/internal/storage/atomicfile"
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

// SyncHistoricalKeys 启动同步：恢复待提交快照，或合并配置与指标库中的历史 Key。
func (cm *ConfigManager) SyncHistoricalKeys() error {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	if cm.historicalKeyStore == nil {
		return nil
	}

	candidate := cloneConfig(cm.config)
	needsCommit := candidate.HistoricalKeysPending
	if !candidate.HistoricalKeysPending {
		if err := cm.mergeHistoricalKeysLocked(&candidate, &needsCommit); err != nil {
			return err
		}
	}
	wasReady := cm.historicalKeysReady
	cm.historicalKeysReady = true
	if !needsCommit {
		cm.publishConfigLocked(candidate)
		return nil
	}
	// 首次迁移也先落盘完整快照，数据库失败时可以按同一提交继续恢复。
	candidate.HistoricalKeysPending = true
	err := cm.saveConfigLocked(candidate)
	if err != nil && !IsCommittedError(err) {
		cm.historicalKeysReady = wasReady
	}
	return err
}

// mergeHistoricalKeysLocked 用指标库补齐候选配置的历史 Key。
// dirty 表示需要剥离配置内的历史 Key，或修正数据库中的历史 Key 注册项。
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
		activeIDs := make(map[string]bool, len(*set.upstreams))
		for index := range *set.upstreams {
			channel := &(*set.upstreams)[index]
			channelID := strings.TrimSpace(channel.ID)
			if channelID == "" {
				continue
			}
			if GetChannelStatus(channel) == ChannelStatusDeleted {
				*dirty = *dirty || len(channel.HistoricalAPIKeys) > 0
				channel.HistoricalAPIKeys = nil
				continue
			}
			activeIDs[channelID] = true
			if len(channel.HistoricalAPIKeys) > 0 {
				*dirty = true
			}
			merged := mergeHistoricalKeyList(channel.HistoricalAPIKeys, stored[channelID])
			if !equalHistoricalKeyList(merged, stored[channelID]) {
				*dirty = true
			}
			channel.HistoricalAPIKeys = merged
		}
		for channelID, keys := range stored {
			if !activeIDs[channelID] && len(keys) > 0 {
				*dirty = true
			}
		}
	}
	return nil
}

// syncHistoricalKeysToStoreLocked 将已提交的完整快照同步到数据库。
// 以数据库实际值比较，重复执行会继续完成中断的写入和删除。
func (cm *ConfigManager) syncHistoricalKeysToStoreLocked(next Config) error {
	store := cm.historicalKeyStore
	if store == nil {
		return nil
	}
	after := historicalKeysByChannelID(next)
	for kind, afterChannels := range after {
		before, err := store.LoadHistoricalKeys(kind)
		if err != nil {
			return fmt.Errorf("读取 %s 历史 Key 失败: %w", kind, err)
		}
		for channelID, keys := range afterChannels {
			if equalHistoricalKeyList(keys, before[channelID]) {
				continue
			}
			if err := store.SaveHistoricalKeys(kind, channelID, keys); err != nil {
				return err
			}
		}
		// before 有、after 没有：渠道被删除或历史 Key 被清空，需要清理注册项。
		for channelID, keys := range before {
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
// 删除占位不保留历史 Key，同步时据此清理注册项。
func historicalKeysByChannelID(cfg Config) map[string]map[string][]string {
	result := make(map[string]map[string][]string)
	for _, set := range cfg.channelSets() {
		channels := make(map[string][]string)
		for index := range *set.upstreams {
			channel := &(*set.upstreams)[index]
			if GetChannelStatus(channel) == ChannelStatusDeleted {
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

func historicalKeysChanged(before, after Config) bool {
	return !reflect.DeepEqual(historicalKeysByChannelID(before), historicalKeysByChannelID(after))
}

// finishHistoricalKeysLocked 仅在完整快照已经落盘后执行。
// 同步或替换失败时保留待同步标记；若替换已提交而目录同步失败，内存跟随磁盘。
// 调用方统一按“已提交但收尾失败”报告，不能回滚业务操作。
func (cm *ConfigManager) finishHistoricalKeysLocked() error {
	if err := cm.syncHistoricalKeysToStoreLocked(cm.config); err != nil {
		return err
	}
	persisted := cloneConfig(cm.config)
	persisted.HistoricalKeysPending = false
	clearHistoricalKeys(&persisted)
	data, err := json.MarshalIndent(persisted, "", "  ")
	if err != nil {
		return err
	}
	err = atomicfile.Write(cm.configFile, data, atomicfile.Options{TempPrefix: ".config-", Mode: 0600})
	if err == nil || atomicfile.IsCommitted(err) {
		cm.config.HistoricalKeysPending = false
		cm.persistedDigest = configDigest(data)
	}
	return err
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
