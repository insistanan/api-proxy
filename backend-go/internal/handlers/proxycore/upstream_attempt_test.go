package proxycore

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BenedictKing/api-proxy/internal/config"
	"github.com/BenedictKing/api-proxy/internal/metrics"
	"github.com/BenedictKing/api-proxy/internal/scheduler"
	"github.com/BenedictKing/api-proxy/internal/types"
	"github.com/gin-gonic/gin"
)

func TestUpstreamAttemptDisablesModelFailoverAfterFirstMapping(t *testing.T) {
	cfgManager, err := config.NewConfigManager(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatalf("创建配置管理器失败: %v", err)
	}
	t.Cleanup(func() { _ = cfgManager.Close() })

	metricsManager := metrics.NewMetricsManager()
	t.Cleanup(metricsManager.Stop)

	upstream := &config.UpstreamConfig{
		BaseURL: "https://example.com",
		APIKeys: []string{"test-key"},
		ModelMapping: map[string][]string{
			"requested-model": {"first-model", "second-model"},
		},
	}
	stopErr := errors.New("stop after observing mapped model")
	var resolvedModels []string

	result := (UpstreamAttempt{
		Context:            newAttemptTestContext(context.Background()),
		EnvConfig:          &config.EnvConfig{LogLevel: "error"},
		ConfigManager:      cfgManager,
		MetricsManager:     metricsManager,
		Upstream:           upstream,
		RequestedModel:     "requested-model",
		AllowModelFailover: false,
		URLResults:         BuildDefaultURLResults(upstream.GetAllBaseURLs()),
		NextAPIKey: func(upstream *config.UpstreamConfig, _ map[string]bool) (string, error) {
			resolvedModels = append(resolvedModels, config.ResolveUpstreamModel("requested-model", upstream))
			return "", stopErr
		},
		BuildRequest: func(*gin.Context, *config.UpstreamConfig, string) (*http.Request, error) {
			return nil, errors.New("unexpected build request")
		},
		HandleSuccess: func(*gin.Context, *http.Response, *config.UpstreamConfig, string) (*types.Usage, error) {
			return nil, errors.New("unexpected success handler")
		},
	}).TryWithModelMappingFailover()

	if result.Handled {
		t.Fatalf("Handled = true, want false")
	}
	if !errors.Is(result.LastError, stopErr) {
		t.Fatalf("LastError = %v, want %v", result.LastError, stopErr)
	}
	if len(resolvedModels) != 1 || resolvedModels[0] != "first-model" {
		t.Fatalf("实际尝试模型 = %v, want [first-model]", resolvedModels)
	}
}

func TestUpstreamAttemptStopsModelFailoverOnContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	result := (UpstreamAttempt{
		Context: newAttemptTestContext(ctx),
		Upstream: &config.UpstreamConfig{
			ModelMapping: map[string][]string{
				"requested-model": {"first-model", "second-model"},
			},
		},
		RequestedModel:     "requested-model",
		AllowModelFailover: true,
	}).TryWithModelMappingFailover()

	if !result.Handled {
		t.Fatalf("Handled = false, want true")
	}
	if !errors.Is(result.LastError, context.Canceled) {
		t.Fatalf("LastError = %v, want context.Canceled", result.LastError)
	}
	if result.SuccessKey != "" || result.FailoverError != nil || result.Usage != nil {
		t.Fatalf("取消结果包含意外成功或故障转移数据: %+v", result)
	}
}

func TestUpstreamAttemptBuildPreparedRequestClassifiesBuildError(t *testing.T) {
	buildErr := errors.New("build request failed")
	attempt := UpstreamAttempt{
		Context: newAttemptTestContext(context.Background()),
		BuildRequest: func(*gin.Context, *config.UpstreamConfig, string) (*http.Request, error) {
			return nil, buildErr
		},
	}

	request, stage, err := attempt.buildPreparedAttemptRequest(&config.UpstreamConfig{}, "test-key")
	if request != nil {
		t.Fatalf("request = %v, want nil", request)
	}
	if stage != "build_request" {
		t.Fatalf("stage = %q, want build_request", stage)
	}
	if !errors.Is(err, buildErr) {
		t.Fatalf("err = %v, want %v", err, buildErr)
	}
}

