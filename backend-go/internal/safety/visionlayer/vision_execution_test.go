package visionlayer

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/insistanan/api-proxy/internal/infra/config"
	"github.com/insistanan/api-proxy/internal/orchestration/circuit"
	"github.com/insistanan/api-proxy/internal/orchestration/scheduler"
	"github.com/insistanan/api-proxy/internal/orchestration/upstreamcontrol"
	"github.com/insistanan/api-proxy/internal/orchestration/urlhealth"
	"github.com/insistanan/api-proxy/internal/protocol/visionmedia"
	"github.com/insistanan/api-proxy/internal/storage/metrics"
	"github.com/stretchr/testify/require"
)

const visionSuccessBody = `{"id":"vision-test","type":"message","role":"assistant","model":"test","stop_reason":"end_turn","content":[{"type":"text","text":"图片中有一只猫"}],"usage":{"input_tokens":1,"output_tokens":1}}`

func newVisionExecutionFixture(t *testing.T, upstreams []config.UpstreamConfig) (*config.ConfigManager, *scheduler.ChannelScheduler, *gin.Context, []visionImage) {
	t.Helper()
	for index := range upstreams {
		upstreams[index].ServiceType = "claude"
		upstreams[index].Status = config.ChannelStatusActive
		upstreams[index].VisionCapable = true
		upstreams[index].MaxConcurrency = upstreamcontrol.DefaultConcurrency
	}
	cfg := config.Config{Upstream: upstreams, FuzzyModeEnabled: true}
	cfg.Settings.Gateway = config.DefaultGatewaySettings()
	data, err := json.Marshal(cfg)
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "config.json")
	require.NoError(t, os.WriteFile(path, data, 0600))
	manager, err := config.NewConfigManager(path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, manager.Close()) })
	metricManagers := make(map[scheduler.ChannelKind]*metrics.MetricsManager)
	for _, kind := range scheduler.ChannelKinds() {
		metricManagers[kind] = metrics.NewMetricsManager()
	}
	sch := scheduler.NewChannelScheduler(manager, metricManagers, nil, urlhealth.NewURLManager(time.Minute, 1))
	t.Cleanup(sch.Stop)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil).WithContext(upstreamcontrol.WithScope(context.Background()))
	block := map[string]interface{}{
		"type":   "image",
		"source": map[string]interface{}{"type": "base64", "media_type": "image/png", "data": "aGVsbG8="},
	}
	images := []visionImage{{id: "image_1", block: block, fingerprint: visionmedia.ImageFingerprintForBlock(block)}}
	return manager, sch, ctx, images
}

