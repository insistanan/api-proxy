package responses

import (
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/insistanan/api-proxy/internal/config"
	"github.com/insistanan/api-proxy/internal/session"
	"github.com/insistanan/api-proxy/internal/upstreamcontrol"
	"github.com/insistanan/api-proxy/internal/utils"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestCompactMissingHistoryReturnsClientError(t *testing.T) {
	manager, err := session.NewPersistentSessionManager(filepath.Join(t.TempDir(), "responses.db"), time.Hour, 0, 0)
	require.NoError(t, err)
	t.Cleanup(manager.Stop)
	body := []byte(`{"previous_response_id":"missing","input":"后缀"}`)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses/compact", strings.NewReader(string(body)))
	upstream := &config.UpstreamConfig{BaseURL: "https://example.invalid", ServiceType: "responses"}
	success, compactErr := tryCompactWithKey(c, upstream, "key", body, &config.EnvConfig{}, nil, manager, nil, "conv-history")
	require.False(t, success)
	require.NotNil(t, compactErr)
	require.Equal(t, http.StatusBadRequest, compactErr.status)
	require.False(t, compactErr.shouldFailover)
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
