// 本文件锁定 tryWithAllKeys 主循环中"同候选兼容性重试"与"quota 降级"的行为契约：
// prompt_cache_key 不支持 → 移除字段同 key 重试一次；
// 模型满载 → 同 BaseURL+Key 重试一次；
// Responses 渠道内容审核误判 → 请求体等价 Unicode 转义后重试一次；
// quota 相关失败 → 成功后对该 key 执行降级。
// 改动主循环前先跑本文件，防止重试语义漂移。
package proxycore

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/insistanan/api-proxy/internal/config"
	"github.com/insistanan/api-proxy/internal/metrics"
	"github.com/insistanan/api-proxy/internal/scheduler"
	"github.com/insistanan/api-proxy/internal/types"
)

// compatRetryHarness 聚合主循环测试所需的最小依赖。
type compatRetryHarness struct {
	cfgManager       *config.ConfigManager
	metricsManager   *metrics.MetricsManager
	channelScheduler *scheduler.ChannelScheduler
}

func newCompatRetryHarness(t *testing.T) *compatRetryHarness {
	t.Helper()
	cfgManager, err := config.NewConfigManager(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatalf("创建配置管理器失败: %v", err)
	}
	t.Cleanup(func() { _ = cfgManager.Close() })

	newMetrics := func() *metrics.MetricsManager {
		m := metrics.NewMetricsManager()
		t.Cleanup(m.Stop)
		return m
	}
	// 五种 kind 都要提供：主循环会按 attempt 的 kind 取用指标管理器。
	messages := newMetrics()
	responses := newMetrics()
	gemini := newMetrics()
	chat := newMetrics()
	images := newMetrics()

	channelScheduler := scheduler.NewChannelScheduler(cfgManager, messages, responses, gemini, chat, images, nil, nil, nil, nil)
	t.Cleanup(channelScheduler.Stop)

	return &compatRetryHarness{cfgManager: cfgManager, metricsManager: messages, channelScheduler: channelScheduler}
}

// rotatingKeySelector 按 APIKeys 顺序轮换，跳过已失败的 key（模拟真实 NextAPIKey）。
func rotatingKeySelector(up *config.UpstreamConfig, failedKeys map[string]bool) (string, error) {
	for _, key := range up.APIKeys {
		if !failedKeys[key] {
			return key, nil
		}
	}
	return "", errors.New("没有可用密钥")
}

func readRequestBodyBytes(c *gin.Context) []byte {
	body, _ := io.ReadAll(c.Request.Body)
	return body
}

// recordingServer 记录收到的原始请求体并按序返回预设响应。
type recordingServer struct {
	server  *httptest.Server
	mu      sync.Mutex
	bodies  []string
	headers []http.Header
}

func newRecordingServer(t *testing.T, respond func(seq int, r *http.Request, body string) (int, string)) *recordingServer {
	t.Helper()
	rs := &recordingServer{}
	rs.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		rs.mu.Lock()
		seq := len(rs.bodies)
		rs.bodies = append(rs.bodies, string(raw))
		rs.headers = append(rs.headers, r.Header.Clone())
		rs.mu.Unlock()
		status, payload := respond(seq, r, string(raw))
		w.WriteHeader(status)
		_, _ = w.Write([]byte(payload))
	}))
	t.Cleanup(rs.server.Close)
	return rs
}

func (rs *recordingServer) requestCount() int {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	return len(rs.bodies)
}

func (rs *recordingServer) bodyAt(i int) string {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	return rs.bodies[i]
}

func (rs *recordingServer) headerAt(i int) http.Header {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	return rs.headers[i]
}

func runAttempt(h *compatRetryHarness, t *testing.T, upstream *config.UpstreamConfig, requestBody []byte, buildRequest func(*gin.Context, *config.UpstreamConfig, string) (*http.Request, error), handleSuccess func(*gin.Context, *http.Response, *config.UpstreamConfig, string) (*types.Usage, error)) UpstreamAttemptResult {
	t.Helper()
	return runConfiguredAttempt(t, UpstreamAttempt{
		Context:          newAttemptTestContext(context.Background()),
		EnvConfig:        &config.EnvConfig{LogLevel: "error"},
		ConfigManager:    h.cfgManager,
		ChannelScheduler: h.channelScheduler,
		Kind:             scheduler.ChannelKindMessages,
		MetricsManager:   h.metricsManager,
		Upstream:         upstream,
		RequestedModel:   "requested-model",
		URLResults:       BuildDefaultURLResults(upstream.GetAllBaseURLs()),
		NextAPIKey:       rotatingKeySelector,
		BuildRequest:     buildRequest,
		HandleSuccess:    handleSuccess,
	})
}

