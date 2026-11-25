package scheduler

import "github.com/insistanan/api-proxy/internal/orchestration/cachekeepalive"

// 保活组件由装配层创建和停止，调度器只为代理入口提供同一实例。
func (s *ChannelScheduler) SetCacheKeepalive(manager *cachekeepalive.Manager) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cacheKeepalive = manager
}

func (s *ChannelScheduler) CacheKeepalive() *cachekeepalive.Manager {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cacheKeepalive
}
