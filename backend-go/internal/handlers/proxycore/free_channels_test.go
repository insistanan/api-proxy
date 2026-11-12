package proxycore

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/insistanan/api-proxy/internal/infra/config"
	"github.com/insistanan/api-proxy/internal/infra/utils"
	"github.com/stretchr/testify/require"
)

func TestFreeRequestUsesAnonymousChannelAndNeverPaidFallback(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusServiceUnavailable} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			env := newRunProxyTestEnv(t)
			require.NoError(t, env.cfgManager.EnsureFreeChannels())
			var attempts atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				attempts.Add(1)
				for _, header := range []string{"Authorization", "X-Api-Key", "X-Goog-Api-Key"} {
					require.Empty(t, r.Header.Get(header))
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(status)
				_, _ = w.Write([]byte(`{"id":"local-response","content":[],"error":null}`))
			}))
			defer server.Close()
			addTestUpstream(t, env, "付费兜底", server.URL, "paid-key")
			rec := &specRecorder{}
			spec := rec.newSpec()
			spec.PreRoute = func(*gin.Context, []byte, string, string, time.Time) bool {
				t.Fatal("免费模型不应进入普通别名路由")
				return true
			}
			spec.BuildUpstreamRequest = func(c *gin.Context, up *config.UpstreamConfig, apiKey string, body []byte) (*http.Request, error) {
				require.Equal(t, "kilo", up.BuiltinProvider, "不得尝试付费兜底")
				require.Empty(t, apiKey)
				require.Equal(t, "kilo-auto/free", config.ResolveUpstreamModel("free/kilo-auto/free", up))
				req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodPost, server.URL, bytes.NewReader(body))
				if err != nil {
					return nil, err
				}
				req.Header = utils.PrepareUpstreamHeaders(c, req.URL.Host)
				utils.SetAuthenticationHeader(req.Header, apiKey)
				return req, nil
			}
			response := performRunProxyRequest(t, env, spec, `{"model":"free/kilo-auto/free","messages":[{"role":"user","content":"hi"}]}`, "test-access-key")
			require.Greater(t, attempts.Load(), int32(0))
			if status == http.StatusOK {
				require.Equal(t, http.StatusOK, response.Code)
			} else {
				require.NotEqual(t, http.StatusOK, response.Code)
			}
		})
	}
}
