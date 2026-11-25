package llmgate

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/insistanan/api-proxy/internal/infra/config"
	"github.com/insistanan/api-proxy/internal/orchestration/scheduler"
	"github.com/insistanan/api-proxy/internal/orchestration/urlhealth"
	"github.com/insistanan/api-proxy/internal/protocol/types"
	"github.com/insistanan/api-proxy/internal/storage/metrics"
	"github.com/stretchr/testify/require"
)

func TestSendRequest_OnlyEndpointFailuresChangeURLOrder(t *testing.T) {
	for _, tc := range []struct {
		name    string
		status  int
		body    string
		demoted bool
	}{
		{"认证", 401, `{"error":{"message":"invalid key"}}`, false},
		{"配额", 402, `{"error":{"message":"payment required"}}`, false},
		{"权限", 403, `{"error":{"message":"forbidden"}}`, false},
		{"限流", 429, `{"error":{"message":"rate limited"}}`, false},
		{"5xx配额信封", 500, `{"error":{"code":"insufficient_quota"}}`, false},
		{"2xx认证信封", 200, `{"error":{"code":"invalid_api_key"}}`, false},
		{"网关故障", 502, `{"error":{"message":"bad gateway"}}`, true},
		{"响应格式错误", 200, `not json`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var received atomic.Bool
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				received.Store(true)
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			t.Cleanup(server.Close)
			cfg, err := config.NewConfigManager(filepath.Join(t.TempDir(), "config.json"))
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, cfg.Close()) })
			upstream := config.UpstreamConfig{
				Name: "url-health", ServiceType: "claude", APIKeys: []string{"key"},
				BaseURL: server.URL, BaseURLs: []string{server.URL, "https://other.example"},
			}
			added, err := cfg.AddChannel("messages", upstream)
			require.NoError(t, err)
			manager := metrics.NewMetricsManager()
			sch := scheduler.NewChannelScheduler(cfg, map[scheduler.ChannelKind]*metrics.MetricsManager{
				scheduler.ChannelKindMessages: manager,
			}, nil, urlhealth.NewURLManager(time.Minute, 1))
			t.Cleanup(sch.Stop)
			urls := upstream.GetAllBaseURLs()
			sch.GetSortedURLsForChannel(scheduler.ChannelKindMessages, added.Index, urls)
			selection := &SelectedChannel{Kind: scheduler.ChannelKindMessages, ChannelIndex: added.Index, Upstream: &upstream}
			caller := &Caller{CfgManager: cfg, ChannelScheduler: sch}
			request, err := http.NewRequest(http.MethodPost, server.URL, nil)
			require.NoError(t, err)
			convert := func(*types.ProviderResponse) (*types.ClaudeResponse, error) {
				return nil, errors.New("上游响应无法转换")
			}
			_, err = caller.sendRequest(convert, selection, &upstream, &callLogContext{}, "attempt", "model", server.URL, "key", request, time.Now(), false)
			require.Error(t, err)
			require.True(t, received.Load(), "必须收到上游响应，不能因本地准备失败而误通过")
			want := urls[0]
			if tc.demoted {
				want = urls[1]
			}
			ordered := sch.GetSortedURLsForChannel(selection.Kind, selection.ChannelIndex, urls)
			require.Equal(t, want, ordered[0].URL)
		})
	}
}
