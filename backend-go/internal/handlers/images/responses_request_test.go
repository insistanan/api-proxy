package images

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/BenedictKing/api-proxy/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestImagesResponsesRequestSeparatesMainAndImageModels(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil)
	c.Request.Header.Set("Content-Type", "application/json")
	upstream := &config.UpstreamConfig{
		BaseURL: "https://upstream.example.com/v1", ServiceType: "responses", ResponsesModel: "gpt-4.1",
		ModelMapping: map[string][]string{"gpt-image-1": {"gpt-image-1.5"}},
	}
	body := []byte(`{"model":"gpt-image-1","prompt":"画一只猫","quality":"high","stream":true,"partial_images":1,"metadata":{"channel_index":0,"purpose":"quick_test"}}`)
	req, err := buildImagesUpstreamRequest(c, upstream, "test-key", "/images/generations", body)
	require.NoError(t, err)
	require.Equal(t, "https://upstream.example.com/v1/responses", req.URL.String())
	require.Equal(t, "Bearer test-key", req.Header.Get("Authorization"))
	require.Equal(t, "application/json", req.Header.Get("Content-Type"))
	encoded, err := io.ReadAll(req.Body)
	require.NoError(t, err)
	var payload map[string]interface{}
	require.NoError(t, json.Unmarshal(encoded, &payload))
	require.Equal(t, "gpt-4.1", payload["model"])
	tool := payload["tools"].([]interface{})[0].(map[string]interface{})
	require.Equal(t, "gpt-image-1.5", tool["model"])
	require.Equal(t, "generate", tool["action"])
	require.Equal(t, "high", tool["quality"])
	require.Equal(t, true, payload["stream"])
	require.NotContains(t, payload, "metadata", "网关的调试元数据不应泄漏到上游")
}

func TestImagesResponsesEditConvertsJSONReferencesAndMask(t *testing.T) {
	payload, err := readImagesPayload("application/json", []byte(`{
		"model":"gpt-image-1","prompt":"给猫加一顶帽子",
		"images":[{"image_url":"https://images.example.com/cat.png"},{"image_url":"data:image/png;base64,AQID"}],
		"mask":{"image_url":"data:image/png;base64,BAUG"},"input_fidelity":"high"
	}`))
	require.NoError(t, err)
	converted, err := convertImagesRequestToResponses(payload, &config.UpstreamConfig{ResponsesModel: "gpt-4.1"}, true)
	require.NoError(t, err)
	content := converted["input"].([]interface{})[0].(map[string]interface{})["content"].([]interface{})
	require.Len(t, content, 3)
	require.Equal(t, "input_image", content[1].(map[string]interface{})["type"])
	require.Equal(t, "https://images.example.com/cat.png", content[1].(map[string]interface{})["image_url"])
	tool := converted["tools"].([]interface{})[0].(map[string]interface{})
	require.Equal(t, "edit", tool["action"])
	require.Equal(t, "high", tool["input_fidelity"])
	require.Equal(t, "data:image/png;base64,BAUG", tool["input_image_mask"].(map[string]interface{})["image_url"])
}

func TestImagesResponsesEditConvertsMultipartImages(t *testing.T) {
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	require.NoError(t, form.WriteField("model", "gpt-image-1"))
	require.NoError(t, form.WriteField("prompt", "给猫加一顶帽子"))
	require.NoError(t, form.WriteField("stream", "true"))
	require.NoError(t, form.WriteField("partial_images", "1"))
	for _, field := range []string{"image[]", "image[]", "mask"} {
		part, err := form.CreateFormFile(field, "image.png")
		require.NoError(t, err)
		_, err = part.Write([]byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A})
		require.NoError(t, err)
	}
	require.NoError(t, form.Close())
	payload, err := readImagesPayload(form.FormDataContentType(), body.Bytes())
	require.NoError(t, err)
	converted, err := convertImagesRequestToResponses(payload, &config.UpstreamConfig{ResponsesModel: "gpt-4.1"}, true)
	require.NoError(t, err)
	content := converted["input"].([]interface{})[0].(map[string]interface{})["content"].([]interface{})
	require.Len(t, content, 3)
	require.True(t, strings.HasPrefix(content[1].(map[string]interface{})["image_url"].(string), "data:image/png;base64,"))
	require.Equal(t, true, converted["stream"])
}

func TestImagesResponsesRejectsRequestsThatCannotPreserveSemantics(t *testing.T) {
	for name, body := range map[string]string{
		"缺少提示词":  `{"model":"gpt-image-1"}`,
		"不能托管链接": `{"model":"gpt-image-1","prompt":"猫","response_format":"url"}`,
		"未知参数":   `{"model":"gpt-image-1","prompt":"猫","style":"vivid"}`,
		"错误流式类型": `{"model":"gpt-image-1","prompt":"猫","stream":"true"}`,
	} {
		t.Run(name, func(t *testing.T) {
			payload, err := readImagesPayload("application/json", []byte(body))
			require.NoError(t, err)
			_, err = convertImagesRequestToResponses(payload, &config.UpstreamConfig{ResponsesModel: "gpt-4.1"}, false)
			require.Error(t, err)
		})
	}
	_, err := readImagesPayload("application/json", []byte(`{"model":"gpt-image-1"} {}`))
	require.Error(t, err)
}

