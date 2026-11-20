package modelfingerprint

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestReferenceDigestRejectsChangedOrIncompatibleData(t *testing.T) {
	payload := []byte("reference-detector-fixture")
	digest := fmt.Sprintf("%x", sha256.Sum256(payload))
	makeCache := func(t *testing.T, schema, hash string) []byte {
		t.Helper()
		manifest, err := json.Marshal(map[string]any{
			"built_at": "fixture-version", "schema": schema, "detector_schema": "shared-detector-v2",
			"files": map[string]any{"shared_detector.bin": map[string]any{"sha256": hash}},
		})
		require.NoError(t, err)
		data := make([]byte, 4)
		binary.LittleEndian.PutUint32(data, uint32(len(manifest)))
		return append(append(data, manifest...), payload...)
	}
	valid := makeCache(t, "lmfpd-data-v1", digest)
	got, err := referenceDigest(valid, "fixture-version")
	require.NoError(t, err)
	require.Equal(t, digest, got)
	for _, test := range []struct {
		name, builtAt, message string
		data                   []byte
	}{
		{"内容被修改", "fixture-version", "SHA-256", makeCache(t, "lmfpd-data-v1", "old-digest")},
		{"格式已升级", "fixture-version", "格式不兼容", makeCache(t, "lmfpd-data-v2", digest)},
		{"构建版本不一致", "different-version", "版本不一致", valid},
		{"头部截断", "fixture-version", "头不完整", valid[:3]},
		{"长度越界", "fixture-version", "长度无效", []byte{255, 255, 255, 255, 0}},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := referenceDigest(test.data, test.builtAt)
			require.ErrorContains(t, err, test.message)
		})
	}
}
