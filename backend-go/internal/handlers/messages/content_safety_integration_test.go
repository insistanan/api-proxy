package messages

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BenedictKing/api-proxy/internal/config"
	rootHandlers "github.com/BenedictKing/api-proxy/internal/handlers"
	"github.com/BenedictKing/api-proxy/internal/handlers/hooks"
	"github.com/BenedictKing/api-proxy/internal/metrics"
	"github.com/BenedictKing/api-proxy/internal/scheduler"
	"github.com/BenedictKing/api-proxy/internal/sensitive"
	"github.com/BenedictKing/api-proxy/internal/session"
	"github.com/BenedictKing/api-proxy/internal/urlhealth"
	"github.com/gin-gonic/gin"
)

const (
	contentSafetyTestAccessKey = "content-safety-e2e-access"
	contentSafetyTestModel     = "claude-content-safety-e2e"
)

type contentSafetyUpstreamCapture struct {
	mu     sync.Mutex
	bodies [][]byte
}

func (c *contentSafetyUpstreamCapture) append(body []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.bodies = append(c.bodies, bytes.Clone(body))
}

func (c *contentSafetyUpstreamCapture) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.bodies)
}

func (c *contentSafetyUpstreamCapture) lastBody(t *testing.T) []byte {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.bodies) == 0 {
		t.Fatal("假上游尚未收到请求")
	}
	return bytes.Clone(c.bodies[len(c.bodies)-1])
}

func TestContentSafetyVerificationSurfaceEndToEnd(t *testing.T) {
	gin.SetMode(gin.TestMode)

	router, cfgManager, capture := newContentSafetyTestRouter(t)

	t.Run("敏感词请求返回 Claude 403 且不会访问上游", func(t *testing.T) {
		response := performContentSafetyMessagesRequest(t, router,
			`{"model":"`+contentSafetyTestModel+`","max_tokens":32,"messages":[{"role":"user","content":"请介绍赌博平台"}]}`,
			"req-sensitive-word",
		)
		assertMessagesContentSafetyError(t, response, http.StatusForbidden, "SENSITIVE_WORD_BLOCKED")
		if got := capture.count(); got != 0 {
			t.Fatalf("敏感词拦截后上游请求数 = %d，期望 0", got)
		}
	})

	t.Run("手机号在假上游收到前已掩码", func(t *testing.T) {
		response := performContentSafetyMessagesRequest(t, router,
			`{"model":"`+contentSafetyTestModel+`","max_tokens":32,"messages":[{"role":"user","content":"联系电话 13800138000"}]}`,
			"req-mask-phone",
		)
		if response.Code != http.StatusOK {
			t.Fatalf("状态码 = %d，期望 200，响应 = %s", response.Code, response.Body.String())
		}
		upstreamBody := string(capture.lastBody(t))
		if !strings.Contains(upstreamBody, "MASKED_PHONE_") || strings.Contains(upstreamBody, "13800138000") {
			t.Fatalf("上游请求体手机号掩码不正确: %s", upstreamBody)
		}
	})

	t.Run("非流式危险代码块返回协议错误", func(t *testing.T) {
		response := performContentSafetyMessagesRequest(t, router,
			`{"model":"`+contentSafetyTestModel+`","max_tokens":32,"messages":[{"role":"user","content":"触发非流式危险响应"}]}`,
			"req-dangerous-normal",
		)
		assertMessagesContentSafetyError(t, response, http.StatusForbidden, "DANGEROUS_COMMAND_BLOCKED")
	})

	t.Run("流式危险命令中断并返回唯一的协议错误事件", func(t *testing.T) {
		response := performContentSafetyMessagesRequest(t, router,
			`{"model":"`+contentSafetyTestModel+`","max_tokens":32,"stream":true,"messages":[{"role":"user","content":"触发流式危险响应"}]}`,
			"req-dangerous-stream",
		)
		if response.Code != http.StatusOK {
			t.Fatalf("流式状态码 = %d，期望 200，响应 = %s", response.Code, response.Body.String())
		}
		body := response.Body.String()
		if !strings.Contains(body, "event: error") ||
			!strings.Contains(body, `"type":"content_safety_error"`) ||
			!strings.Contains(body, "DANGEROUS_COMMAND_BLOCKED") {
			t.Fatalf("流式内容安全错误事件不完整: %s", body)
		}
		if strings.Contains(body, `"type":"stream_error"`) {
			t.Fatalf("流式内容安全错误不应混入通用 stream_error: %s", body)
		}
		if strings.Contains(body, "\"text\":\" /\\n```\"") {
			t.Fatalf("命中危险命令的 chunk 不应继续转发: %s", body)
		}
	})

	t.Run("关闭赌博分类后对应敏感词不再拦截", func(t *testing.T) {
		settings := cfgManager.GetSettings()
		settings.ContentSafety.SensitiveWord.GamblingEnabled = false
		settings.ContentSafety.SensitiveWord.Enabled = false
		if err := cfgManager.UpdateSettings(settings); err != nil {
			t.Fatalf("关闭赌博分类失败: %v", err)
		}

		before := capture.count()
		response := performContentSafetyMessagesRequest(t, router,
			`{"model":"`+contentSafetyTestModel+`","max_tokens":32,"messages":[{"role":"user","content":"请介绍赌博平台"}]}`,
			"req-gambling-disabled",
		)
		if response.Code != http.StatusOK {
			t.Fatalf("关闭分类后状态码 = %d，期望 200，响应 = %s", response.Code, response.Body.String())
		}
		if got := capture.count(); got != before+1 {
			t.Fatalf("关闭分类后上游请求数 = %d，期望 %d", got, before+1)
		}
	})

	t.Run("安全处置记录 API 返回四次真实处置", func(t *testing.T) {
		request := httptest.NewRequest(http.MethodGet, "/api/blocked-logs?page=1&pageSize=20&apiType=messages", nil)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("拦截记录状态码 = %d，期望 200，响应 = %s", response.Code, response.Body.String())
		}

		var page sensitive.BlockedLogPage
		if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil {
			t.Fatalf("解析拦截记录失败: %v，响应 = %s", err, response.Body.String())
		}
		if page.Total != 4 || len(page.Logs) != 4 {
			t.Fatalf("安全处置记录数量 = %d/%d，期望 4: %+v", page.Total, len(page.Logs), page.Logs)
		}

		counts := make(map[string]int, len(page.Logs))
		for _, entry := range page.Logs {
			if entry.RequestID == "" {
				t.Fatalf("拦截记录缺少内部请求 ID: %+v", entry)
			}
			counts[entry.BlockType+":"+entry.RuleName]++
		}
		if counts[sensitive.BlockTypeSensitiveWord+":gambling"] != 1 ||
			counts[sensitive.BlockTypeSensitiveInfo+":"+config.SensitiveInfoRulePhone] != 1 ||
			counts[sensitive.BlockTypeDangerousCmd+":"+config.DangerousCmdRuleDestructive] != 2 {
			t.Fatalf("安全处置记录类型或规则不完整: %+v", page.Logs)
		}
	})
}

