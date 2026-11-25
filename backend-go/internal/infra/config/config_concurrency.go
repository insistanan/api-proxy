package config

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/insistanan/api-proxy/internal/infra/upstreamreq"
	"github.com/insistanan/api-proxy/internal/orchestration/upstreamcontrol"
)

// EffectiveConcurrencyLimit 以 Key 下标展示组合，不额外暴露凭据或其摘要。
type EffectiveConcurrencyLimit struct {
	BaseURL        string `json:"baseUrl"`
	Protocol       string `json:"protocol"`
	KeyIndex       int    `json:"keyIndex"`
	MaxConcurrency int    `json:"maxConcurrency"`
}

func upstreamLists(candidate *Config) [][]UpstreamConfig {
	sets := candidate.channelSets()
	lists := make([][]UpstreamConfig, 0, len(sets))
	for _, set := range sets {
		lists = append(lists, *set.upstreams)
	}
	return lists
}

func validateConcurrency(candidate *Config) error {
	for _, list := range upstreamLists(candidate) {
		for _, upstream := range list {
			if GetChannelStatus(&upstream) == ChannelStatusDeleted {
				continue
			}
			if upstream.MaxConcurrency < 1 {
				return fmt.Errorf("渠道 %q 的最大并发必须是正整数", upstream.Name)
			}
			if upstream.ClientIdentityMode != "" && upstream.ClientIdentityMode != "inherit" && upstream.ClientIdentityMode != "api" && upstream.ClientIdentityMode != "compatibility" {
				return fmt.Errorf("渠道 %q 的客户端身份模式无效", upstream.Name)
			}
		}
	}
	return nil
}

// publishConfigLocked 是内存快照和派生准入表的唯一发布点，调用方持 cm.mu。
func (cm *ConfigManager) publishConfigLocked(candidate Config) {
	if cm.admission == nil {
		cm.admission = upstreamcontrol.NewController()
	}
	rules := make(map[upstreamcontrol.Identity]int)
	for _, set := range candidate.channelSets() {
		list := *set.upstreams
		for i := range list {
			upstream := &list[i]
			upstream.upstreamProtocol = upstream.ServiceType
			if upstream.ServiceType == "openai" {
				switch set.kind {
				case "images", "embeddings", "decisions":
					upstream.upstreamProtocol = set.kind
				}
			}
			upstream.noClientIdentity = set.kind != "messages" && set.kind != "responses"
			if GetChannelStatus(upstream) == ChannelStatusDeleted {
				continue
			}
			for _, baseURL := range upstream.GetAllBaseURLs() {
				for _, key := range upstream.APIKeys {
					id := upstreamIdentity(upstream, baseURL, key)
					if old, exists := rules[id]; !exists || upstream.MaxConcurrency < old {
						rules[id] = upstream.MaxConcurrency
					}
				}
			}
		}
	}
	for _, list := range upstreamLists(&candidate) {
		for i := range list {
			upstream := &list[i]
			upstream.EffectiveConcurrency = nil
			seen := make(map[upstreamcontrol.Identity]bool)
			for _, baseURL := range upstream.GetAllBaseURLs() {
				for keyIndex, key := range upstream.APIKeys {
					id := upstreamIdentity(upstream, baseURL, key)
					if limit, exists := rules[id]; exists && !seen[id] {
						upstream.EffectiveConcurrency = append(upstream.EffectiveConcurrency, EffectiveConcurrencyLimit{BaseURL: baseURL, Protocol: upstream.upstreamProtocol, KeyIndex: keyIndex, MaxConcurrency: limit})
						seen[id] = true
					}
				}
			}
		}
	}
	cm.admission.Reconcile(rules)
	previousDisguise := cm.config.Settings.Disguise
	keepaliveEnabled := false
	for _, set := range candidate.channelSets() {
		for _, upstream := range *set.upstreams {
			if upstream.CacheKeepalive.Enabled && SupportsCacheKeepalive(set.kind, upstream.ServiceType) && GetChannelStatus(&upstream) == ChannelStatusActive {
				keepaliveEnabled = true
				break
			}
		}
	}
	// 有效配置更新后旧请求不再用于付费重放；忽略促销计数等运行字段。
	if (cm.keepaliveEnabled || keepaliveEnabled) && cacheKeepaliveConfigChanged(cm.config, candidate) {
		cm.keepaliveRevision++
		if cm.keepaliveChanges != nil {
			select {
			case cm.keepaliveChanges <- struct{}{}:
			default:
			}
		}
	}
	cm.keepaliveEnabled = keepaliveEnabled
	cm.config = candidate
	disguise := candidate.Settings.Disguise
	upstreamreq.SetDisguiseSettings(disguise.ClaudeCodeVersion, disguise.CodexVersion, disguise.AutoUpdateFromRegistry)
	if disguise != previousDisguise {
		select {
		case cm.disguiseChanges <- struct{}{}:
		default:
		}
	}
}