func newAttemptTestContext(ctx context.Context) *gin.Context {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil).WithContext(ctx)
	return c
}

// truncatedBodyHandler 写出声明长度与实际不符的 400 响应：客户端
// io.ReadAll 读取时必然得到 unexpected EOF，模拟上游连接半途断开。
func truncatedBodyHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Length", "100")
	w.WriteHeader(http.StatusBadRequest)
	_, _ = w.Write([]byte(`{"error":"truncated`))
	if hijacker, ok := w.(http.Hijacker); ok {
		if conn, _, err := hijacker.Hijack(); err == nil {
			_ = conn.Close() // 立即断开，制造 unexpected EOF
		}
	}
}

// TestUpstreamAttemptReadBodyErrorFailsOverToNextKey 锚点：错误响应体读取
// 失败必须按网络故障换 Key，不得把截断 body 用于错误分类或回给客户端。
func TestUpstreamAttemptReadBodyErrorFailsOverToNextKey(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("x-api-key")
		if key == "key-a" {
			truncatedBodyHandler(w, r)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	cfgManager, err := config.NewConfigManager(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatalf("创建配置管理器失败: %v", err)
	}
	t.Cleanup(func() { _ = cfgManager.Close() })

	metricsManager := metrics.NewMetricsManager()
	t.Cleanup(metricsManager.Stop)

	channelScheduler := scheduler.NewChannelScheduler(cfgManager, metricsManager, nil, nil, nil, nil, nil, nil)
	t.Cleanup(channelScheduler.Stop)

	upstream := &config.UpstreamConfig{
		BaseURL: server.URL,
		APIKeys: []string{"key-a", "key-b"},
	}
	var successKeys []string

	result := (UpstreamAttempt{
		Context:          newAttemptTestContext(context.Background()),
		EnvConfig:        &config.EnvConfig{LogLevel: "error"},
		ConfigManager:    cfgManager,
		ChannelScheduler: channelScheduler,
		Kind:             scheduler.ChannelKindMessages,
		MetricsManager:   metricsManager,
		Upstream:         upstream,
		RequestedModel:   "requested-model",
		URLResults:       BuildDefaultURLResults(upstream.GetAllBaseURLs()),
		NextAPIKey: func(up *config.UpstreamConfig, failedKeys map[string]bool) (string, error) {
			for _, key := range up.APIKeys {
				if !failedKeys[key] {
					return key, nil
				}
			}
			return "", errors.New("没有可用密钥")
		},
		BuildRequest: func(_ *gin.Context, up *config.UpstreamConfig, apiKey string) (*http.Request, error) {
			req, err := http.NewRequest(http.MethodPost, server.URL, nil)
			if err != nil {
				return nil, err
			}
			req.Header.Set("x-api-key", apiKey)
			return req, nil
		},
		HandleSuccess: func(_ *gin.Context, resp *http.Response, _ *config.UpstreamConfig, apiKey string) (*types.Usage, error) {
			defer resp.Body.Close()
			if _, err := io.ReadAll(resp.Body); err != nil {
				return nil, err
			}
			successKeys = append(successKeys, apiKey)
			return &types.Usage{}, nil
		},
	}).TryWithModelMappingFailover()

	if !result.Handled {
		t.Fatalf("Handled = false, want true（第二个 key 应成功）")
	}
	if result.SuccessKey != "key-b" {
		t.Fatalf("SuccessKey = %q, want key-b（读取失败应换 key 而非用截断 body 分类）", result.SuccessKey)
	}
	if len(successKeys) != 1 || successKeys[0] != "key-b" {
		t.Fatalf("实际成功 key = %v, want [key-b]", successKeys)
	}
	if result.LastError != nil {
		t.Fatalf("LastError = %v, want nil", result.LastError)
	}
}

// 切片 A 反证：HTTP 200 + JSON 错误信封必须按 Key 失败继续 failover，
// 不得当成功交给 HandleSuccess。
func TestUpstreamAttempt2xxErrorEnvelopeFailsOverToNextKey(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"error":{"message":"boom","type":"server_error","code":"internal"}}`))
	}))
	defer server.Close()

	cfgManager, err := config.NewConfigManager(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatalf("创建配置管理器失败: %v", err)
	}
	t.Cleanup(func() { _ = cfgManager.Close() })

	metricsManager := metrics.NewMetricsManager()
	t.Cleanup(metricsManager.Stop)

	channelScheduler := scheduler.NewChannelScheduler(cfgManager, metricsManager, nil, nil, nil, nil, nil, nil)
	t.Cleanup(channelScheduler.Stop)

	upstream := &config.UpstreamConfig{
		BaseURL: server.URL,
		APIKeys: []string{"key-a", "key-b"},
	}
	handleSuccessCalls := 0

	result := (UpstreamAttempt{
		Context:          newAttemptTestContext(context.Background()),
		EnvConfig:        &config.EnvConfig{LogLevel: "error"},
		ConfigManager:    cfgManager,
		ChannelScheduler: channelScheduler,
		Kind:             scheduler.ChannelKindMessages,
		MetricsManager:   metricsManager,
		Upstream:         upstream,
		RequestedModel:   "requested-model",
		URLResults:       BuildDefaultURLResults(upstream.GetAllBaseURLs()),
		NextAPIKey: func(up *config.UpstreamConfig, failedKeys map[string]bool) (string, error) {
			for _, key := range up.APIKeys {
				if !failedKeys[key] {
					return key, nil
				}
			}
			return "", errors.New("没有可用密钥")
		},
		BuildRequest: func(_ *gin.Context, up *config.UpstreamConfig, apiKey string) (*http.Request, error) {
			req, err := http.NewRequest(http.MethodPost, server.URL, nil)
			if err != nil {
				return nil, err
			}
			req.Header.Set("x-api-key", apiKey)
			return req, nil
		},
		HandleSuccess: func(_ *gin.Context, resp *http.Response, _ *config.UpstreamConfig, apiKey string) (*types.Usage, error) {
			handleSuccessCalls++
			defer resp.Body.Close()
			return &types.Usage{}, nil
		},
	}).TryWithModelMappingFailover()

	// 全部 Key 都被信封判失败：Handled=false（可 failover 到其他渠道），且
	// HandleSuccess 从未被调用（错误没有被当成功消费）。
	if result.Handled {
		t.Fatalf("2xx 错误信封不应判为 Handled")
	}
	if handleSuccessCalls != 0 {
		t.Fatalf("HandleSuccess 被调用 %d 次，错误信封不应交给成功路径", handleSuccessCalls)
	}
	if result.FailoverError == nil {
		t.Fatalf("应携带 FailoverError 供外层继续选渠道")
	}
}

// 切片 A 反证：正常 2xx JSON 不受信封检测影响，body 完整交给 HandleSuccess。
func TestUpstreamAttempt2xxNormalJSONPassesThrough(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"msg_1","error":null,"content":[{"type":"text","text":"hi"}]}`))
	}))
	defer server.Close()

	cfgManager, err := config.NewConfigManager(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatalf("创建配置管理器失败: %v", err)
	}
	t.Cleanup(func() { _ = cfgManager.Close() })

	metricsManager := metrics.NewMetricsManager()
	t.Cleanup(metricsManager.Stop)

	channelScheduler := scheduler.NewChannelScheduler(cfgManager, metricsManager, nil, nil, nil, nil, nil, nil)
	t.Cleanup(channelScheduler.Stop)

	upstream := &config.UpstreamConfig{
		BaseURL: server.URL,
		APIKeys: []string{"key-a"},
	}
	var receivedBody string

	result := (UpstreamAttempt{
		Context:          newAttemptTestContext(context.Background()),
		EnvConfig:        &config.EnvConfig{LogLevel: "error"},
		ConfigManager:    cfgManager,
		ChannelScheduler: channelScheduler,
		Kind:             scheduler.ChannelKindMessages,
		MetricsManager:   metricsManager,
		Upstream:         upstream,
		RequestedModel:   "requested-model",
		URLResults:       BuildDefaultURLResults(upstream.GetAllBaseURLs()),
		NextAPIKey: func(up *config.UpstreamConfig, failedKeys map[string]bool) (string, error) {
			if len(up.APIKeys) == 0 {
				return "", errors.New("没有可用密钥")
			}
			return up.APIKeys[0], nil
		},
		BuildRequest: func(_ *gin.Context, up *config.UpstreamConfig, apiKey string) (*http.Request, error) {
			req, err := http.NewRequest(http.MethodPost, server.URL, nil)
			if err != nil {
				return nil, err
			}
			return req, nil
		},
		HandleSuccess: func(_ *gin.Context, resp *http.Response, _ *config.UpstreamConfig, apiKey string) (*types.Usage, error) {
			defer resp.Body.Close()
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				return nil, err
			}
			receivedBody = string(body)
			return &types.Usage{}, nil
		},
	}).TryWithModelMappingFailover()

	if !result.Handled || result.SuccessKey != "key-a" {
		t.Fatalf("正常响应应成功: Handled=%v SuccessKey=%q", result.Handled, result.SuccessKey)
	}
	if !strings.Contains(receivedBody, `"content"`) {
		t.Fatalf("HandleSuccess 收到的 body 不完整: %q", receivedBody)
	}
}

