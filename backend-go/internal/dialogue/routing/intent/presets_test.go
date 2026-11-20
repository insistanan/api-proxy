package intent

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func testPresetManifest() EmbeddingPresets {
	return EmbeddingPresets{Version: 1, Presets: []EmbeddingPreset{{ID: "cpu", Name: "CPU 测试资源", Description: "仅测试清单校验", SizeBytes: 123,
		Platforms: map[string]LocalManifest{runtime.GOOS + "/" + runtime.GOARCH: {
			RuntimeURL: "https://example.invalid/runtime.zip", RuntimeSHA256: strings.Repeat("a", 64), Executable: "llama-server.exe",
			ModelURL: "https://example.invalid/model.gguf", ModelSHA256: strings.Repeat("b", 64), ModelName: "embedding", Pooling: "mean",
		}},
	}}}
}

func TestPresetCatalogCachesFiltersAndPreservesFailedRefresh(t *testing.T) {
	requests := 0
	body, err := json.Marshal(testPresetManifest())
	require.NoError(t, err)
	status := http.StatusOK
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		w.WriteHeader(status)
		_, _ = w.Write(body)
	}))
	defer server.Close()
	local, err := NewLocal(t.TempDir())
	require.NoError(t, err)
	local.presets.url, local.presets.client.Transport = server.URL, server.Client().Transport
	first := local.Presets(context.Background())
	require.Empty(t, first.Error)
	require.Len(t, first.Presets, 1)
	require.True(t, filepath.IsAbs(first.InstallPath))
	require.Equal(t, server.URL, local.Status().PresetsURL)
	require.Len(t, local.Presets(context.Background()).Presets, 1)
	require.Equal(t, 1, requests, "成功清单在 10 分钟内复用")
	local.presets.fetchedAt = time.Now().Add(-presetCacheTTL)
	status = http.StatusBadGateway
	stale := local.Presets(context.Background())
	require.Contains(t, stale.Error, "HTTP 502")
	require.True(t, stale.Stale)
	require.Equal(t, first.Presets, stale.Presets)
	require.ErrorContains(t, local.InstallPreset(context.Background(), "cpu"), "HTTP 502")
	require.False(t, local.Status().Installed, "失败刷新不能被悄悄当作成功安装")
	status = http.StatusOK
	body = []byte(`{"version":1,"presets":[]}`)
	fresh := local.Presets(context.Background())
	require.Empty(t, fresh.Error)
	require.False(t, fresh.Stale)
	require.Empty(t, fresh.Presets)
}

func TestPresetCatalogRejectsUnsafeAndOversizedResponses(t *testing.T) {
	valid, err := json.Marshal(testPresetManifest())
	require.NoError(t, err)
	for _, tc := range []struct {
		name, body, contains string
		redirect             bool
	}{
		{"非 HTTPS 重定向", "", "HTTPS", true},
		{"过大无长度响应", strings.Repeat(" ", presetBodyLimit+1), "256 KiB", false},
		{"错误版本", `{"version":2,"presets":[]}`, "version=1", false},
		{"缺少数组", `{"version":1}`, "presets", false},
		{"网页内容", "<html>missing</html>", "JSON", false},
		{"非 HTTPS 安装项", strings.Replace(string(valid), "https://example.invalid/model", "http://example.invalid/model", 1), "HTTPS", false},
		{"缺失摘要", strings.Replace(string(valid), strings.Repeat("a", 64), "abc", 1), "SHA-256", false},
		{"错误池化", strings.Replace(string(valid), `"mean"`, `"max"`, 1), "池化", false},
		{"路径逃逸", strings.Replace(string(valid), `llama-server.exe`, `../llama-server.exe`, 1), "相对路径", false},
		{"未知字段", `{"version":1,"presets":[],"ignored":true}`, "JSON", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tc.redirect {
					http.Redirect(w, r, "http://example.invalid/list", http.StatusFound)
					return
				}
				w.(http.Flusher).Flush()
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			catalog := newPresetCatalog()
			catalog.url, catalog.client.Transport = server.URL, server.Client().Transport
			_, err := catalog.fetch(context.Background())
			require.ErrorContains(t, err, tc.contains)
		})
	}
}

func TestPresetSelectionRejectsUnknownOrUnsupportedIDs(t *testing.T) {
	local, err := NewLocal(t.TempDir())
	require.NoError(t, err)
	local.presets.manifest, local.presets.fetchedAt = testPresetManifest(), time.Now()
	require.ErrorContains(t, local.InstallPreset(context.Background(), "unlisted"), "没有预设")
	local.presets.manifest.Presets[0].Platforms = map[string]LocalManifest{"other/arch": {}}
	require.Empty(t, local.Presets(context.Background()).Presets)
	require.ErrorContains(t, local.InstallPreset(context.Background(), "cpu"), "不支持当前平台")
	require.False(t, local.Status().Installed)
}

func TestPresetsURLIsEditablePersistedAndResets(t *testing.T) {
	body, err := json.Marshal(testPresetManifest())
	require.NoError(t, err)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	}))
	defer server.Close()
	dir := t.TempDir()
	local, err := NewLocal(dir)
	require.NoError(t, err)
	require.Equal(t, DefaultPresetsURL, local.Status().PresetsURL)
	local.presets.client.Transport = server.Client().Transport
	require.NoError(t, local.SetPresetsURL(server.URL))
	require.Equal(t, server.URL, local.Presets(context.Background()).PresetsURL)
	require.Len(t, local.Presets(context.Background()).Presets, 1)

	reopened, err := NewLocal(dir)
	require.NoError(t, err)
	reopened.presets.client.Transport = server.Client().Transport
	require.Equal(t, server.URL, reopened.Status().PresetsURL, "清单地址须跨重启保留")
	require.NoError(t, reopened.SetPresetsURL("  "))
	require.Equal(t, DefaultPresetsURL, reopened.Status().PresetsURL)
	require.ErrorContains(t, reopened.SetPresetsURL("http://example.invalid/presets.json"), "HTTPS")
}

// 发布清单是实际下发内容，损坏或字段缺失必须在测试阶段被发现。
func TestDeployPresetManifestIsValid(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "..", "deploy", "embedding-presets.json"))
	if errors.Is(err, os.ErrNotExist) {
		t.Skip("deploy 清单不随包分发")
	}
	require.NoError(t, err)
	manifest, err := validatePresets(data)
	require.NoError(t, err)
	require.NotEmpty(t, manifest.Presets)
	for _, preset := range manifest.Presets {
		_, ok := preset.Platforms["windows/amd64"]
		require.True(t, ok, "当前一键安装只支持 windows/amd64：%s", preset.ID)
	}
}
