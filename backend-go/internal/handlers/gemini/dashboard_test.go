package gemini

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/insistanan/api-proxy/internal/config"
	"github.com/insistanan/api-proxy/internal/handlers"
	"github.com/insistanan/api-proxy/internal/metrics"
	"github.com/insistanan/api-proxy/internal/scheduler"
	"github.com/insistanan/api-proxy/internal/session"
	"github.com/insistanan/api-proxy/internal/upstreamcontrol"
	"github.com/insistanan/api-proxy/internal/urlhealth"
)

func TestGetDashboard_IncludesStripThoughtSignature(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := config.Config{
		GeminiUpstream: []config.UpstreamConfig{
			{
				Name:                  "gemini-test",
				ServiceType:           "gemini",
				BaseURL:               "https://example.com",
				APIKeys:               []string{"test-key"},
				StripThoughtSignature: true,
				MaxConcurrency:        upstreamcontrol.DefaultConcurrency,
			},
		},
		GeminiLoadBalance: "round-robin",
	}

	tmpDir := t.TempDir()
	configFile := filepath.Join(tmpDir, "config.json")
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatalf("序列化配置失败: %v", err)
	}
	if err := os.WriteFile(configFile, data, 0644); err != nil {
		t.Fatalf("写入配置文件失败: %v", err)
	}

	cfgManager, err := config.NewConfigManager(configFile)
	if err != nil {
		t.Fatalf("创建配置管理器失败: %v", err)
	}
	t.Cleanup(func() { cfgManager.Close() })

	messagesMetrics := metrics.NewMetricsManager()
	responsesMetrics := metrics.NewMetricsManager()
	geminiMetrics := metrics.NewMetricsManager()
	chatMetrics := metrics.NewMetricsManager()
	imagesMetrics := metrics.NewMetricsManager()
	t.Cleanup(func() {
		messagesMetrics.Stop()
		responsesMetrics.Stop()
		geminiMetrics.Stop()
		chatMetrics.Stop()
		imagesMetrics.Stop()
	})

	traceAffinity := session.NewTraceAffinityManager()
	urlManager := urlhealth.NewURLManager(30*time.Second, 3)
	sch := scheduler.NewChannelScheduler(cfgManager, messagesMetrics, responsesMetrics, geminiMetrics, chatMetrics, imagesMetrics, traceAffinity, urlManager)

	r := gin.New()
	r.GET("/gemini/channels/dashboard", handlers.GetChannelDashboard(cfgManager, sch, scheduler.ChannelKindGemini))

	req := httptest.NewRequest(http.MethodGet, "/gemini/channels/dashboard", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d, want=%d, body=%s", w.Code, http.StatusOK, w.Body.String())
	}

	var resp struct {
		Channels []map[string]any `json:"channels"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if len(resp.Channels) != 1 {
		t.Fatalf("channels len=%d, want=1", len(resp.Channels))
	}

	value, ok := resp.Channels[0]["stripThoughtSignature"]
	if !ok {
		t.Fatalf("响应缺少 stripThoughtSignature 字段: %v", resp.Channels[0])
	}
	strip, ok := value.(bool)
	if !ok {
		t.Fatalf("stripThoughtSignature 类型=%T, want=bool", value)
	}
	if strip != true {
		t.Fatalf("stripThoughtSignature=%v, want=true", strip)
	}
}
