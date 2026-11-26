package intent

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/stretchr/testify/require"
	"insistanan/api-proxy/internal/storage/atomicfile"
)

func TestLocalImportValidatesBeforeReplacingCache(t *testing.T) {
	local, err := NewLocal(t.TempDir())
	require.NoError(t, err)
	manifest := testPresetManifest()
	entry := manifest.Presets[0].Platforms[runtime.GOOS+"/"+runtime.GOARCH]
	model := "GGUFoffline-model"
	entry.ModelSHA256 = digest(model)
	manifest.Presets[0].Platforms[runtime.GOOS+"/"+runtime.GOARCH] = entry
	local.presets.manifest, local.presets.fetchedAt = manifest, time.Now()
	require.NoError(t, local.ImportResource(context.Background(), "cpu", "model", strings.NewReader(model)))
	file := filepath.Join(local.dir, entry.ModelSHA256+".gguf")
	require.ErrorContains(t, local.ImportResource(context.Background(), "cpu", "model", strings.NewReader("GGUFwrong")), "SHA-256")
	data, err := os.ReadFile(file)
	require.NoError(t, err)
	require.Equal(t, model, string(data))
	require.NoError(t, local.downloadResource(context.Background(), []string{"https://example.invalid/must-not-be-called"}, entry.ModelSHA256, 1<<30, file, "GGUF"))
	require.Contains(t, local.Status().Notice, "复用")
	require.False(t, local.Status().Installed, "离线导入本身不能启动或发布运行时")
}

func TestBoundedEmbeddingPreservesUTF8AndInputTail(t *testing.T) {
	input := strings.Repeat("中a🙂", 1000) + "最后才给出具体任务"
	chunks := embeddingChunks(input)
	require.Greater(t, len(chunks), 1)
	require.Equal(t, input, strings.Join(chunks, ""))
	for _, chunk := range chunks {
		require.True(t, utf8.ValidString(chunk))
		require.LessOrEqual(t, len(chunk), 384)
	}
	text, truncated := normalizeInput(strings.Repeat("中", 6000) + "请修复接口")
	require.True(t, truncated)
	require.Len(t, []rune(text), 4000)
	require.True(t, strings.HasSuffix(text, "请修复接口"))
	require.Contains(t, text, "中间材料已省略")
}

func TestVerifiedDownloadCommitsOnlyCompleteValidModel(t *testing.T) {
	model := "GGUF" + strings.Repeat("model", 20000)
	for _, tc := range []struct {
		name, body, hash string
		limit            int64
		truncated        bool
		wantError        string
	}{
		{name: "成功", body: model, hash: digest(model), limit: int64(len(model))},
		{name: "摘要错误", body: model, hash: digest("wrong"), limit: int64(len(model)), wantError: "SHA-256"},
		{name: "无长度的超限响应", body: model, hash: digest(model), limit: 20, wantError: "大小限制"},
		{name: "格式错误", body: "HTML error", hash: digest("HTML error"), limit: 100, wantError: "GGUF"},
		{name: "响应截断", body: "GGUF", hash: digest("GGUF"), limit: 100, truncated: true, wantError: "unexpected EOF"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if tc.truncated {
					w.Header().Set("Content-Length", fmt.Sprint(len(tc.body)+1))
				}
				w.(http.Flusher).Flush()
				_, _ = io.WriteString(w, tc.body)
			}))
			defer server.Close()
			path := filepath.Join(t.TempDir(), "model.gguf")
			require.NoError(t, os.WriteFile(path, []byte("原模型"), 0600))
			err := atomicfile.WriteFunc(path, func(w io.Writer) error {
				return downloadVerified(context.Background(), server.Client(), server.URL, tc.hash, tc.limit, w, "GGUF")
			}, atomicfile.Options{})
			data, readErr := os.ReadFile(path)
			require.NoError(t, readErr)
			if tc.wantError == "" {
				require.NoError(t, err)
				require.Equal(t, model, string(data))
			} else {
				require.ErrorContains(t, err, tc.wantError)
				require.False(t, atomicfile.IsCommitted(err))
				require.Equal(t, "原模型", string(data))
			}
			entries, err := os.ReadDir(filepath.Dir(path))
			require.NoError(t, err)
			require.Len(t, entries, 1, "临时下载必须清理")
		})
	}
}

func TestStopDoesNotSucceedWhileStartIsUnpublished(t *testing.T) {
	l, err := NewLocal(t.TempDir())
	require.NoError(t, err)
	l.busy = true
	require.ErrorContains(t, l.Stop(), "等待当前操作")
	l.busy = false
	require.NoError(t, l.Stop())
}
