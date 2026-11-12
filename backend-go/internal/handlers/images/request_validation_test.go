package images

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/insistanan/api-proxy/internal/infra/config"
	"github.com/insistanan/api-proxy/internal/infra/httpclient"
	"github.com/stretchr/testify/require"
)

func TestImagesNewOutputFormatsAndConstraints(t *testing.T) {
	for _, example := range []struct {
		name      string
		body      string
		responses bool
		failure   string
	}{
		{"原生 GIF", `{"output_format":"gif","frame_count":9,"frame_duration_ms":200,"complexity":"medium"}`, false, ""},
		{"原生帧时长不套用 Responses 上限", `{"output_format":"gif","frame_duration_ms":3000}`, false, ""},
		{"Responses GIF 生命周期流", `{"output_format":"gif","stream":true,"partial_images":0}`, true, ""},
		{"SVG 对象", `{"output_format":"svg","svg":{"model":"gpt-5.6","reasoning":{"effort":"high"}},"n":1}`, false, ""},
		{"原生未知参数保持", `{"output_format":"custom-format","upstream_option":true}`, false, ""},
		{"帧数小数", `{"output_format":"gif","frame_count":2.5}`, true, "整数"},
		{"帧数越界", `{"output_format":"gif","frame_count":17}`, false, "2–16"},
		{"非 GIF 帧参数", `{"output_format":"png","frame_count":9}`, false, "仅适用于"},
		{"Responses 帧时长越界", `{"output_format":"gif","frame_duration_ms":2001}`, true, "20–2000"},
		{"GIF 原生流", `{"output_format":"gif","stream":true}`, false, "不支持 stream"},
		{"GIF 预览", `{"output_format":"gif","partial_images":1}`, true, "预览"},
		{"SVG 压缩", `{"output_format":"svg","output_compression":100}`, true, "output_compression"},
		{"GIF 蒙版", `{"output_format":"gif","mask":"mask.png"}`, false, "蒙版"},
		{"GIF Responses 蒙版", `{"output_format":"gif","input_image_mask":{"file_id":"mask"}}`, true, "蒙版"},
		{"SVG 多图", `{"output_format":"svg","n":2}`, false, "n=1"},
		{"SVG 错误对象", `{"output_format":"svg","svg":"wrong"}`, true, "对象"},
		{"GIF 提示词改写", `{"output_format":"gif","prompt_rewriter":true}`, false, "prompt_rewriter"},
	} {
		t.Run(example.name, func(t *testing.T) {
			payload, err := readImagesPayload("application/json", []byte(example.body))
			require.NoError(t, err)
			err = validateImagesOutputParameters(payload, example.responses)
			if example.failure == "" {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, example.failure)
			}
		})
	}
}

func TestImagesResponsesNewFieldsAndSingleImageSemantics(t *testing.T) {
	payload, err := readImagesPayload("application/json", []byte(`{"model":"gpt-image-2.6-sunburst","prompt":"生成 GIF 动图","output_format":"gif","frame_count":9,"frame_duration_ms":200,"complexity":"high","n":1,"stream":true,"safety_identifier":"test-hash"}`))
	require.NoError(t, err)
	up := &config.UpstreamConfig{ResponsesModel: "gpt-5.6"}
	converted, err := convertImagesRequestToResponses(payload, up, false)
	require.NoError(t, err)
	tool := converted["tools"].([]interface{})[0].(map[string]interface{})
	require.NotContains(t, tool, "n")
	require.NotContains(t, tool, "safety_identifier")
	require.Equal(t, "test-hash", converted["safety_identifier"])
	require.Equal(t, true, converted["stream"])
	require.Equal(t, json.Number("9"), tool["frame_count"])
	require.Equal(t, "high", tool["complexity"])
	payload["n"] = json.Number("2")
	_, err = convertImagesRequestToResponses(payload, up, false)
	require.ErrorContains(t, err, "多图生成请使用 Images API")
}

func TestImagesMultipartNewParametersAndEffectiveOverrides(t *testing.T) {
	for _, format := range []string{"gif", "svg"} {
		t.Run(format, func(t *testing.T) {
			var body bytes.Buffer
			form := multipart.NewWriter(&body)
			require.NoError(t, form.WriteField("model", "gpt-image-2.6-flare"))
			require.NoError(t, form.WriteField("prompt", "修改图片"))
			require.NoError(t, form.WriteField("output_format", format))
			require.NoError(t, form.WriteField("stream", "true"))
			if format == "gif" {
				require.NoError(t, form.WriteField("frame_count", "9"))
				require.NoError(t, form.WriteField("frame_duration_ms", "200"))
			} else {
				require.NoError(t, form.WriteField("svg", `{"model":"gpt-5.6","reasoning":{"effort":"high"}}`))
			}
			part, err := form.CreateFormFile("image", "image.png")
			require.NoError(t, err)
			_, err = part.Write([]byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A})
			require.NoError(t, err)
			require.NoError(t, form.Close())
			payload, err := readImagesPayload(form.FormDataContentType(), body.Bytes())
			require.NoError(t, err)
			_, err = convertImagesRequestToResponses(payload, &config.UpstreamConfig{ResponsesModel: "gpt-5.6"}, true)
			require.NoError(t, err)
			require.ErrorContains(t, validateNativeImagesMultipartOutput(form.FormDataContentType(), body.Bytes()), "不支持 stream")
			mapped, contentType, err := applyImagesModelMapping(form.FormDataContentType(), body.Bytes(), &config.UpstreamConfig{ImagesForceNonStream: true})
			require.NoError(t, err)
			require.NoError(t, validateNativeImagesMultipartOutput(contentType, mapped))
		})
	}
	// JSON 也应以覆盖后的 output_format 与 stream 判断，不保留客户端冲突值。
	body, _, err := applyImagesModelMapping("application/json", []byte(`{"prompt":"猫","output_format":"png","stream":true,"partial_images":1}`), &config.UpstreamConfig{
		ImagesForceNonStream: true, ImagesExtraParams: map[string]string{"output_format": "gif", "frame_count": "9"},
	})
	require.NoError(t, err)
	require.Contains(t, string(body), `"output_format":"gif"`)
}