func TestVisionFailover_UsesErrorKindInsteadOfFailureCount(t *testing.T) {
	for _, entry := range []string{"explicit", "public", "pool"} {
		for _, scenario := range []struct {
			name     string
			status   int
			body     string
			encoding string
			fallback bool
			failure  bool
			reason   string
		}{
			{name: "网关故障", status: http.StatusBadGateway, body: `bad gateway`, fallback: true, failure: true},
			{name: "限流", status: http.StatusTooManyRequests, body: `{"error":{"code":"rate_limit_exceeded"}}`, fallback: true},
			{name: "内容拒绝", status: http.StatusBadRequest, body: `{"error":{"code":"invalid_request"}}`, reason: "invalid_request"},
			{name: "200 服务错误", status: http.StatusOK, body: `{"error":{"type":"overloaded_error","message":"busy"}}`, fallback: true, failure: true},
			{name: "200 限流", status: http.StatusOK, body: `{"error":{"code":"rate_limit_exceeded"}}`, fallback: true},
			{name: "200 审核拒绝", status: http.StatusOK, body: `{"error":{"code":"content_policy_violation"}}`, reason: "content_policy_violation"},
			{name: "200 拒绝结束", status: http.StatusOK, body: strings.Replace(visionSuccessBody, `"end_turn"`, `"refusal"`, 1), reason: "refusal"},
			{name: "解压失败换渠道", status: http.StatusOK, body: visionSuccessBody, encoding: "unknown", fallback: true, failure: true},
		} {
			t.Run(entry+"/"+scenario.name, func(t *testing.T) {
				t.Cleanup(func() { ClearConversationCache(t.Name()) })
				var firstCalls, secondCalls atomic.Int32
				first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					firstCalls.Add(1)
					if scenario.encoding != "" {
						w.Header().Set("Content-Encoding", scenario.encoding)
					}
					w.WriteHeader(scenario.status)
					_, _ = w.Write([]byte(scenario.body))
				}))
				t.Cleanup(first.Close)
				second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					secondCalls.Add(1)
					_, _ = w.Write([]byte(visionSuccessBody))
				}))
				t.Cleanup(second.Close)
				cfg, sch, ctx, images := newVisionExecutionFixture(t, []config.UpstreamConfig{
					{ID: "first", Name: "first", BaseURL: first.URL, APIKeys: []string{"first"}, ExcludeFromConversation: entry != "pool"},
					{ID: "second", Name: "second", BaseURL: second.URL, APIKeys: []string{"second"}},
				})
				breaker := circuit.NewManager(circuit.Config{FailureThreshold: 1}, true)
				sch.SetCircuitManager(breaker)
				preferred := ""
				if entry == "explicit" {
					preferred = "first"
				}
				result, err := describeImages(ctx, config.DefaultEnvConfig(), cfg, sch, scheduler.ChannelKindMessages, config.DefaultChannelPoolID, preferred, "test", t.Name(), visionAnalysisProfile{}, images)
				require.EqualValues(t, 1, firstCalls.Load())
				if scenario.fallback {
					require.NoError(t, err)
					require.Equal(t, "图片中有一只猫", result["image_1"])
					require.EqualValues(t, 1, secondCalls.Load())
				} else {
					require.ErrorContains(t, err, scenario.reason)
					require.Zero(t, secondCalls.Load())
				}
				state := circuit.StateClosed
				var failureCount int64
				if scenario.failure {
					state = circuit.StateOpen
					failureCount = 1
				}
				require.Equal(t, state, breaker.SnapshotEntry("messages", 0).State)
				keyMetrics := sch.MetricsManager(scheduler.ChannelKindMessages).GetKeyMetrics(first.URL, "first", 0)
				require.NotNil(t, keyMetrics)
				require.Equal(t, failureCount, keyMetrics.FailureCount, "业务拒绝和限流不能累计 Key 故障")
				for index := range 2 {
					require.Zero(t, sch.GetChannelInFlight(scheduler.ChannelKindMessages, index))
				}
			})
		}
	}
}

func TestVisionAttempt_ReusesKeysOnBackupURL(t *testing.T) {
	var primaryCalls, backupCalls atomic.Int32
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		primaryCalls.Add(1)
		w.WriteHeader(http.StatusBadGateway)
	}))
	t.Cleanup(primary.Close)
	backup := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		backupCalls.Add(1)
		_, _ = w.Write([]byte(visionSuccessBody))
	}))
	t.Cleanup(backup.Close)
	cfg, sch, ctx, images := newVisionExecutionFixture(t, []config.UpstreamConfig{
		{ID: "vision", Name: "vision", BaseURL: primary.URL, BaseURLs: []string{primary.URL, backup.URL}, APIKeys: []string{"key-a", "key-b"}},
	})
	// 仅主地址上的两个 Key 被暂停，备用地址上的相同 Key 仍应可用。
	for _, key := range []string{"key-a", "key-b"} {
		for range 10 {
			sch.RecordFailure(primary.URL, key, 0, scheduler.ChannelKindMessages)
		}
		require.True(t, sch.ShouldSuspendKey(primary.URL, key, 0, scheduler.ChannelKindMessages))
	}
	upstreams, _, err := cfg.GetChannelConfig("messages")
	require.NoError(t, err)
	selection, err := sch.AdmitChannelCandidate(ctx.Request.Context(), &scheduler.SelectionResult{Upstream: &upstreams[0], Kind: scheduler.ChannelKindMessages, ChannelIndex: 0})
	require.NoError(t, err)
	result := describeImagesOnChannel(ctx, config.DefaultEnvConfig(), cfg, sch, selection, "test", t.Name(), visionAnalysisProfile{}, images, 2)
	require.NoError(t, result.err)
	require.Equal(t, "图片中有一只猫", result.descriptions["image_1"])
	require.Zero(t, primaryCalls.Load())
	require.EqualValues(t, 1, backupCalls.Load())
	require.Zero(t, sch.GetChannelInFlight(scheduler.ChannelKindMessages, 0))
}

