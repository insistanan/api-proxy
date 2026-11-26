package httpclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"insistanan/api-proxy/internal/infra/config"
)

func TestDownloadClient_UsesProjectProxyForAuxiliaryRequests(t *testing.T) {
	seen := make(chan string, 1)
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen <- r.URL.Host
		w.WriteHeader(http.StatusOK)
	}))
	defer proxy.Close()
	data, err := json.Marshal(config.Config{Settings: config.SettingsConfig{Gateway: config.DefaultGatewaySettings(), Network: config.NetworkSettings{UpstreamProxyEnabled: true, UpstreamProxyURL: proxy.URL}}})
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "config.json")
	require.NoError(t, os.WriteFile(path, data, 0600))
	cfg, err := config.NewConfigManager(path)
	require.NoError(t, err)
	defer cfg.Close()
	client, err := DownloadClient(cfg, time.Second)
	require.NoError(t, err)
	defer client.CloseIdleConnections()
	response, err := client.Get("http://download.invalid/resource")
	require.NoError(t, err)
	response.Body.Close()
	require.Equal(t, "download.invalid", <-seen)
}

func TestDownloadClient_DirectPolicyIgnoresEnvironmentAndHonorsCancellation(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "http://invalid.example:9876")
	client, err := DownloadClient(nil, time.Second)
	require.NoError(t, err)
	defer client.CloseIdleConnections()
	require.Nil(t, client.Transport.(*http.Transport).Proxy)
	require.Error(t, client.CheckRedirect(&http.Request{URL: &url.URL{Scheme: "http", Host: "example"}}, nil))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) }))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL, nil)
	require.NoError(t, err)
	_, err = client.Do(req)
	require.ErrorIs(t, err, context.Canceled)
}
