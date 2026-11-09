package proxycore

import (
	"context"
	"net/http"
	"testing"

	"github.com/insistanan/api-proxy/internal/config"
	"github.com/insistanan/api-proxy/internal/scheduler"
	"github.com/insistanan/api-proxy/internal/upstreamcontrol"
	"github.com/stretchr/testify/require"
)

// 旧测试直接构造 attempt；真实准入要求测试上游先登记到配置中。
func runConfiguredAttempt(t *testing.T, attempt UpstreamAttempt) UpstreamAttemptResult {
	t.Helper()
	if attempt.ConfigManager != nil && attempt.Upstream != nil && len(attempt.Upstream.APIKeys) > 0 &&
		attempt.Context != nil && attempt.Context.Request != nil && attempt.Context.Request.Context().Err() == nil {
		require.NoError(t, attempt.ConfigManager.AddUpstream(*attempt.Upstream))
	}
	return attempt.TryWithModelMappingFailover()
}

func TestChannelFailoverRequiresFuzzy(t *testing.T) {
	for _, fuzzy := range []bool{false, true} {
		t.Run(map[bool]string{false: "关闭", true: "开启"}[fuzzy], func(t *testing.T) {
			env := newRunProxyTestEnv(t)
			addTestUpstream(t, env, "first", "https://first.example", "first-key")
			addTestUpstream(t, env, "second", "https://second.example", "second-key")
			ctx := newAttemptTestContext(context.Background())
			calls := 0
			HandleMultiChannelFailover(ctx, env.envCfg, env.channelScheduler, scheduler.ChannelKindMessages, "Messages", "", "model", fuzzy,
				func(*scheduler.SelectionResult) MultiChannelAttemptResult {
					calls++
					return MultiChannelAttemptResult{Attempted: true, Health: channelHealthNeutral, LastError: upstreamcontrol.ErrQueueFull}
				}, nil, nil)
			if fuzzy {
				require.Equal(t, 2, calls)
			} else {
				require.Equal(t, 1, calls)
			}
		})
	}
}

func TestSingleChannelCapacityIsExplicitAndNeutral(t *testing.T) {
	ctx := newAttemptTestContext(context.Background())
	result := localAdmissionResult(ctx, upstreamcontrol.ErrQueueFull, false)
	require.True(t, result.Handled)
	require.Equal(t, channelHealthNeutral, result.Health)
	require.Equal(t, http.StatusServiceUnavailable, ctx.Writer.Status())
}

func TestMultiChannelCapacityCanTransferButBudgetCannot(t *testing.T) {
	ctx := newAttemptTestContext(context.Background())
	result := localAdmissionResult(ctx, upstreamcontrol.ErrQueueTimeout, true)
	require.False(t, result.Handled)
	require.False(t, result.AbortFailover)
	result = localAdmissionResult(ctx, upstreamcontrol.ErrBudget, true)
	require.True(t, result.Handled)
	require.True(t, result.AbortFailover)
}

func registerHealthUpstream(t *testing.T, cfgManager *config.ConfigManager, upstream *config.UpstreamConfig) {
	t.Helper()
	require.NoError(t, cfgManager.AddUpstream(*upstream))
}
