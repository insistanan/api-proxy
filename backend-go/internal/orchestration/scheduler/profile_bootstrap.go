package scheduler

import (
	"fmt"
	"slices"
	"strings"

	"insistanan/api-proxy/internal/infra/config"
	"insistanan/api-proxy/internal/storage/metrics"
)

// InitializeProfiles 在启动装配时校验所有渠道的指标依赖，并同步已有画像。
func (s *ChannelScheduler) InitializeProfiles() error {
	if s.configManager == nil || s.profileManager == nil {
		return fmt.Errorf("初始化性能画像缺少配置或画像管理器")
	}
	s.profileManager.SetIdentityValidator(func(identity metrics.ProfileIdentity, baseURL, credentials string) bool {
		located, ok := s.configManager.FindChannelByID(identity.ID)
		return ok && located.Kind == identity.Kind && config.GetChannelStatus(&located.Upstream) != config.ChannelStatusDeleted &&
			slices.Contains(located.Upstream.GetAllBaseURLs(), baseURL) && metrics.ProfileCredentials(located.Upstream.APIKeys) == credentials
	})
	for _, kind := range ChannelKinds() {
		mm := s.metricsManagers[kind]
		if mm == nil {
			return fmt.Errorf("初始化性能画像缺少 %s 指标管理器", kind)
		}
		upstreams, _, err := s.configManager.GetChannelConfig(string(kind))
		if err != nil {
			return err
		}
		syncUpstreamProfiles(mm, s.profileManager, upstreams, string(kind))
	}
	return nil
}

func syncUpstreamProfiles(mm *metrics.MetricsManager, pm *metrics.ProfileManager, upstreams []config.UpstreamConfig, kind string) {
	for channelIdx := range upstreams {
		upstream := &upstreams[channelIdx]
		if len(upstream.APIKeys) == 0 {
			continue
		}
		models := make(map[string]struct{})
		if model := strings.TrimSpace(upstream.DefaultModel); model != "" {
			models[model] = struct{}{}
		}
		for sourceModel, targetModels := range upstream.ModelMapping {
			if source := strings.TrimSpace(sourceModel); source != "" && source != "*" {
				models[source] = struct{}{}
			}
			for _, targetModel := range targetModels {
				if target := strings.TrimSpace(targetModel); target != "" {
					models[target] = struct{}{}
				}
			}
		}
		for _, baseURL := range upstream.GetAllBaseURLs() {
			for model := range models {
				mm.SyncToProfile(pm, baseURL, upstream.APIKeys, model, metrics.ProfileIdentity{Kind: kind, ID: upstream.ID, Index: channelIdx})
			}
		}
	}
}