func successRecorder(successKeys *[]string) func(*gin.Context, *http.Response, *config.UpstreamConfig, string) (*types.Usage, error) {
	return func(_ *gin.Context, resp *http.Response, _ *config.UpstreamConfig, apiKey string) (*types.Usage, error) {
		defer resp.Body.Close()
		if _, err := io.ReadAll(resp.Body); err != nil {
			return nil, err
		}
		*successKeys = append(*successKeys, apiKey)
		return &types.Usage{}, nil
	}
}

// TestPromptCacheKeyUnsupportedStripsFieldAndRetriesSameKey 锚点：首次明确拒绝
// prompt_cache_key 时，移除字段后用同一渠道、同一 key 重试一次并记住能力。
func TestPromptCacheKeyUnsupportedStripsFieldAndRetriesSameKey(t *testing.T) {
	h := newCompatRetryHarness(t)
	rs := newRecordingServer(t, func(seq int, r *http.Request, body string) (int, string) {
		if r.Header.Get("x-prompt-cache-key") != "" {
			return http.StatusBadRequest, `{"error":{"message":"unsupported parameter","param":"prompt_cache_key"}}`
		}
		return http.StatusOK, `{"ok":true}`
	})

	upstream := &config.UpstreamConfig{
		BaseURL: rs.server.URL,
		APIKeys: []string{"key-a"},
		ID:      "up-1",
		Name:    "compat-test",
	}
	var successKeys []string

	result := runAttempt(h, t, upstream, []byte(`{}`),
		func(_ *gin.Context, up *config.UpstreamConfig, apiKey string) (*http.Request, error) {
			req, err := http.NewRequest(http.MethodPost, up.BaseURL, nil)
			if err != nil {
				return nil, err
			}
			req.Header.Set("x-api-key", apiKey)
			if !up.DisablePromptCacheKey {
				req.Header.Set("x-prompt-cache-key", "cache-scope")
			}
			return req, nil
		},
		successRecorder(&successKeys))

	if !result.Handled || result.SuccessKey != "key-a" {
		t.Fatalf("Handled=%v SuccessKey=%q, want 同一 key 重试后成功", result.Handled, result.SuccessKey)
	}
	if n := rs.requestCount(); n != 2 {
		t.Fatalf("上游请求数 = %d, want 2（首次拒绝 + 移除字段重试）", n)
	}
	if rs.headerAt(0).Get("x-prompt-cache-key") == "" {
		t.Fatal("首次请求应携带 prompt_cache_key")
	}
	if rs.headerAt(1).Get("x-prompt-cache-key") != "" {
		t.Fatal("重试请求应已移除 prompt_cache_key")
	}
	if len(successKeys) != 1 || successKeys[0] != "key-a" {
		t.Fatalf("成功 key = %v, want [key-a]", successKeys)
	}
}

// TestModelCapacityErrorRetriesSameCandidateOnce 锚点：模型满载不计入 Key 失败，
// 使用同一 BaseURL+Key 重试一次；仍满载才放弃该 key。
func TestModelCapacityErrorRetriesSameCandidateOnce(t *testing.T) {
	h := newCompatRetryHarness(t)
	capacityBody := `{"error":{"type":"overloaded_error","message":"selected model is at capacity"}}`
	rs := newRecordingServer(t, func(seq int, r *http.Request, body string) (int, string) {
		if seq == 0 {
			return http.StatusServiceUnavailable, capacityBody
		}
		return http.StatusOK, `{"ok":true}`
	})

	upstream := &config.UpstreamConfig{
		BaseURL: rs.server.URL,
		APIKeys: []string{"key-a"},
		Name:    "capacity-test",
	}
	var successKeys []string

	result := runAttempt(h, t, upstream, []byte(`{}`),
		func(_ *gin.Context, up *config.UpstreamConfig, apiKey string) (*http.Request, error) {
			return http.NewRequest(http.MethodPost, up.BaseURL, nil)
		},
		successRecorder(&successKeys))

	if !result.Handled || result.SuccessKey != "key-a" {
		t.Fatalf("Handled=%v SuccessKey=%q, want 同候选重试后成功", result.Handled, result.SuccessKey)
	}
	if n := rs.requestCount(); n != 2 {
		t.Fatalf("上游请求数 = %d, want 2（满载一次 + 重试成功）", n)
	}
}

