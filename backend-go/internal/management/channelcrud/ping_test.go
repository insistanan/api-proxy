package channelcrud

import (
	"context"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"insistanan/api-proxy/internal/infra/config"
	"insistanan/api-proxy/internal/management/modelcatalog"
	"insistanan/api-proxy/internal/orchestration/scheduler"
)

func TestBatchPingBoundsConcurrencyAndPropagatesCancellation(t *testing.T) {
	manager, err := config.NewConfigManager(filepath.Join(t.TempDir(), "config.json"))
	require.NoError(t, err)
	defer manager.Close()
	for range 20 {
		_, err := manager.AddChannel("messages", config.UpstreamConfig{Name: "test", BaseURL: "https://example.test", APIKeys: []string{"key"}})
		require.NoError(t, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{}, 20)
	var active, peak atomic.Int32
	h := &Handlers{cfg: manager, kind: scheduler.ChannelKindMessages, discover: func(ctx context.Context, _ *config.UpstreamConfig, _ string) (modelcatalog.ModelsResponse, error) {
		n := active.Add(1)
		defer active.Add(-1)
		for old := peak.Load(); n > old && !peak.CompareAndSwap(old, n); old = peak.Load() {
		}
		started <- struct{}{}
		<-ctx.Done()
		return modelcatalog.ModelsResponse{}, ctx.Err()
	}}
	response := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(response)
	c.Request = httptest.NewRequest("POST", "/ping", nil).WithContext(ctx)
	done := make(chan struct{})
	go func() { defer close(done); h.PingAllChannels(c) }()
	for range 8 {
		select {
		case <-started:
		case <-time.After(3 * time.Second):
			t.Fatal("探测没有启动")
		}
	}
	require.EqualValues(t, 8, peak.Load())
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("取消未传播到发现器")
	}
	require.Zero(t, active.Load())
	require.Equal(t, 408, response.Code)
}
