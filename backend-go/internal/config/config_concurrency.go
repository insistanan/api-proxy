package config

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/insistanan/api-proxy/internal/upstreamcontrol"
	"github.com/insistanan/api-proxy/internal/utils"
)

// EffectiveConcurrencyLimit 以 Key 下标展示组合，不额外暴露凭据或其摘要。
type EffectiveConcurrencyLimit struct {
	BaseURL        string `json:"baseUrl"`
	Protocol       string `json:"protocol"`
	KeyIndex       int    `json:"keyIndex"`
	MaxConcurrency int    `json:"maxConcurrency"`
}

func upstreamLists(candidate *Config) [][]UpstreamConfig {
	return [][]UpstreamConfig{
		candidate.Upstream,
		candidate.ResponsesUpstream,
		candidate.GeminiUpstream,
		candidate.ChatUpstream,
		candidate.ImagesUpstream,
		candidate.EmbeddingsUpstream,
		candidate.DecisionsUpstream,
	}
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
	for group, list := range upstreamLists(&candidate) {
		for i := range list {
			upstream := &list[i]
			upstream.upstreamProtocol = upstream.ServiceType
			if group == 4 && upstream.ServiceType == "openai" {
				upstream.upstreamProtocol = "images"
			}
			if group == 5 && upstream.ServiceType == "openai" {
				upstream.upstreamProtocol = "embeddings"
			}
			if group == 6 && upstream.ServiceType == "openai" {
				upstream.upstreamProtocol = "decisions"
			}
			// 分组 0 = Messages、1 = Responses，只有这两组支持客户端伪装。
			upstream.noClientIdentity = group > 1
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
	cm.config = candidate
	gateway := candidate.Settings.Gateway
	activeGatewaySettings.Store(&gateway)
	disguise := candidate.Settings.Disguise
	utils.SetDisguiseSettings(disguise.ClaudeCodeVersion, disguise.CodexVersion, disguise.AutoUpdateFromRegistry)
	if disguise != previousDisguise {
		select {
		case cm.settingsChanged <- struct{}{}:
		default:
		}
	}
}

// SettingsChanged 合并配置发布事件，后台任务读取最新快照，不积压旧设置。
func (cm *ConfigManager) SettingsChanged() <-chan struct{} {
	return cm.settingsChanged
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
	if !upstream.UseFullURL {
		baseURL = strings.TrimRight(baseURL, "/")
	}
	return upstreamcontrol.NewIdentity(baseURL, protocol, apiKey)
}

// RequestIdentity 与准入表使用同一规范化规则，也供 Responses 链归属使用。
func (u *UpstreamConfig) RequestIdentity(apiKey string) upstreamcontrol.Identity {
	return upstreamIdentity(u, u.GetEffectiveBaseURL(), apiKey)
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
