package intent

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"insistanan/api-proxy/internal/infra/config"
)

// ActivePoolChannels 只核对静态可执行条件；实时熔断与请求模型兼容性仍由调度器判断。
func (s *Service) ActivePoolChannels(kind string) (map[string]int, error) {
	upstreams, _, err := s.Cfg.GetChannelConfig(kind)
	if err != nil {
		return nil, err
	}
	counts := map[string]int{}
	for _, pool := range s.Cfg.GetChannelPools(kind) {
		counts[pool.ID] = 0
	}
	for _, up := range upstreams {
		if config.GetChannelStatus(&up) != config.ChannelStatusActive || up.ExcludeFromConversation || len(up.APIKeys) == 0 {
			continue
		}
		if up.PoolID == config.FreeChannelPoolID && !s.freeEnabled() {
			continue
		}
		counts[up.PoolID]++
	}
	return counts, nil
}

func (s *Service) ValidateAvailability(ctx context.Context, kind string, cfg config.IntentRoutingConfig) error {
	if err := config.ValidateIntentRouting(cfg, s.Cfg.GetChannelPools(kind)); err != nil {
		return err
	}
	counts, err := s.ActivePoolChannels(kind)
	if err != nil {
		return err
	}
	pools := []string{}
	if cfg.Strategy == "auto" {
		for _, group := range cfg.Groups {
			pools = append(pools, group.PoolID)
		}
	} else {
		for _, route := range cfg.Routes {
			pools = append(pools, route.PoolID)
		}
	}
	if cfg.Unmatched == "fallback" {
		pools = append(pools, cfg.FallbackPoolID)
	}
	for _, poolID := range pools {
		if counts[poolID] == 0 {
			name := poolID
			for _, pool := range s.Cfg.GetChannelPools(kind) {
				if pool.ID == poolID {
					name = pool.Name
				}
			}
			return fmt.Errorf("分组 %s 没有启用且已配置密钥的对话渠道", name)
		}
	}
	assistant := slices.Contains(config.IntentRecognitionStages(cfg), "assistant")
	if cfg.RecognitionMode == "" {
		assistant = slices.Contains([]string{"auto", "complexity", "adaptive"}, cfg.Strategy) && cfg.Classifier != "heuristic"
	}
	if !assistant {
		return nil
	}
	if s.Caller == nil {
		return fmt.Errorf("辅助分类服务尚未初始化")
	}
	options, err := s.Caller.ChannelOptions(ctx)
	if err != nil {
		return err
	}
	decisions := cfg.Classifier == "decisions" || cfg.Classifier == "systemone"
	for _, option := range options {
		if (option.Kind == "decisions") != decisions {
			continue
		}
		if option.ID == cfg.ClassifierChannel || cfg.ClassifierChannel == "" || option.SharedKey == "kilo" && strings.HasPrefix(cfg.ClassifierChannel, "builtin-kilo-") {
			return nil
		}
	}
	return fmt.Errorf("辅助分类渠道不存在、未启用或当前不可用，请重新选择渠道")
}
