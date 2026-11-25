package images

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"github.com/insistanan/api-proxy/internal/protocol/codec"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/insistanan/api-proxy/internal/infra/config"
	"github.com/insistanan/api-proxy/internal/protocol/tokenusage"
	"github.com/stretchr/testify/require"
)

// buildImagesForm 构造 /v1/images/edits 常见的表单请求：model + prompt + 图片文件。
func buildImagesForm(t *testing.T) ([]byte, string) {
	t.Helper()
	var buffer bytes.Buffer
	writer := multipart.NewWriter(&buffer)
	if err := writer.WriteField("model", "gpt-image-1"); err != nil {
		t.Fatalf("写入 model 字段失败: %v", err)
	}
	if err := writer.WriteField("prompt", "  给猫加一顶帽子  "); err != nil {
		t.Fatalf("写入 prompt 字段失败: %v", err)
	}
	if err := writer.WriteField("stream", "true"); err != nil {
		t.Fatalf("写入 stream 字段失败: %v", err)
	}
	filePart, err := writer.CreateFormFile("image", "cat.png")
	if err != nil {
		t.Fatalf("创建文件部件失败: %v", err)
	}
	if _, err := filePart.Write([]byte{0x89, 0x50, 0x4E, 0x47, 0x0D}); err != nil {
		t.Fatalf("写入文件内容失败: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("关闭表单失败: %v", err)
	}
	return buffer.Bytes(), writer.FormDataContentType()
}

func TestExtractImagesPromptsCoversJSONAndMultipart(t *testing.T) {
	jsonPrompts := extractImagesPrompts("application/json", []byte(`{"prompt":"画一只猫","model":"gpt-image-1"}`))
	if len(jsonPrompts) != 1 || jsonPrompts[0] != "画一只猫" {
		t.Fatalf("JSON 载荷提示词提取错误: %+v", jsonPrompts)
	}

	body, contentType := buildImagesForm(t)
	formPrompts := extractImagesPrompts(contentType, body)
	if len(formPrompts) != 1 || formPrompts[0] != "给猫加一顶帽子" {
		t.Fatalf("multipart 载荷提示词提取错误: %+v", formPrompts)
	}

	var empty bytes.Buffer
	writer := multipart.NewWriter(&empty)
	if err := writer.WriteField("model", "gpt-image-1"); err != nil {
		t.Fatalf("写入 model 字段失败: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("关闭表单失败: %v", err)
	}
	if prompts := extractImagesPrompts(writer.FormDataContentType(), empty.Bytes()); len(prompts) != 0 {
		t.Fatalf("无 prompt 字段时应返回空: %+v", prompts)
	}
}

func TestApplyImagesModelMappingRewritesMultipartAndSyncsBoundary(t *testing.T) {
	body, contentType := buildImagesForm(t)
	originalBoundary, _ := codec.MultipartBoundary(contentType)
	upstream := &config.UpstreamConfig{Name: "images-channel", DefaultModel: "dall-e-3"}

	mappedBody, mappedContentType, err := applyImagesModelMapping(contentType, body, upstream)
	if err != nil {
		t.Fatalf("模型映射失败: %v", err)
	}
	mappedBoundary, ok := codec.MultipartBoundary(mappedContentType)
	if !ok {
		t.Fatalf("映射后的 Content-Type 缺少 boundary: %s", mappedContentType)
	}
	if mappedBoundary == originalBoundary {
		t.Fatal("重新编码的表单必须带新 boundary，且由调用方同步到请求头")
	}

	parts, err := codec.ParseMultipartParts(mappedBody, mappedBoundary)
	if err != nil {
		t.Fatalf("解析映射后的表单失败: %v", err)
	}
	if len(parts) != 4 {
		t.Fatalf("映射后部件数量 = %d，期望 4", len(parts))
	}
	if string(parts[0].Content) != "dall-e-3" {
		t.Errorf("model 字段未映射: %q", parts[0].Content)
	}
	if string(parts[1].Content) != "  给猫加一顶帽子  " {
		t.Errorf("prompt 字段不应被改写: %q", parts[1].Content)
	}
	if parts[3].FileName != "cat.png" || !bytes.Equal(parts[3].Content, []byte{0x89, 0x50, 0x4E, 0x47, 0x0D}) {
		t.Errorf("文件部件在重建后被破坏: %+v", parts[3])
	}
}

func TestHandleImagesSuccessFlushesRequestedStreamWithoutSSEContentType(t *testing.T) {
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", strings.NewReader(`{"stream":true}`))
	response := &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader("data: {\"type\":\"image_generation.completed\",\"b64_json\":\"AQID\"}\n\n")),
	}

	_, err := handleImagesSuccess(ctx, response, &config.EnvConfig{}, time.Now(), "/images/generations", true, false)
	require.NoError(t, err)
	require.True(t, recorder.Flushed, "请求 stream=true 时即使上游 Content-Type 不是 SSE 也必须 flush")
	require.Contains(t, recorder.Body.String(), `"type":"image_generation.completed"`)
}

