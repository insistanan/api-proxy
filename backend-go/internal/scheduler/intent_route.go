package scheduler

import (
	"context"
	"fmt"

	"github.com/insistanan/api-proxy/internal/config"
)

type intentRouteKey struct{}

// PoolConstraint 只作用于当前业务请求；内部分类调用不继承该选路约束。
type PoolConstraint struct {
	PoolID             string
	ChannelID          string
	PreferredChannelID string
}

func WithPoolConstraint(ctx context.Context, route PoolConstraint) context.Context {
	return context.WithValue(ctx, intentRouteKey{}, route)
}

func PoolConstraintFrom(ctx context.Context) PoolConstraint {
	if ctx == nil {
		return PoolConstraint{}
	}
	route, _ := ctx.Value(intentRouteKey{}).(PoolConstraint)
	return route
}

func (s *ChannelScheduler) ActiveChannelCountForRequest(ctx context.Context, kind ChannelKind, model string) int {
	route := PoolConstraintFrom(ctx)
	if route.PoolID == "" {
		return s.GetActiveChannelCountForModel(kind, model)
	}
	if route.ChannelID != "" {
		return 1
	}
	return len(s.getActiveChannels(kind, route.PoolID))
}

func (s *ChannelScheduler) poolRouteForRequest(ctx context.Context, kind ChannelKind, model string) ([]config.ChannelPool, error) {
	route := PoolConstraintFrom(ctx)
	if route.PoolID == "" {
		return config.SelectChannelPoolRoute(s.getChannelPools(kind), model)
	}
	for _, pool := range s.getChannelPools(kind) {
		if pool.ID == route.PoolID {
			return []config.ChannelPool{pool}, nil
		}
	}
	return nil, fmt.Errorf("意图路由分组 %q 已不存在", route.PoolID)
}

// IntentSingleChannel 保持单渠道模式的配置顺序，不取得或重复占用调度许可。
func (s *ChannelScheduler) IntentSingleChannel(ctx context.Context, kind ChannelKind) (*config.UpstreamConfig, int, error) {
	route := PoolConstraintFrom(ctx)
	channels := s.preferIntentChannel(ctx, kind, s.getActiveChannels(kind, route.PoolID))
	for _, ch := range channels {
		up := s.getUpstreamByIndex(ch.Index, kind)
		if ch.Status == "active" && up != nil && (route.ChannelID == "" || up.ID == route.ChannelID) {
			return up, ch.Index, nil
		}
	}
	return nil, 0, fmt.Errorf("意图路由分组 %q 没有符合会话固定约束的活跃渠道", route.PoolID)
}

func (s *ChannelScheduler) preferIntentChannel(ctx context.Context, kind ChannelKind, channels []ChannelInfo) []ChannelInfo {
	id := PoolConstraintFrom(ctx).PreferredChannelID
	if id == "" {
		return channels
	}
	for i, ch := range channels {
		up := s.getUpstreamByIndex(ch.Index, kind)
		if up != nil && up.ID == id {
			result := append([]ChannelInfo{ch}, channels[:i]...)
			return append(result, channels[i+1:]...)
		}
	}
	return channels
}
