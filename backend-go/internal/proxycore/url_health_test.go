package proxycore

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/insistanan/api-proxy/internal/infra/config"
	"github.com/insistanan/api-proxy/internal/protocol/providers"
	"github.com/insistanan/api-proxy/internal/protocol/types"
	"github.com/stretchr/testify/require"
)

func TestUpstreamAttempt_KeyFailoverOnlyPenalizesEndpointFailures(t *testing.T) {
	for _, tc := range []struct {
		name        string
		status      int
		body        string
		urlFailures int
	}{
		{"认证失败", 401, `{"error":{"message":"invalid api key"}}`, 0},
		{"余额不足", 402, `{"error":{"message":"payment required"}}`, 0},
		{"权限不足", 403, `{"error":{"message":"forbidden"}}`, 0},
		{"500额度不足", 500, `{"error":{"code":"insufficient_quota"}}`, 0},
		{"2xx认证错误信封", 200, `{"error":{"code":"invalid_api_key"}}`, 0},
		{"网关故障", 502, `{"error":{"message":"bad gateway"}}`, 1},
		{"2xx未知错误信封", 200, `{"error":{"message":"upstream failed"}}`, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := newHealthAttemptEnv(t)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.Header.Get("x-api-key") == "key-a" {
					w.WriteHeader(tc.status)
					_, _ = w.Write([]byte(tc.body))
					return
				}
				_, _ = w.Write([]byte(`{"ok":true}`))
			}))
			t.Cleanup(server.Close)
			attempt := newHealthAttempt(env, server.URL, []string{"key-a", "key-b"})
			failures := 0
			attempt.MarkURLFailure = func(string) { failures++ }
			result := attempt.TryWithModelMappingFailover()
			require.True(t, result.Handled)
			require.Equal(t, "key-b", result.SuccessKey, "凭据失败仍应正常换 Key")
			require.Equal(t, tc.urlFailures, failures)
		})
	}
}

func TestUpstreamAttempt_StreamCredentialErrorDoesNotPenalizeURL(t *testing.T) {
	env := newHealthAttemptEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(server.Close)
	attempt := newHealthAttempt(env, server.URL, []string{"key-a"})
	attempt.IsStream = true
	attempt.EnvConfig.ResponseHeaderTimeout = 5
	attempt.EnvConfig.StreamIdleTimeout = 5
	delivered := false
	attempt.HandleSuccess = func(_ *gin.Context, response *http.Response, _ *config.UpstreamConfig, _ string) (*types.Usage, error) {
		delivered = true
		_ = response.Body.Close()
		return nil, providers.NewUpstreamStreamFailedError(false, `{"error":{"code":"invalid_api_key"}}`)
	}
	failures := 0
	attempt.MarkURLFailure = func(string) { failures++ }
	result := attempt.TryWithModelMappingFailover()
	require.True(t, delivered, "必须实际进入流内响应处理")
	require.Error(t, result.LastError)
	require.Zero(t, failures)
}
