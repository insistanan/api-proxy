package images

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/BenedictKing/api-proxy/internal/config"
	"github.com/BenedictKing/api-proxy/internal/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func imageStreamContext() (*gin.Context, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil)
	return c, recorder
}

func completedImageStream(base64 string) string {
	item := fmt.Sprintf(`{"id":"image-1","type":"image_generation_call","status":"completed","result":"%s","output_format":"png"}`, base64)
	return "event: response.output_item.done\r\ndata:" + `{"type":"response.output_item.done","item":` + item + "}\r\n\r\n" +
		"event: response.completed\ndata: " + `{"type":"response.completed","response":{"status":"completed","created_at":123,"output":[` + item + "]}}\n\n"
}

func TestResponsesStreamLargeImageIsPreservedAndDeduplicated(t *testing.T) {
	// 超过 Scanner 默认 64KB 上限，也超过普通文本事件大小。
	base64 := strings.Repeat("AQID", 256*1024)
	c, recorder := imageStreamContext()
	_, err := convertResponsesImageStream(c, strings.NewReader(completedImageStream(base64)), make(http.Header), "/images/generations", true, 4*1024*1024)
	require.NoError(t, err)
	require.Equal(t, "text/event-stream", recorder.Header().Get("Content-Type"))
	require.Equal(t, 1, strings.Count(recorder.Body.String(), "event: image_generation.completed"))
	require.NotContains(t, recorder.Body.String(), "response.completed")
	require.NotContains(t, recorder.Body.String(), "image_generation_call")
	raw := strings.TrimSpace(strings.SplitN(recorder.Body.String(), "data: ", 2)[1])
	var payload map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(raw), &payload))
	require.Equal(t, base64, payload["b64_json"])
}

func TestResponsesStreamConvertsPreviewAndReportsTruncation(t *testing.T) {
	c, recorder := imageStreamContext()
	stream := "event: response.image_generation_call.partial_image\ndata: " +
		`{"type":"response.image_generation_call.partial_image","partial_image_b64":"AQID","partial_image_index":2}` + "\n\n"
	_, err := convertResponsesImageStream(c, strings.NewReader(stream), make(http.Header), "/images/edits", true, 1024*1024)
	require.ErrorContains(t, err, "未收到 response.completed")
	require.True(t, recorder.Flushed)
	require.Contains(t, recorder.Body.String(), "event: image_edit.partial_image")
	require.Contains(t, recorder.Body.String(), `"partial_image_index":2`)
	require.Contains(t, recorder.Body.String(), "event: error")
	require.NotContains(t, recorder.Body.String(), "partial_image_b64")
}

func TestResponsesStreamFailuresAreNotSuccessfulImages(t *testing.T) {
	for _, stream := range []string{
		"event: response.failed\ndata: " + `{"type":"response.failed","response":{"error":{"message":"上游生图失败"}}}` + "\n\n",
		"event: response.completed\ndata: " + `{"type":"response.completed","response":{"status":"completed","output":[{"type":"message"}]}}` + "\n\n",
		"event: response.completed\ndata: {broken}\n\n",
	} {
		c, recorder := imageStreamContext()
		_, err := convertResponsesImageStream(c, strings.NewReader(stream), make(http.Header), "/images/generations", true, 1024*1024)
		require.Error(t, err)
		require.NotContains(t, recorder.Body.String(), "image_generation.completed")
	}
}

func TestResponsesStreamCompletedOnlyAndMissingIDs(t *testing.T) {
	for _, stream := range []string{
		"event: response.completed\ndata: " + `{"type":"response.completed","response":{"status":"completed","output":[{"type":"image_generation_call","result":"AQID"}]}}` + "\n\n",
		strings.ReplaceAll(completedImageStream("AQID"), `"id":"image-1",`, ""),
	} {
		c, recorder := imageStreamContext()
		_, err := convertResponsesImageStream(c, strings.NewReader(stream), make(http.Header), "/images/generations", true, 1024*1024)
		require.NoError(t, err)
		require.Equal(t, 1, strings.Count(recorder.Body.String(), "event: image_generation.completed"))
	}
}

