package visionlayer

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"insistanan/api-proxy/internal/infra/config"
	"insistanan/api-proxy/internal/orchestration/scheduler"
)

func TestPrepareRequest_ReplayUsesFinalImageDescription(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(visionSuccessBody))
	}))
	t.Cleanup(server.Close)
	cfg, sch, ctx, _ := newVisionExecutionFixture(t, []config.UpstreamConfig{
		{ID: "vision", Name: "vision", BaseURL: server.URL, APIKeys: []string{"key"}},
	})
	t.Cleanup(func() { ClearConversationCache(t.Name()) })
	original := `{"model":"test","messages":[{"role":"user","content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"aGVsbG8="}}]}]}`
	request, err := http.NewRequestWithContext(ctx.Request.Context(), http.MethodPost, "http://example.test/v1/messages", strings.NewReader(original))
	require.NoError(t, err)
	target := &config.UpstreamConfig{ServiceType: "claude", VisionLayerEnabled: true, VisionLayerChannelID: "vision"}
	require.NoError(t, PrepareRequest(ctx, config.DefaultEnvConfig(), cfg, sch, scheduler.ChannelKindMessages, target, "test", t.Name(), request))
	actual, err := io.ReadAll(request.Body)
	require.NoError(t, err)
	require.NoError(t, request.Body.Close())
	require.Contains(t, string(actual), "图片中有一只猫")
	require.NotContains(t, string(actual), "aGVsbG8=")
	require.NotNil(t, request.GetBody)
	for range 2 {
		replay, err := request.GetBody()
		require.NoError(t, err)
		body, err := io.ReadAll(replay)
		require.NoError(t, err)
		require.NoError(t, replay.Close())
		require.Equal(t, actual, body, "保活不得重放替换前的图片正文")
		require.EqualValues(t, len(body), request.ContentLength)
	}
}
