package proxycore

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/insistanan/api-proxy/internal/infra/config"
	"github.com/insistanan/api-proxy/internal/orchestration/availability"
	"github.com/insistanan/api-proxy/internal/orchestration/scheduler"
	"github.com/insistanan/api-proxy/internal/protocol/types"
	"github.com/insistanan/api-proxy/internal/storage/logger"
	"github.com/insistanan/api-proxy/internal/storage/metrics"
	"github.com/stretchr/testify/require"
)

func TestAvailabilityRecordsEachRetryBeforeFinalSuccess(t *testing.T) {
	for _, status := range []int{http.StatusTooManyRequests, http.StatusBadRequest} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			store := setupLifecycleLogStore(t)
			h := newCompatRetryHarness(t)
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if calls.Add(1) == 1 {
					w.Header().Set("Retry-After", "0")
					w.WriteHeader(status)
					_, _ = w.Write([]byte(`{"error":{"message":"Unsupported parameter: prompt_cache_key"}}`))
					return
				}
				_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"ok"}]}`))
			}))
			defer server.Close()
			upstream := &config.UpstreamConfig{ID: "availability-channel", Name: "测试", BaseURL: server.URL, APIKeys: []string{"key"}, ServiceType: "openai"}
			c := newAttemptTestContext(context.Background())
			result := runConfiguredAttempt(t, UpstreamAttempt{
				Context: c, EnvConfig: &config.EnvConfig{LogLevel: "error"},
				ConfigManager: h.cfgManager, ChannelScheduler: h.channelScheduler, MetricsManager: h.metricsManager,
				Kind: scheduler.ChannelKindMessages, APIType: "Messages", Upstream: upstream, RequestedModel: "model", RequestBody: []byte(`{}`),
				URLResults: BuildDefaultURLResults(upstream.GetAllBaseURLs()), NextAPIKey: rotatingKeySelector,
				LogContext: AttemptLogContext{Model: "model", RequestLogStore: metrics.NewRequestLogStore(store)},
				BuildRequest: func(c *gin.Context, up *config.UpstreamConfig, _ string) (*http.Request, error) {
					return http.NewRequestWithContext(c.Request.Context(), http.MethodPost, up.BaseURL, strings.NewReader(`{}`))
				},
				HandleSuccess: func(_ *gin.Context, response *http.Response, _ *config.UpstreamConfig, _ string) (*types.Usage, error) {
					_, err := io.ReadAll(response.Body)
					return nil, err
				},
			})
			require.NotEmpty(t, result.SuccessKey)
			require.Equal(t, int32(2), calls.Load())
			require.NoError(t, store.Flush())
			chain, err := store.GetLogCallAttempts(context.Background(), RequestLogID(c))
			require.NoError(t, err)
			require.Len(t, chain.Attempts, 2)
			for _, payload := range chain.Attempts {
				var entry metrics.RequestLogEntry
				require.NoError(t, json.Unmarshal(payload, &entry))
				require.NotNil(t, entry.UpstreamSent)
				require.True(t, *entry.UpstreamSent)
				require.NotNil(t, entry.ResponseHeadersMs)
				require.GreaterOrEqual(t, *entry.ResponseHeadersMs, int64(0))
				require.Positive(t, entry.StartedAt)
				require.Positive(t, entry.UpstreamStatusCode)
			}
			rows, err := store.QueryAvailability(context.Background(), "messages", time.Now())
			require.NoError(t, err)
			counts := make(map[string]int64)
			for _, row := range rows {
				counts[row.Outcome] += row.Count
			}
			require.Equal(t, int64(1), counts[availability.Success])
			if status == http.StatusTooManyRequests {
				require.Equal(t, int64(1), counts[availability.RateLimited])
			} else {
				require.Equal(t, int64(1), counts[availability.Ignored])
			}
		})
	}
}

func TestUnsentAttemptRecordsExplicitStateAndDuplicateCannotChangeLifecycle(t *testing.T) {
	store := setupLifecycleLogStore(t)
	c := newAttemptTestContext(context.Background())
	id := BindRequestLogID(c)
	c.Request = c.Request.WithContext(logger.ContextWithAttemptID(c.Request.Context(), "not-sent"))
	state := newRequestLifecycle(c, []byte(`{}`), time.Now())
	upstream := &config.UpstreamConfig{ID: "channel", BaseURL: "https://example.com", ServiceType: "openai"}
	logs := AttemptLogContext{Model: "model", RequestLogStore: metrics.NewRequestLogStore(store)}
	a := UpstreamAttempt{Context: c, Kind: scheduler.ChannelKindMessages, LogContext: logs}
	a.beginAvailabilityObservation(upstream, "key")
	finishAttempt(c, logs, upstream, "Messages", id, upstream.BaseURL, "key", "failed", 503, false, time.Now(), "local_admission", "队列已满", false, false, nil)
	finishAttempt(c, logs, upstream, "Messages", id, upstream.BaseURL, "key", "completed", 200, true, time.Now(), "", "", false, false, nil)
	require.Equal(t, "failed", state.Status)
	require.NoError(t, store.Flush())
	chain, err := store.GetLogCallAttempts(context.Background(), id)
	require.NoError(t, err)
	require.Len(t, chain.Attempts, 1)
	var entry metrics.RequestLogEntry
	require.NoError(t, json.Unmarshal(chain.Attempts[0], &entry))
	require.NotNil(t, entry.UpstreamSent)
	require.False(t, *entry.UpstreamSent)
	require.Nil(t, entry.ResponseHeadersMs)
	require.Zero(t, entry.UpstreamStatusCode)
}

func TestUnsentAttemptDoesNotCreateAvailabilityEvidence(t *testing.T) {
	c := newAttemptTestContext(context.Background())
	a := UpstreamAttempt{Context: c, Kind: scheduler.ChannelKindMessages, LogContext: AttemptLogContext{Model: "model"}}
	a.beginAvailabilityObservation(&config.UpstreamConfig{ID: "channel", BaseURL: "https://example.com", ServiceType: "openai"}, "key")
	require.Nil(t, availabilitySample(c, false, 503, "local_admission", "队列已满"))
	currentAvailabilityObservation(c).sent = true
	sample := availabilitySample(c, false, 200, "response_processing", "unexpected EOF")
	require.Equal(t, availability.Failure, sample.Outcome)
	sample = availabilitySample(c, false, 200, "client_cancelled", "context canceled")
	require.Equal(t, availability.Ignored, sample.Outcome)
	sample = availabilitySample(c, false, 500, availability.IntentState, "保存意图状态失败")
	require.Equal(t, availability.Ignored, sample.Outcome)
	require.Equal(t, "local_error", sample.Reason)
}

func TestModelCapacityCooldown_StartsAtHeadersAndRequiresExplicitDeadline(t *testing.T) {
	for _, value := range []string{"90", "", "invalid"} {
		t.Run(value, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				c := newAttemptTestContext(context.Background())
				a := UpstreamAttempt{Context: c, Kind: scheduler.ChannelKindMessages, LogContext: AttemptLogContext{Model: "model"}}
				a.beginAvailabilityObservation(&config.UpstreamConfig{ID: "channel", BaseURL: "https://example.com", ServiceType: "openai"}, "key")
				currentAvailabilityObservation(c).sent = true
				headersAt := time.Now()
				a.observeAvailabilityResponse(&http.Response{StatusCode: 503, Header: http.Header{"Retry-After": {value}}})
				time.Sleep(10 * time.Second) // 虚拟时间，模拟较慢的响应正文读取。
				sample := availabilitySample(c, false, 503, availability.ModelCapacity, "model is at capacity")
				require.Equal(t, availability.ModelCapacity, sample.Reason)
				if value == "90" {
					require.Equal(t, headersAt.Add(90*time.Second).UnixMilli(), sample.CooldownUntil)
				} else {
					require.Zero(t, sample.CooldownUntil)
				}
				sample = availabilitySample(c, false, 429, "upstream", "rate limited")
				require.Zero(t, sample.CooldownUntil, "共享冷却不从日志样本重新建立")
			})
		})
	}
}