func TestUpstreamAttemptThinkingBudgetRectifierRetriesSuccessfully(t *testing.T) {
	var requestCount int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		reqBody, _ := io.ReadAll(r.Body)
		if requestCount == 1 {
			// 第一次请求返回 400 budget_tokens 约束错误
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"type":"invalid_request_error","message":"thinking.budget_tokens: Input should be greater than or equal to 1024"}}`))
			return
		}
		// 第二次请求由整流器修复参数后重试，断言收到了修复后的 budget_tokens 和 max_tokens
		if !strings.Contains(string(reqBody), `"budget_tokens":32000`) {
			t.Errorf("重试请求未包含修复后的 budget_tokens: %s", string(reqBody))
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"msg_ok","content":[{"type":"text","text":"budget ok"}]}`))
	}))
	defer server.Close()

	cfgManager, err := config.NewConfigManager(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatalf("创建配置管理器失败: %v", err)
	}
	t.Cleanup(func() { _ = cfgManager.Close() })

	metricsManager := metrics.NewMetricsManager()
	t.Cleanup(metricsManager.Stop)

	channelScheduler := scheduler.NewChannelScheduler(cfgManager, metricsManager, nil, nil, nil, nil, nil, nil)
	t.Cleanup(channelScheduler.Stop)

	upstream := &config.UpstreamConfig{
		BaseURL: server.URL,
		APIKeys: []string{"key-a"},
	}

	initialBody := `{"model":"claude-3-7-sonnet-20250219","thinking":{"type":"enabled","budget_tokens":500},"max_tokens":1000}`
	result := (UpstreamAttempt{
		Context:          newAttemptTestContext(context.Background()),
		EnvConfig:        &config.EnvConfig{LogLevel: "error"},
		ConfigManager:    cfgManager,
		ChannelScheduler: channelScheduler,
		Kind:             scheduler.ChannelKindMessages,
		MetricsManager:   metricsManager,
		Upstream:         upstream,
		RequestedModel:   "claude-3-7-sonnet-20250219",
		RequestBody:      []byte(initialBody),
		URLResults:       BuildDefaultURLResults(upstream.GetAllBaseURLs()),
		NextAPIKey: func(up *config.UpstreamConfig, failedKeys map[string]bool) (string, error) {
			return up.APIKeys[0], nil
		},
		BuildRequest: func(c *gin.Context, up *config.UpstreamConfig, apiKey string) (*http.Request, error) {
			bodyBytes, _ := io.ReadAll(c.Request.Body)
			req, err := http.NewRequest(http.MethodPost, server.URL, strings.NewReader(string(bodyBytes)))
			return req, err
		},
		HandleSuccess: func(_ *gin.Context, resp *http.Response, _ *config.UpstreamConfig, apiKey string) (*types.Usage, error) {
			defer resp.Body.Close()
			return &types.Usage{}, nil
		},
	}).TryWithModelMappingFailover()

	if !result.Handled || result.SuccessKey != "key-a" {
		t.Fatalf("Budget 整流就地重试期望成功，实际: Handled=%v LastError=%v", result.Handled, result.LastError)
	}
	if requestCount != 2 {
		t.Fatalf("期望请求上游 2 次（初次失败+就地重试），实际: %d", requestCount)
	}
}

