package intent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"insistanan/api-proxy/internal/infra/config"
)

func TestPreparation_StaleCancelDoesNotCancelReplacementAndRetainsPartialOutput(t *testing.T) {
	s := &Service{}
	t.Cleanup(func() { require.NoError(t, s.StopPreparation()) })
	a, err := s.startPreparation("chat", func(context.Context) (*config.IntentRoutingConfig, error) { return nil, nil })
	require.NoError(t, err)
	<-s.preparationDone
	started := make(chan struct{})
	b, err := s.startPreparation("chat", func(ctx context.Context) (*config.IntentRoutingConfig, error) {
		recordPreparation(ctx, "dataset:revision-2")
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	})
	require.NoError(t, err)
	<-started
	_, err = s.CancelPreparation(a.ID)
	require.Error(t, err)
	require.True(t, s.PreparationStatus().Running)
	_, err = s.CancelPreparation(b.ID)
	require.NoError(t, err)
	<-s.preparationDone
	status := s.PreparationStatus()
	require.Equal(t, "cancelled", status.Phase)
	require.True(t, status.Committed)
	require.Equal(t, []string{"dataset:revision-2"}, status.Completed)
	require.Nil(t, status.Config)
}

func TestPresetFetch_SlowNetworkDoesNotBlockStatusOrPublishOldURL(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-release
		_, _ = w.Write([]byte(`{"version":1,"presets":[]}`))
	}))
	defer server.Close()
	local, err := NewLocal(t.TempDir())
	require.NoError(t, err)
	local.presets.url, local.presets.client = server.URL, server.Client()
	done := make(chan struct{})
	go func() { local.Presets(context.Background()); close(done) }()
	<-started
	status := make(chan LocalStatus, 1)
	go func() { status <- local.Status() }()
	select {
	case <-status:
	case <-time.After(time.Second):
		close(release)
		t.Fatal("网络下载阻塞了状态查询")
	}
	require.NoError(t, local.SetPresetsURL("https://new.example/catalog.json"))
	close(release)
	<-done
	require.Zero(t, local.presets.manifest.Version, "旧地址下载不能覆盖新地址清单")
}

func TestLocalCache_CleansOnlyUnusedManagedResources(t *testing.T) {
	dir := t.TempDir()
	local, err := NewLocal(dir)
	require.NoError(t, err)
	current, old := strings.Repeat("a", 64), strings.Repeat("b", 64)
	local.manifest.ModelSHA256 = current
	for _, name := range []string{current + ".gguf", old + ".gguf", "user-file.gguf"} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte("GGUF"), 0600))
	}
	status, err := local.LocalCache(context.Background(), false)
	require.NoError(t, err)
	require.EqualValues(t, 8, status.TotalBytes)
	require.Equal(t, 1, status.UnusedEntries)
	local.cmd = &localProcess{}
	_, err = local.LocalCache(context.Background(), true)
	require.Error(t, err)
	local.cmd = nil
	status, err = local.LocalCache(context.Background(), true)
	require.NoError(t, err)
	require.EqualValues(t, 4, status.RemovedBytes)
	require.FileExists(t, filepath.Join(dir, current+".gguf"))
	require.FileExists(t, filepath.Join(dir, "user-file.gguf"))
	require.NoFileExists(t, filepath.Join(dir, old+".gguf"))
}
