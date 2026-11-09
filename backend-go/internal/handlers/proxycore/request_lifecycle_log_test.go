package proxycore

import (
	"context"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/insistanan/api-proxy/internal/config"
	"github.com/insistanan/api-proxy/internal/logger"
	"github.com/insistanan/api-proxy/internal/ratelimit"
	"github.com/insistanan/api-proxy/internal/scheduler"
	"github.com/insistanan/api-proxy/internal/types"
	"github.com/stretchr/testify/require"
)

func setupLifecycleLogStore(t *testing.T) *logger.Store {
	t.Helper()
	writer, flags := log.Writer(), log.Flags()
	ginWriter, ginErrorWriter := gin.DefaultWriter, gin.DefaultErrorWriter
	store, err := logger.Setup(&logger.Config{DBPath: filepath.Join(t.TempDir(), "logs.db")})
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, logger.CloseDefault())
		log.SetOutput(writer)
		log.SetFlags(flags)
		gin.DefaultWriter, gin.DefaultErrorWriter = ginWriter, ginErrorWriter
	})
	return store
}

func TestCancelledSendNeverReachesUpstream(t *testing.T) {
	var sent atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { sent.Add(1); w.WriteHeader(200) }))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL, strings.NewReader(`{}`))
	require.NoError(t, err)
	_, err = SendRequest(req, &config.UpstreamConfig{}, &config.EnvConfig{}, false, "Messages", "", nil, time.Second)
	require.ErrorIs(t, err, context.Canceled)
	require.Zero(t, sent.Load())
}

func TestSingleModelCancelledAttemptDoesNotSelectKey(t *testing.T) {
	h := newCompatRetryHarness(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	selected := 0
	upstream := &config.UpstreamConfig{BaseURL: "https://example.invalid", APIKeys: []string{"key"}}
	result := (UpstreamAttempt{
		Context: newAttemptTestContext(ctx), EnvConfig: &config.EnvConfig{}, ConfigManager: h.cfgManager,
		MetricsManager: h.metricsManager, ChannelScheduler: h.channelScheduler, Upstream: upstream,
		RequestedModel: "model", URLResults: BuildDefaultURLResults(upstream.GetAllBaseURLs()),
		NextAPIKey: func(*config.UpstreamConfig, map[string]bool) (string, error) { selected++; return "key", nil },
		BuildRequest: func(*gin.Context, *config.UpstreamConfig, string) (*http.Request, error) {
			return nil, errors.New("不应构建请求")
		},
		HandleSuccess: func(*gin.Context, *http.Response, *config.UpstreamConfig, string) (*types.Usage, error) {
			return nil, errors.New("不应处理响应")
		},
	}).TryWithModelMappingFailover()
	require.ErrorIs(t, result.LastError, context.Canceled)
	require.Equal(t, channelHealthNeutral, result.Health)
	require.Zero(t, selected)
}

func TestCoolingBaseURLDoesNotSendOrRotateKeys(t *testing.T) {
	h := newCompatRetryHarness(t)
	var sent atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { sent.Add(1); w.WriteHeader(200) }))
	defer server.Close()
	ratelimit.GetDefaultLimiter().Record429(server.URL, 3*time.Second)
	upstream := &config.UpstreamConfig{BaseURL: server.URL, APIKeys: []string{"one", "two"}}
	selected := 0
	result := (UpstreamAttempt{
		Context: newAttemptTestContext(context.Background()), EnvConfig: &config.EnvConfig{LogLevel: "error"},
		ConfigManager: h.cfgManager, MetricsManager: h.metricsManager, ChannelScheduler: h.channelScheduler,
		Kind: scheduler.ChannelKindMessages, APIType: "Messages", Upstream: upstream,
		URLResults: BuildDefaultURLResults(upstream.GetAllBaseURLs()), RequestBody: []byte(`{}`),
		NextAPIKey: func(*config.UpstreamConfig, map[string]bool) (string, error) { selected++; return "one", nil },
		BuildRequest: func(c *gin.Context, up *config.UpstreamConfig, _ string) (*http.Request, error) {
			return http.NewRequestWithContext(c.Request.Context(), http.MethodPost, up.BaseURL, strings.NewReader(`{}`))
		},
		HandleSuccess: func(*gin.Context, *http.Response, *config.UpstreamConfig, string) (*types.Usage, error) {
			return nil, errors.New("不应发送请求")
		},
	}).TryWithModelMappingFailover()
	require.ErrorIs(t, result.LastError, errRateLimitQueueTimeout)
	require.Equal(t, channelHealthNeutral, result.Health)
	require.Equal(t, 1, selected)
	require.Zero(t, sent.Load())
}

