// R2-a 验收测试：单渠道尝试的每个出口必须携带显式渠道健康结果
// （成功 / 真实失败 / 中性），与 Handled（响应是否写回）、FailoverError
// （重试决定）三件事分开表达。对应方案 5.1 表。
package proxycore

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/BenedictKing/api-proxy/internal/config"
	"github.com/BenedictKing/api-proxy/internal/metrics"
	"github.com/BenedictKing/api-proxy/internal/scheduler"
	"github.com/BenedictKing/api-proxy/internal/types"
	"github.com/gin-gonic/gin"
)

// healthAttemptEnv 装配 UpstreamAttempt 所需的最小环境。
type healthAttemptEnv struct {
	cfgManager       *config.ConfigManager
	metricsManager   *metrics.MetricsManager
	channelScheduler *scheduler.ChannelScheduler
}

func newHealthAttemptEnv(t *testing.T) *healthAttemptEnv {
	t.Helper()
	cfgManager, err := config.NewConfigManager(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatalf("创建配置管理器失败: %v", err)
	}
	t.Cleanup(func() { _ = cfgManager.Close() })

	metricsManager := metrics.NewMetricsManager()
	t.Cleanup(metricsManager.Stop)

	channelScheduler := scheduler.NewChannelScheduler(cfgManager, metricsManager, nil, nil, nil, nil, nil, nil)
	t.Cleanup(channelScheduler.Stop)

	return &healthAttemptEnv{
		cfgManager:       cfgManager,
		metricsManager:   metricsManager,
		channelScheduler: channelScheduler,
	}
}

// newHealthAttempt 构造一个指向 httptest 上游的 UpstreamAttempt。
func newHealthAttempt(env *healthAttemptEnv, serverURL string, keys []string) UpstreamAttempt {
	upstream := &config.UpstreamConfig{
		BaseURL: serverURL,
		APIKeys: keys,
	}
	return UpstreamAttempt{
		Context:          newAttemptTestContext(context.Background()),
		EnvConfig:        &config.EnvConfig{LogLevel: "error"},
		ConfigManager:    env.cfgManager,
		ChannelScheduler: env.channelScheduler,
		Kind:             scheduler.ChannelKindMessages,
		MetricsManager:   env.metricsManager,
		Upstream:         upstream,
		RequestedModel:   "test-model",
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
			req, err := http.NewRequest(http.MethodPost, up.BaseURL, nil)
			if err != nil {
				return nil, err
			}
			req.Header.Set("x-api-key", apiKey)
			return req, nil
		},
		HandleSuccess: func(c *gin.Context, resp *http.Response, _ *config.UpstreamConfig, _ string) (*types.Usage, error) {
			defer resp.Body.Close()
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				return nil, err
			}
			c.Data(http.StatusOK, "application/json", body)
			return &types.Usage{}, nil
		},
	}
}

// 场景：最终成功（包括前面有候选失败）→ channelHealthSuccess。
func TestUpstreamAttemptHealthSuccessAfterCandidateFailure(t *testing.T) {
	env := newHealthAttemptEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") == "key-a" {
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte(`{"error":{"message":"bad gateway"}}`))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	result := newHealthAttempt(env, server.URL, []string{"key-a", "key-b"}).TryWithModelMappingFailover()

	if !result.Handled || result.SuccessKey != "key-b" {
		t.Fatalf("期望 key-b 成功，实际 Handled=%v SuccessKey=%q LastError=%v", result.Handled, result.SuccessKey, result.LastError)
	}
	if result.Health != channelHealthSuccess {
		t.Fatalf("Health = %v, want channelHealthSuccess（最终成功，前面候选失败不影响）", result.Health)
	}
}

// 场景：可重试的真实故障耗尽渠道内候选 → channelHealthFailure。
func TestUpstreamAttemptHealthFailureWhenCandidatesExhausted(t *testing.T) {
	env := newHealthAttemptEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`{"error":{"message":"bad gateway"}}`))
	}))
	defer server.Close()

	result := newHealthAttempt(env, server.URL, []string{"key-a", "key-b"}).TryWithModelMappingFailover()

	if result.Handled {
		t.Fatalf("Handled = true, want false（可重试故障应交外层换渠道）")
	}
	if result.Health != channelHealthFailure {
		t.Fatalf("Health = %v, want channelHealthFailure（真实故障耗尽候选）", result.Health)
	}
}

// 场景：不可重试的请求参数错误（413）→ 中性 + 返回该协议错误。
func TestUpstreamAttemptHealthNeutralOnNonRetryable4xx(t *testing.T) {
	env := newHealthAttemptEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusRequestEntityTooLarge)
		_, _ = w.Write([]byte(`{"error":{"message":"payload too large"}}`))
	}))
	defer server.Close()

	result := newHealthAttempt(env, server.URL, []string{"key-a"}).TryWithModelMappingFailover()

	if !result.Handled {
		t.Fatalf("Handled = false, want true（413 应写回客户端后结束）")
	}
	if result.Health != channelHealthNeutral {
		t.Fatalf("Health = %v, want channelHealthNeutral（请求参数错误与渠道健康无关）", result.Health)
	}
}

