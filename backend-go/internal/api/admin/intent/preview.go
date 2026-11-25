package intent

import (
	"fmt"
	"slices"

	"github.com/gin-gonic/gin"
	"github.com/insistanan/api-proxy/internal/dialogue/routing/intent"
	"github.com/insistanan/api-proxy/internal/infra/config"
)

// 只展示配置候选，不取得调度许可或实际调用任务模型。
func previewModelRoute(cm *config.ConfigManager, kind string, cfg config.IntentRoutingConfig, decision intent.Decision, model string) (gin.H, error) {
	if len(model) > 256 {
		return nil, fmt.Errorf("请求模型名不能超过 256 字节")
	}
	pools := cm.GetChannelPools(kind)
	original, err := config.SelectChannelPoolRoute(pools, model)
	if err != nil {
		return nil, err
	}
	selected := original
	target := decision.PoolID
	if target == "" && cfg.Unmatched == "fallback" {
		target = cfg.FallbackPoolID
	}
	if target != "" {
		selected = slices.DeleteFunc(slices.Clone(pools), func(pool config.ChannelPool) bool { return pool.ID != target })
	} else if cfg.Unmatched == "error" {
		selected = nil
	}
	upstreams, _, err := cm.GetChannelConfig(kind)
	if err != nil {
		return nil, err
	}
	candidates := []gin.H{}
	for _, up := range upstreams {
		if !slices.ContainsFunc(selected, func(pool config.ChannelPool) bool { return pool.ID == up.PoolID }) || config.GetChannelStatus(&up) != config.ChannelStatusActive || up.ExcludeFromConversation {
			continue
		}
		models, channels := config.ResolveUpstreamModelTargets(model, &up)
		if len(models) == 0 {
			models = []string{config.ResolveUpstreamModel(model, &up)}
		}
		for i, actual := range models {
			name := up.Name
			if i < len(channels) && channels[i] != "" {
				found := false
				for _, redirected := range upstreams {
					if redirected.ID == channels[i] {
						found = true
						if target != "" && redirected.PoolID != target {
							return nil, fmt.Errorf("渠道 %s 的模型映射指向分组外渠道 %s，实际请求会被意图约束阻止；请调整映射", up.Name, redirected.Name)
						}
						if config.GetChannelStatus(&redirected) != config.ChannelStatusActive || redirected.ExcludeFromConversation {
							return nil, fmt.Errorf("模型映射目标 %s 未启用或不能处理对话", redirected.Name)
						}
						name = redirected.Name
					}
				}
				if !found {
					return nil, fmt.Errorf("模型映射目标渠道 %s 已不存在", channels[i])
				}
			}
			candidates = append(candidates, gin.H{"channel": name, "model": actual})
		}
	}
	names := func(values []config.ChannelPool) []string {
		out := []string{}
		for _, pool := range values {
			out = append(out, pool.Name)
		}
		return out
	}
	return gin.H{"requestedModel": model, "originalPools": names(original), "selectedPools": names(selected), "candidates": candidates}, nil
}
