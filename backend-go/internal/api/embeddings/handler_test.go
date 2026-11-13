package embeddings

import (
	"encoding/json"
	"testing"

	"github.com/insistanan/api-proxy/internal/infra/config"
	"github.com/stretchr/testify/require"
)

func decodePayload(t *testing.T, body []byte) map[string]interface{} {
	t.Helper()
	var payload map[string]interface{}
	require.NoError(t, json.Unmarshal(body, &payload))
	return payload
}

// 渠道设置了输出维度时，覆盖客户端请求里的 dimensions，其他字段原样保留。
func TestRewriteEmbeddingsPayloadOverridesDimensions(t *testing.T) {
	up := &config.UpstreamConfig{ServiceType: "openai", EmbeddingsDimensions: 1024}
	body := []byte(`{"model":"text-embedding-3-small","input":["你好，世界"],"dimensions":1536}`)

	out, err := rewriteEmbeddingsPayload(body, up)
	require.NoError(t, err)
	payload := decodePayload(t, out)
	require.Equal(t, float64(1024), payload["dimensions"])
	require.Equal(t, []interface{}{"你好，世界"}, payload["input"])
	require.Equal(t, "text-embedding-3-small", payload["model"])
}

// 渠道维度为零时不改写，客户端请求的 dimensions 原样透传。
func TestRewriteEmbeddingsPayloadKeepsDimensionsWhenUnset(t *testing.T) {
	up := &config.UpstreamConfig{ServiceType: "openai"}
	body := []byte(`{"model":"text-embedding-3-small","input":"hi","dimensions":1536}`)

	out, err := rewriteEmbeddingsPayload(body, up)
	require.NoError(t, err)
	require.Equal(t, float64(1536), decodePayload(t, out)["dimensions"])
}

// 客户端未传 dimensions 且渠道设置了维度时，补上渠道维度。
func TestRewriteEmbeddingsPayloadAddsDimensionsWhenMissing(t *testing.T) {
	up := &config.UpstreamConfig{ServiceType: "openai", EmbeddingsDimensions: 1024}
	body := []byte(`{"model":"text-embedding-3-small","input":"hi"}`)

	out, err := rewriteEmbeddingsPayload(body, up)
	require.NoError(t, err)
	require.Equal(t, float64(1024), decodePayload(t, out)["dimensions"])
}
