package images

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/BenedictKing/api-proxy/internal/config"
	"github.com/BenedictKing/api-proxy/internal/utils"
	"github.com/stretchr/testify/require"
)

func decodeImagesJSONObject(t *testing.T, body []byte) map[string]interface{} {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var payload map[string]interface{}
	require.NoError(t, decoder.Decode(&payload))
	return payload
}

func TestApplyImagesModelMappingRewritesExactAndWildcardJSON(t *testing.T) {
	exact := &config.UpstreamConfig{
		ModelMapping: map[string][]string{"gpt-image-2.5-sunburst": {"sensenova-u1.5-lite"}},
	}
	mapped, contentType, err := applyImagesModelMapping("application/json", []byte(`{"model":"gpt-image-2.5-sunburst","prompt":"猫"}`), exact)
	require.NoError(t, err)
	require.Equal(t, "application/json", contentType)
	require.Equal(t, "sensenova-u1.5-lite", decodeImagesJSONObject(t, mapped)["model"])

	wildcard := &config.UpstreamConfig{
		ModelMapping: map[string][]string{"*": {"sensenova-u1.5-lite"}},
	}
	mapped, _, err = applyImagesModelMapping("application/json", []byte(`{"model":"anything-else","prompt":"猫"}`), wildcard)
	require.NoError(t, err)
	require.Equal(t, "sensenova-u1.5-lite", decodeImagesJSONObject(t, mapped)["model"])

	mapped, contentType, err = applyImagesModelMapping("", []byte(`{"model":"gpt-image-2.5-sunburst","prompt":"猫"}`), exact)
	require.NoError(t, err)
	require.Equal(t, "application/json", contentType)
	require.Equal(t, "sensenova-u1.5-lite", decodeImagesJSONObject(t, mapped)["model"])

	mapped, _, err = applyImagesModelMapping("application/json", []byte(`{"prompt":"猫"}`), wildcard)
	require.NoError(t, err)
	require.Equal(t, "sensenova-u1.5-lite", decodeImagesJSONObject(t, mapped)["model"])
}

func TestApplyImagesModelMappingInjectsTypedExtraParamsWithoutOverwriting(t *testing.T) {
	upstream := &config.UpstreamConfig{
		ImagesExtraParams: map[string]string{
			"watermark": "false",
			"label":     `"false"`,
			"count":     "2",
			"meta":      `{"k":1}`,
			"tags":      `["a"]`,
			"empty":     "null",
			"plain":     "hello",
			"n":         "9",
		},
	}
	body := []byte(`{"model":"gpt-image-1","prompt":"猫","n":1}`)
	mapped, _, err := applyImagesModelMapping("application/json", body, upstream)
	require.NoError(t, err)
	payload := decodeImagesJSONObject(t, mapped)
	require.Equal(t, "gpt-image-1", payload["model"])
	require.Equal(t, json.Number("1"), payload["n"])
	require.Equal(t, false, payload["watermark"])
	require.Equal(t, "false", payload["label"])
	require.Equal(t, json.Number("2"), payload["count"])
	require.Equal(t, "hello", payload["plain"])
	require.Nil(t, payload["empty"])
	require.Equal(t, json.Number("1"), payload["meta"].(map[string]interface{})["k"])
	require.Equal(t, []interface{}{"a"}, payload["tags"])
}

func TestApplyImagesModelMappingKeepsBodyWhenExtraParamsEmpty(t *testing.T) {
	body := []byte(`{"model":"gpt-image-1","prompt":"猫"}`)
	mapped, contentType, err := applyImagesModelMapping("application/json", body, &config.UpstreamConfig{})
	require.NoError(t, err)
	require.Equal(t, "application/json", contentType)
	require.Equal(t, body, mapped)
}