func TestUpstreamAttemptThinkingSignatureRectifierRetriesSuccessfully(t *testing.T) {
	var requestCount int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		reqBody, _ := io.ReadAll(r.Body)
		if requestCount == 1 {
			// 第一次请求返回 400 signature 错误
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"Invalid 'signature' in 'thinking' block"}}`))
			return
		}
		// 第二次请求断言 signature/thinking 块已被清理
		if strings.Contains(string(reqBody), `"bad_signature"`) {
			t.Errorf("重试请求仍包含非法 signature: %s", string(reqBody))
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"msg_sig_ok","content":[{"type":"text","text":"signature rectified"}]}`))
	}))
	defer server.Close()

	cfgManager, err := config.NewConfigManager(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatalf("创建配置管理器失败: %v", err)
	}
	t.Cleanup(func() { _ = cfgManager.Close() })

	metricsManager := metrics.NewMetricsManager()
	t.Cleanup(metricsManager.Stop)

	channelScheduler := scheduler.NewChannelScheduler(cfgManager, metricsManager, nil, nil, nil, nil, nil, nil)
	t.Cleanup(channelScheduler.Stop)

	upstream := &config.UpstreamConfig{
		BaseURL: server.URL,
		APIKeys: []string{"key-a"},
	}

	initialBody := `{"model":"claude-3-7-sonnet-20250219","messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"text","signature":"bad_signature"},{"type":"text","text":"hi"}]}]}`
	result := (UpstreamAttempt{
		Context:          newAttemptTestContext(context.Background()),
		EnvConfig:        &config.EnvConfig{LogLevel: "error"},
		ConfigManager:    cfgManager,
		ChannelScheduler: channelScheduler,
		Kind:             scheduler.ChannelKindMessages,
		MetricsManager:   metricsManager,
		Upstream:         upstream,
		RequestedModel:   "claude-3-7-sonnet-20250219",
		RequestBody:      []byte(initialBody),
		URLResults:       BuildDefaultURLResults(upstream.GetAllBaseURLs()),
		NextAPIKey: func(up *config.UpstreamConfig, failedKeys map[string]bool) (string, error) {
			return up.APIKeys[0], nil
		},
		BuildRequest: func(c *gin.Context, up *config.UpstreamConfig, apiKey string) (*http.Request, error) {
			bodyBytes, _ := io.ReadAll(c.Request.Body)
			req, err := http.NewRequest(http.MethodPost, server.URL, strings.NewReader(string(bodyBytes)))
			return req, err
		},
		HandleSuccess: func(_ *gin.Context, resp *http.Response, _ *config.UpstreamConfig, apiKey string) (*types.Usage, error) {
			defer resp.Body.Close()
			return &types.Usage{}, nil
		},
	}).TryWithModelMappingFailover()

	if !result.Handled || result.SuccessKey != "key-a" {
		t.Fatalf("Signature 整流就地重试期望成功，实际: Handled=%v LastError=%v", result.Handled, result.LastError)
	}
	if requestCount != 2 {
		t.Fatalf("期望请求上游 2 次（初次失败+就地重试），实际: %d", requestCount)
	}
}