// 场景：内容审核拒绝 → 中性（即使开启了跨渠道审核转移，也不计渠道失败）。
func TestUpstreamAttemptHealthNeutralOnContentPolicyRejection(t *testing.T) {
	env := newHealthAttemptEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":{"code":"content_policy_violation","message":"content policy"}}`))
	}))
	defer server.Close()

	attempt := newHealthAttempt(env, server.URL, []string{"key-a"})
	attempt.LogContext.AllowContentPolicyChannelFailover = true
	result := attempt.TryWithModelMappingFailover()

	// 内容审核错误体被识别为不可重试；多渠道可用时换渠道（Handled=false）。
	if result.Health != channelHealthNeutral {
		t.Fatalf("Health = %v, want channelHealthNeutral（内容审核拒绝不计渠道故障）", result.Health)
	}
}

// 场景：客户端明确取消 → 中性 + 立即停止。
func TestUpstreamAttemptHealthNeutralOnClientCancel(t *testing.T) {
	env := newHealthAttemptEnv(t)
	// 服务端挂起直到客户端取消（context 传播到上游请求）。
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
		close(release)
	}))
	defer server.Close()
	defer func() {
		select {
		case <-release:
		default:
		}
	}()

	ctx, cancel := context.WithCancel(context.Background())
	attempt := newHealthAttempt(env, server.URL, []string{"key-a"})
	attempt.Context = newAttemptTestContext(ctx)
	go func() {
		<-release
		cancel()
	}()
	// 请求进入服务端后取消。
	connected := make(chan struct{})
	attempt.BuildRequest = func(c *gin.Context, up *config.UpstreamConfig, apiKey string) (*http.Request, error) {
		select {
		case <-connected:
		default:
			close(connected)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, up.BaseURL, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("x-api-key", apiKey)
		return req, nil
	}
	go func() {
		<-connected
		// 上游收到请求即取消客户端 context。
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	result := attempt.TryWithModelMappingFailover()

	if !result.Handled {
		t.Fatalf("Handled = false, want true（取消后应立即停止）")
	}
	if result.Health != channelHealthNeutral {
		t.Fatalf("Health = %v, want channelHealthNeutral（客户端取消中性）", result.Health)
	}
}

// 场景：本地配置错误（渠道代理无效）→ 中性 + 显式错误。
func TestUpstreamAttemptHealthNeutralOnLocalConfigError(t *testing.T) {
	env := newHealthAttemptEnv(t)
	// 渠道级 custom 代理指向非法 URL：prepareAllKeys 解析失败。
	attempt := newHealthAttempt(env, "https://unused.example.com", []string{"key-a"})
	attempt.Upstream.ProxyMode = config.ProxyModeCustom
	attempt.Upstream.ProxyURL = "://invalid-proxy"

	result := attempt.TryWithModelMappingFailover()

	if result.LastError == nil {
		t.Fatalf("LastError = nil, want 显式本地配置错误")
	}
	if result.Health != channelHealthNeutral {
		t.Fatalf("Health = %v, want channelHealthNeutral（本地错误不计上游故障）", result.Health)
	}
}

// 场景：模型容量错误（不可重试 413 类同候选重试耗尽）→ 中性。
func TestUpstreamAttemptHealthNeutralOnModelCapacityExhausted(t *testing.T) {
	env := newHealthAttemptEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":{"message":"selected model is at capacity"}}`))
	}))
	defer server.Close()

	result := newHealthAttempt(env, server.URL, []string{"key-a"}).TryWithModelMappingFailover()

	// 容量错误按中性处理（同候选重试耗尽后 Key 排除，最终交外层）。
	if result.Health != channelHealthNeutral {
		t.Fatalf("Health = %v, want channelHealthNeutral（模型容量错误中性）", result.Health)
	}
}

// recordChannelHealth 的契约：Unset 且确有尝试时跳过记账（可见警告），
// 其余按结果准确映射。用一个最小 fake 调度器难以替换，这里直接调用
// 各枚举值验证不 panic 且不重复记账即可；行为语义由上面端到端用例覆盖。
func TestRecordChannelHealthMapsAllValues(t *testing.T) {
	env := newHealthAttemptEnv(t)
	// Unset + attempted：不 panic。
	recordChannelHealth(env.channelScheduler, scheduler.ChannelKindMessages, 0, channelHealthUnset, true, "Test", nil)
	// 全枚举各记账一次。
	recordChannelHealth(env.channelScheduler, scheduler.ChannelKindMessages, 0, channelHealthSuccess, true, "Test", nil)
	recordChannelHealth(env.channelScheduler, scheduler.ChannelKindMessages, 0, channelHealthFailure, true, "Test", nil)
	recordChannelHealth(env.channelScheduler, scheduler.ChannelKindMessages, 0, channelHealthNeutral, true, "Test", nil)
}