func newContentSafetyTestRouter(t *testing.T) (*gin.Engine, *config.ConfigManager, *contentSafetyUpstreamCapture) {
	t.Helper()

	capture := &contentSafetyUpstreamCapture{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		capture.append(body)

		switch {
		case bytes.Contains(body, []byte("触发非流式危险响应")):
			writeContentSafetyClaudeResponse(w, "```sh\nrm -rf /\n```")
		case bytes.Contains(body, []byte("触发流式危险响应")):
			writeContentSafetyClaudeStream(w)
		default:
			writeContentSafetyClaudeResponse(w, "安全响应")
		}
	}))
	t.Cleanup(upstream.Close)

	settings := config.DefaultContentSafetyConfig()
	settings.SensitiveData.Mode = config.ContentSafetyModeMask
	settings.SensitiveWord.Enabled = true
	settings.SensitiveWord.GamblingEnabled = true
	settings.SensitiveInfo.Enabled = true
	settings.SensitiveInfo.EnabledRules = []string{config.SensitiveInfoRulePhone}
	settings.DangerousCmd.Enabled = true
	settings.DangerousCmd.EnabledRules = []string{config.DangerousCmdRuleDestructive}
	configFile := filepath.Join(t.TempDir(), "config.json")
	configBytes, err := json.Marshal(config.Config{
		Settings: config.SettingsConfig{ContentSafety: settings},
		Upstream: []config.UpstreamConfig{{
			Name: "content-safety-e2e", ServiceType: "claude", BaseURL: upstream.URL,
			APIKeys: []string{"upstream-test-key"},
		}},
		LoadBalance: "failover",
	})
	if err != nil {
		t.Fatalf("序列化端到端配置失败: %v", err)
	}
	if err := os.WriteFile(configFile, configBytes, 0600); err != nil {
		t.Fatalf("写入端到端配置失败: %v", err)
	}
	cfgManager, err := config.NewConfigManager(configFile)
	if err != nil {
		t.Fatalf("创建配置管理器失败: %v", err)
	}
	t.Cleanup(func() {
		if err := cfgManager.Close(); err != nil {
			t.Errorf("关闭配置管理器失败: %v", err)
		}
	})

	metricsManagers := []*metrics.MetricsManager{
		metrics.NewMetricsManager(), metrics.NewMetricsManager(), metrics.NewMetricsManager(),
		metrics.NewMetricsManager(), metrics.NewMetricsManager(),
	}
	t.Cleanup(func() {
		for _, manager := range metricsManagers {
			manager.Stop()
		}
	})
	traceAffinity := session.NewTraceAffinityManager()
	t.Cleanup(traceAffinity.Stop)
	channelScheduler := scheduler.NewChannelScheduler(
		cfgManager,
		metricsManagers[0], metricsManagers[1], metricsManagers[2], metricsManagers[3], metricsManagers[4],
		traceAffinity,
		urlhealth.NewURLManager(30*time.Second, 3),
	)

	store, err := sensitive.NewBlockedStore(filepath.Join(t.TempDir(), "blocked-logs.db"))
	if err != nil {
		t.Fatalf("创建拦截记录存储失败: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("关闭拦截记录存储失败: %v", err)
		}
	})
	pipeline := hooks.NewContentSafetyPipelineWithRecorder(cfgManager, store)

	envCfg := config.NewEnvConfig()
	envCfg.ProxyAccessKey = contentSafetyTestAccessKey
	envCfg.EnableRequestLogs = false
	envCfg.EnableResponseLogs = false
	envCfg.LogLevel = "error"

	router := gin.New()
	router.POST("/v1/messages", Handler(envCfg, cfgManager, channelScheduler, pipeline))
	router.GET("/api/blocked-logs", rootHandlers.GetBlockedLogs(store, nil))
	return router, cfgManager, capture
}