func TestRateLimit429RetriesSameCandidateOnce(t *testing.T) {
	h := newCompatRetryHarness(t)
	rateLimitBody := `{"error":{"type":"rate_limit_error","message":"Rate limit exceeded, please slow down"}}`
	rs := newRecordingServer(t, func(seq int, r *http.Request, body string) (int, string) {
		if seq == 0 {
			return http.StatusTooManyRequests, rateLimitBody
		}
		return http.StatusOK, `{"ok":true}`
	})

	upstream := &config.UpstreamConfig{
		BaseURL: rs.server.URL,
		APIKeys: []string{"key-a"},
		Name:    "ratelimit-test",
	}
	var successKeys []string

	result := runAttempt(h, t, upstream, []byte(`{}`),
		func(_ *gin.Context, up *config.UpstreamConfig, apiKey string) (*http.Request, error) {
			return http.NewRequest(http.MethodPost, up.BaseURL, nil)
		},
		successRecorder(&successKeys))

	if !result.Handled || result.SuccessKey != "key-a" {
		t.Fatalf("Handled=%v SuccessKey=%q, want 同候选在 429 退避后重试成功", result.Handled, result.SuccessKey)
	}
}

// TestContentPolicyEscapeRetryOnResponsesChannel 锚点：Responses 渠道把请求正文中的
// 审核错误码原文误判为拦截时，保持 JSON 值不变改用 Unicode 转义重试一次。
func TestContentPolicyEscapeRetryOnResponsesChannel(t *testing.T) {
	h := newCompatRetryHarness(t)
	const marker = "sensitive_words_detected"
	policyBody := `{"error":{"code":"` + marker + `"}}`
	rs := newRecordingServer(t, func(seq int, r *http.Request, body string) (int, string) {
		if strings.Contains(body, marker) {
			return http.StatusBadRequest, policyBody
		}
		return http.StatusOK, `{"ok":true}`
	})

	requestBody := []byte(`{"model":"m","input":[{"role":"user","content":"介绍一下 ` + marker + ` 的由来"}]}`)
	upstream := &config.UpstreamConfig{
		BaseURL:     rs.server.URL,
		APIKeys:     []string{"key-a"},
		ServiceType: "responses",
		Name:        "escape-test",
	}
	var successKeys []string

	attempt := runConfiguredAttempt(t, UpstreamAttempt{
		Context:          newAttemptTestContext(context.Background()),
		EnvConfig:        &config.EnvConfig{LogLevel: "error"},
		ConfigManager:    h.cfgManager,
		ChannelScheduler: h.channelScheduler,
		Kind:             scheduler.ChannelKindResponses,
		MetricsManager:   h.metricsManager,
		Upstream:         upstream,
		RequestedModel:   "requested-model",
		RequestBody:      requestBody,
		URLResults:       BuildDefaultURLResults(upstream.GetAllBaseURLs()),
		NextAPIKey:       rotatingKeySelector,
		BuildRequest: func(c *gin.Context, up *config.UpstreamConfig, apiKey string) (*http.Request, error) {
			// 从 gin context 读体：转义重试会经 RestoreRequestBody 替换它，
			// 这样才能验证"等价转义后的请求体"真正发往上游。
			req, err := http.NewRequest(http.MethodPost, up.BaseURL, nil)
			if err != nil {
				return nil, err
			}
			req.Body = io.NopCloser(strings.NewReader(string(readRequestBodyBytes(c))))
			return req, nil
		},
		HandleSuccess: successRecorder(&successKeys),
	})

	if !attempt.Handled || attempt.SuccessKey != "key-a" {
		t.Fatalf("Handled=%v SuccessKey=%q, want 转义重试后成功", attempt.Handled, attempt.SuccessKey)
	}
	if n := rs.requestCount(); n != 2 {
		t.Fatalf("上游请求数 = %d, want 2（原文被拒 + 转义重试）", n)
	}
	if !strings.Contains(rs.bodyAt(0), marker) {
		t.Fatal("首次请求应包含审核错误码原文")
	}
	if strings.Contains(rs.bodyAt(1), marker) {
		t.Fatal("重试请求不应再包含可被字节扫描误判的原文")
	}
	var escaped map[string]interface{}
	if err := json.Unmarshal([]byte(rs.bodyAt(1)), &escaped); err != nil {
		t.Fatalf("转义后请求体应为合法 JSON: %v", err)
	}
	if len(successKeys) != 1 {
		t.Fatalf("成功次数 = %d, want 1", len(successKeys))
	}
}

