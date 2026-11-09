package proxycore

import (
	"context"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/insistanan/api-proxy/internal/config"
	"github.com/insistanan/api-proxy/internal/providers"
	"github.com/insistanan/api-proxy/internal/scheduler"
	"github.com/insistanan/api-proxy/internal/session"
	"github.com/insistanan/api-proxy/internal/types"
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

func TestMissingResponseHistoryIsClientErrorAndDoesNotRotateKeys(t *testing.T) {
	h := newCompatRetryHarness(t)
	manager, err := session.NewPersistentSessionManager(filepath.Join(t.TempDir(), "responses.db"), time.Hour, 0, 0)
	require.NoError(t, err)
	t.Cleanup(manager.Stop)
	provider := &providers.ResponsesProvider{SessionManager: manager}
	upstream := &config.UpstreamConfig{BaseURL: "https://example.invalid", APIKeys: []string{"one", "two"}, ServiceType: "responses"}
	ctx := newAttemptTestContext(context.Background())
	selected := 0
	result := runConfiguredAttempt(t, UpstreamAttempt{
		Context: ctx, EnvConfig: &config.EnvConfig{}, ConfigManager: h.cfgManager,
		MetricsManager: h.metricsManager, ChannelScheduler: h.channelScheduler, Upstream: upstream,
		Kind: scheduler.ChannelKindMessages, APIType: "Messages", RequestedModel: "model",
		RequestBody: []byte(`{"model":"model","previous_response_id":"missing","input":"后缀"}`),
		URLResults:  BuildDefaultURLResults(upstream.GetAllBaseURLs()),
		NextAPIKey: func(up *config.UpstreamConfig, failed map[string]bool) (string, error) {
			selected++
			return rotatingKeySelector(up, failed)
		},
		BuildRequest: func(c *gin.Context, up *config.UpstreamConfig, key string) (*http.Request, error) {
			outcome, err := provider.BuildRequest(providers.RequestInput{
				Ctx: c.Request.Context(), ConversationID: "conv-history", Body: []byte(`{"model":"model","previous_response_id":"missing","input":"后缀"}`),
			}, up, key)
			return outcome.Request, err
		},
		HandleSuccess: func(*gin.Context, *http.Response, *config.UpstreamConfig, string) (*types.Usage, error) {
			t.Fatal("缺失历史不能发送到上游")
			return nil, nil
		},
	})
	require.True(t, result.Handled)
	require.Equal(t, channelHealthNeutral, result.Health)
	require.Equal(t, http.StatusBadRequest, ctx.Writer.Status())
	require.Equal(t, 1, selected)
	for _, key := range upstream.APIKeys {
		require.Nil(t, h.metricsManager.GetKeyMetrics(upstream.BaseURL, key, 0))
	}
}

func registerHealthUpstream(t *testing.T, cfgManager *config.ConfigManager, upstream *config.UpstreamConfig) {
	t.Helper()
	require.NoError(t, cfgManager.AddUpstream(*upstream))
}
