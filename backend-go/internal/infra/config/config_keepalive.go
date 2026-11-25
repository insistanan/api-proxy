package config

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
)

// CacheKeepaliveConfig 只控制后台保活；零值保持关闭，不能影响正常代理请求。
type CacheKeepaliveConfig struct {
	Enabled            bool   `json:"enabled"`
	IntervalSeconds    int    `json:"intervalSeconds"`
	MaxAttempts        int    `json:"maxAttempts"`
	MaxIdleMinutes     int    `json:"maxIdleMinutes"`
	Prompt             string `json:"prompt"`
	MinCacheHitPercent int    `json:"minCacheHitPercent"`
	MaxOutputTokens    int    `json:"maxOutputTokens"`
	MaxTotalTokens     int64  `json:"maxTotalTokens"`
}

// SupportsCacheKeepalive 与现有对话入口的实际上游转换能力保持一致。
func SupportsCacheKeepalive(kind, service string) bool {
	switch kind {
	case "messages", "responses":
		return service == "claude" || service == "openai" || service == "responses" || service == "gemini"
	case "chat":
		return service == "openai"
	case "gemini":
		return service == "gemini" || service == "claude" || service == "openai"
	default:
		return false
	}
}

func ValidateCacheKeepalive(kind string, upstream *UpstreamConfig) error {
	c := upstream.CacheKeepalive
	if !c.Enabled {
		return nil
	}
	if !SupportsCacheKeepalive(kind, upstream.ServiceType) || upstream.BuiltinProvider != "" {
		return fmt.Errorf("缓存保活需要受支持的对话入口及上游协议，不支持内置渠道、图片生成或原样透传入口")
	}
	if upstream.LowQuality {
		return fmt.Errorf("缓存保活需要真实上游用量，不能与低质量渠道的估算用量同时启用")
	}
	if c.IntervalSeconds < 180 || c.IntervalSeconds > 3540 {
		return fmt.Errorf("缓存保活间隔必须为 180–3540 秒，发送前还会核对实际缓存 TTL")
	}
	if c.MaxAttempts < 0 || c.MaxAttempts > 10000 || c.MaxIdleMinutes < 0 || c.MaxIdleMinutes > 1440 {
		return fmt.Errorf("缓存保活次数须为 0–10000，持续时间须为 0–1440 分钟")
	}
	if c.MaxAttempts == 0 && c.MaxIdleMinutes == 0 {
		return fmt.Errorf("缓存保活至少需要设置次数或持续时间上限")
	}
	if strings.TrimSpace(c.Prompt) == "" || len(c.Prompt) > 2048 {
		return fmt.Errorf("缓存保活内容不能为空，且不能超过 2048 字节")
	}
	if c.MinCacheHitPercent < 1 || c.MinCacheHitPercent > 100 {
		return fmt.Errorf("缓存保活最低命中保持比例必须为 1–100%%")
	}
	if c.MaxOutputTokens < 1 || c.MaxOutputTokens > 4096 {
		return fmt.Errorf("缓存保活输出上限必须为 1–4096 Token")
	}
	if c.MaxTotalTokens < 1 || c.MaxTotalTokens > 1000000000 {
		return fmt.Errorf("缓存保活累计 Token 上限必须为 1–1000000000")
	}
	return nil
}

// CacheKeepaliveState 不复制渠道或正文，供真实请求的快速启用判断和版本校验。
func (cm *ConfigManager) CacheKeepaliveState() (bool, uint64) {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	return cm.keepaliveEnabled, cm.keepaliveRevision
}

// CacheKeepaliveChanges 仅由一个保活管理器消费；配置发布合并通知。
func (cm *ConfigManager) CacheKeepaliveChanges() <-chan struct{} {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	if cm.keepaliveChanges == nil {
		cm.keepaliveChanges = make(chan struct{}, 1)
	}
	return cm.keepaliveChanges
}

func cacheKeepaliveConfigChanged(previous, candidate Config) bool {
	// 促销消耗会随每次真实请求保存；它和历史 Key 同步均不改变冻结请求。
	// 仅浅拷贝渠道列表以屏蔽这些运行字段，不复制模型映射或其他配置内容。
	for _, value := range []*Config{&previous, &candidate} {
		value.HistoricalKeysPending = false
		for _, set := range value.channelSets() {
			list := slices.Clone(*set.upstreams)
			for i := range list {
				list[i].PromotionCount = 0
				list[i].PromotionUntil = nil
				list[i].HistoricalAPIKeys = nil
			}
			*set.upstreams = list
		}
	}
	return !reflect.DeepEqual(previous, candidate)
}