func TestNativeImagesGIFRejectsMultipartMask(t *testing.T) {
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	require.NoError(t, form.WriteField("output_format", "gif"))
	part, err := form.CreateFormFile("mask", "mask.png")
	require.NoError(t, err)
	_, err = io.WriteString(part, "mask-data")
	require.NoError(t, err)
	require.NoError(t, form.Close())
	require.ErrorContains(t, validateNativeImagesMultipartOutput(form.FormDataContentType(), body.Bytes()), "蒙版")
}

func TestChannelGIFConfigReachesBothUpstreamProtocols(t *testing.T) {
	for _, protocol := range []string{"openai", "responses"} {
		t.Run(protocol, func(t *testing.T) {
			observed := make(chan map[string]interface{}, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var payload map[string]interface{}
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}
				observed <- payload
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"data":[{"b64_json":"AQID"}],"output_format":"gif"}`)
			}))
			defer server.Close()
			c, _ := imageStreamContext()
			c.Request.Header.Set("Content-Type", "application/json")
			up := &config.UpstreamConfig{
				BaseURL: server.URL, ServiceType: protocol, ResponsesModel: "gpt-main",
				ImagesOutputFormat: "gif", ImagesFrameCount: 9, ImagesFrameDurationMS: 200,
				ImagesExtraParams: map[string]string{"output_format": "png", "watermark": "false"},
			}
			// 客户端仍发普通静态图片参数，由渠道设置切换为 GIF 并清除不支持的控制字段。
			req, err := buildImagesUpstreamRequest(c, up, "fixture", "/images/generations", []byte(`{"model":"gpt-image-2.6-sunburst","prompt":"猫咪挥手","stream":true,"output_format":"png","output_compression":100,"partial_images":2,"n":1}`))
			require.NoError(t, err)
			defer finishResponsesLifecycle(c)
			resp, err := httpclient.Send(req, httpclient.SendOptions{Timeout: imagesRequestTimeout})
			require.NoError(t, err)
			require.NoError(t, resp.Body.Close())
			payload := <-observed
			var imageParams map[string]interface{}
			if protocol == "responses" {
				require.Equal(t, true, payload["stream"])
				imageParams = payload["tools"].([]interface{})[0].(map[string]interface{})
				require.NotContains(t, imageParams, "watermark", "旧自定义参数仍不自动激活")
			} else {
				require.Equal(t, false, payload["stream"])
				imageParams = payload
			}
			require.Equal(t, "gif", imageParams["output_format"])
			require.Equal(t, float64(9), imageParams["frame_count"])
			require.Equal(t, float64(200), imageParams["frame_duration_ms"])
			require.NotContains(t, imageParams, "partial_images")
			require.NotContains(t, imageParams, "output_compression")
		})
	}
}

func TestChannelGIFConfigOverridesMultipartAndStaticModeClearsFrames(t *testing.T) {
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	for key, value := range map[string]string{"model": "image-model", "prompt": "编辑图片", "output_format": "png", "stream": "true", "partial_images": "2", "output_compression": "100"} {
		require.NoError(t, form.WriteField(key, value))
	}
	part, err := form.CreateFormFile("image", "image.png")
	require.NoError(t, err)
	image := []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}
	_, err = part.Write(image)
	require.NoError(t, err)
	require.NoError(t, form.Close())
	up := &config.UpstreamConfig{ServiceType: "openai", ImagesOutputFormat: "gif", ImagesFrameCount: 9, ImagesFrameDurationMS: 200}
	mapped, contentType, err := applyImagesModelMapping(form.FormDataContentType(), body.Bytes(), up)
	require.NoError(t, err)
	payload, err := readImagesPayload(contentType, mapped)
	require.NoError(t, err)
	require.Equal(t, "gif", payload["output_format"])
	require.Equal(t, false, payload["stream"])
	require.Equal(t, json.Number("9"), payload["frame_count"])
	require.NotContains(t, payload, "output_compression")
	require.NotContains(t, payload, "partial_images")
	require.NoError(t, validateNativeImagesMultipartOutput(contentType, mapped))
	up.ImagesOutputFormat, up.ImagesFrameCount, up.ImagesFrameDurationMS = "png", 0, 0
	mapped, _, err = applyImagesModelMapping("application/json", []byte(`{"output_format":"gif","frame_count":9,"frame_duration_ms":200,"svg":{"model":"unused"}}`), up)
	require.NoError(t, err)
	static, err := readImagesPayload("application/json", mapped)
	require.NoError(t, err)
	require.Equal(t, "png", static["output_format"])
	require.NotContains(t, static, "frame_count")
	require.NotContains(t, static, "frame_duration_ms")
	require.NotContains(t, static, "svg")
}
