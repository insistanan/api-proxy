package proxycore

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"insistanan/api-proxy/internal/dialogue/conversation"
	"insistanan/api-proxy/internal/dialogue/routing/intent"
	"insistanan/api-proxy/internal/infra/config"
	"insistanan/api-proxy/internal/infra/upstreamreq"
	"insistanan/api-proxy/internal/orchestration/scheduler"
)

func TestSummaryPoolKeepsMainConversationAndDoesNotFallback(t *testing.T) {
	env := newRunProxyTestEnv(t)
	registry := conversation.NewRegistry()
	t.Cleanup(func() { registry.Stop() })
	env.channelScheduler.SetConversationRegistry(registry)
	main := newRecordingUpstreamServer(t, nil)
	small := newRecordingUpstreamServer(t, nil)
	_, err := env.cfgManager.AddChannel("messages", config.UpstreamConfig{ID: "main", Name: "main", ServiceType: "claude", BaseURL: main.URL(), APIKeys: []string{"main-key"}, Compaction: config.CompactionConfig{SummaryPool: true}})
	require.NoError(t, err)
	_, err = env.cfgManager.AddChannel("messages", config.UpstreamConfig{ID: "summary", Name: "summary", ServiceType: "openai", BaseURL: small.URL(), APIKeys: []string{"small-key"}, ExcludeFromConversation: true, Compaction: config.CompactionConfig{SummaryCapable: true, SummaryModel: "cheap"}})
	require.NoError(t, err)
	recorder := &specRecorder{}
	spec := recorder.newSpec()
	originalBuild := spec.BuildUpstreamRequest
	spec.BuildUpstreamRequest = func(c *gin.Context, up *config.UpstreamConfig, key string, body []byte) (*http.Request, error) {
		if c.GetHeader("X-Api-Proxy-Task") == "compact" {
			require.True(t, c.GetBool(upstreamreq.ContextKeyAuxiliaryTask))
			require.Empty(t, c.GetString(upstreamreq.ContextKeyConversationUserID))
			require.Nil(t, currentCacheKeepalive(c))
			require.Equal(t, "cheap", config.ResolveUpstreamModel("main-model", up))
		}
		return originalBuild(c, up, key, body)
	}
	invoke := func(task string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("POST", "/v1/messages", strings.NewReader(`{"model":"main-model","messages":[{"role":"user","content":"独立文本"}]}`))
		c.Request.Header.Set("Authorization", "Bearer test-access-key")
		c.Request.Header.Set("Content-Type", "application/json")
		c.Request.Header.Set("X-Conversation-Id", "main-conversation")
		c.Request.Header.Set("X-Api-Proxy-Task", task)
		RunProxyRequest(c, env.envCfg, env.cfgManager, env.channelScheduler, spec)
		return w
	}
	require.Equal(t, 200, invoke("").Code)
	before, err := registry.QueryPage(context.Background(), conversation.ListOptions{Page: 1, PageSize: 10})
	require.NoError(t, err)
	require.Len(t, before.Conversations, 1)
	response := invoke("compact")
	require.Equal(t, 200, response.Code, response.Body.String())
	require.Equal(t, "summary_pool", response.Header().Get("X-Api-Proxy-Compaction-Route"))
	require.Len(t, main.receivedKeys(), 1)
	require.Equal(t, []string{"small-key"}, small.receivedKeys())
	after, err := registry.Lookup(context.Background(), before.Conversations[0].ID)
	require.NoError(t, err)
	require.Equal(t, before.Conversations[0].RequestCount, after.RequestCount)
	require.Equal(t, before.Conversations[0].LastResolved, after.LastResolved)
	require.Equal(t, before.Conversations[0].LastResolvedModel, after.LastResolvedModel)
	status := config.ChannelStatusSuspended
	_, err = env.cfgManager.UpdateChannel("messages", 1, config.UpstreamUpdate{Status: &status})
	require.NoError(t, err)
	response = invoke("compact")
	require.Equal(t, 503, response.Code)
	require.Contains(t, response.Body.String(), "COMPACTION_UNAVAILABLE")
	require.Len(t, main.receivedKeys(), 1, "摘要池失败不能静默回到主模型")
}

func TestNativeCompactionOverrideOnlyAppliesToIndependentControl(t *testing.T) {
	for _, tc := range []struct{ body, kind, model string }{
		{`{"model":"main","compaction":{"type":"summarize"}}`, "native", "cheap"},
		{`{"model":"main","context_management":{"edits":[{"type":"compact_20260112"}]}}`, "automatic", "main"},
		{`{"model":"main","messages":[{"role":"assistant","content":[{"type":"compaction","content":"summary","signature":"signed"}]}]}`, "history", "main"},
	} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("POST", "/v1/messages", strings.NewReader(tc.body))
		r, err := BindCompactionRequest(c, "messages", []byte(tc.body))
		require.NoError(t, err)
		require.Equal(t, tc.kind, r.Kind)
		up := &config.UpstreamConfig{ServiceType: "claude", BaseURL: "https://example.invalid", APIKeys: []string{"key"}, Compaction: config.CompactionConfig{NativeSourceModel: "main", NativeModel: "cheap", NativeCompatibilityConfirmed: true}}
		attempt, err := (UpstreamAttempt{Context: c, Kind: scheduler.ChannelKindMessages, Upstream: up, RequestedModel: "main"}).prepareCompactionAttempt()
		require.NoError(t, err)
		require.Equal(t, tc.model, config.ResolveUpstreamModel("main", attempt.Upstream))
		require.Empty(t, up.DefaultModel, "不能改写持久配置")
		up.ServiceType = "openai"
		_, err = (UpstreamAttempt{Context: c, Kind: scheduler.ChannelKindMessages, Upstream: up, RequestedModel: "main"}).prepareCompactionAttempt()
		require.Error(t, err)
	}
}

