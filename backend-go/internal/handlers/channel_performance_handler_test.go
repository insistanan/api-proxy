package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/insistanan/api-proxy/internal/metrics"
	"github.com/stretchr/testify/require"
)

// 覆盖报告迁移前的完整 JSON 契约，避免直接序列化画像泄露密钥。
func TestChannelPerformanceReportContract(t *testing.T) {
	pm := metrics.NewProfileManager()
	pm.Stop()
	low := pm.GetOrCreateProfile("https://slow.example", []string{"private-key"}, "model-low", 7)
	low.HealthScore = 40
	p := pm.GetOrCreateProfile("https://fast.example", []string{"secret-key"}, "model-high", 3)
	p.HealthScore, p.PerformanceTier = 95, "S"
	p.ActiveRequests, p.RecentTPS, p.Peak1MinTPS = 2, 3.5, 6.5
	p.AvgTTFB, p.AvgResponseTime = 120, 800
	p.P50Latency, p.P95Latency, p.P99Latency = 750, 900, 950
	p.AvgTokenSpeed, p.SuccessRate, p.RecentSuccessRate = 55, 99, 98
	p.LastScoreUpdate = time.Date(2026, 11, 6, 9, 8, 7, 0, time.FixedZone("CST", 8*60*60))

	r := gin.New()
	r.GET("/report", GetChannelPerformanceReport(pm))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/report", nil))
	require.Equal(t, http.StatusOK, w.Code)
	require.JSONEq(t, `{"success":true,"data":[
		{"channelIndex":3,"baseUrl":"https://fast.example","model":"model-high","healthScore":95,"performanceTier":"S",
		"activeRequests":2,"recentTps":3.5,"peak1MinTps":6.5,"avgTtfb":120,"avgResponseTime":800,
		"p50Latency":750,"p95Latency":900,"p99Latency":950,"avgTokenSpeed":55,"successRate":99,"recentSuccessRate":98,
		"lastScoreUpdate":"2026-11-06T09:08:07+08:00"},
		{"channelIndex":7,"baseUrl":"https://slow.example","model":"model-low","healthScore":40,"performanceTier":"A",
		"activeRequests":0,"recentTps":0,"peak1MinTps":0,"avgTtfb":0,"avgResponseTime":0,
		"p50Latency":0,"p95Latency":0,"p99Latency":0,"avgTokenSpeed":0,"successRate":100,"recentSuccessRate":100,
		"lastScoreUpdate":"0001-01-01T00:00:00Z"}]}`, w.Body.String())
}

func TestChannelPerformanceReportEmptySemantics(t *testing.T) {
	pm := metrics.NewProfileManager()
	pm.Stop()
	for _, tc := range []struct {
		name    string
		manager *metrics.ProfileManager
		want    string
	}{
		{"未注入", nil, `{"success":true,"data":null}`},
		{"空画像", pm, `{"success":true,"data":[]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := gin.New()
			r.GET("/report", GetChannelPerformanceReport(tc.manager))
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/report", nil))
			require.Equal(t, http.StatusOK, w.Code)
			require.JSONEq(t, tc.want, w.Body.String())
		})
	}
}
