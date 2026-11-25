package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"insistanan/api-proxy/internal/dialogue/session"
	"insistanan/api-proxy/internal/infra/config"
	"insistanan/api-proxy/internal/infra/middleware"
	"insistanan/api-proxy/internal/orchestration/scheduler"
	"insistanan/api-proxy/internal/safety/pipeline"
	"insistanan/api-proxy/internal/storage/metrics"
)

var channelAdminKinds = []scheduler.ChannelKind{
	scheduler.ChannelKindMessages, scheduler.ChannelKindResponses, scheduler.ChannelKindGemini,
	scheduler.ChannelKindChat, scheduler.ChannelKindImages, scheduler.ChannelKindEmbeddings, scheduler.ChannelKindDecisions,
}

func TestFingerprintRouteRequiresAdminAndStableChannel(t *testing.T) {
	r, a := newChannelAdminTestRouter(t)
	for _, endpoint := range []struct{ method, path string }{
		{http.MethodGet, "/api/channels/missing/fingerprint"},
		{http.MethodDelete, "/api/channels/missing/fingerprint/task-id"},
		{http.MethodPost, "/api/channels/missing/fingerprint/task-id/samples/1/retry"},
		{http.MethodPost, "/api/channels/missing/fingerprint/task-id/retry"},
	} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(endpoint.method, endpoint.path, nil))
		require.Equal(t, http.StatusUnauthorized, w.Code, endpoint.path)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/channels/missing/fingerprint", strings.NewReader(`{"model":"requested"}`))
	request.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, request)
	require.Equal(t, http.StatusUnauthorized, w.Code)
	w = channelAdminRequest(t, r, http.MethodPost, "/api/channels/missing/fingerprint", `{"model":"requested"}`, http.StatusBadRequest)
	require.Contains(t, w.Body.String(), "渠道不存在")
	added, err := a.cfgManager.AddUpstreamWithResult(config.UpstreamConfig{
		Name: "指纹查询测试", ServiceType: "claude", BaseURL: "https://example.invalid", APIKeys: []string{"fixture-key"},
	})
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(added.ID, "ch_"))
	w = channelAdminRequest(t, r, http.MethodGet, "/api/channels/"+added.ID+"/fingerprint", "", http.StatusOK)
	require.Equal(t, "null", w.Body.String(), "新渠道应返回无记录，不能误报渠道 ID 无效")
}

func TestModelCatalogRoutesCoexistWithProxyEndpoints(t *testing.T) {
	r, a := newChannelAdminTestRouter(t)
	a.envCfg = &config.EnvConfig{ProxyAccessKey: "test-admin"}
	a.contentSafetyPipeline = pipeline.NewContentSafetyPipeline(a.cfgManager)
	require.NoError(t, a.cfgManager.EnsureFreeChannels())
	a.setupProxyEndpoints(r)
	paths := make(map[string]bool)
	for _, route := range r.Routes() {
		paths[route.Method+" "+route.Path] = true
	}
	for _, path := range []string{
		"GET /v1/models", "GET /v1/models/*model", "GET /v1beta/models", "GET /v1beta/models/*model",
		"POST /v1beta/models/*modelAction", "POST /v1/messages", "POST /v1/responses", "POST /v1/chat/completions",
	} {
		require.True(t, paths[path], path)
	}
	for _, path := range []string{"/v1/models/free/kilo-auto/free?type=messages", "/v1beta/models/free/kilo-auto/free"} {
		channelAdminRequest(t, r, http.MethodGet, path, "", http.StatusOK)
	}
	// 验证 Gemini POST 仍命中原代理鉴权，不执行任何推理或上游请求。
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1beta/models/free/kilo-auto/free:generateContent", nil))
	require.Equal(t, http.StatusUnauthorized, w.Code)
}

// 只装配渠道管理边界：临时配置、内存指标和真实鉴权，不启动 HTTP 服务。
func newChannelAdminTestRouter(t *testing.T) (*gin.Engine, *app) {
	t.Helper()
	cm, err := config.NewConfigManager(filepath.Join(t.TempDir(), "config.json"))
	require.NoError(t, err)
	mms := make(map[scheduler.ChannelKind]*metrics.MetricsManager)
	for _, kind := range channelAdminKinds {
		mms[kind] = metrics.NewMetricsManager()
	}
	sch := scheduler.NewChannelScheduler(cm, mms, session.NewTraceAffinityManager(), nil)
	t.Cleanup(func() {
		sch.Stop()
		require.NoError(t, cm.Close())
	})
	a := &app{cfgManager: cm, channelScheduler: sch, metricsByKind: mms}
	r := gin.New()
	group := r.Group("/api", middleware.AdminAuthMiddleware(&config.EnvConfig{ProxyAccessKey: "test-admin"}))
	a.setupChannelAdminAPI(group)
	t.Cleanup(func() { require.NoError(t, a.shutdown()) })
	return r, a
}