func TestVisionAttempt_HalfOpenSuccessAndCancellationReleasePermit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(visionSuccessBody))
	}))
	t.Cleanup(server.Close)
	cfg, sch, ctx, images := newVisionExecutionFixture(t, []config.UpstreamConfig{
		{ID: "vision", Name: "vision", BaseURL: server.URL, APIKeys: []string{"key"}},
	})
	synctest.Test(t, func(t *testing.T) {
		defer server.CloseClientConnections()
		breaker := circuit.NewManager(circuit.Config{FailureThreshold: 1, Cooldown: time.Minute, SuccessThreshold: 1}, true)
		sch.SetCircuitManager(breaker)
		breaker.RecordFailure("messages", 0)
		time.Sleep(time.Minute)
		selection, err := sch.SelectVisionChannel(ctx.Request.Context(), scheduler.ChannelKindMessages, "vision", config.DefaultChannelPoolID)
		require.NoError(t, err)
		require.Equal(t, 1, breaker.SnapshotEntry("messages", 0).HalfOpenInFlight)
		originalRequest := ctx.Request
		cancelCtx, cancel := context.WithCancel(originalRequest.Context())
		cancel()
		ctx.Request = originalRequest.WithContext(cancelCtx)
		result := describeImagesOnChannel(ctx, config.DefaultEnvConfig(), cfg, sch, selection, "test", t.Name(), visionAnalysisProfile{}, images, 2)
		require.ErrorIs(t, result.err, context.Canceled)
		require.False(t, result.retryable)
		require.Zero(t, breaker.SnapshotEntry("messages", 0).HalfOpenInFlight)
		require.Zero(t, sch.GetChannelInFlight(scheduler.ChannelKindMessages, 0))
		ctx.Request = originalRequest
		selection, err = sch.SelectVisionChannel(ctx.Request.Context(), scheduler.ChannelKindMessages, "vision", config.DefaultChannelPoolID)
		require.NoError(t, err)
		result = describeImagesOnChannel(ctx, config.DefaultEnvConfig(), cfg, sch, selection, "test", t.Name(), visionAnalysisProfile{}, images, 2)
		require.NoError(t, result.err)
		require.Equal(t, circuit.StateClosed, breaker.SnapshotEntry("messages", 0).State)
		require.Zero(t, sch.GetChannelInFlight(scheduler.ChannelKindMessages, 0))
	})
}

func TestPrepareRequest_RejectsInvalidImageBeforeAnalysis(t *testing.T) {
	body := `{"messages":[{"role":"user","content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"aGVsbG8="}},{"type":"image","source":{"type":"base64","media_type":"image/png","data":"%%%"}}]}]}`
	conversationID := t.Name()
	fingerprints := visionmedia.ExtractImageFingerprints([]byte(body))
	require.Len(t, fingerprints, 1)
	request := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(body))
	err := PrepareRequest(nil, nil, nil, nil, scheduler.ChannelKindMessages, &config.UpstreamConfig{ServiceType: "claude"}, "test", conversationID, request)
	require.ErrorContains(t, err, "指纹")
}
