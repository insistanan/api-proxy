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
	"github.com/insistanan/api-proxy/internal/infra/config"
	"github.com/insistanan/api-proxy/internal/orchestration/scheduler"
	"github.com/insistanan/api-proxy/internal/orchestration/upstreamcontrol"
	"github.com/insistanan/api-proxy/internal/protocol/types"
	"github.com/insistanan/api-proxy/internal/storage/logger"
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
	result := runConfiguredAttempt(t, UpstreamAttempt{
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
	})
	require.ErrorIs(t, result.LastError, context.Canceled)
	require.Equal(t, channelHealthNeutral, result.Health)
	require.Zero(t, selected)
}

func TestUnavailableCombinationRotatesWithinChannel(t *testing.T) {
	for _, mode := range []string{"其他Key", "其他URL", "全部不可用", "预算耗尽"} {
		t.Run(mode, func(t *testing.T) {
			h := newCompatRetryHarness(t)
			var sent atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				sent.Add(1)
				_, _ = io.WriteString(w, `{"ok":true}`)
			}))
			defer server.Close()
			upstream := &config.UpstreamConfig{BaseURL: server.URL, APIKeys: []string{"one", "two"}, ServiceType: "claude"}
			if mode == "其他URL" {
				upstream.BaseURLs = []string{"https://unavailable.invalid", server.URL}
				upstream.APIKeys = []string{"one"}
			}
			registerHealthUpstream(t, h.cfgManager, upstream)
			scope := upstreamcontrol.WithScope(context.Background())
			bound := h.cfgManager.UpstreamRequestContext(scope, upstream, "one")
			candidate, ok := upstreamcontrol.CandidateFromContext(bound)
			require.True(t, ok)
			available := upstream.Clone()
			available.BaseURL = server.URL
			available.BaseURLs = nil
			key := "two"
			if mode == "其他URL" {
				key = "one"
			}
			rules := map[upstreamcontrol.Identity]int{available.RequestIdentity(key): 1}
			if mode == "全部不可用" {
				rules = map[upstreamcontrol.Identity]int{}
			}
			candidate.Controller.Reconcile(rules)
			if mode == "预算耗尽" {
				budgetCtx := h.cfgManager.UpstreamRequestContext(scope, available, key)
				for range upstreamcontrol.MaxAttempts {
					lease, _, _, err := upstreamcontrol.AcquireRequest(budgetCtx)
					require.NoError(t, err)
					lease.Release()
				}
			}
			selected := 0
			ctx := newAttemptTestContext(scope)
			result := (UpstreamAttempt{
				Context: ctx, EnvConfig: &config.EnvConfig{LogLevel: "error"},
				ConfigManager: h.cfgManager, MetricsManager: h.metricsManager, ChannelScheduler: h.channelScheduler,
				Kind: scheduler.ChannelKindMessages, APIType: "Messages", Upstream: upstream,
				URLResults: BuildDefaultURLResults(upstream.GetAllBaseURLs()), RequestBody: []byte(`{}`),
				NextAPIKey: func(up *config.UpstreamConfig, failed map[string]bool) (string, error) {
					selected++
					return rotatingKeySelector(up, failed)
				},
				BuildRequest: func(c *gin.Context, up *config.UpstreamConfig, key string) (*http.Request, error) {
					req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodPost, up.GetEffectiveBaseURL(), strings.NewReader(`{}`))
					if err == nil {
						req.Header.Set("X-Api-Key", key)
					}
					return req, err
				},
				HandleSuccess: func(c *gin.Context, resp *http.Response, _ *config.UpstreamConfig, _ string) (*types.Usage, error) {
					defer resp.Body.Close()
					_, err := io.Copy(io.Discard, resp.Body)
					if err == nil {
						c.Status(http.StatusOK)
					}
					return nil, err
				},
			}).TryWithModelMappingFailover()
			if mode == "全部不可用" || mode == "预算耗尽" {
				want := upstreamcontrol.ErrRemoved
				if mode == "预算耗尽" {
					want = upstreamcontrol.ErrBudget
					require.Equal(t, 1, selected)
				}
				require.ErrorIs(t, result.LastError, want)
				require.True(t, result.Handled)
				require.Equal(t, channelHealthNeutral, result.Health)
				require.Equal(t, http.StatusServiceUnavailable, ctx.Writer.Status())
				require.Zero(t, sent.Load())
			} else {
				require.NoError(t, result.LastError)
				require.Equal(t, key, result.SuccessKey)
				require.Equal(t, int32(1), sent.Load())
			}
			for _, url := range upstream.GetAllBaseURLs() {
				for _, key := range upstream.APIKeys {
					if stats := h.metricsManager.GetKeyMetrics(url, key, 0); stats != nil {
						require.Zero(t, stats.FailureCount)
					}
				}
			}
		})
	}
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
	result := runConfiguredAttempt(t, UpstreamAttempt{
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
	})
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
	result := runConfiguredAttempt(t, UpstreamAttempt{
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
	})
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

type cancelOnCloseBody struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (b *cancelOnCloseBody) Close() error {
	err := b.ReadCloser.Close()
	b.cancel()
	return err
}

func TestDeliveredResponseSurvivesLateClientDisconnect(t *testing.T) {
	for _, cancelOnClose := range []bool{false, true} {
		name := "交付后取消"
		if cancelOnClose {
			name = "关闭上游响应时取消"
		}
		t.Run(name, func(t *testing.T) {
			store := setupLifecycleLogStore(t)
			h := newCompatRetryHarness(t)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, "data: {\"type\":\"response.completed\"}\n\n")
			}))
			defer server.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			c := newAttemptTestContext(ctx)
			BindRequestLogID(c)
			state := newRequestLifecycle(c, []byte(`{}`), time.Now())
			env := &config.EnvConfig{LogLevel: "error", EnableRequestLogs: true}
			upstream := &config.UpstreamConfig{BaseURL: server.URL, APIKeys: []string{"key"}, ServiceType: "responses"}
			result := runConfiguredAttempt(t, UpstreamAttempt{
				Context: c, EnvConfig: env, ConfigManager: h.cfgManager,
				MetricsManager: h.metricsManager, ChannelScheduler: h.channelScheduler,
				Kind: scheduler.ChannelKindMessages, APIType: "Messages", Upstream: upstream,
				URLResults: BuildDefaultURLResults(upstream.GetAllBaseURLs()), NextAPIKey: rotatingKeySelector,
				BuildRequest: func(c *gin.Context, up *config.UpstreamConfig, _ string) (*http.Request, error) {
					return http.NewRequestWithContext(c.Request.Context(), http.MethodPost, up.BaseURL, strings.NewReader(`{}`))
				},
				HandleSuccess: func(c *gin.Context, response *http.Response, _ *config.UpstreamConfig, _ string) (*types.Usage, error) {
					body, err := io.ReadAll(response.Body)
					require.NoError(t, err)
					_, err = c.Writer.Write(body)
					require.NoError(t, err)
					if cancelOnClose {
						response.Body = &cancelOnCloseBody{ReadCloser: response.Body, cancel: cancel}
					} else {
						cancel()
					}
					return &types.Usage{InputTokens: 10, OutputTokens: 8}, nil
				},
			})
			require.NoError(t, result.LastError)
			require.Equal(t, "key", result.SuccessKey)
			require.Equal(t, channelHealthSuccess, result.Health)
			require.Equal(t, 8, result.Usage.OutputTokens)
			finishRequestLifecycle(c, env, "Messages", state)
			require.Equal(t, "completed", state.Status)
			require.Empty(t, state.ErrorType)
			require.NoError(t, store.Flush())
		})
	}
}