func TestUpstreamAttemptMediaSanitizerRetriesSuccessfully(t *testing.T) {
	var requestCount int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		reqBody, _ := io.ReadAll(r.Body)
		if requestCount == 1 {
			// 第一次请求返回 400 模态拒绝错误
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"Model only support text input"}}`))
			return
		}
		// 第二次请求断言图片已被替换为纯文本占位符
		if strings.Contains(string(reqBody), `"base64"`) {
			t.Errorf("重试请求仍包含图片 base64 数据: %s", string(reqBody))
		}
		if !strings.Contains(string(reqBody), `[Unsupported Image]`) {
			t.Errorf("重试请求未包含降级占位符: %s", string(reqBody))
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"msg_media_ok","content":[{"type":"text","text":"sanitized ok"}]}`))
	}))
	defer server.Close()

	cfgManager, err := config.NewConfigManager(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatalf("创建配置管理器失败: %v", err)
	}
	t.Cleanup(func() { _ = cfgManager.Close() })

	metricsManager := metrics.NewMetricsManager()
	t.Cleanup(metricsManager.Stop)

	channelScheduler := scheduler.NewChannelScheduler(cfgManager, metricsManager, nil, nil, nil, nil, nil, nil)
	t.Cleanup(channelScheduler.Stop)

	// 渠道声明支持图片（visionlayer 因此放行原图），上游却回模态拒绝——
	// 这正是 mediasanitizer 的适用场景；VisionCapable=false 的渠道图片早被转成文本了。
	upstream := &config.UpstreamConfig{
		BaseURL:       server.URL,
		APIKeys:       []string{"key-a"},
		ServiceType:   "claude",
		VisionCapable: true,
	}

	initialBody := `{"model":"deepseek-chat","messages":[{"role":"user","content":[{"type":"image","source":{"type":"base64","data":"xyz"}},{"type":"text","text":"hi"}]}]}`
	result := (UpstreamAttempt{
		Context:          newAttemptTestContext(context.Background()),
		EnvConfig:        &config.EnvConfig{LogLevel: "error"},
		ConfigManager:    cfgManager,
		ChannelScheduler: channelScheduler,
		Kind:             scheduler.ChannelKindMessages,
		MetricsManager:   metricsManager,
		Upstream:         upstream,
		RequestedModel:   "deepseek-chat",
		RequestBody:      []byte(initialBody),
		URLResults:       BuildDefaultURLResults(upstream.GetAllBaseURLs()),
		NextAPIKey: func(up *config.UpstreamConfig, failedKeys map[string]bool) (string, error) {
			return up.APIKeys[0], nil
		},
		BuildRequest: func(c *gin.Context, up *config.UpstreamConfig, apiKey string) (*http.Request, error) {
			bodyBytes, _ := io.ReadAll(c.Request.Body)
			req, err := http.NewRequest(http.MethodPost, server.URL, strings.NewReader(string(bodyBytes)))
			return req, err
		},
		HandleSuccess: func(_ *gin.Context, resp *http.Response, _ *config.UpstreamConfig, apiKey string) (*types.Usage, error) {
			defer resp.Body.Close()
			return &types.Usage{}, nil
		},
	}).TryWithModelMappingFailover()

	if !result.Handled || result.SuccessKey != "key-a" {
		t.Fatalf("Media 降级就地重试期望成功，实际: Handled=%v LastError=%v", result.Handled, result.LastError)
	}
	if requestCount != 2 {
		t.Fatalf("期望请求上游 2 次（初次失败+就地重试），实际: %d", requestCount)
	}
}