func channelAdminRequest(t *testing.T, r *gin.Engine, method, path, body string, want int) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", "test-admin")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, want, w.Code, "%s %s: %s", method, path, w.Body.String())
	return w
}

func TestChannelAdminRouteContract(t *testing.T) {
	r, _ := newChannelAdminTestRouter(t)
	actual := make(map[string]bool)
	for _, route := range r.Routes() {
		actual[route.Method+" "+route.Path] = true
	}
	common := []string{
		"GET /channels/:id/deletion-impact", "GET /pools/:id/deletion-impact",
		"GET /channels", "POST /channels", "PUT /channels/:id", "DELETE /channels/:id",
		"POST /channels/:id/keys", "DELETE /channels/:id/keys/:apiKey",
		"POST /channels/:id/keys/:apiKey/top", "POST /channels/:id/keys/:apiKey/bottom",
		"GET /pools", "POST /pools", "PUT /pools/layout", "PUT /pools/:id", "DELETE /pools/:id",
		"POST /channels/reorder", "POST /channels/tidy", "PATCH /channels/:id/status",
		"POST /channels/:id/duplicate", "POST /channels/:id/resume", "POST /channels/:id/promotion",
		"PUT /loadbalance", "GET /channels/dashboard", "GET /mapping-targets", "GET /mapping-targets/:channelId/models",
		"GET /channels/metrics", "GET /channels/metrics/history", "GET /channels/:id/keys/metrics/history",
		"GET /channels/:id/logs", "GET /global/stats/history", "GET /ping/:id", "GET /ping",
	}
	for _, kind := range channelAdminKinds {
		for _, endpoint := range common {
			parts := strings.SplitN(endpoint, " ", 2)
			require.True(t, actual[parts[0]+" /api/"+string(kind)+parts[1]], "%s: %s", kind, endpoint)
		}
		require.Equal(t, kind == scheduler.ChannelKindMessages,
			actual["GET /api/"+string(kind)+"/channels/scheduler/stats"])
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/"+string(kind)+"/channels", nil))
		require.Equal(t, http.StatusUnauthorized, w.Code)
	}
	// 公共图片理解池是四个会话协议共享的入口，独立于七个渠道前缀。
	require.True(t, actual["GET /api/vision-channels"])
	require.True(t, actual["POST /api/channels/:id/fingerprint"])
	require.True(t, actual["GET /api/channels/:id/fingerprint"])
	require.True(t, actual["DELETE /api/channels/:id/fingerprint/:taskId"])
	require.True(t, actual["POST /api/channels/:id/fingerprint/:taskId/samples/:sampleIndex/retry"])
	require.True(t, actual["POST /api/channels/:id/fingerprint/:taskId/retry"])
	require.True(t, actual["GET /api/channels/:id/cache-keepalive"])
	require.True(t, actual["POST /api/channels/:id/cache-keepalive/stop"])
	require.Len(t, actual, len(common)*len(channelAdminKinds)+9)
}

func TestChannelDeletionImpactRoutesAndCrossProtocolDetach(t *testing.T) {
	r, a := newChannelAdminTestRouter(t)
	vision, err := a.cfgManager.AddChannel("messages", config.UpstreamConfig{
		Name: "vision", BaseURL: "https://example.com", APIKeys: []string{"secret-fixture"}, ServiceType: "openai",
		VisionCapable: true, ExcludeFromConversation: true,
	})
	require.NoError(t, err)
	_, err = a.cfgManager.AddChannel("chat", config.UpstreamConfig{
		Name: "owner", BaseURL: "https://example.com", APIKeys: []string{"owner-fixture"}, ServiceType: "openai",
		VisionLayerEnabled: true, VisionLayerChannelID: vision.ID, VisionLayerModel: "vision-model",
	})
	require.NoError(t, err)
	path := fmt.Sprintf("/api/messages/channels/%d/deletion-impact", vision.Index)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
	require.Equal(t, http.StatusUnauthorized, w.Code)
	w = channelAdminRequest(t, r, http.MethodGet, path, "", http.StatusOK)
	var impact config.ChannelDeletionImpact
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &impact))
	require.Equal(t, "vision", impact.Name)
	require.Equal(t, 1, impact.ChannelCount)
	require.Equal(t, []config.VisionLayerDependent{{Kind: "chat", Index: 0, Name: "owner"}}, impact.Dependents)
	require.NotContains(t, w.Body.String(), "fixture")
	require.NotContains(t, w.Body.String(), "baseUrl")
	require.True(t, a.cfgManager.GetConfig().ChatUpstream[0].VisionLayerEnabled)
	channelAdminRequest(t, r, http.MethodDelete, fmt.Sprintf("/api/messages/channels/%d", vision.Index), "", http.StatusOK)
	owner := a.cfgManager.GetConfig().ChatUpstream[0]
	require.False(t, owner.VisionCapable)
	require.False(t, owner.VisionLayerEnabled)
	require.Empty(t, owner.VisionLayerChannelID)
	require.Empty(t, owner.VisionLayerModel)
	channelAdminRequest(t, r, http.MethodGet, "/api/messages/channels/invalid/deletion-impact", "", http.StatusBadRequest)
	channelAdminRequest(t, r, http.MethodGet, "/api/messages/pools/default/deletion-impact", "", http.StatusBadRequest)
}

