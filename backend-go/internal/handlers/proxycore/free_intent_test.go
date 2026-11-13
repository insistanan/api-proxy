package proxycore

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/insistanan/api-proxy/internal/dialogue/intent"
	"github.com/insistanan/api-proxy/internal/infra/config"
	"github.com/insistanan/api-proxy/internal/orchestration/scheduler"
	"github.com/stretchr/testify/require"
)

func TestIntentSelectedFreePoolReusesFreeModelCandidates(t *testing.T) {
	for _, kind := range []string{"messages", "responses", "chat", "gemini"} {
		t.Run(kind, func(t *testing.T) {
			env := newHealthAttemptEnv(t)
			require.NoError(t, env.cfgManager.EnsureFreeChannels())
			require.NoError(t, env.cfgManager.SetKiloModels([]string{"model-a", "model-b", "model-c", config.KiloAutoModelID}, ""))
			cfg := config.DefaultIntentRouting()
			cfg.Enabled, cfg.Strategy = true, "auto"
			cfg.Groups = []config.IntentGroupCapability{{PoolID: config.FreeChannelPoolID, Tasks: []string{"code_generation"}, Tiers: []int{1, 2}}}
			require.NoError(t, env.cfgManager.UpdateIntentRouting(kind, cfg))
			service, err := intent.New(env.cfgManager, nil, filepath.Join(t.TempDir(), "intent.db"), nil)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, service.Close()) })
			up, index, err := env.cfgManager.GetCurrentUpstreamWithIndexForModelForKind(kind, config.FreeModelID)
			require.NoError(t, err)
			c := newAttemptTestContext(context.Background())
			require.Error(t, validateFreeRoute(c, env.cfgManager, scheduler.ChannelKind(kind), "client-alias", &index))
			require.NoError(t, validateFreeRoute(c, env.cfgManager, scheduler.ChannelKind(kind), config.FreeModelID, &index))
			attempt := UpstreamAttempt{Context: c, Upstream: up, RequestedModel: "client-alias"}
			require.False(t, attempt.isFreeAttempt(), "普通请求不能只凭免费上游标记获得授权")
			ctx, release, err := service.Begin(context.Background(), kind, "auto-free", "first", []byte(`{"input":"实现一个并发队列"}`), "")
			require.NoError(t, err)
			c.Request = c.Request.WithContext(ctx)
			require.NoError(t, validateFreeRoute(c, env.cfgManager, scheduler.ChannelKind(kind), "client-alias", &index))
			require.True(t, attempt.isFreeAttempt())
			prepared, err := attempt.prepareFreeModels()
			require.NoError(t, err)
			require.Equal(t, []string{"model-a", "model-b", "model-c"}, prepared.Upstream.ModelMapping["client-alias"])
			require.True(t, prepared.AllowModelFailover)
			require.NotContains(t, up.ModelMapping, "client-alias", "不能污染共享免费模型配置")
			require.NoError(t, intent.TurnFrom(ctx).CommitTarget(up, "", "model-b"))
			require.NoError(t, intent.TurnFrom(ctx).Complete(true))
			release()

			ctx, release, err = service.Begin(context.Background(), kind, "auto-free", "tool", []byte(`{"messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"a","content":"结果"}]}]}`), "")
			require.NoError(t, err)
			defer release()
			c.Request = c.Request.WithContext(ctx)
			prepared, err = attempt.prepareFreeModels()
			require.NoError(t, err)
			require.Equal(t, "model-b", prepared.Upstream.ModelMapping["client-alias"][0], "工具历史须优先原实际模型")
			withoutPrevious := up.Clone()
			withoutPrevious.ModelMapping = map[string][]string{config.FreeModelID: {"model-a"}}
			attempt.Upstream = withoutPrevious
			_, err = attempt.prepareFreeModels()
			require.ErrorContains(t, err, "原会话模型已移除")
		})
	}
}