// result 避免结构体字面量在 if 条件中触发的 vet 语义歧义。
func result(r UpstreamAttemptResult) UpstreamAttemptResult { return r }

func TestCompatibilityRetry_MissingReasoningReturnsClientError(t *testing.T) {
	historyErr := &types.ReasoningHistoryError{Message: "原始 reasoning_content 无法恢复"}
	response := &http.Response{StatusCode: http.StatusBadRequest}
	var reported error
	_, _, succeeded, aborted := applyCompatibilityRetryResult(
		compatibilityRetryResult{PreparationStage: "build_request", Err: historyErr},
		response, []byte(`{"error":"reasoning_content required"}`), false,
		"Messages", compatRetryLogs{}, func(err error) { reported = err },
	)
	if !errors.Is(reported, historyErr) || aborted == nil || !aborted.Handled || succeeded {
		t.Fatalf("无法恢复原文应向客户端报错并终止重试：reported=%v aborted=%+v succeeded=%v", reported, aborted, succeeded)
	}
	if aborted.Health != channelHealthNeutral || !errors.Is(aborted.LastError, historyErr) {
		t.Fatalf("历史缺失不能记为上游故障：%+v", aborted)
	}
}

// TestQuotaFailureDeprioritizesKeyAfterSuccess 锚点：quota 相关失败在本次请求内
// 记为待降级候选，任一 key 成功后统一执行降级回调。
func TestQuotaFailureDeprioritizesKeyAfterSuccess(t *testing.T) {
	h := newCompatRetryHarness(t)
	rs := newRecordingServer(t, func(seq int, r *http.Request, body string) (int, string) {
		if r.Header.Get("x-api-key") == "key-a" {
			return http.StatusForbidden, `{"error":{"message":"insufficient credit"}}`
		}
		return http.StatusOK, `{"ok":true}`
	})

	upstream := &config.UpstreamConfig{
		BaseURL: rs.server.URL,
		APIKeys: []string{"key-a", "key-b"},
		Name:    "quota-test",
	}
	var successKeys []string
	var deprioritized []string

	result := runConfiguredAttempt(t, UpstreamAttempt{
		Context:          newAttemptTestContext(context.Background()),
		EnvConfig:        &config.EnvConfig{LogLevel: "error"},
		ConfigManager:    h.cfgManager,
		ChannelScheduler: h.channelScheduler,
		Kind:             scheduler.ChannelKindMessages,
		MetricsManager:   h.metricsManager,
		Upstream:         upstream,
		RequestedModel:   "requested-model",
		URLResults:       BuildDefaultURLResults(upstream.GetAllBaseURLs()),
		NextAPIKey:       rotatingKeySelector,
		BuildRequest: func(_ *gin.Context, up *config.UpstreamConfig, apiKey string) (*http.Request, error) {
			req, err := http.NewRequest(http.MethodPost, up.BaseURL, nil)
			if err != nil {
				return nil, err
			}
			req.Header.Set("x-api-key", apiKey)
			return req, nil
		},
		DeprioritizeKey: func(apiKey string) {
			deprioritized = append(deprioritized, apiKey)
		},
		HandleSuccess: successRecorder(&successKeys),
	})

	if !result.Handled || result.SuccessKey != "key-b" {
		t.Fatalf("Handled=%v SuccessKey=%q, want key-b 成功", result.Handled, result.SuccessKey)
	}
	if len(deprioritized) != 1 || deprioritized[0] != "key-a" {
		t.Fatalf("降级 key = %v, want [key-a]", deprioritized)
	}
	if len(successKeys) != 1 || successKeys[0] != "key-b" {
		t.Fatalf("成功 key = %v, want [key-b]", successKeys)
	}
}