// DisguiseChanges 供唯一的伪装同步任务消费合并通知；其他设置不触发此通道。
func (cm *ConfigManager) DisguiseChanges() <-chan struct{} {
	return cm.disguiseChanges
}

// UpstreamRequestContext 必须使用本次实际 URL、上游协议和 Key，不能使用入口协议。
func (cm *ConfigManager) UpstreamRequestContext(ctx context.Context, upstream *UpstreamConfig, apiKey string) context.Context {
	cm.mu.RLock()
	controller := cm.admission
	inherited := cm.config.CodexDisguiseEnabled
	if upstream.ServiceType == "claude" {
		inherited = cm.config.ClaudeCodeDisguiseEnabled
	}
	cm.mu.RUnlock()
	protocol := upstream.upstreamProtocol
	if protocol == "" {
		protocol = upstream.ServiceType
	}
	id := upstreamIdentity(upstream, upstream.GetEffectiveBaseURL(), apiKey)
	compatibility := upstream.UseClientCompatibility(inherited) && (protocol == "claude" || protocol == "responses")
	return upstreamcontrol.WithCandidate(ctx, controller, id, compatibility)
}

func upstreamIdentity(upstream *UpstreamConfig, baseURL, apiKey string) upstreamcontrol.Identity {
	protocol := upstream.upstreamProtocol
	if protocol == "" {
		protocol = upstream.ServiceType
	}
	baseURL = strings.TrimSpace(baseURL)
	targetURL := upstreamreq.BuildUpstreamIdentityURL(baseURL, protocol, upstream.UseFullURL)
	return upstreamcontrol.NewIdentity(targetURL, protocol, apiKey)
}

// RequestIdentity 与准入表使用同一规范化规则，也供 Responses 链归属使用。
func (u *UpstreamConfig) RequestIdentity(apiKey string) upstreamcontrol.Identity {
	return upstreamIdentity(u, u.GetEffectiveBaseURL(), apiKey)
}

// UpstreamCooldowns 对已发布配置读取准入状态，不暴露 URL、明文凭据或可变控制器。
func (cm *ConfigManager) UpstreamCooldowns() map[string]int64 {
	cm.mu.RLock()
	controller := cm.admission
	cm.mu.RUnlock()
	result := make(map[string]int64)
	for id, until := range controller.Cooldowns() {
		result[id.Fingerprint()] = until.UnixMilli()
	}
	return result
}

// ClientCompatibilityEnabled 让辅助推理的正文构建与最终发送使用同一身份模式。
func (cm *ConfigManager) ClientCompatibilityEnabled(upstream *UpstreamConfig) bool {
	cm.mu.RLock()
	inherited := cm.config.CodexDisguiseEnabled
	if upstream.ServiceType == "claude" {
		inherited = cm.config.ClaudeCodeDisguiseEnabled
	}
	cm.mu.RUnlock()
	return upstream.UseClientCompatibility(inherited)
}

// UseClientCompatibility 允许渠道覆盖旧的全局伪装设置。
// 伪装只属于 Messages / Responses 渠道：其他协议池无论全局开关与渠道设置如何都不启用，
// 这是所有伪装判断的唯一入口，调用方不得绕过。
func (u *UpstreamConfig) UseClientCompatibility(inherited bool) bool {
	if u.noClientIdentity {
		return false
	}
	switch strings.TrimSpace(u.ClientIdentityMode) {
	case "api":
		return false
	case "compatibility":
		return true
	default:
		return inherited
	}
}

func cloneEffectiveConcurrency(limits []EffectiveConcurrencyLimit) []EffectiveConcurrencyLimit {
	return slices.Clone(limits)
}
