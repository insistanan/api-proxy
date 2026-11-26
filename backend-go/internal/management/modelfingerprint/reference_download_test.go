package modelfingerprint

import (
	"context"
	"crypto/sha256"
	"fmt"
	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/require"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestReferenceDownload_UsesInjectedClientAndVerifiesChunks(t *testing.T) {
	payload := []byte("fixture detector bytes")
	encoder, err := zstd.NewWriter(nil)
	require.NoError(t, err)
	compressed := encoder.EncodeAll(payload, nil)
	require.NoError(t, encoder.Close())
	digest := fmt.Sprintf("%x", sha256.Sum256(payload))
	for _, hash := range []string{digest, "invalid-digest"} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/bundle.json" {
				_, _ = fmt.Fprintf(w, `{"built_at":"fixture","schema":"lmfpd-data-v1","detector_schema":"shared-detector-v2","files":{"shared_detector.bin":{"sha256":%q,"parts":["part.zst"]}}}`, hash)
			} else if r.URL.Path == "/chunks/part.zst" {
				_, _ = w.Write(compressed)
			} else {
				w.WriteHeader(404)
			}
		}))
		data, err := downloadReferenceSource(context.Background(), server.Client(), server.URL+"/")
		server.Close()
		if hash != digest {
			require.ErrorContains(t, err, "SHA-256")
			continue
		}
		require.NoError(t, err)
		actual, err := referenceDigest(data, "fixture")
		require.NoError(t, err)
		require.Equal(t, digest, actual)
	}
}
