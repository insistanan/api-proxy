package responses

import (
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/insistanan/api-proxy/internal/config"
	"github.com/insistanan/api-proxy/internal/metrics"
	"github.com/insistanan/api-proxy/internal/scheduler"
	"github.com/insistanan/api-proxy/internal/session"
	"github.com/insistanan/api-proxy/internal/types"
	"github.com/insistanan/api-proxy/internal/upstreamcontrol"
	"github.com/insistanan/api-proxy/internal/utils"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestCompactMissingHistoryReturnsClientError(t *testing.T) {
	manager, err := session.NewPersistentSessionManager(filepath.Join(t.TempDir(), "responses.db"), time.Hour, 0, 0)
	require.NoError(t, err)
	t.Cleanup(manager.Stop)
	body := []byte(`{"model":"gpt-test","previous_response_id":"missing","input":"后缀"}`)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses/compact", strings.NewReader(string(body)))
	upstream := &config.UpstreamConfig{BaseURL: "https://example.invalid", ServiceType: "responses"}
	success, compactErr := tryCompactWithKey(c, upstream, "key", body, &config.EnvConfig{}, nil, manager, nil, "conv-history")
	require.False(t, success)
	require.NotNil(t, compactErr)
	require.Equal(t, http.StatusBadRequest, compactErr.status)
	require.False(t, compactErr.shouldFailover)
	require.Contains(t, string(compactErr.body), "历史")
}

func TestCompactTruncatedAndLocalCommitFailure(t *testing.T) {
	for _, truncated := range []bool{false, true} {
		name := "本地会话提交失败"
		if truncated {
			name = "上游200响应截断"
		}
		t.Run(name, func(t *testing.T) {
			var requests atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				w.Header().Set("Content-Type", "application/json")
				if truncated {
					w.Header().Set("Content-Length", "1000")
					_, _ = io.WriteString(w, `{"id":"truncated"}`)
					return
				}
				_, _ = io.WriteString(w, `{"id":"compact-new","output":[{"type":"compaction","encrypted_content":"opaque"}]}`)
			}))
			defer server.Close()
			cfgManager, err := config.NewConfigManager(filepath.Join(t.TempDir(), "config.json"))
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, cfgManager.Close()) })
			require.NoError(t, cfgManager.SetFuzzyModeEnabled(false))
			require.NoError(t, cfgManager.AddResponsesUpstream(config.UpstreamConfig{
				Name: "compact", BaseURL: server.URL, APIKeys: []string{"key-a", "key-b"}, ServiceType: "responses",
			}))
			mm := metrics.NewMetricsManager()
			t.Cleanup(mm.Stop)
			sch := scheduler.NewChannelScheduler(cfgManager, nil, mm, nil, nil, nil, nil, nil, nil, nil)
			t.Cleanup(sch.Stop)
			manager, err := session.NewPersistentSessionManager(filepath.Join(t.TempDir(), "responses.db"), time.Hour, 0, 0)
			require.NoError(t, err)
			t.Cleanup(manager.Stop)
			body := []byte(`{"model":"test-model","input":"compact"}`)
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses/compact", strings.NewReader(string(body)))
			// 没有 conversation ID 时本地提交明确失败，不能重发成功的 compact。
			handleSingleChannelCompact(c, &config.EnvConfig{RequestTimeout: 5000}, cfgManager, manager, sch, body, "", "test-model")
			if truncated {
				require.Equal(t, http.StatusBadGateway, c.Writer.Status())
				require.Equal(t, int64(2), requests.Load())
			} else {
				require.Equal(t, http.StatusInternalServerError, c.Writer.Status())
				require.Equal(t, int64(1), requests.Load())
				for _, key := range []string{"key-a", "key-b"} {
					if keyMetrics := mm.GetKeyMetrics(server.URL, key, 0); keyMetrics != nil {
						require.Zero(t, keyMetrics.FailureCount)
					}
				}
			}
		})
	}
}