func TestResponsesStreamCanReturnNormalImagesJSON(t *testing.T) {
	c, recorder := imageStreamContext()
	_, err := convertResponsesImageStream(c, strings.NewReader(completedImageStream("AQID")), make(http.Header), "/images/generations", false, 1024*1024)
	require.NoError(t, err)
	var payload imagesResponse
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &payload))
	require.Equal(t, int64(123), payload.Created)
	require.Len(t, payload.Data, 1)
	require.Equal(t, "AQID", payload.Data[0].Base64)
	require.NotContains(t, recorder.Body.String(), "event:")
}

func TestResponsesStreamClientCloseAfterImageKeepsUsage(t *testing.T) {
	c, recorder := imageStreamContext()
	requestContext, cancel := context.WithCancel(c.Request.Context())
	c.Request = c.Request.WithContext(requestContext)
	base64 := strings.Repeat("AQID", 64*1024)
	item := fmt.Sprintf(`{"id":"image-1","type":"image_generation_call","status":"completed","result":"%s"}`, base64)
	reader, writer := io.Pipe()
	writeErr := make(chan error, 1)
	go func() {
		_, err := fmt.Fprintf(writer, "event: response.output_item.done\ndata: {\"type\":\"response.output_item.done\",\"item\":%s}\n\n", item)
		writeErr <- err
	}()
	result := make(chan error, 1)
	var usage *types.Usage
	go func() {
		parsed, err := convertResponsesImageStream(c, reader, make(http.Header), "/images/generations", true, 4*1024*1024)
		usage = parsed
		result <- err
	}()
	select {
	case err := <-writeErr:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("图片事件没有写出")
	}
	require.Eventually(t, func() bool {
		return strings.Contains(recorder.Body.String(), "event: image_generation.completed")
	}, 2*time.Second, 10*time.Millisecond)
	cancel()
	completed := fmt.Sprintf("event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"created_at\":123,\"usage\":{\"input_tokens\":9233,\"output_tokens\":702,\"input_tokens_details\":{\"cached_tokens\":4224}},\"output\":[%s]}}\n\n", item)
	_, err := io.WriteString(writer, completed)
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	select {
	case err = <-result:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("客户端断开后没有完成上游读取")
	}
	require.NotNil(t, usage)
	require.Equal(t, 9233, usage.InputTokens)
	require.Equal(t, 702, usage.OutputTokens)
	require.Equal(t, 4224, usage.CacheReadInputTokens)
}

func TestResponsesStreamReaderEnforcesBudgetAndCancellation(t *testing.T) {
	_, _, err := readResponsesImageEvent(context.Background(), bufio.NewReaderSize(strings.NewReader("data: "+strings.Repeat("A", 128*1024)+"\n\n"), 32*1024), 64*1024)
	require.ErrorContains(t, err, "超过大小限制")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err = readResponsesImageEvent(ctx, bufio.NewReader(strings.NewReader("data: {}\n\n")), 1024)
	require.ErrorIs(t, err, context.Canceled)

	// 无关的巨大文本 delta 直接跳过，其正文不占图片事件预算。
	reader := bufio.NewReaderSize(strings.NewReader("event: response.output_text.delta\ndata: "+strings.Repeat("A", 128*1024)+"\n\n"), 32*1024)
	name, raw, err := readResponsesImageEvent(context.Background(), reader, 1024)
	require.NoError(t, err)
	require.Equal(t, "response.output_text.delta", name)
	require.Empty(t, raw)
}