func performContentSafetyMessagesRequest(t *testing.T, router http.Handler, body, requestID string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Api-Key", contentSafetyTestAccessKey)
	request.Header.Set("X-Request-Id", requestID)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

func assertMessagesContentSafetyError(t *testing.T, response *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if response.Code != status {
		t.Fatalf("状态码 = %d，期望 %d，响应 = %s", response.Code, status, response.Body.String())
	}
	var payload struct {
		Type  string `json:"type"`
		Error struct {
			Type string `json:"type"`
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("解析 Messages 错误响应失败: %v，响应 = %s", err, response.Body.String())
	}
	if payload.Type != "error" || payload.Error.Type != "content_safety_error" || payload.Error.Code != code {
		t.Fatalf("Messages 内容安全错误格式不正确: %+v，响应 = %s", payload, response.Body.String())
	}
}

func assertContentSafetyBlockedLog(t *testing.T, entry sensitive.BlockedLog, blockType, ruleName string) {
	t.Helper()
	if entry.ID == 0 || entry.APIType != "messages" || entry.BlockType != blockType ||
		entry.RuleName != ruleName || (entry.ChannelName != "" && entry.ChannelName != "content-safety-e2e") ||
		entry.Model != contentSafetyTestModel || entry.PromptSnippet == "" {
		t.Fatalf("拦截记录不完整: %+v", entry)
	}
}

func writeContentSafetyClaudeResponse(w http.ResponseWriter, text string) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id": "msg-content-safety-e2e", "type": "message", "role": "assistant", "model": contentSafetyTestModel,
		"content":     []map[string]any{{"type": "text", "text": text}},
		"stop_reason": "end_turn", "stop_sequence": nil,
		"usage": map[string]any{"input_tokens": 10, "output_tokens": 5},
	})
}

func writeContentSafetyClaudeStream(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "response writer does not support flushing", http.StatusInternalServerError)
		return
	}
	events := []string{
		"event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg-content-safety-stream\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"" + contentSafetyTestModel + "\",\"content\":[],\"stop_reason\":null,\"stop_sequence\":null,\"usage\":{\"input_tokens\":10,\"output_tokens\":1}}}\n\n",
		"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n",
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"```sh\\nrm -rf\"}}\n\n",
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\" /\\n```\"}}\n\n",
		"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n",
		"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n",
	}
	for _, event := range events {
		_, _ = io.WriteString(w, event)
		flusher.Flush()
	}
}