func TestBuildCompactRequestModelAndURL(t *testing.T) {
	for _, tc := range []struct {
		name     string
		upstream config.UpstreamConfig
		model    string
		url      string
	}{
		{"默认模型", config.UpstreamConfig{DefaultModel: "default", ModelMapping: map[string][]string{"*": {"star"}}}, "default", "https://upstream.example/v1/responses/compact"},
		{"强制星号", config.UpstreamConfig{ModelMapping: map[string][]string{"*": {"star"}, "alias": {"exact"}}}, "star", "https://upstream.example/v1/responses/compact"},
		{"具体映射", config.UpstreamConfig{ModelMapping: map[string][]string{"alias": {"exact"}}}, "exact", "https://upstream.example/v1/responses/compact"},
		{"跳过版本前缀", config.UpstreamConfig{BaseURL: "https://upstream.example/custom#"}, "alias", "https://upstream.example/custom/responses/compact"},
		{"完整地址", config.UpstreamConfig{BaseURL: "https://upstream.example/specific", UseFullURL: true}, "alias", "https://upstream.example/specific"},
		{"仅数组地址", config.UpstreamConfig{BaseURLs: []string{"https://upstream.example/v2"}}, "alias", "https://upstream.example/v2/responses/compact"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upstream := tc.upstream
			upstream.ServiceType = "responses"
			if upstream.BaseURL == "" && len(upstream.BaseURLs) == 0 {
				upstream.BaseURL = "https://upstream.example"
			}
			body := []byte(`{"model":"alias","input":"保留","vendor_extension":{"opaque":true}}`)
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses/compact", strings.NewReader(string(body)))
			req, err := buildCompactRequest(c, &upstream, "key", body, nil, nil, "")
			require.NoError(t, err)
			defer req.Body.Close()
			got, err := io.ReadAll(req.Body)
			require.NoError(t, err)
			require.Equal(t, tc.model, gjson.GetBytes(got, "model").String())
			require.True(t, gjson.GetBytes(got, "vendor_extension.opaque").Bool())
			require.Equal(t, tc.url, req.URL.String())
		})
	}
}

func TestCompactMappedTargetUsesActualURLAndOwner(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/bad/") {
			w.WriteHeader(http.StatusBadGateway)
			_, _ = io.WriteString(w, `{"error":"暂时不可用"}`)
			return
		}
		if r.URL.Path != "/api/responses/compact" || r.Header.Get("Authorization") != "Bearer target-key" {
			t.Errorf("实际目标错误: path=%s authorization=%s", r.URL.Path, r.Header.Get("Authorization"))
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil || gjson.GetBytes(body, "model").String() != "mapped-model" {
			t.Errorf("目标模型错误: model=%s err=%v", gjson.GetBytes(body, "model").String(), err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		_, _ = io.WriteString(w, `{"id":"compact-new","output":[{"type":"message","role":"user","content":"必须保留"},{"type":"compaction","encrypted_content":"opaque"}]}`)
	}))
	defer server.Close()
	cfgManager, err := config.NewConfigManager(filepath.Join(t.TempDir(), "config.json"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, cfgManager.Close()) })
	require.NoError(t, cfgManager.SetFuzzyModeEnabled(true))
	require.NoError(t, cfgManager.AddResponsesUpstream(config.UpstreamConfig{
		ID: "source", Name: "源渠道", BaseURL: "https://unused.example", APIKeys: []string{"source-key"}, ServiceType: "claude",
	}))
	require.NoError(t, cfgManager.AddResponsesUpstream(config.UpstreamConfig{
		ID: "target", Name: "目标渠道", BaseURLs: []string{server.URL + "/bad", server.URL + "/api#"},
		APIKeys: []string{"target-key"}, ServiceType: "responses", DefaultModel: "不能覆盖显式目标",
		ModelMapping: map[string][]string{"*": {"不能再次映射"}},
	}))
	// 保留源渠道索引，目标渠道存在后再配置跨渠道映射。
	_, err = cfgManager.UpdateResponsesUpstream(0, config.UpstreamUpdate{
		ModelMapping:         map[string][]string{"alias": {"mapped-model"}},
		ModelMappingChannels: map[string]map[string]string{"alias": {"mapped-model": "target"}},
	})
	require.NoError(t, err)
	mm := metrics.NewMetricsManager()
	t.Cleanup(mm.Stop)
	sch := scheduler.NewChannelScheduler(cfgManager, nil, mm, nil, nil, nil, nil, nil, nil, nil)
	t.Cleanup(sch.Stop)
	manager, err := session.NewPersistentSessionManager(filepath.Join(t.TempDir(), "responses.db"), time.Hour, 0, 0)
	require.NoError(t, err)
	t.Cleanup(manager.Stop)
	sess, err := manager.GetOrCreateSessionForConversation("", "compact-conv")
	require.NoError(t, err)
	require.NoError(t, manager.CommitTurn(sess.ID, []types.ResponsesItem{{Type: "message", Role: "user", Content: "旧历史"}}, 10, false, "old-response"))
	body := []byte(`{"model":"alias","input":"压缩整个窗口"}`)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses/compact", strings.NewReader(string(body)))
	upstream := cfgManager.GetConfig().ResponsesUpstream[0]
	result := compactAttempt(c, &upstream, 0, nil, cfgManager, manager, sch, body, "compact-conv", &config.EnvConfig{RequestTimeout: 5000}).TryWithModelMappingFailover()
	require.True(t, result.Handled)
	require.Equal(t, "target-key", result.SuccessKey)
	owner, found, err := manager.ResponseOwner("compact-new")
	require.NoError(t, err)
	require.True(t, found)
	target := cfgManager.GetConfig().ResponsesUpstream[1]
	target.BaseURL = server.URL + "/api#"
	require.Equal(t, target.RequestIdentity("target-key").Fingerprint(), owner)
	if sourceMetrics := mm.GetKeyMetrics(upstream.BaseURL, "source-key", 0); sourceMetrics != nil {
		require.Zero(t, sourceMetrics.FailureCount)
	}
	targetMetrics := mm.GetKeyMetrics(server.URL+"/api#", "target-key", 1)
	require.NotNil(t, targetMetrics)
	require.Equal(t, int64(1), targetMetrics.SuccessCount)
	continued, err := manager.GetOrCreateSessionForConversation("compact-new", "compact-conv")
	require.NoError(t, err)
	require.Len(t, continued.Messages, 2)
	require.Equal(t, "必须保留", continued.Messages[0].Content)
}