func TestHandleImagesSuccessWrapsJSONAsCompletedWhenForceNonStream(t *testing.T) {
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", strings.NewReader(`{"stream":true}`))
	response := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"created":123,"data":[{"b64_json":"AQID","revised_prompt":"猫"}],"output_format":"png"}`)),
	}

	_, err := handleImagesSuccess(ctx, response, &config.EnvConfig{MaxRequestBodySize: 1024 * 1024}, time.Now(), "/images/generations", true, true)
	require.NoError(t, err)
	require.Equal(t, "text/event-stream", recorder.Header().Get("Content-Type"))
	require.Equal(t, 1, strings.Count(recorder.Body.String(), "event: image_generation.completed"))
	require.NotContains(t, recorder.Body.String(), "partial_image")
	require.Contains(t, recorder.Body.String(), `"b64_json":"AQID"`)
	require.Contains(t, recorder.Body.String(), `"revised_prompt":"猫"`)
}

func TestHandleImagesSuccessForceNonStreamRejectsJSONWithoutImageData(t *testing.T) {
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", strings.NewReader(`{"stream":true}`))
	response := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"type":"partial_image"}`)),
	}

	_, err := handleImagesSuccess(ctx, response, &config.EnvConfig{MaxRequestBodySize: 1024 * 1024}, time.Now(), "/images/generations", true, true)
	require.ErrorContains(t, err, "未返回任何图片数据")
}

func TestHandleImagesSuccessForwardsUpstreamSSEEvenIfForceNonStream(t *testing.T) {
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", strings.NewReader(`{"stream":true}`))
	body := "event: image_generation.completed\ndata: {\"b64_json\":\"AQID\"}\n\n"
	response := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}

	_, err := handleImagesSuccess(ctx, response, &config.EnvConfig{}, time.Now(), "/images/generations", true, true)
	require.NoError(t, err)
	require.Equal(t, body, recorder.Body.String())
}

func TestHandleImagesSuccess_NormalizesUsageOnAllNativeResponsePaths(t *testing.T) {
	payload := `{"created":123,"data":[{"b64_json":"AQID"}],"usage":{"input_tokens":100,"output_tokens":20,"input_tokens_details":{"cached_tokens":70}}}`
	ssePayload := `{"type":"image_generation.completed","b64_json":"AQID","usage":{"input_tokens":100,"output_tokens":20,"input_tokens_details":{"cached_tokens":70}}}`
	for _, tc := range []struct {
		name, contentType, body string
		stream, force           bool
	}{
		{"JSON", "application/json", payload, false, false},
		{"强制非流式转 SSE", "application/json", payload, true, true},
		{"SSE", "text/event-stream", "event: image_generation.completed\ndata: " + ssePayload + "\n\n", true, false},
		{"无 Content-Type 的 SSE", "", "data: " + ssePayload + "\n\n", true, false},
		{"多行 SSE", "text/event-stream", "event: image_generation.completed\ndata: {\n" + "data: " + ssePayload[1:] + "\n\n", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil)
			c.Request = c.Request.WithContext(tokenusage.WithRecorder(c.Request.Context(), func(tokenusage.Fact) {}))
			resp := &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(tc.body))}
			resp.Header.Set("Content-Type", tc.contentType)
			usage, err := handleImagesSuccess(c, resp, &config.EnvConfig{MaxRequestBodySize: 1 << 20}, time.Now(), "/images/generations", tc.stream, tc.force)
			require.NoError(t, err)
			require.NotNil(t, usage)
			require.Equal(t, 30, usage.InputTokens)
			require.Equal(t, 70, usage.CacheReadInputTokens)
			require.Equal(t, 20, usage.OutputTokens)
			if !tc.force {
				require.Equal(t, tc.body, recorder.Body.String())
			}
		})
	}
}

func TestNativeUsageCaptureKeepsMetadataWithoutBufferingImage(t *testing.T) {
	payload := `{"data":[{"b64_json":"` + strings.Repeat("A", 2<<20) + `"}],"usage":{"input_tokens":100,"output_tokens":20}}`
	for _, sse := range []bool{false, true} {
		body := payload
		if sse {
			body = "data: " + body + "\n\n"
		}
		capture := &nativeUsageCapture{sse: sse, modeKnown: true}
		for len(body) > 0 {
			n := min(31, len(body))
			_, err := capture.Write([]byte(body[:n]))
			require.NoError(t, err)
			require.LessOrEqual(t, len(capture.buffer)+len(capture.event), 2*nativeMetadataLimit)
			body = body[n:]
		}
		usage := capture.result()
		require.NotNil(t, usage)
		require.Equal(t, 100, usage.InputTokens)
		require.Equal(t, 20, usage.OutputTokens)
	}
}

func TestNativeUsageCaptureCompressedPassthrough(t *testing.T) {
	for _, encoding := range []string{"gzip", "deflate"} {
		t.Run(encoding, func(t *testing.T) {
			var compressed bytes.Buffer
			var zip io.WriteCloser
			if encoding == "deflate" {
				zip = zlib.NewWriter(&compressed)
			} else {
				zip = gzip.NewWriter(&compressed)
			}
			_, err := io.WriteString(zip, `{"data":[{"b64_json":"`+strings.Repeat("A", 2<<20)+`"}],"usage":{"input_tokens":100,"output_tokens":20}}`)
			require.NoError(t, err)
			require.NoError(t, zip.Close())
			resp := &http.Response{Header: http.Header{"Content-Encoding": {encoding}}, Body: io.NopCloser(bytes.NewReader(compressed.Bytes()))}
			finish := captureNativeUsage(resp, 3<<20)
			body, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			usage := finish()
			require.NotNil(t, usage)
			require.Equal(t, 100, usage.InputTokens)
			require.Equal(t, compressed.Bytes(), body)
		})
	}
}
