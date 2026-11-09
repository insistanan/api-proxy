package proxycore

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/insistanan/api-proxy/internal/config"
	"github.com/insistanan/api-proxy/internal/handlers/hooks"
	"github.com/insistanan/api-proxy/internal/metrics"
	"github.com/insistanan/api-proxy/internal/scheduler"
	"github.com/insistanan/api-proxy/internal/sensitive"
	"github.com/insistanan/api-proxy/internal/types"
)

// runProxyTestEnv 提供 RunProxyRequest 特征测试所需的最小运行环境。
// 只装配 messages 一种渠道类型，其余协议管理器保持 nil。
type runProxyTestEnv struct {
	envCfg           *config.EnvConfig
	cfgManager       *config.ConfigManager
	channelScheduler *scheduler.ChannelScheduler
}

func TestRunProxyRequestFiltersPrivateParams(t *testing.T) {
	env := newRunProxyTestEnv(t)
	server := newRecordingUpstreamServer(t, nil)
	addTestUpstream(t, env, "ch-0", server.URL(), "key-a")

	var receivedBody string
	rec := &specRecorder{}
	spec := rec.newSpec()
	spec.BuildUpstreamRequest = func(c *gin.Context, up *config.UpstreamConfig, apiKey string, bodyBytes []byte) (*http.Request, error) {
		receivedBody = string(bodyBytes)
		req, err := http.NewRequest(http.MethodPost, up.GetEffectiveBaseURL()+"/v1/test", bytes.NewReader(bodyBytes))
		if err != nil {
			return nil, err
		}
		req.Header.Set("x-api-key", apiKey)
		return req, nil
	}

	inputBody := `{"model":"test-model","_internal_trace_id":"xyz-123","messages":[{"role":"user","content":"hi","_debug":true}]}`
	w := performRunProxyRequest(t, env, spec, inputBody, "test-access-key")
	if w.Code != http.StatusOK {
		t.Fatalf("期望 200，实际: %d", w.Code)
	}
	if strings.Contains(receivedBody, "_internal_trace_id") {
		t.Errorf("上游收到的请求体仍包含 _internal_trace_id: %s", receivedBody)
	}
	if strings.Contains(receivedBody, "_debug") {
		t.Errorf("上游收到的请求体仍包含 _debug: %s", receivedBody)
	}
}

func TestRunProxyRequestMasksBeforeRoutingAndUpstreamBuild(t *testing.T) {
	env := newRunProxyTestEnv(t)
	server := newRecordingUpstreamServer(t, nil)
	addTestUpstream(t, env, "ch-0", server.URL(), "key-a")
	settings := env.cfgManager.GetSettings()
	settings.ContentSafety.SensitiveData.Mode = config.ContentSafetyModeMask
	settings.ContentSafety.SensitiveInfo.Enabled = true
	settings.ContentSafety.SensitiveInfo.EnabledRules = []string{config.SensitiveInfoRulePhone}
	if err := env.cfgManager.UpdateSettings(settings); err != nil {
		t.Fatalf("更新内容安全设置失败: %v", err)
	}

	var receivedBody string
	rec := &specRecorder{}
	spec := rec.newSpec()
	spec.HookPipeline = hooks.NewContentSafetyPipeline(env.cfgManager)
	spec.PreRoute = func(c *gin.Context, body []byte, model string, conversationID string, startTime time.Time) bool {
		if strings.Contains(string(body), "13800138000") || !strings.Contains(string(body), "MASKED_PHONE_") {
			t.Errorf("路由阶段收到未脱敏请求体: %s", body)
		}
		return false
	}
	spec.BuildUpstreamRequest = func(c *gin.Context, up *config.UpstreamConfig, apiKey string, bodyBytes []byte) (*http.Request, error) {
		receivedBody = string(bodyBytes)
		req, err := http.NewRequest(http.MethodPost, up.GetEffectiveBaseURL()+"/v1/test", bytes.NewReader(bodyBytes))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("x-api-key", apiKey)
		return req, nil
	}

	w := performRunProxyRequest(t, env, spec, `{"model":"test-model","messages":[{"role":"user","content":"phone 13800138000"}]}`, "test-access-key")
	if w.Code != http.StatusOK {
		t.Fatalf("期望 200，实际 %d，响应体: %s", w.Code, w.Body.String())
	}
	if strings.Contains(receivedBody, "13800138000") || !strings.Contains(receivedBody, "MASKED_PHONE_") {
		t.Fatalf("上游构建阶段收到未脱敏请求体: %s", receivedBody)
	}
}