func TestNativeCompaction_KeepsActualModelAfterMappingFailover(t *testing.T) {
	for _, tc := range []struct {
		name, observed, committed, source, override, want string
	}{
		{name: "沿用观测模型", observed: "B", want: "B"},
		{name: "按实际模型替换", observed: "B", source: "B", override: "cheap", want: "cheap"},
		{name: "优先已交付模型", observed: "A", committed: "B", want: "B"},
		{name: "按已交付模型替换", observed: "A", committed: "B", source: "B", override: "cheap", want: "cheap"},
		{name: "拒绝错误模型对", observed: "B", source: "A", override: "cheap"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := newHealthAttemptEnv(t)
			attempt := newHealthAttempt(env, "https://unused.invalid", []string{"key"})
			compaction := config.CompactionConfig{}
			if tc.override != "" {
				compaction = config.CompactionConfig{NativeSourceModel: tc.source, NativeModel: tc.override, NativeCompatibilityConfirmed: true}
			}
			protocol := "claude"
			_, err := env.cfgManager.UpdateChannel("messages", 0, config.UpstreamUpdate{
				ServiceType: &protocol, ModelMapping: map[string][]string{"test-model": {"A", "B"}}, Compaction: &compaction,
			})
			require.NoError(t, err)
			channels, _, err := env.cfgManager.GetChannelConfig("messages")
			require.NoError(t, err)
			up := channels[0]
			registry := conversation.NewRegistry()
			t.Cleanup(func() { require.NoError(t, registry.Stop()) })
			env.channelScheduler.SetConversationRegistry(registry)
			record, err := registry.ObserveRequestWithError(context.Background(), conversation.Observation{APIKind: "messages", Model: "test-model"})
			require.NoError(t, err)
			registry.MarkAttempt(record.ID, "messages", 0, up.Name, "test-model", tc.observed, false)
			registry.MarkComplete(record.ID, "messages")
			body := []byte(`{"model":"test-model","compaction":{"type":"summarize"},"messages":[{"role":"user","content":"历史"}]}`)
			_, err = BindCompactionRequest(attempt.Context, "messages", body)
			require.NoError(t, err)
			if tc.committed != "" {
				cfg := config.DefaultIntentRouting()
				cfg.Enabled = true
				cfg.Routes = []config.IntentRoute{{ID: "task", PoolID: "default", Patterns: []string{"任务"}}}
				require.NoError(t, env.cfgManager.UpdateIntentRouting("messages", cfg))
				service, err := intent.New(env.cfgManager, nil, filepath.Join(t.TempDir(), "intent.db"), nil)
				require.NoError(t, err)
				t.Cleanup(func() { require.NoError(t, service.Close()) })
				ctx, release, err := service.Begin(context.Background(), "messages", record.ID, "main", []byte(`{"input":"任务"}`), "")
				require.NoError(t, err)
				require.NoError(t, intent.TurnFrom(ctx).CommitTarget(&up, "key", tc.committed))
				require.NoError(t, intent.TurnFrom(ctx).Complete(true))
				release()
				ctx, release, err = service.BeginProtocolControl(context.Background(), "messages", record.ID, "compact", body)
				require.NoError(t, err)
				defer release()
				attempt.Context.Request = attempt.Context.Request.WithContext(ctx)
			}
			owner, _, err := ResolveCompactionSource(attempt.Context, env.cfgManager, env.channelScheduler, scheduler.ChannelKindMessages, body, "test-model", record.ID)
			require.NoError(t, err)
			// 获取许可会重新读取渠道，不能丢掉主会话的实际模型。
			selection, err := env.channelScheduler.SelectMappedChannel(scheduler.ChannelKindMessages, owner.ID)
			require.NoError(t, err)
			defer selection.Permit.Release()
			attempt.Upstream, attempt.Permit = selection.Upstream, selection.Permit
			actual := ""
			attempt.BuildRequest = func(_ *gin.Context, candidate *config.UpstreamConfig, _ string) (*http.Request, error) {
				actual = config.ResolveUpstreamModel(attempt.RequestedModel, candidate)
				return nil, &intent.HistoryError{Message: "测试在发送前结束"}
			}
			result := attempt.TryWithModelMappingFailover()
			require.Equal(t, tc.want, actual)
			if tc.want == "" {
				require.ErrorContains(t, result.LastError, "模型对")
			} else {
				require.ErrorContains(t, result.LastError, "测试在发送前结束")
			}
			channels, _, err = env.cfgManager.GetChannelConfig("messages")
			require.NoError(t, err)
			require.Equal(t, up.ModelMapping, channels[0].ModelMapping)
			require.Empty(t, channels[0].DefaultModel)
		})
	}
}
