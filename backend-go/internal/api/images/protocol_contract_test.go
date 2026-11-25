package images

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"

	"github.com/insistanan/api-proxy/internal/infra/config"
	"github.com/insistanan/api-proxy/internal/protocol/types"
	"github.com/insistanan/api-proxy/internal/proxycore"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestImagesSSEChecksFinalOverriddenFormat(t *testing.T) {
	for _, multipartBody := range []bool{false, true} {
		for _, finalFormat := range []string{"url", "b64_json"} {
			c, _ := imageStreamContext()
			original := "url"
			if finalFormat == "url" {
				original = "b64_json"
			}
			body := []byte(`{"model":"image","stream":true,"response_format":"` + original + `"}`)
			c.Request.Header.Set("Content-Type", "application/json")
			if multipartBody {
				var buffer bytes.Buffer
				writer := multipart.NewWriter(&buffer)
				for name, value := range map[string]string{"model": "image", "stream": "true", "response_format": original} {
					require.NoError(t, writer.WriteField(name, value))
				}
				require.NoError(t, writer.Close())
				body = buffer.Bytes()
				c.Request.Header.Set("Content-Type", writer.FormDataContentType())
			}
			req, err := buildImagesUpstreamRequest(c, &config.UpstreamConfig{ServiceType: "openai", BaseURL: "https://example.test", ImagesForceNonStream: true, ImagesExtraParams: map[string]string{"response_format": finalFormat}}, "key", "/images/generations", body)
			if finalFormat == "url" {
				require.True(t, types.IsUnsupportedProtocol(err), "%v", err)
				require.Nil(t, req)
			} else {
				require.NoError(t, err)
				actual, err := io.ReadAll(req.Body)
				require.NoError(t, err)
				require.NoError(t, req.Body.Close())
				require.Contains(t, string(actual), "b64_json")
			}
		}
	}
}

func TestResponsesImagesMergeFinalMetadataBeforeDelivery(t *testing.T) {
	stream := "data: " + `{"type":"response.output_item.done","item":{"id":"img","type":"image_generation_call","status":"completed","result":"AQID"}}` + "\n\n" +
		"data: " + `{"type":"response.completed","response":{"created_at":123,"status":"completed","output":[{"id":"img","type":"image_generation_call","status":"completed","revised_prompt":"final prompt","output_format":"webp","size":"1024x1024","quality":"high","usage":{"output_tokens":42}}]}}` + "\n\n"
	for _, streaming := range []bool{false, true} {
		c, recorder := imageStreamContext()
		_, err := convertResponsesImageStream(c, strings.NewReader(stream), make(http.Header), "/images/generations", streaming, 1024*1024)
		require.NoError(t, err)
		output := recorder.Body.String()
		require.Equal(t, 1, strings.Count(output, "AQID"))
		for _, value := range []string{"final prompt", "webp", "1024x1024", "high", "42", "123"} {
			require.Contains(t, output, value)
		}
		if streaming {
			require.Equal(t, 1, strings.Count(output, "event: image_generation.completed"))
		} else {
			require.Equal(t, "final prompt", gjson.Get(output, "data.0.revised_prompt").String())
		}
	}
}

func TestImagesCapabilitiesAreNotInvalidRequests(t *testing.T) {
	for _, body := range []string{`{"model":"image","prompt":"x","n":2}`, `{"model":"image","prompt":"x","response_format":"url"}`} {
		c, _ := imageStreamContext()
		c.Request.Header.Set("Content-Type", "application/json")
		request, err := buildImagesResponsesRequest(c, &config.UpstreamConfig{ServiceType: "responses", BaseURL: "https://example.test", ResponsesModel: "m"}, "fixture", "/images/generations", []byte(body))
		require.Nil(t, request)
		require.True(t, types.IsUnsupportedProtocol(err), "%v", err)
	}
	c, _ := imageStreamContext()
	c.Request.Header.Set("Content-Type", "application/json")
	_, err := buildImagesResponsesRequest(c, &config.UpstreamConfig{ResponsesModel: "m"}, "fixture", "/images/generations", []byte(`{"model":"image","prompt":"x","n":0}`))
	var invalid *proxycore.RequestValidationError
	require.ErrorAs(t, err, &invalid)
}

func TestImagesRejectURLStreamAndDuplicateMultipartModelBeforeSending(t *testing.T) {
	for _, multipartBody := range []bool{false, true} {
		c, _ := imageStreamContext()
		body := []byte(`{"model":"image","prompt":"x","stream":true,"response_format":"url"}`)
		c.Request.Header.Set("Content-Type", "application/json")
		if multipartBody {
			var buffer bytes.Buffer
			writer := multipart.NewWriter(&buffer)
			for name, value := range map[string]string{"model": "image", "prompt": "x", "stream": "true", "response_format": "url"} {
				require.NoError(t, writer.WriteField(name, value))
			}
			require.NoError(t, writer.Close())
			body = buffer.Bytes()
			c.Request.Header.Set("Content-Type", writer.FormDataContentType())
		}
		req, err := buildImagesUpstreamRequest(c, &config.UpstreamConfig{ServiceType: "openai", BaseURL: "https://example.test", ImagesForceNonStream: true}, "fixture", "/images/generations", body)
		require.Nil(t, req)
		require.True(t, types.IsUnsupportedProtocol(err), "%v", err)
	}
	var buffer bytes.Buffer
	writer := multipart.NewWriter(&buffer)
	require.NoError(t, writer.WriteField("model", "first"))
	require.NoError(t, writer.WriteField("model", "second"))
	require.NoError(t, writer.Close())
	c, _ := imageStreamContext()
	c.Request.Header.Set("Content-Type", writer.FormDataContentType())
	req, err := buildImagesUpstreamRequest(c, &config.UpstreamConfig{ServiceType: "openai", BaseURL: "https://example.test", DefaultModel: "mapped"}, "fixture", "/images/edits", buffer.Bytes())
	require.Nil(t, req)
	require.ErrorContains(t, err, "重复")
}