func TestRunProxyRequestRecordsRequestAndConversationIDs(t *testing.T) {
	env := newRunProxyTestEnv(t)
	settings := env.cfgManager.GetSettings()
	settings.ContentSafety.SensitiveWord.Enabled = true
	settings.ContentSafety.SensitiveWord.CustomWords = []string{"仅用于代理入口归组测试的禁词"}
	if err := env.cfgManager.UpdateSettings(settings); err != nil {
		t.Fatalf("更新内容安全设置失败: %v", err)
	}

	store, err := sensitive.NewBlockedStore(filepath.Join(t.TempDir(), "blocked.db"))
	if err != nil {
		t.Fatalf("创建拦截记录存储失败: %v", err)
	}
	defer store.Close()

	rec := &specRecorder{}
	spec := rec.newSpec()
	spec.HookPipeline = hooks.NewContentSafetyPipelineWithRecorder(env.cfgManager, store)
	requestBody := `{"model":"test-model","conversation_id":"thread-log-test","messages":[{"role":"user","content":"包含仅用于代理入口归组测试的禁词"}]}`
	for attempt := 0; attempt < 2; attempt++ {
		response := performRunProxyRequest(t, env, spec, requestBody, "test-access-key")
		if response.Code != http.StatusForbidden {
			t.Fatalf("第 %d 次请求状态码 = %d，期望 403，响应体: %s", attempt+1, response.Code, response.Body.String())
		}
	}

	page, err := store.ListGrouped(context.Background(), sensitive.BlockedLogListOptions{Page: 1, PageSize: 20})
	if err != nil {
		t.Fatalf("读取会话分组失败: %v", err)
	}
	if page.Total != 2 || page.TotalGroups != 1 || len(page.Groups) != 1 || page.Groups[0].Count != 2 {
		t.Fatalf("代理入口拦截记录未归入同一会话: %+v", page)
	}
	group := page.Groups[0]
	if group.ConversationID == "" {
		t.Fatal("代理入口拦截记录缺少内部会话 ID")
	}
	entries, err := store.ListGroupEntries(context.Background(), sensitive.BlockedLogListOptions{Page: 1, PageSize: 20}, group.Key)
	if err != nil {
		t.Fatalf("读取会话组内明细失败: %v", err)
	}
	if entries.Total != 2 || len(entries.Logs) != 2 {
		t.Fatalf("会话组内明细数量错误: %+v", entries)
	}
	requestIDs := make(map[string]struct{}, len(entries.Logs))
	for _, entry := range entries.Logs {
		if entry.RequestID == "" || entry.ConversationID != group.ConversationID {
			t.Fatalf("拦截记录请求或会话 ID 不完整: %+v", entry)
		}
		requestIDs[entry.RequestID] = struct{}{}
	}
	if len(requestIDs) != 2 {
		t.Fatalf("两次 HTTP 请求应保留不同 requestId，实际: %+v", entries.Logs)
	}
}

