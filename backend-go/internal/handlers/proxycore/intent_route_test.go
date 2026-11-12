package proxycore

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/insistanan/api-proxy/internal/dialogue/intent"
	"github.com/insistanan/api-proxy/internal/infra/config"
	"github.com/insistanan/api-proxy/internal/protocol/types"
	"github.com/stretchr/testify/require"
)

func TestIntentGuardRejectsMappedPoolBeforeBuildingRequest(t *testing.T) {
	env := newHealthAttemptEnv(t)
	attempt := newHealthAttempt(env, "https://unused.invalid", []string{"key"})
	cfg := config.DefaultIntentRouting()
	cfg.Enabled = true
	cfg.Routes = []config.IntentRoute{{ID: "task", PoolID: "default", Patterns: []string{"任务"}}}
	if err := env.cfgManager.UpdateIntentRouting("messages", cfg); err != nil {
		t.Fatal(err)
	}
	service, err := intent.New(env.cfgManager, nil, filepath.Join(t.TempDir(), "state.json"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := service.Close(); err != nil {
			t.Error(err)
		}
	})
	ctx, release, err := service.Begin(context.Background(), "messages", "conv", "req", []byte(`{"input":"任务"}`), "")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	attempt.Context.Request = attempt.Context.Request.WithContext(ctx)
	attempt.Upstream.PoolID = "outside"
	builds := 0
	attempt.BuildRequest = func(*gin.Context, *config.UpstreamConfig, string) (*http.Request, error) { builds++; return nil, nil }
	result := attempt.TryWithModelMappingFailover()
	if builds != 0 || !result.Handled || result.LastError == nil || result.Health != channelHealthNeutral {
		t.Fatalf("跨组候选应在发送前中性拒绝: builds=%d result=%+v", builds, result)
	}
}

func TestIntentContinuationKeepsActualModelFromMappedChannel(t *testing.T) {
	env := newHealthAttemptEnv(t)
	attempt := newHealthAttempt(env, "https://unused.invalid", []string{"key"})
	attempt.Upstream.ID, attempt.Upstream.PoolID, attempt.Upstream.ServiceType = "target", "default", "claude"
	attempt.Upstream.DefaultModel = "channel-default"
	cfg := config.DefaultIntentRouting()
	cfg.Enabled = true
	cfg.Routes = []config.IntentRoute{{ID: "task", PoolID: "default", Patterns: []string{"任务"}}}
	if err := env.cfgManager.UpdateIntentRouting("messages", cfg); err != nil {
		t.Fatal(err)
	}
	service, err := intent.New(env.cfgManager, nil, filepath.Join(t.TempDir(), "state.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := service.Close(); err != nil {
			t.Error(err)
		}
	})
	ctx, release, err := service.Begin(context.Background(), "messages", "conv", "first", []byte(`{"input":"任务"}`), "")
	if err != nil {
		t.Fatal(err)
	}
	if err := intent.TurnFrom(ctx).CommitTarget(attempt.Upstream, "key", "mapped-actual"); err != nil {
		t.Fatal(err)
	}
	release()
	ctx, release, err = service.Begin(context.Background(), "messages", "conv", "next", []byte(`{"messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"id","content":"结果"}]}]}`), "")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	attempt.Context.Request = attempt.Context.Request.WithContext(ctx)
	actual := ""
	attempt.BuildRequest = func(_ *gin.Context, up *config.UpstreamConfig, _ string) (*http.Request, error) {
		actual = config.ResolveUpstreamModel(attempt.RequestedModel, up)
		return nil, &intent.HistoryError{Message: "测试在发送前结束"}
	}
	attempt.TryWithModelMappingFailover()
	if actual != "mapped-actual" {
		t.Fatalf("工具续轮不应改用目标渠道默认模型: %s", actual)
	}
	if attempt.Upstream.DefaultModel != "channel-default" {
		t.Fatal("污染了渠道配置")
	}
}

func TestIntentResponseDeliveryRetryBoundary(t *testing.T) {
	for _, tc := range []struct {
		name                    string
		partial, storageFailure bool
		wantCalls               int
	}{
		{name: "未交付允许换密钥", wantCalls: 2},
		{name: "部分交付禁止重试", partial: true, wantCalls: 1},
		{name: "恢复失败禁止重试", storageFailure: true, wantCalls: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := newHealthAttemptEnv(t)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(w, `{"ok":true}`)
			}))
			defer server.Close()
			attempt := newHealthAttempt(env, server.URL, []string{"key-a", "key-b"})
			attempt.Upstream.PoolID = "default"
			attempt.Upstream.ID = "channel"
			cfg := config.DefaultIntentRouting()
			cfg.Enabled = true
			cfg.Routes = []config.IntentRoute{{ID: "task", PoolID: "default", Patterns: []string{"任务"}}}
			require.NoError(t, env.cfgManager.UpdateIntentRouting("messages", cfg))
			service, err := intent.New(env.cfgManager, nil, filepath.Join(t.TempDir(), "state.db"), nil)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, service.Close()) })
			ctx, release, err := service.Begin(context.Background(), "messages", "conv", "req", []byte(`{"input":"任务"}`), "")
			require.NoError(t, err)
			defer release()
			attempt.Context.Request = attempt.Context.Request.WithContext(ctx)
			calls := 0
			attempt.HandleSuccess = func(c *gin.Context, _ *http.Response, _ *config.UpstreamConfig, key string) (*types.Usage, error) {
				calls++
				if key == "key-a" {
					if tc.partial {
						c.Data(http.StatusOK, "text/event-stream", []byte("data: partial\n\n"))
					}
					if tc.storageFailure {
						require.NoError(t, service.Close())
					}
					return nil, errors.New("响应读取失败")
				}
				c.Data(http.StatusOK, "application/json", []byte(`{"ok":true}`))
				return &types.Usage{}, nil
			}
			result := attempt.TryWithModelMappingFailover()
			require.Equal(t, tc.wantCalls, calls)
			require.True(t, result.Handled)
			if tc.wantCalls == 2 {
				require.Equal(t, "key-b", result.SuccessKey)
				require.Len(t, service.Events("messages"), 1)
				require.True(t, service.Events("messages")[0].Success)
			} else {
				require.Empty(t, result.SuccessKey)
				require.Error(t, intent.TurnFrom(ctx).CheckTarget(attempt.Upstream, "key-b", "test-model"))
			}
			if tc.storageFailure {
				require.Equal(t, http.StatusInternalServerError, attempt.Context.Writer.Status())
				require.Equal(t, channelHealthNeutral, result.Health)
			}
		})
	}
}
