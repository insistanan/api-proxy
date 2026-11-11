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
	"github.com/insistanan/api-proxy/internal/config"
	"github.com/insistanan/api-proxy/internal/metrics"
	"github.com/insistanan/api-proxy/internal/middleware"
	"github.com/insistanan/api-proxy/internal/scheduler"
	"github.com/insistanan/api-proxy/internal/session"
	"github.com/stretchr/testify/require"
)

var channelAdminKinds = []scheduler.ChannelKind{
	scheduler.ChannelKindMessages, scheduler.ChannelKindResponses, scheduler.ChannelKindGemini,
	scheduler.ChannelKindChat, scheduler.ChannelKindImages, scheduler.ChannelKindEmbeddings, scheduler.ChannelKindDecisions,
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
	sch := scheduler.NewChannelScheduler(cm, mms[scheduler.ChannelKindMessages], mms[scheduler.ChannelKindResponses],
		mms[scheduler.ChannelKindGemini], mms[scheduler.ChannelKindChat], mms[scheduler.ChannelKindImages],
		mms[scheduler.ChannelKindEmbeddings], mms[scheduler.ChannelKindDecisions], session.NewTraceAffinityManager(), nil)
	t.Cleanup(func() {
		sch.Stop()
		require.NoError(t, cm.Close())
	})
	a := &app{cfgManager: cm, channelScheduler: sch, metricsByKind: mms}
	r := gin.New()
	group := r.Group("/api", middleware.AdminAuthMiddleware(&config.EnvConfig{ProxyAccessKey: "test-admin"}))
	a.setupChannelAdminAPI(group)
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
	require.Len(t, actual, len(common)*len(channelAdminKinds)+1)
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