func newRunProxyTestEnv(t *testing.T) *runProxyTestEnv {
	t.Helper()
	gin.SetMode(gin.TestMode)

	cfgManager, err := config.NewConfigManager(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatalf("创建配置管理器失败: %v", err)
	}
	t.Cleanup(func() { _ = cfgManager.Close() })

	metricsManager := metrics.NewMetricsManager()
	t.Cleanup(metricsManager.Stop)

	channelScheduler := scheduler.NewChannelScheduler(cfgManager, metricsManager, nil, nil, nil, nil, nil, nil)
	t.Cleanup(channelScheduler.Stop)

	return &runProxyTestEnv{
		envCfg: &config.EnvConfig{
			LogLevel:           "error",
			MaxRequestBodySize: 50 * 1024 * 1024,
			RequestTimeout:     5000,
			StreamIdleTimeout:  300,
			ProxyAccessKey:     "test-access-key",
		},
		cfgManager:       cfgManager,
		channelScheduler: channelScheduler,
	}
}

// specRecorder 记录各协议阶段是否被调用，用于断言骨架的分派行为。
type specRecorder struct {
	mu            sync.Mutex
	preRouteCalls int
	parseCalls    int
	buildCalls    int
	successCalls  int
}

func (r *specRecorder) newSpec() ProtocolSpec {
	spec := ProtocolSpec{
		Kind:    scheduler.ChannelKindMessages,
		LogName: "Messages",
		ParseRequest: func(c *gin.Context, bodyBytes []byte) (string, bool, []string, bool) {
			r.mu.Lock()
			r.parseCalls++
			r.mu.Unlock()
			var payload struct {
				Model string `json:"model"`
			}
			if err := json.Unmarshal(bodyBytes, &payload); err != nil || payload.Model == "" {
				c.JSON(http.StatusBadRequest, gin.H{"error": "invalid model"})
				return "", false, nil, false
			}
			return payload.Model, false, nil, true
		},
		BuildUpstreamRequest: func(c *gin.Context, up *config.UpstreamConfig, apiKey string, bodyBytes []byte) (*http.Request, error) {
			r.mu.Lock()
			r.buildCalls++
			r.mu.Unlock()
			req, err := http.NewRequest(http.MethodPost, up.GetEffectiveBaseURL()+"/v1/test", bytes.NewReader(bodyBytes))
			if err != nil {
				return nil, err
			}
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("x-api-key", apiKey)
			return req, nil
		},
		HandleSuccess: func(c *gin.Context, resp *http.Response, up *config.UpstreamConfig, apiKey string, bodyBytes []byte, startTime time.Time) (*types.Usage, error) {
			r.mu.Lock()
			r.successCalls++
			r.mu.Unlock()
			defer resp.Body.Close()
			respBody, err := io.ReadAll(resp.Body)
			if err != nil {
				return nil, err
			}
			c.Data(http.StatusOK, "application/json", respBody)
			return &types.Usage{}, nil
		},
	}
	return spec
}

func (r *specRecorder) snapshot() (preRoute, parse, build, success int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.preRouteCalls, r.parseCalls, r.buildCalls, r.successCalls
}

func performRunProxyRequest(t *testing.T, env *runProxyTestEnv, spec ProtocolSpec, body string, accessKey string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if accessKey != "" {
		req.Header.Set("Authorization", "Bearer "+accessKey)
	}
	c.Request = req
	RunProxyRequest(c, env.envCfg, env.cfgManager, env.channelScheduler, spec)
	return w
}

func addTestUpstream(t *testing.T, env *runProxyTestEnv, name string, baseURL string, apiKey string) {
	t.Helper()
	if err := env.cfgManager.AddUpstream(config.UpstreamConfig{
		Name:    name,
		BaseURL: baseURL,
		APIKeys: []string{apiKey},
	}); err != nil {
		t.Fatalf("添加测试渠道失败: %v", err)
	}
}

func TestRunProxyRequestAuthFailureAbortsBeforeParse(t *testing.T) {
	env := newRunProxyTestEnv(t)
	rec := &specRecorder{}
	spec := rec.newSpec()

	w := performRunProxyRequest(t, env, spec, `{"model":"test-model"}`, "")

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("期望 401，实际 %d，响应体: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "Invalid proxy access key") {
		t.Fatalf("期望 401 响应体包含 Invalid proxy access key，实际: %s", w.Body.String())
	}
	_, parseCalls, buildCalls, successCalls := rec.snapshot()
	if parseCalls != 0 || buildCalls != 0 || successCalls != 0 {
		t.Fatalf("认证失败后不应进入任何协议阶段，parse=%d build=%d success=%d", parseCalls, buildCalls, successCalls)
	}
}

