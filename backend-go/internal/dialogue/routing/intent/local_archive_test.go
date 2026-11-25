package intent

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRuntimeArchiveRejectsTraversalAndExpandsInternalLinks(t *testing.T) {
	for _, format := range []string{"zip", "tar.gz"} {
		t.Run(format, func(t *testing.T) {
			var buffer bytes.Buffer
			if format == "zip" {
				writer := zip.NewWriter(&buffer)
				file, err := writer.Create("../llama-server")
				require.NoError(t, err)
				_, err = file.Write([]byte("invalid"))
				require.NoError(t, err)
				require.NoError(t, writer.Close())
			} else {
				compressed := gzip.NewWriter(&buffer)
				writer := tar.NewWriter(compressed)
				require.NoError(t, writer.WriteHeader(&tar.Header{Name: "../llama-server", Mode: 0700, Size: 7}))
				_, err := writer.Write([]byte("invalid"))
				require.NoError(t, err)
				require.NoError(t, writer.Close())
				require.NoError(t, compressed.Close())
			}
			root := t.TempDir()
			archive := filepath.Join(root, "runtime.archive")
			require.NoError(t, os.WriteFile(archive, buffer.Bytes(), 0600))
			_, err := extractRuntime(context.Background(), archive, filepath.Join(root, "unpacked"), format, "llama-server")
			require.ErrorContains(t, err, "不安全路径")
			_, err = os.Stat(filepath.Join(root, "llama-server"))
			require.ErrorIs(t, err, os.ErrNotExist)
		})
	}
	var buffer bytes.Buffer
	compressed := gzip.NewWriter(&buffer)
	writer := tar.NewWriter(compressed)
	require.NoError(t, writer.WriteHeader(&tar.Header{Name: "bin/llama-server", Mode: 0700, Size: 6}))
	_, err := writer.Write([]byte("server"))
	require.NoError(t, err)
	require.NoError(t, writer.WriteHeader(&tar.Header{Name: "bin/server-alias", Linkname: "llama-server", Typeflag: tar.TypeSymlink}))
	require.NoError(t, writer.Close())
	require.NoError(t, compressed.Close())
	root := t.TempDir()
	archive := filepath.Join(root, "runtime.archive")
	require.NoError(t, os.WriteFile(archive, buffer.Bytes(), 0600))
	entry, err := extractRuntime(context.Background(), archive, filepath.Join(root, "unpacked"), "tar.gz", "llama-server")
	require.NoError(t, err)
	require.Equal(t, filepath.Join("bin", "llama-server"), entry)
	data, err := os.ReadFile(filepath.Join(root, "unpacked", "bin", "server-alias"))
	require.NoError(t, err)
	require.Equal(t, "server", string(data))
}