func TestResponsesStreamReaderHandlesMultilineDataAndEOF(t *testing.T) {
	name, raw, err := readResponsesImageEvent(context.Background(), bufio.NewReader(strings.NewReader("event: error\r\ndata: {\r\ndata: \"message\":\"failed\"}")), 1024)
	require.ErrorIs(t, err, io.EOF)
	require.Equal(t, "error", name)
	require.JSONEq(t, `{"message":"failed"}`, raw)
}

func TestImagesResponsesSuccessHandlesCompressedJSON(t *testing.T) {
	var body bytes.Buffer
	compressed := gzip.NewWriter(&body)
	_, err := compressed.Write([]byte(`{"status":"completed","created_at":123,"output":[{"type":"image_generation_call","result":"AQID","output_format":"png"}]}`))
	require.NoError(t, err)
	require.NoError(t, compressed.Close())
	c, recorder := imageStreamContext()
	_, err = handleImagesResponsesSuccess(c, &http.Response{
		Header: http.Header{"Content-Type": {"application/json"}, "Content-Encoding": {"gzip"}, "Content-Length": {"999"}},
		Body:   io.NopCloser(bytes.NewReader(body.Bytes())),
	}, &config.EnvConfig{MaxRequestBodySize: 1024 * 1024}, "/images/generations", false)
	require.NoError(t, err)
	require.Empty(t, recorder.Header().Get("Content-Encoding"))
	require.Empty(t, recorder.Header().Get("Content-Length"))
	require.Contains(t, recorder.Body.String(), `"b64_json":"AQID"`)
	require.NotContains(t, recorder.Body.String(), `"output"`)
}

func TestImagesResponsesSuccessDetectsTransportWithIncorrectHeaders(t *testing.T) {
	for _, example := range []struct {
		name        string
		contentType string
		body        string
	}{
		{"SSE 缺少类型", "application/json", completedImageStream("AQID")},
		{"JSON 被标成 SSE", "text/event-stream", ` {"status":"completed","output":[{"type":"image_generation_call","result":"AQID"}]}`},
	} {
		t.Run(example.name, func(t *testing.T) {
			c, recorder := imageStreamContext()
			_, err := handleImagesResponsesSuccess(c, &http.Response{
				Header: http.Header{"Content-Type": {example.contentType}},
				Body:   io.NopCloser(strings.NewReader(example.body)),
			}, &config.EnvConfig{MaxRequestBodySize: 1024 * 1024}, "/images/generations", true)
			require.NoError(t, err)
			require.Contains(t, recorder.Body.String(), "event: image_generation.completed")
			require.Contains(t, recorder.Body.String(), `"b64_json":"AQID"`)
		})
	}
}

type discardImagesResponseWriter struct{ headers http.Header }

func (writer *discardImagesResponseWriter) Header() http.Header { return writer.headers }
func (*discardImagesResponseWriter) WriteHeader(int)            {}
func (*discardImagesResponseWriter) Write(body []byte) (int, error) {
	return len(body), nil
}
func (*discardImagesResponseWriter) Flush() {}

// BenchmarkResponsesImageStream 不使用缓存全部输出的 ResponseRecorder，
// 以便分开观察转换本身的分配成本；数据含重复完成事件，覆盖大图片去重路径。
func BenchmarkResponsesImageStream(b *testing.B) {
	gin.SetMode(gin.TestMode)
	for _, size := range []int{1024 * 1024, 8 * 1024 * 1024} {
		b.Run(fmt.Sprintf("%dMiB", size/(1024*1024)), func(b *testing.B) {
			stream := completedImageStream(strings.Repeat("A", size))
			b.SetBytes(int64(len(stream)))
			b.ReportAllocs()
			b.ResetTimer()
			for index := 0; index < b.N; index++ {
				writer := &discardImagesResponseWriter{headers: make(http.Header)}
				c, _ := gin.CreateTestContext(writer)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil)
				if _, err := convertResponsesImageStream(c, strings.NewReader(stream), make(http.Header), "/images/generations", true, 16*1024*1024); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
