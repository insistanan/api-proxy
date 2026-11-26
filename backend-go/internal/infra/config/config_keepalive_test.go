package config

import (
	"encoding/json"
	"testing"
	"time"
)

func TestKeepaliveTargetDigest_OnlyRelevantConfigurationInvalidatesTarget(t *testing.T) {
	manager := &ConfigManager{}
	manager.publishConfigLocked(Config{Upstream: []UpstreamConfig{
		{ID: "a", BaseURL: "https://a.example", APIKeys: []string{"a"}},
		{ID: "b", BaseURL: "https://b.example", APIKeys: []string{"b"}},
	}})
	digest, _ := manager.CacheKeepaliveTargetDigest("messages", "a")
	cfg := manager.GetConfig()
	cfg.Upstream[1].APIKeys = []string{"rotated"}
	manager.publishConfigLocked(cfg)
	other, _ := manager.CacheKeepaliveTargetDigest("messages", "a")
	if digest != other {
		t.Fatal("其他渠道变更不应废弃本目标")
	}
	cfg = manager.GetConfig()
	cfg.Upstream[0].APIKeys = []string{"rotated"}
	manager.publishConfigLocked(cfg)
	changed, _ := manager.CacheKeepaliveTargetDigest("messages", "a")
	if digest == changed {
		t.Fatal("目标凭据变更未使候选失效")
	}
	cfg = manager.GetConfig()
	cfg.Settings.ContentSafety.Enabled = !cfg.Settings.ContentSafety.Enabled
	manager.publishConfigLocked(cfg)
	safety, _ := manager.CacheKeepaliveTargetDigest("messages", "a")
	if safety == changed {
		t.Fatal("安全策略变更未使候选失效")
	}
}

func TestCacheKeepaliveConfig_RejectsRemovedTokenLimit(t *testing.T) {
	var upstream UpstreamConfig
	if err := json.Unmarshal([]byte(`{"serviceType":"claude","cacheKeepalive":{"maxTotalTokens":1}}`), &upstream); err == nil {
		t.Fatal("已移除字段必须报错，不能在保存时静默丢弃")
	}
}
func TestCacheKeepaliveValidation_RejectsInvalidLimitsAndUnsupportedChannels(t *testing.T) {
	valid := UpstreamConfig{ServiceType: "claude", CacheKeepalive: CacheKeepaliveConfig{
		Enabled: true, IntervalSeconds: 180, MaxAttempts: 100, Prompt: "接下来应该怎么做？",
		MinCacheHitPercent: 90, MaxOutputTokens: 16,
	}}
	if err := ValidateCacheKeepalive("messages", &valid); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		change func(*UpstreamConfig)
	}{
		{"不足三分钟", func(u *UpstreamConfig) { u.CacheKeepalive.IntervalSeconds = 179 }},
		{"没有停止上限", func(u *UpstreamConfig) { u.CacheKeepalive.MaxAttempts = 0 }},
		{"空心跳内容", func(u *UpstreamConfig) { u.CacheKeepalive.Prompt = " " }},
		{"未知上游", func(u *UpstreamConfig) { u.ServiceType = "unknown" }},
		{"估算用量", func(u *UpstreamConfig) { u.LowQuality = true }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upstream := valid
			tc.change(&upstream)
			if err := ValidateCacheKeepalive("messages", &upstream); err == nil {
				t.Fatal("无效配置被接受")
			}
		})
	}
	if err := ValidateCacheKeepalive("chat", &valid); err == nil {
		t.Fatal("Chat 入口不能配置尚不存在的 Claude 转换")
	}
	if err := ValidateCacheKeepalive("messages", &UpstreamConfig{}); err != nil {
		t.Fatal("旧配置的零值必须保持关闭并兼容加载")
	}
}

func TestCacheKeepaliveRevision_IgnoresPromotionConsumptionButInvalidatesRealChanges(t *testing.T) {
	manager := &ConfigManager{}
	until := time.Now().Add(time.Hour)
	manager.publishConfigLocked(Config{Upstream: []UpstreamConfig{{ID: "channel", ServiceType: "claude",
		BaseURL: "https://example.test", APIKeys: []string{"key"}, MaxConcurrency: 5,
		PromotionCount: 1, PromotionUntil: &until, CacheKeepalive: CacheKeepaliveConfig{Enabled: true}}}})
	enabled, revision := manager.CacheKeepaliveState()
	if !enabled || revision == 0 {
		t.Fatal("启用渠道未发布保活状态")
	}
	candidate := manager.GetConfig()
	candidate.Upstream[0].PromotionCount = 0
	candidate.Upstream[0].PromotionUntil = nil
	manager.publishConfigLocked(candidate)
	if _, after := manager.CacheKeepaliveState(); after != revision {
		t.Fatal("促销请求结束导致全部保活候选失效")
	}
	changes := manager.CacheKeepaliveChanges()
	candidate = manager.GetConfig()
	candidate.Upstream[0].APIKeys = []string{"new-key"}
	manager.publishConfigLocked(candidate)
	if _, after := manager.CacheKeepaliveState(); after <= revision {
		t.Fatal("凭据变更没有使旧快照失效")
	}
	select {
	case <-changes:
	default:
		t.Fatal("配置变更未通知保活服务")
	}
}

func TestCacheKeepalive_EnablementCoversEveryConversationPool(t *testing.T) {
	for _, kind := range []string{"messages", "responses", "chat", "gemini", "images", "embeddings", "decisions"} {
		for _, protocol := range []string{"claude", "openai", "responses", "gemini"} {
			t.Run(kind+"/"+protocol, func(t *testing.T) {
				want := (kind == "messages" || kind == "responses" || kind == "gemini") || kind == "chat" && protocol == "openai"
				up := UpstreamConfig{ID: "channel", ServiceType: protocol, BaseURL: "https://example.test", APIKeys: []string{"fixture"}, MaxConcurrency: 5,
					CacheKeepalive: CacheKeepaliveConfig{Enabled: true, IntervalSeconds: 280, MaxAttempts: 100, Prompt: "继续", MinCacheHitPercent: 90, MaxOutputTokens: 16}}
				if err := ValidateCacheKeepalive(kind, &up); (err == nil) != want {
					t.Fatalf("协议支持判定与入口能力不符: %v", err)
				}
				candidate := Config{}
				for _, set := range candidate.channelSets() {
					if set.kind == kind {
						*set.upstreams = []UpstreamConfig{up}
					}
				}
				manager := &ConfigManager{}
				manager.publishConfigLocked(candidate)
				if enabled, _ := manager.CacheKeepaliveState(); enabled != want {
					t.Fatalf("仅该渠道池开启时保活全局状态错误: %t", enabled)
				}
			})
		}
	}
}