func TestCompatibilityRetryCancellationStopsFurtherRequests(t *testing.T) {
	h := newCompatRetryHarness(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var sent atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sent.Add(1)
		w.WriteHeader(400)
		_, _ = w.Write([]byte(`{"error":{"message":"prompt_cache_key is not supported"}}`))
	}))
	defer server.Close()
	upstream := &config.UpstreamConfig{BaseURL: server.URL, APIKeys: []string{"key-a", "key-b"}, ServiceType: "openai"}
	builds := 0
	result := (UpstreamAttempt{
		Context: newAttemptTestContext(ctx), EnvConfig: &config.EnvConfig{LogLevel: "error"},
		ConfigManager: h.cfgManager, MetricsManager: h.metricsManager, ChannelScheduler: h.channelScheduler,
		Kind: scheduler.ChannelKindMessages, APIType: "Messages", Upstream: upstream, RequestedModel: "model",
		URLResults: BuildDefaultURLResults(upstream.GetAllBaseURLs()), RequestBody: []byte(`{}`), NextAPIKey: rotatingKeySelector,
		BuildRequest: func(c *gin.Context, up *config.UpstreamConfig, _ string) (*http.Request, error) {
			builds++
			if builds == 2 {
				cancel()
			}
			return http.NewRequestWithContext(c.Request.Context(), http.MethodPost, up.BaseURL, strings.NewReader(`{}`))
		},
		HandleSuccess: func(*gin.Context, *http.Response, *config.UpstreamConfig, string) (*types.Usage, error) {
			return nil, errors.New("不应处理成功响应")
		},
	}).TryWithModelMappingFailover()
	require.ErrorIs(t, result.LastError, context.Canceled)
	require.Equal(t, channelHealthNeutral, result.Health)
	require.Equal(t, int32(1), sent.Load())
}

func TestBufferedResponseStillRecordsTrafficWithAttemptID(t *testing.T) {
	store := setupLifecycleLogStore(t)
	h := newCompatRetryHarness(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"ok"}]}`))
	}))
	defer server.Close()
	c := newAttemptTestContext(context.Background())
	upstream := &config.UpstreamConfig{BaseURL: server.URL, APIKeys: []string{"key"}}
	result := (UpstreamAttempt{
		Context: c, EnvConfig: &config.EnvConfig{LogLevel: "error", EnableRequestLogs: true, EnableResponseLogs: true},
		ConfigManager: h.cfgManager, MetricsManager: h.metricsManager, ChannelScheduler: h.channelScheduler,
		Kind: scheduler.ChannelKindMessages, APIType: "Messages", Upstream: upstream,
		URLResults: BuildDefaultURLResults(upstream.GetAllBaseURLs()), NextAPIKey: rotatingKeySelector,
		BuildRequest: func(c *gin.Context, up *config.UpstreamConfig, _ string) (*http.Request, error) {
			return http.NewRequestWithContext(c.Request.Context(), http.MethodPost, up.BaseURL, strings.NewReader(`{}`))
		},
		HandleSuccess: func(_ *gin.Context, response *http.Response, _ *config.UpstreamConfig, _ string) (*types.Usage, error) {
			defer response.Body.Close()
			_, err := io.ReadAll(response.Body)
			return nil, err
		},
	}).TryWithModelMappingFailover()
	require.Nil(t, result.LastError)
	require.NotEmpty(t, result.SuccessKey)
	require.NoError(t, store.Flush())
	rows, err := store.QueryTrafficByRequestID(context.Background(), RequestLogID(c))
	require.NoError(t, err)
	var request, response *logger.TrafficLog
	for i := range rows {
		if rows[i].Phase == logger.PhaseUpstreamRequest {
			request = &rows[i]
		}
		if rows[i].Phase == logger.PhaseUpstreamResponse {
			response = &rows[i]
		}
	}
	require.NotNil(t, request)
	require.NotNil(t, response)
	require.NotEmpty(t, request.AttemptID)
	require.Equal(t, request.AttemptID, response.AttemptID)
	require.Contains(t, response.Body, "ok")
}

func TestCompletedLifecycleSurvivesLateClientDisconnect(t *testing.T) {
	store := setupLifecycleLogStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	c := newAttemptTestContext(ctx)
	BindRequestLogID(c)
	state := newRequestLifecycle(c, []byte(`{}`), time.Now())
	state.Status = "completed"
	cancel()
	finishRequestLifecycle(c, &config.EnvConfig{EnableRequestLogs: true}, "Messages", state)
	require.Equal(t, "completed", state.Status)
	require.NoError(t, store.Flush())
}
