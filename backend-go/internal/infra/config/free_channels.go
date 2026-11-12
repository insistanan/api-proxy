package config

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
)

const FreeChannelPoolID = "builtin-free"
const FreeModelID = "free"
const KiloAutoModelID = "kilo-auto/free"
const KiloChatURL = "https://api.kilo.ai/api/gateway/chat/completions"
const KiloModelsURL = "https://api.kilo.ai/api/gateway/models"

// 免费配置只保存一份；各协议数组仅持久化稳定引用，位置不随其他渠道增删漂移。
type FreeChannelsConfig struct {
	Kilo KiloFreeConfig `json:"kilo"`
}

type KiloFreeConfig struct {
	Enabled  bool     `json:"enabled"`
	Models   []string `json:"models"`
	SyncedAt string   `json:"syncedAt,omitempty"`
}

func SupportsFreeChannels(kind string) bool {
	return kind == "messages" || kind == "responses" || kind == "chat" || kind == "gemini"
}

func IsFreeModel(model string) bool { return model == FreeModelID || strings.HasPrefix(model, "free/") }

// MarshalJSON 只落盘引用，避免四份配置互相覆盖或逐渐漂移。
func (u UpstreamConfig) MarshalJSON() ([]byte, error) {
	if u.BuiltinProvider != "" {
		return json.Marshal(struct {
			ID              string `json:"id"`
			BuiltinProvider string `json:"builtinProvider"`
		}{u.ID, u.BuiltinProvider})
	}
	type plain UpstreamConfig
	return json.Marshal(plain(u))
}

func (cm *ConfigManager) EnsureFreeChannels() error {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	if cm.config.FreeChannels != nil {
		return nil
	}
	candidate := cloneConfig(cm.config)
	candidate.FreeChannels = &FreeChannelsConfig{Kilo: KiloFreeConfig{Enabled: true, Models: []string{"kilo-auto/free"}}}
	return cm.saveConfigLocked(candidate)
}

func (cm *ConfigManager) GetFreeChannels() (FreeChannelsConfig, error) {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	if cm.config.FreeChannels == nil {
		return FreeChannelsConfig{}, fmt.Errorf("免费分组尚未初始化")
	}
	result := *cm.config.FreeChannels
	result.Kilo.Models = slices.Clone(result.Kilo.Models)
	return result, nil
}

func (cm *ConfigManager) SetKiloEnabled(enabled bool) error {
	return cm.updateKilo(func(kilo *KiloFreeConfig) { kilo.Enabled = enabled })
}

func (cm *ConfigManager) SetKiloModels(models []string, syncedAt string) error {
	return cm.updateKilo(func(kilo *KiloFreeConfig) {
		kilo.Models = slices.Clone(models)
		kilo.SyncedAt = syncedAt
	})
}

func (cm *ConfigManager) updateKilo(update func(*KiloFreeConfig)) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	if cm.config.FreeChannels == nil {
		return fmt.Errorf("免费分组尚未初始化")
	}
	candidate := cloneConfig(cm.config)
	update(&candidate.FreeChannels.Kilo)
	return cm.saveConfigLocked(candidate)
}