func TestApplyImagesModelMappingRejectsInvalidExtraParamsAndNonJSON(t *testing.T) {
	body := []byte(`{"model":"gpt-image-1","prompt":"猫"}`)
	_, _, err := applyImagesModelMapping("application/json", body, &config.UpstreamConfig{
		ImagesExtraParams: map[string]string{"meta": "{"},
	})
	require.ErrorContains(t, err, "不是合法 JSON")

	_, _, err = applyImagesModelMapping("text/plain", []byte("not-json"), &config.UpstreamConfig{
		ImagesExtraParams: map[string]string{"watermark": "false"},
	})
	require.ErrorContains(t, err, "只能写入 JSON 或 multipart")

	_, _, err = applyImagesModelMapping("text/plain", []byte("not-json"), &config.UpstreamConfig{ImagesForceNonStream: true})
	require.ErrorContains(t, err, "强制非流式只能写入")
}

func TestApplyImagesModelMappingRewritesMultipartModelAndAppendsParams(t *testing.T) {
	body, contentType := buildImagesForm(t)
	upstream := &config.UpstreamConfig{
		ModelMapping:      map[string][]string{"*": {"sensenova-u1.5-lite"}},
		ImagesExtraParams: map[string]string{"watermark": "false"},
	}
	mappedBody, mappedContentType, err := applyImagesModelMapping(contentType, body, upstream)
	require.NoError(t, err)
	boundary, ok := utils.MultipartBoundary(mappedContentType)
	require.True(t, ok)
	parts, err := utils.ParseMultipartParts(mappedBody, boundary)
	require.NoError(t, err)

	values := map[string]string{}
	for _, part := range parts {
		if part.FileName == "" {
			values[part.FormName] = string(part.Content)
		}
	}
	require.Equal(t, "sensenova-u1.5-lite", values["model"])
	require.Equal(t, "false", values["watermark"])
	require.Equal(t, "  给猫加一顶帽子  ", values["prompt"])
}

func TestExtractImagesRequestMetadataReadsJSONWithoutContentType(t *testing.T) {
	metadata := extractImagesRequestMetadata("", []byte(`{"model":"gpt-image-2.5-sunburst","stream":true}`))
	require.Equal(t, "gpt-image-2.5-sunburst", metadata.Model)
	require.True(t, metadata.Stream)
}

func TestApplyImagesModelMappingForcesNonStreamJSON(t *testing.T) {
	upstream := &config.UpstreamConfig{ImagesForceNonStream: true}
	mapped, _, err := applyImagesModelMapping("application/json", []byte(`{"model":"gpt-image-1","prompt":"猫","stream":true,"partial_images":1}`), upstream)
	require.NoError(t, err)
	payload := decodeImagesJSONObject(t, mapped)
	require.Equal(t, false, payload["stream"])
	require.NotContains(t, payload, "partial_images")

	alreadyFalse := []byte(`{"model":"gpt-image-1","prompt":"猫","stream":false}`)
	mapped, contentType, err := applyImagesModelMapping("application/json", alreadyFalse, upstream)
	require.NoError(t, err)
	require.Equal(t, "application/json", contentType)
	require.Equal(t, alreadyFalse, mapped)

	mapped, _, err = applyImagesModelMapping("application/json", []byte(`{"model":"gpt-image-1","prompt":"猫"}`), upstream)
	require.NoError(t, err)
	require.Equal(t, false, decodeImagesJSONObject(t, mapped)["stream"])
}

func TestApplyImagesModelMappingForcesNonStreamMultipart(t *testing.T) {
	body, contentType := buildImagesForm(t)
	mappedBody, mappedContentType, err := applyImagesModelMapping(contentType, body, &config.UpstreamConfig{ImagesForceNonStream: true})
	require.NoError(t, err)
	boundary, ok := utils.MultipartBoundary(mappedContentType)
	require.True(t, ok)
	parts, err := utils.ParseMultipartParts(mappedBody, boundary)
	require.NoError(t, err)
	values := map[string]string{}
	for _, part := range parts {
		if part.FileName == "" {
			values[part.FormName] = string(part.Content)
		}
	}
	require.Equal(t, "false", values["stream"])
	require.Equal(t, "gpt-image-1", values["model"])
	require.NotContains(t, values, "partial_images")
}
