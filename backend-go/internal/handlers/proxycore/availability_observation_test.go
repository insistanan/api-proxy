package proxycore

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/insistanan/api-proxy/internal/infra/config"
	"github.com/insistanan/api-proxy/internal/orchestration/availability"
	"github.com/insistanan/api-proxy/internal/orchestration/scheduler"
	"github.com/insistanan/api-proxy/internal/protocol/types"
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
			result := runConfiguredAttempt(t, UpstreamAttempt{
				Context: newAttemptTestContext(context.Background()), EnvConfig: &config.EnvConfig{LogLevel: "error"},
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
}
