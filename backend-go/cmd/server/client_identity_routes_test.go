package main

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/insistanan/api-proxy/internal/dialogue/session"
	"github.com/insistanan/api-proxy/internal/handlers/hooks"
	"github.com/insistanan/api-proxy/internal/infra/config"
	"github.com/insistanan/api-proxy/internal/infra/httpclient"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// 用内存发送出口覆盖真实 handler，验证继承开关、协议转换与最终身份一致，不访问官方服务。
func TestConvertedRequestIdentityFollowsUpstream(t *testing.T) {
	previousMode := gin.Mode()
	gin.SetMode(gin.TestMode)
	t.Cleanup(func() { gin.SetMode(previousMode) })
	for _, entry := range []string{"messages", "responses"} {
		for _, mode := range []struct {
			name, identity              string
			targetEnabled, wantDisguise bool
		}{
			{"inherit-on", "inherit", true, true},
			{"inherit-off", "inherit", false, false},
			{"api", "api", true, false},
			{"compatibility", "compatibility", false, true},
		} {
			t.Run(entry+"/"+mode.name, func(t *testing.T) {
				r, app := newChannelAdminTestRouter(t)
				require.NoError(t, app.cfgManager.SetFuzzyModeEnabled(false))
				target, upstream, sourceUA := "messages", "claude", "codex_cli_rs/fixture"
				body := `{"model":"fixture","input":"hello"}`
				if entry == "messages" {
					target, upstream, sourceUA = "responses", "responses", "claude-cli/2.1.0"
					body = `{"model":"fixture","max_tokens":128,"messages":[{"role":"user","content":"hello"}]}`
				}
				require.NoError(t, app.cfgManager.SetClientDisguiseEnabled(target, mode.targetEnabled))
				require.NoError(t, app.cfgManager.SetClientDisguiseEnabled(entry, !mode.targetEnabled))
				channelAdminRequest(t, r, http.MethodPost, "/api/"+entry+"/channels", fmt.Sprintf(
					`{"name":"official","baseUrl":"https://upstream.example/v1/%s","useFullUrl":true,"apiKeys":["fixture-key"],"serviceType":%q,"clientIdentityMode":%q}`,
					target, upstream, mode.identity), http.StatusOK)
				app.envCfg = &config.EnvConfig{ProxyAccessKey: "test-admin", LogLevel: "error", MaxRequestBodySize: 1024 * 1024, RequestTimeout: 5502}
				app.contentSafetyPipeline = hooks.NewContentSafetyPipeline(app.cfgManager)
				manager, err := session.NewPersistentSessionManager(filepath.Join(t.TempDir(), "sessions.db"), time.Hour, 0, 0)
				require.NoError(t, err)
				t.Cleanup(manager.Stop)
				app.sessionManager = manager
				app.setupProxyEndpoints(r)
				client, err := httpclient.GetManager().GetStandardClient(5502*time.Millisecond, false, "")
				require.NoError(t, err)
				original := client.Transport
				t.Cleanup(func() { client.Transport = original })
				calls := 0
				client.Transport = freePoolTransport(func(req *http.Request) (*http.Response, error) {
					calls++
					require.Equal(t, "/v1/"+target, req.URL.Path)
					data, err := io.ReadAll(req.Body)
					require.NoError(t, err)
					require.Equal(t, int64(len(data)), req.ContentLength)
					require.NotNil(t, req.GetBody)
					replay, err := req.GetBody()
					require.NoError(t, err)
					replayed, err := io.ReadAll(replay)
					require.NoError(t, err)
					require.NoError(t, replay.Close())
					require.True(t, bytes.Equal(data, replayed))
					require.Empty(t, req.Header.Get("Chatgpt-Account-Id"))
					if target == "messages" {
						require.Empty(t, req.Header.Get("Originator"))
						require.Empty(t, req.Header.Get("Session_id"))
						require.Equal(t, "2023-06-01", req.Header.Get("Anthropic-Version"))
						require.True(t, gjson.GetBytes(data, "messages").IsArray())
						require.Equal(t, mode.wantDisguise, gjson.GetBytes(data, "metadata.user_id").Exists())
						if mode.wantDisguise {
							require.True(t, strings.HasPrefix(req.Header.Get("User-Agent"), "claude-cli/"))
							require.Contains(t, req.Header.Get("Anthropic-Beta"), "claude-code-")
							require.Contains(t, string(data), "x-anthropic-billing-header:")
						}
					} else {
						require.Empty(t, req.Header.Get("X-App"))
						require.Empty(t, req.Header.Get("Anthropic-Beta"))
						require.True(t, gjson.GetBytes(data, "input").IsArray())
						if mode.wantDisguise {
							require.True(t, strings.HasPrefix(req.Header.Get("User-Agent"), "codex_cli_rs/"))
						}
					}
					if !mode.wantDisguise {
						require.Equal(t, "api-proxy", req.Header.Get("User-Agent"))
					}
					return &http.Response{StatusCode: 400, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"error":{"code":"context_length_exceeded","message":"fixture end"}}`)), Request: req}, nil
				})
				req := httptest.NewRequest(http.MethodPost, "/v1/"+entry, strings.NewReader(body))
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("X-Api-Key", "test-admin")
				req.Header.Set("User-Agent", sourceUA)
				req.Header.Set("Originator", "codex_cli_rs")
				req.Header.Set("Session_id", "source-session")
				req.Header.Set("Anthropic-Beta", "source-beta")
				req.Header.Set("X-App", "cli")
				req.Header.Set("Chatgpt-Account-Id", "source-account")
				w := httptest.NewRecorder()
				r.ServeHTTP(w, req)
				require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
				require.Equal(t, 1, calls)
			})
		}
	}
}