func TestRunProxyRequestParseFailureStopsFlow(t *testing.T) {
	env := newRunProxyTestEnv(t)
	rec := &specRecorder{}
	spec := rec.newSpec()

	w := performRunProxyRequest(t, env, spec, `{}`, "test-access-key")

	if w.Code != http.StatusBadRequest {
		t.Fatalf("期望 400，实际 %d，响应体: %s", w.Code, w.Body.String())
	}
	_, parseCalls, buildCalls, successCalls := rec.snapshot()
	if parseCalls != 1 {
		t.Fatalf("ParseRequest 应恰好被调用一次，实际 %d", parseCalls)
	}
	if buildCalls != 0 || successCalls != 0 {
		t.Fatalf("ParseRequest 失败后不应构建上游请求，build=%d success=%d", buildCalls, successCalls)
	}
}

func TestRunProxyRequestInvalidChannelIndexMetadataType(t *testing.T) {
	env := newRunProxyTestEnv(t)
	rec := &specRecorder{}
	spec := rec.newSpec()

	w := performRunProxyRequest(t, env, spec, `{"model":"test-model","metadata":"not-an-object"}`, "test-access-key")

	if w.Code != http.StatusBadRequest {
		t.Fatalf("期望 400，实际 %d，响应体: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("解析错误响应失败: %v，响应体: %s", err, w.Body.String())
	}
	if resp.Code != "INVALID_CHANNEL_INDEX" {
		t.Fatalf("期望错误码 INVALID_CHANNEL_INDEX，实际: %s", w.Body.String())
	}
	_, parseCalls, buildCalls, _ := rec.snapshot()
	if parseCalls != 1 || buildCalls != 0 {
		t.Fatalf("metadata 校验失败应发生在 ParseRequest 之后、构建上游之前，parse=%d build=%d", parseCalls, buildCalls)
	}
}

func TestRunProxyRequestRequestedChannelOutOfRange(t *testing.T) {
	env := newRunProxyTestEnv(t)
	server := newRecordingUpstreamServer(t, nil)
	addTestUpstream(t, env, "ch-0", server.URL(), "key-a")

	rec := &specRecorder{}
	spec := rec.newSpec()

	w := performRunProxyRequest(t, env, spec, `{"model":"test-model","metadata":{"channel_index":5}}`, "test-access-key")

	if w.Code != http.StatusBadRequest {
		t.Fatalf("期望 400，实际 %d，响应体: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("解析错误响应失败: %v，响应体: %s", err, w.Body.String())
	}
	if resp.Code != "INVALID_CHANNEL_INDEX" {
		t.Fatalf("期望错误码 INVALID_CHANNEL_INDEX，实际: %s", w.Body.String())
	}
	_, _, buildCalls, _ := rec.snapshot()
	if buildCalls != 0 {
		t.Fatalf("显式渠道越界后不应构建上游请求，build=%d", buildCalls)
	}
}

func TestRunProxyRequestPreRouteShortCircuitsDispatch(t *testing.T) {
	env := newRunProxyTestEnv(t)
	rec := &specRecorder{}
	spec := rec.newSpec()
	spec.PreRoute = func(c *gin.Context, body []byte, model string, conversationID string, startTime time.Time) bool {
		rec.mu.Lock()
		rec.preRouteCalls++
		rec.mu.Unlock()
		if model != "test-model" {
			t.Errorf("PreRoute 收到的 model 应为 test-model，实际 %q", model)
		}
		c.JSON(http.StatusAccepted, gin.H{"handled": true})
		return true
	}

	w := performRunProxyRequest(t, env, spec, `{"model":"test-model"}`, "test-access-key")

	if w.Code != http.StatusAccepted {
		t.Fatalf("期望 202，实际 %d，响应体: %s", w.Code, w.Body.String())
	}
	preRouteCalls, parseCalls, buildCalls, successCalls := rec.snapshot()
	if preRouteCalls != 1 || parseCalls != 1 {
		t.Fatalf("PreRoute 应在 ParseRequest 之后调用一次，preRoute=%d parse=%d", preRouteCalls, parseCalls)
	}
	if buildCalls != 0 || successCalls != 0 {
		t.Fatalf("PreRoute 短路后不应分派上游请求，build=%d success=%d", buildCalls, successCalls)
	}
}

func TestRunProxyRequestSingleChannelEndToEnd(t *testing.T) {
	env := newRunProxyTestEnv(t)
	server := newRecordingUpstreamServer(t, nil)
	addTestUpstream(t, env, "ch-0", server.URL(), "key-a")

	rec := &specRecorder{}
	spec := rec.newSpec()

	w := performRunProxyRequest(t, env, spec, `{"model":"test-model"}`, "test-access-key")

	if w.Code != http.StatusOK {
		t.Fatalf("期望 200，实际 %d，响应体: %s", w.Code, w.Body.String())
	}
	if w.Body.String() != `{"ok":true}` {
		t.Fatalf("期望透传上游响应体 {\"ok\":true}，实际: %s", w.Body.String())
	}
	if got := server.receivedKeys(); len(got) != 1 || got[0] != "key-a" {
		t.Fatalf("上游应收到 key-a，实际: %v", got)
	}
	_, parseCalls, buildCalls, successCalls := rec.snapshot()
	if parseCalls != 1 || buildCalls != 1 || successCalls != 1 {
		t.Fatalf("单渠道成功路径各阶段应各调用一次，parse=%d build=%d success=%d", parseCalls, buildCalls, successCalls)
	}
}

func TestRunProxyRequestMultiChannelEndToEnd(t *testing.T) {
	env := newRunProxyTestEnv(t)
	if err := env.cfgManager.SetFuzzyModeEnabled(true); err != nil {
		t.Fatal(err)
	}
	server := newRecordingUpstreamServer(t, nil)
	addTestUpstream(t, env, "ch-0", server.URL(), "key-a")
	addTestUpstream(t, env, "ch-1", server.URL(), "key-b")

	rec := &specRecorder{}
	spec := rec.newSpec()

	w := performRunProxyRequest(t, env, spec, `{"model":"test-model"}`, "test-access-key")

	if w.Code != http.StatusOK {
		t.Fatalf("期望 200，实际 %d，响应体: %s", w.Code, w.Body.String())
	}
	if w.Body.String() != `{"ok":true}` {
		t.Fatalf("期望透传上游响应体 {\"ok\":true}，实际: %s", w.Body.String())
	}
	keys := server.receivedKeys()
	if len(keys) != 1 {
		t.Fatalf("成功路径应只向上游发送一次请求，实际: %v", keys)
	}
	if keys[0] != "key-a" && keys[0] != "key-b" {
		t.Fatalf("上游收到的 key 应为配置的渠道 key，实际: %v", keys)
	}
	_, parseCalls, buildCalls, successCalls := rec.snapshot()
	if parseCalls != 1 || buildCalls != 1 || successCalls != 1 {
		t.Fatalf("多渠道成功路径各阶段应各调用一次，parse=%d build=%d success=%d", parseCalls, buildCalls, successCalls)
	}
}

func TestRunProxyRequestQuickTestBypassesModelMappingAndDefaultModel(t *testing.T) {
	env := newRunProxyTestEnv(t)
	server := newRecordingUpstreamServer(t, nil)

	if err := env.cfgManager.AddUpstream(config.UpstreamConfig{
		Name:         "ch-redirect",
		BaseURL:      server.URL(),
		APIKeys:      []string{"key-redirect"},
		DefaultModel: "upstream-default-model",
		ModelMapping: map[string][]string{
			"*": {"upstream-mapped-model"},
		},
	}); err != nil {
		t.Fatalf("添加测试渠道失败: %v", err)
	}

	var upstreamReceivedModel string
	rec := &specRecorder{}
	spec := rec.newSpec()
	spec.BuildUpstreamRequest = func(c *gin.Context, up *config.UpstreamConfig, apiKey string, bodyBytes []byte) (*http.Request, error) {
		rec.mu.Lock()
		rec.buildCalls++
		rec.mu.Unlock()

		upstreamModel := config.ResolveUpstreamModel("user-selected-model", up)
		upstreamReceivedModel = upstreamModel

		req, err := http.NewRequest(http.MethodPost, up.GetEffectiveBaseURL()+"/v1/test", bytes.NewReader(bodyBytes))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("x-api-key", apiKey)
		return req, nil
	}

	// 1. 快捷测试请求（带 metadata.purpose = quick_test 与 channel_index = 0）
	w := performRunProxyRequest(t, env, spec, `{"model":"user-selected-model","metadata":{"channel_index":0,"purpose":"quick_test"}}`, "test-access-key")
	if w.Code != http.StatusOK {
		t.Fatalf("快捷测试期望 200，实际 %d，响应体: %s", w.Code, w.Body.String())
	}
	if upstreamReceivedModel != "user-selected-model" {
		t.Fatalf("快捷测试应绕过 ModelMapping 和 DefaultModel 直接使用请求模型，实际: %q", upstreamReceivedModel)
	}

	// 2. 快捷测试请求（通过请求头 X-Proxy-Purpose = quick_test）
	wHeader := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(wHeader)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"user-selected-model","metadata":{"channel_index":0}}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test-access-key")
	req.Header.Set("X-Proxy-Purpose", "quick_test")
	c.Request = req
	RunProxyRequest(c, env.envCfg, env.cfgManager, env.channelScheduler, spec)
	if wHeader.Code != http.StatusOK {
		t.Fatalf("请求头快捷测试期望 200，实际 %d，响应体: %s", wHeader.Code, wHeader.Body.String())
	}
	if upstreamReceivedModel != "user-selected-model" {
		t.Fatalf("请求头快捷测试应绕过 ModelMapping 和 DefaultModel，实际: %q", upstreamReceivedModel)
	}

	// 3. 非测试的指定渠道请求（正常请求仍应触发 ModelMapping / DefaultModel）
	wNormal := performRunProxyRequest(t, env, spec, `{"model":"user-selected-model","metadata":{"channel_index":0}}`, "test-access-key")
	if wNormal.Code != http.StatusOK {
		t.Fatalf("普通请求期望 200，实际 %d，响应体: %s", wNormal.Code, wNormal.Body.String())
	}
	if upstreamReceivedModel != "upstream-default-model" {
		t.Fatalf("非测试请求应正常使用渠道 DefaultModel，实际: %q", upstreamReceivedModel)
	}
}

// recordingUpstreamServer 模拟上游服务：记录收到的 x-api-key 并返回固定成功响应。
type recordingUpstreamServer struct {
	server *httptest.Server

	mu     sync.Mutex
	keys   []string
	bodies []string
}

func newRecordingUpstreamServer(t *testing.T, handler func(w http.ResponseWriter, r *http.Request)) *recordingUpstreamServer {
	t.Helper()
	rec := &recordingUpstreamServer{}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/test", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		rec.mu.Lock()
		rec.keys = append(rec.keys, r.Header.Get("x-api-key"))
		rec.bodies = append(rec.bodies, string(body))
		rec.mu.Unlock()
		if handler != nil {
			handler(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	rec.server = httptest.NewServer(mux)
	t.Cleanup(rec.server.Close)
	return rec
}

func (s *recordingUpstreamServer) URL() string {
	return s.server.URL
}

func (s *recordingUpstreamServer) receivedKeys() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.keys...)
}

func (s *recordingUpstreamServer) receivedBodies() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.bodies...)
}