func materializeFreeChannels(candidate *Config) error {
	for _, kind := range []string{"messages", "responses", "chat", "gemini", "images", "embeddings", "decisions"} {
		cs, err := candidate.channelSetFor(kind)
		if err != nil {
			return err
		}
		for _, up := range *cs.upstreams {
			if up.BuiltinProvider != "" && (candidate.FreeChannels == nil || !SupportsFreeChannels(kind)) {
				return fmt.Errorf("%s 免费渠道缺少共享配置或协议不支持", kind)
			}
		}
	}
	if candidate.FreeChannels == nil {
		return nil
	}
	kilo := candidate.FreeChannels.Kilo
	if len(kilo.Models) == 0 {
		return fmt.Errorf("Kilo 免费模型清单不能为空")
	}
	seen := make(map[string]bool)
	mapping := make(map[string][]string, len(kilo.Models))
	for _, model := range kilo.Models {
		if strings.TrimSpace(model) != model || model == "" || strings.ContainsAny(model, "\r\n\t") || seen[model] {
			return fmt.Errorf("Kilo 模型 ID 为空、重复或格式无效")
		}
		seen[model] = true
		mapping["free/"+model] = []string{model}
	}
	// free 是统一入口，模型候选与显式调用映射来自同一份配置快照。
	mapping[FreeModelID] = slices.Clone(kilo.Models)
	for _, kind := range []string{"messages", "responses", "chat", "gemini"} {
		cs, err := candidate.channelSetFor(kind)
		if err != nil {
			return err
		}
		if !poolExists(*cs.pools, FreeChannelPoolID) {
			*cs.pools = append(*cs.pools, ChannelPool{ID: FreeChannelPoolID, Name: "免费分组", ModelMatcher: "free/", Priority: len(*cs.pools) + 1})
		}
		for _, pool := range *cs.pools {
			if pool.ID == FreeChannelPoolID && (pool.Name != "免费分组" || pool.ModelMatcher != "free/") {
				return fmt.Errorf("免费分组的名称和规则不能修改")
			}
		}
		normalized, err := normalizeChannelPools(*cs.pools)
		if err != nil {
			return fmt.Errorf("%s 免费分组初始化失败: %w", kind, err)
		}
		*cs.pools = normalized
		index := -1
		id := "builtin-kilo-" + kind
		for i, up := range *cs.upstreams {
			if up.BuiltinProvider != "" {
				if up.BuiltinProvider != "kilo" || up.ID != id || index >= 0 {
					return fmt.Errorf("%s 免费渠道引用无效或重复", kind)
				}
				index = i
			} else if up.ID == id || up.PoolID == FreeChannelPoolID {
				return fmt.Errorf("普通渠道不能占用内置免费渠道或免费分组")
			}
		}
		status := ChannelStatusDisabled
		if kilo.Enabled {
			status = ChannelStatusActive
		}
		// 空串是匿名凭据槽位，只参与并发、冷却和统计；不落盘、不发送认证头。
		upstream := UpstreamConfig{ID: id, BuiltinProvider: "kilo", Name: "Kilo 免费池", PoolID: FreeChannelPoolID,
			ServiceType: "openai", BaseURL: KiloChatURL, UseFullURL: true, APIKeys: []string{""},
			MaxConcurrency: 1, Priority: 1, Status: status, ModelMapping: maps.Clone(mapping),
			DisablePromptCacheKey: true, Description: "免注册、免 Key；通过 Chat 协议共享给四个入口"}
		if index < 0 {
			*cs.upstreams = append(*cs.upstreams, upstream)
		} else {
			(*cs.upstreams)[index] = upstream
		}
	}
	return nil
}

func rejectBuiltinChannel(upstreams []UpstreamConfig, index int) error {
	if index >= 0 && index < len(upstreams) && upstreams[index].BuiltinProvider != "" {
		return fmt.Errorf("内置免费渠道请通过免费分组管理，修改会同步到所有协议")
	}
	return nil
}

// 校验在共享请求骨架中执行，指定渠道和会话固定也不能绕过免费边界。
func (cm *ConfigManager) ValidateFreeRequest(kind, model string, channelIndex *int) error {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	cs, err := cm.config.channelSetFor(kind)
	if err != nil {
		return err
	}
	if IsFreeModel(model) {
		if !SupportsFreeChannels(kind) || cm.config.FreeChannels == nil {
			return fmt.Errorf("该协议未启用免费分组")
		}
		if !cm.config.FreeChannels.Kilo.Enabled {
			return fmt.Errorf("Kilo 免费渠道已停用")
		}
		if model != FreeModelID && !slices.Contains(cm.config.FreeChannels.Kilo.Models, strings.TrimPrefix(model, "free/")) {
			return fmt.Errorf("未知免费模型，请同步免费模型清单后重试")
		}
	}
	if channelIndex != nil && *channelIndex >= 0 && *channelIndex < len(*cs.upstreams) {
		isBuiltin := (*cs.upstreams)[*channelIndex].BuiltinProvider != ""
		if isBuiltin != IsFreeModel(model) {
			return fmt.Errorf("免费模型必须使用免费分组，不能指定普通渠道；免费渠道仅接受 free 或 free/模型ID")
		}
	}
	return nil
}