func TestTryCompactWithKey_AppliesCodexDisguise(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("CODEX_DISGUISE_VERSION", "8.8.8")
	for _, enabled := range []bool{false, true} {
		name := "关闭伪装"
		if enabled {
			name = "开启伪装"
		}
		t.Run(name, func(t *testing.T) {
			type receivedRequest struct {
				header http.Header
				body   []byte
				length int64
				path   string
				err    error
			}
			received := make(chan receivedRequest, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				received <- receivedRequest{header: r.Header.Clone(), body: body, length: r.ContentLength, path: r.URL.Path, err: err}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"output":[]}`)
			}))
			defer server.Close()
			cfgManager, err := config.NewConfigManager(filepath.Join(t.TempDir(), "config.json"))
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, cfgManager.Close()) })
			require.NoError(t, cfgManager.SetClientDisguiseEnabled("responses", enabled))
			require.NoError(t, cfgManager.AddResponsesUpstream(config.UpstreamConfig{
				Name:                  "compact-test",
				BaseURL:               server.URL,
				APIKeys:               []string{"sk-test"},
				ServiceType:           "responses",
				DisablePromptCacheKey: true,
			}))
			upstream := cfgManager.GetConfig().ResponsesUpstream[0]
			body := `{"model":"gpt-5","input":"你好","prompt_cache_key":"old-key","prompt_cache_retention":"24h"}`
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses/compact", strings.NewReader(body))
			c.Request = c.Request.WithContext(upstreamcontrol.WithScope(c.Request.Context()))
			c.Request.Header.Set("User-Agent", "codex_cli_rs/0.133.0 old-client")
			c.Request.Header.Set("version", "0.133.0")
			c.Request.Header.Set("session_id", "session-test")
			c.Set(utils.ContextKeyCodexDisguise, enabled)
			success, compactErr := tryCompactWithKey(c, &upstream, "sk-test", []byte(body), &config.EnvConfig{RequestTimeout: 5000}, cfgManager, nil, nil, "")
			require.Nil(t, compactErr)
			require.True(t, success)
			got := <-received
			require.NoError(t, got.err)
			require.Equal(t, "/v1/responses/compact", got.path)
			require.Equal(t, int64(len(got.body)), got.length)
			require.False(t, gjson.GetBytes(got.body, "prompt_cache_key").Exists())
			require.False(t, gjson.GetBytes(got.body, "prompt_cache_retention").Exists())
			require.Equal(t, "Bearer sk-test", got.header.Get("Authorization"))
			if enabled {
				require.Equal(t, "session-test", got.header.Get("session_id"))
				require.True(t, strings.HasPrefix(got.header.Get("User-Agent"), "codex_cli_rs/8.8.8 "))
				require.Equal(t, "8.8.8", got.header.Get("version"))
			} else {
				require.Equal(t, "api-proxy", got.header.Get("User-Agent"))
				require.Empty(t, got.header.Get("version"))
				require.Empty(t, got.header.Get("session_id"))
			}
			require.Equal(t, "0.133.0", c.Request.Header.Get("version"))
		})
	}
}