func TestImagesResponsesForceNonStreamDropsPartialImages(t *testing.T) {
	payload, err := readImagesPayload("application/json", []byte(`{"model":"gpt-image-1","prompt":"猫","stream":true,"partial_images":1}`))
	require.NoError(t, err)
	converted, err := convertImagesRequestToResponses(payload, &config.UpstreamConfig{ResponsesModel: "gpt-4.1", ImagesForceNonStream: true}, false)
	require.NoError(t, err)
	require.Equal(t, false, converted["stream"])
	tool := converted["tools"].([]interface{})[0].(map[string]interface{})
	require.NotContains(t, tool, "partial_images")

	payload, err = readImagesPayload("application/json", []byte(`{"model":"gpt-image-1","prompt":"猫"}`))
	require.NoError(t, err)
	converted, err = convertImagesRequestToResponses(payload, &config.UpstreamConfig{ResponsesModel: "gpt-4.1", ImagesForceNonStream: true}, false)
	require.NoError(t, err)
	require.Equal(t, false, converted["stream"])
}

func TestNativeImagesRequestKeepsEndpointAndPayload(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil)
	c.Request.Header.Set("Content-Type", "application/json")
	body := []byte(`{"model":"gpt-image-1","prompt":"猫","n":2}`)
	req, err := buildImagesUpstreamRequest(c, &config.UpstreamConfig{BaseURL: "https://api.example.com", ServiceType: "openai"}, "test-key", "/images/generations", body)
	require.NoError(t, err)
	require.Equal(t, "https://api.example.com/v1/images/generations", req.URL.String())
	got, err := io.ReadAll(req.Body)
	require.NoError(t, err)
	require.Equal(t, body, got)
}

func TestNativeImagesRequestForcesNonStreamWithoutChangingClientMetadata(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil)
	c.Request.Header.Set("Content-Type", "application/json")
	body := []byte(`{"model":"gpt-image-1","prompt":"猫","stream":true,"partial_images":1}`)
	req, err := buildImagesUpstreamRequest(c, &config.UpstreamConfig{
		BaseURL: "https://api.example.com", ServiceType: "openai", ImagesForceNonStream: true,
	}, "test-key", "/images/generations", body)
	require.NoError(t, err)
	got, err := io.ReadAll(req.Body)
	require.NoError(t, err)
	decoder := json.NewDecoder(bytes.NewReader(got))
	decoder.UseNumber()
	var payload map[string]interface{}
	require.NoError(t, decoder.Decode(&payload))
	require.Equal(t, false, payload["stream"])
	require.NotContains(t, payload, "partial_images")
	metadata := extractImagesRequestMetadata("application/json", body)
	require.True(t, metadata.Stream)
}

func TestNativeImagesRequestRewritesMappedModelAndExtraParamsWithoutContentType(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil)
	body := []byte(`{"model":"gpt-image-2.5-sunburst","prompt":"猫","n":1}`)
	req, err := buildImagesUpstreamRequest(c, &config.UpstreamConfig{
		BaseURL: "https://api.example.com", ServiceType: "openai",
		ModelMapping:      map[string][]string{"*": {"sensenova-u1.5-lite"}},
		ImagesExtraParams: map[string]string{"watermark": "false", "n": "9"},
	}, "test-key", "/images/generations", body)
	require.NoError(t, err)
	require.Equal(t, "https://api.example.com/v1/images/generations", req.URL.String())
	require.Equal(t, "application/json", req.Header.Get("Content-Type"))
	got, err := io.ReadAll(req.Body)
	require.NoError(t, err)
	decoder := json.NewDecoder(bytes.NewReader(got))
	decoder.UseNumber()
	var payload map[string]interface{}
	require.NoError(t, decoder.Decode(&payload))
	require.Equal(t, "sensenova-u1.5-lite", payload["model"])
	require.Equal(t, json.Number("1"), payload["n"])
	require.Equal(t, false, payload["watermark"])
}

func TestImagesResponsesWildcardMappingRewritesToolModelAndIgnoresExtraParams(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil)
	c.Request.Header.Set("Content-Type", "application/json")
	body := []byte(`{"model":"gpt-image-2.5-sunburst","prompt":"画一只猫","quality":"high"}`)
	req, err := buildImagesUpstreamRequest(c, &config.UpstreamConfig{
		BaseURL: "https://upstream.example.com/v1", ServiceType: "responses", ResponsesModel: "gpt-4.1",
		ModelMapping:      map[string][]string{"*": {"sensenova-u1.5-lite"}},
		ImagesExtraParams: map[string]string{"watermark": "false"},
	}, "test-key", "/images/generations", body)
	require.NoError(t, err)
	encoded, err := io.ReadAll(req.Body)
	require.NoError(t, err)
	var payload map[string]interface{}
	require.NoError(t, json.Unmarshal(encoded, &payload))
	require.Equal(t, "gpt-4.1", payload["model"])
	tool := payload["tools"].([]interface{})[0].(map[string]interface{})
	require.Equal(t, "sensenova-u1.5-lite", tool["model"])
	require.NotContains(t, payload, "watermark")
	require.NotContains(t, tool, "watermark")
}
