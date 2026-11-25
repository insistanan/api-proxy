package proxycore

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/insistanan/api-proxy/internal/dialogue/conversation"
	"github.com/insistanan/api-proxy/internal/infra/config"
	"github.com/insistanan/api-proxy/internal/orchestration/cachekeepalive"
	"github.com/insistanan/api-proxy/internal/orchestration/scheduler"
	"github.com/insistanan/api-proxy/internal/protocol/types"
	"github.com/stretchr/testify/require"
)

func TestCacheKeepaliveKey_UsesStableConversationAndAgent(t *testing.T) {
	identity := conversation.Identity{ClientFamily: "claude-code", ExplicitID: "session", ScopeID: "old-cache-key"}
	key := cacheKeepaliveKey(identity, "access-key")
	identity.ScopeID = "new-cache-key"
	require.Equal(t, key, cacheKeepaliveKey(identity, "access-key"))
	identity.AgentID = "subagent"
	require.NotEqual(t, key, cacheKeepaliveKey(identity, "access-key"))
	identity.AgentID = ""
	require.NotEqual(t, key, cacheKeepaliveKey(identity, "other-access-key"))
	identity.ExplicitID = "other-session"
	require.NotEqual(t, key, cacheKeepaliveKey(identity, "access-key"))
	identity.ExplicitID = "session"
	identity.ClientFamily = "codex"
	require.NotEqual(t, key, cacheKeepaliveKey(identity, "access-key"))
}

func TestCacheKeepalive_ProxyAttemptCapturesFinalRequestAndUsage(t *testing.T) {
	h := newCompatRetryHarness(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		_, _ = w.Write([]byte(`{"type":"message","content":[{"type":"text","text":"ok"}]}`))
	}))
	defer server.Close()
	upstream := &config.UpstreamConfig{ID: "keepalive-test", Name: "缓存保活测试", BaseURL: server.URL,
		ServiceType: "claude", APIKeys: []string{"test-key"}, MaxConcurrency: 5,
		CacheKeepalive: config.CacheKeepaliveConfig{Enabled: true, IntervalSeconds: 280, MaxAttempts: 2,
			Prompt: "接下来应该怎么做？", MinCacheHitPercent: 90, MaxOutputTokens: 16}}
	require.NoError(t, h.cfgManager.AddUpstream(*upstream))
	manager := cachekeepalive.New(h.cfgManager, nil)
	t.Cleanup(func() { manager.Stop() })
	h.channelScheduler.SetCacheKeepalive(manager)
	identity := conversation.Identity{ClientFamily: "claude-code", ExplicitID: "session", Source: "session_header"}
	for _, tc := range []struct {
		identity conversation.Identity
		kind     scheduler.ChannelKind
		quick    bool
	}{
		{identity, scheduler.ChannelKindMessages, true},
		{identity, scheduler.ChannelKindImages, false},
		{conversation.Identity{ScopeID: "only-window"}, scheduler.ChannelKindMessages, false},
		{conversation.Identity{ExplicitID: "response", Source: "previous_response"}, scheduler.ChannelKindMessages, false},
	} {
		require.Nil(t, beginCacheKeepalive(newAttemptTestContext(context.Background()), h.channelScheduler, tc.kind, tc.identity, "access", tc.quick))
	}
	c := newAttemptTestContext(context.Background())
	turn := beginCacheKeepalive(c, h.channelScheduler, scheduler.ChannelKindMessages, identity, "access", false)
	require.NotNil(t, turn)
	defer turn.End()
	turn.BindConversation("conversation-test")
	attempt := UpstreamAttempt{Context: c, EnvConfig: &config.EnvConfig{LogLevel: "error"},
		ConfigManager: h.cfgManager, ChannelScheduler: h.channelScheduler, MetricsManager: h.metricsManager,
		Kind: scheduler.ChannelKindMessages, APIType: "Messages", Upstream: upstream, RequestedModel: "claude-test",
		RequestBody: []byte(`{}`), URLResults: BuildDefaultURLResults(upstream.GetAllBaseURLs()), NextAPIKey: rotatingKeySelector,
		LogContext: AttemptLogContext{Model: "claude-test", ConversationID: "conversation-test"},
		BuildRequest: func(c *gin.Context, up *config.UpstreamConfig, _ string) (*http.Request, error) {
			return http.NewRequestWithContext(c.Request.Context(), http.MethodPost, up.BaseURL+"/v1/messages",
				strings.NewReader(`{"model":"claude-test","max_tokens":32,"messages":[{"role":"user","content":[{"type":"text","text":"test","cache_control":{"type":"ephemeral"}}]}]}`))
		},
		HandleSuccess: func(_ *gin.Context, response *http.Response, _ *config.UpstreamConfig, _ string) (*types.Usage, error) {
			_, err := io.Copy(io.Discard, response.Body)
			return &types.Usage{InputTokens: 10, CacheCreationInputTokens: 1000, OutputTokens: 1}, err
		},
	}
	result := attempt.TryWithModelMappingFailover()
	require.NotEmpty(t, result.SuccessKey)
	turn.End()
	status := manager.Status(upstream.ID)
	require.Len(t, status, 1)
	require.Equal(t, "waiting", status[0].State)
	require.Equal(t, "conversation-test", status[0].ConversationID)
	require.Zero(t, status[0].Attempts)
	manager.Forget("conversation-test")
	require.Empty(t, manager.Status(upstream.ID))
}
