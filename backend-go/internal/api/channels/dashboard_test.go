package channels

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"insistanan/api-proxy/internal/dialogue/session"
	"insistanan/api-proxy/internal/infra/config"
	"insistanan/api-proxy/internal/orchestration/scheduler"
	"insistanan/api-proxy/internal/orchestration/upstreamcontrol"
	"insistanan/api-proxy/internal/orchestration/urlhealth"
	"insistanan/api-proxy/internal/storage/metrics"
)

func TestGetDashboard_IncludesStripThoughtSignature(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := config.Config{
		Settings: config.SettingsConfig{Gateway: config.DefaultGatewaySettings()},
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
	sch := scheduler.NewChannelScheduler(cfgManager, map[scheduler.ChannelKind]*metrics.MetricsManager{
		scheduler.ChannelKindMessages:  messagesMetrics,
		scheduler.ChannelKindResponses: responsesMetrics,
		scheduler.ChannelKindGemini:    geminiMetrics,
		scheduler.ChannelKindChat:      chatMetrics,
		scheduler.ChannelKindImages:    imagesMetrics,
	}, traceAffinity, urlManager)
	t.Cleanup(sch.Stop)
	upstreams, _, err := cfgManager.GetChannelConfig("gemini")
	if err != nil || len(upstreams) != 1 {
		t.Fatalf("读取渠道失败: %v", err)
	}
	finishMain := sch.TrackChannelConversation(scheduler.ChannelKindGemini, upstreams[0].ID, "one-conversation", "main")
	defer finishMain()
	finishSide := sch.TrackChannelConversation(scheduler.ChannelKindGemini, upstreams[0].ID, "one-conversation", "side")
	defer finishSide()
	finishRetry := sch.TrackChannelConversation(scheduler.ChannelKindGemini, upstreams[0].ID, "one-conversation", "side")
	defer finishRetry()

	r := gin.New()
	r.GET("/gemini/channels/dashboard", GetChannelDashboard(cfgManager, sch, scheduler.ChannelKindGemini))

	req := httptest.NewRequest(http.MethodGet, "/gemini/channels/dashboard", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d, want=%d, body=%s", w.Code, http.StatusOK, w.Body.String())
	}

	var resp struct {
		Channels []map[string]any `json:"channels"`
		Metrics  []struct {
			Conversations *int `json:"activeConversations"`
			Branches      *int `json:"activeBranches"`
			Unidentified  *int `json:"unidentifiedBranchConversations"`
		} `json:"metrics"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if len(resp.Channels) != 1 {
		t.Fatalf("channels len=%d, want=1", len(resp.Channels))
	}
	if len(resp.Metrics) != 1 || resp.Metrics[0].Conversations == nil || resp.Metrics[0].Branches == nil || resp.Metrics[0].Unidentified == nil {
		t.Fatalf("响应未包含完整的并发快照: %+v", resp.Metrics)
	}
	if *resp.Metrics[0].Conversations != 1 || *resp.Metrics[0].Branches != 2 || *resp.Metrics[0].Unidentified != 0 {
		t.Fatalf("同会话两分支的重复请求不应增加并发: %+v", resp.Metrics[0])
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