func TestChannelAdminProtocolIsolation(t *testing.T) {
	r, a := newChannelAdminTestRouter(t)
	for _, kind := range channelAdminKinds {
		prefix := "/api/" + string(kind)
		body := fmt.Sprintf(`{"name":%q,"baseUrl":"https://example.com","apiKeys":["fixture-key"],"serviceType":"openai"}`, kind)
		channelAdminRequest(t, r, http.MethodPost, prefix+"/channels", body, http.StatusOK)
		channelAdminRequest(t, r, http.MethodPut, prefix+"/channels/0", fmt.Sprintf(`{"name":%q}`, string(kind)+"-edited"), http.StatusOK)
		channelAdminRequest(t, r, http.MethodPut, prefix+"/loadbalance", `{"strategy":"failover"}`, http.StatusOK)
		channelAdminRequest(t, r, http.MethodPut, prefix+"/loadbalance", `{"strategy":"random"}`, http.StatusBadRequest)
	}
	// 相同 URL/key/index 在五个协议中仍然是独立的观测口径。
	a.metricsByKind[scheduler.ChannelKindChat].RecordFailure("https://example.com", "fixture-key", 0)
	for _, kind := range channelAdminKinds {
		prefix := "/api/" + string(kind)
		for _, endpoint := range []string{"/channels", "/channels/dashboard"} {
			w := channelAdminRequest(t, r, http.MethodGet, prefix+endpoint, "", http.StatusOK)
			var result struct {
				Channels []struct {
					Name string `json:"name"`
				} `json:"channels"`
				LoadBalance string `json:"loadBalance"`
				Metrics     []struct {
					FailureCount int `json:"failureCount"`
				} `json:"metrics"`
			}
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
			require.Len(t, result.Channels, 1)
			require.Equal(t, string(kind)+"-edited", result.Channels[0].Name)
			require.Equal(t, "failover", result.LoadBalance)
			if endpoint == "/channels/dashboard" {
				require.Len(t, result.Metrics, 1)
				wantFailures := 0
				if kind == scheduler.ChannelKindChat {
					wantFailures = 1
				}
				require.Equal(t, wantFailures, result.Metrics[0].FailureCount)
			}
		}
	}
}

func TestChannelResumeDoesNotActivateSuspendedConfig(t *testing.T) {
	r, _ := newChannelAdminTestRouter(t)
	for _, kind := range channelAdminKinds {
		prefix := "/api/" + string(kind) + "/channels"
		channelAdminRequest(t, r, http.MethodPost, prefix,
			`{"name":"suspended","baseUrl":"https://example.com","apiKeys":["fixture-key"],"serviceType":"openai","status":"suspended"}`, http.StatusOK)
		channelAdminRequest(t, r, http.MethodPost, prefix+"/0/resume", "", http.StatusOK)
		w := channelAdminRequest(t, r, http.MethodGet, prefix, "", http.StatusOK)
		var result struct {
			Channels []struct {
				Status string `json:"status"`
			} `json:"channels"`
		}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
		require.Len(t, result.Channels, 1)
		require.Equal(t, "suspended", result.Channels[0].Status)
		channelAdminRequest(t, r, http.MethodPatch, prefix+"/0/status", `{"status":"active"}`, http.StatusOK)
		channelAdminRequest(t, r, http.MethodPost, prefix+"/0/promotion", `{"duration":60,"count":2}`, http.StatusOK)
	}
}
