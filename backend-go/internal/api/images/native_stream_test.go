package images

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/insistanan/api-proxy/internal/infra/config"
	"github.com/insistanan/api-proxy/internal/protocol/types"
	"github.com/stretchr/testify/require"
)

type imageReadError struct{ err error }

func (r imageReadError) Read([]byte) (int, error) { return 0, r.err }

type imageDeliveryWriter struct {
	gin.ResponseWriter
	failOn, cancelOn string
	err              error
	cancel           context.CancelFunc
}

func (w imageDeliveryWriter) Write(body []byte) (int, error) {
	if w.err != nil && strings.Contains(string(body), w.failOn) {
		return 0, w.err
	}
	n, err := w.ResponseWriter.Write(body)
	if w.cancel != nil && strings.Contains(string(body), w.cancelOn) {
		w.cancel()
	}
	return n, err
}

func TestNativeImageStream_CompletionAndFailure(t *testing.T) {
	preview := "data: {\"type\":\"image_generation.partial_image\",\"b64_json\":\"AQID\"}\n\n"
	terminal := "data: {\"type\":\"image_generation.completed\",\"b64_json\":\"AQID\",\"usage\":{\"input_tokens\":10,\"output_tokens\":5}}"
	for _, tc := range []struct {
		name, tail string
		success    bool
		count      int
	}{
		{"正常完成", terminal + "\n\n", true, 1},
		{"缺少末尾换行", terminal, true, 1},
		{"完成后事件不再交付", terminal + "\n\ndata: {\"text\":\"late\"}\n\n", true, 1},
		{"只有预览", "", false, 1},
		{"失败后不能完成", "data: {\"error\":{\"message\":\"boom\"}}\n\n" + terminal, false, 1},
		{"非法数据", "data: {broken}\n\n", false, 1},
		{"多个图片未齐", terminal + "\n\n", false, 2},
		{"多个图片完成", terminal + "\n\n" + terminal + "\n\n", true, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, readErr := range []error{io.EOF, errors.New("upstream reset")} {
				c, recorder := imageStreamContext()
				c.Set(nativeImageCountKey, tc.count)
				resp := &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(io.MultiReader(strings.NewReader(preview+tc.tail), imageReadError{readErr}))}
				usage, err := handleImagesSuccess(c, resp, &config.EnvConfig{}, time.Now(), "/images/generations", true, false)
				if tc.success {
					require.NoError(t, err)
					require.Equal(t, 5, usage.OutputTokens)
					require.NotContains(t, recorder.Body.String(), "event: error")
				} else {
					require.Error(t, err)
					require.Equal(t, 1, strings.Count(recorder.Body.String(), "event: error"))
				}
				require.NotContains(t, recorder.Body.String(), "late")
			}
		})
	}
}

func TestNativeImageStream_LargeEventPreservesUnknownFields(t *testing.T) {
	c, recorder := imageStreamContext()
	body := "id: image-1\nevent: image_edit.completed\ndata: {\"b64_json\":\"" + strings.Repeat("A", 2<<20) + "\",\"extension\":{\"extra\":true}}\n\n"
	_, err := handleImagesSuccess(c, &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(body))}, &config.EnvConfig{}, time.Now(), "/images/edits", true, false)
	require.NoError(t, err)
	require.Equal(t, body, recorder.Body.String())
}

func TestNativeImageStream_WriteFailureAndCancelAfterTerminal(t *testing.T) {
	for _, failure := range []bool{false, true} {
		c, recorder := imageStreamContext()
		ctx, cancel := context.WithCancel(c.Request.Context())
		defer cancel()
		c.Request = c.Request.WithContext(ctx)
		writer := imageDeliveryWriter{ResponseWriter: c.Writer, cancelOn: "completed", cancel: cancel}
		writeErr := errors.New("下游写入失败")
		if failure {
			writer.err = writeErr
		}
		c.Writer = writer
		body := "data: {\"type\":\"image_generation.completed\",\"b64_json\":\"AQID\"}\n\n"
		_, err := handleImagesSuccess(c, &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, &config.EnvConfig{}, time.Now(), "/images/generations", true, false)
		if failure {
			require.ErrorIs(t, err, writeErr)
			require.True(t, types.IsDownstreamWriteError(err))
			require.NoError(t, ctx.Err())
			require.Empty(t, recorder.Body.String())
		} else {
			require.NoError(t, err)
			require.ErrorIs(t, ctx.Err(), context.Canceled)
		}
	}
}

func TestImageResponse_CancellationClosesBlockedDecode(t *testing.T) {
	for _, upstream := range []string{"native", "responses"} {
		for _, encoding := range []string{"", "gzip"} {
			t.Run(upstream+"/"+encoding, func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					c, _ := imageStreamContext()
					ctx, cancel := context.WithCancel(c.Request.Context())
					defer cancel()
					c.Request = c.Request.WithContext(ctx)
					reader, writer := io.Pipe()
					defer writer.Close()
					resp := &http.Response{StatusCode: 200, Header: http.Header{"Content-Encoding": {encoding}}, Body: reader}
					done := make(chan error, 1)
					go func() {
						var err error
						if upstream == "native" {
							_, err = writeImagesJSONAsCompletedStream(c, resp, &config.EnvConfig{MaxRequestBodySize: 1 << 20}, "/images/generations")
						} else {
							_, err = handleImagesResponsesSuccess(c, resp, &config.EnvConfig{MaxRequestBodySize: 1 << 20}, "/images/generations", true)
						}
						done <- err
					}()
					synctest.Wait()
					cancel()
					synctest.Wait()
					require.ErrorIs(t, <-done, context.Canceled)
				})
			})
		}
	}
}

func TestNativeImageStream_InvalidFirstEventDoesNotCommit(t *testing.T) {
	for _, body := range []string{
		": heartbeat\n\ndata: {\"error\":{\"message\":\"boom\"}}\n\n",
		"data: {\"type\":\"image_generation.completed\"}\n\n",
		`{"type":"partial_image"}`,
	} {
		c, recorder := imageStreamContext()
		_, err := handleImagesSuccess(c, &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, &config.EnvConfig{}, time.Now(), "/images/generations", true, false)
		require.Error(t, err)
		require.False(t, c.Writer.Written())
		require.Empty(t, recorder.Body.String())
	}
}

func TestNativeImageStream_CompressedEventUsesDecodedHeaders(t *testing.T) {
	var compressed bytes.Buffer
	zip := gzip.NewWriter(&compressed)
	body := "event: image_generation.completed\r\ndata: {\"b64_json\":\"AQID\",\"extension\":true}\r\n\r\n"
	_, err := io.WriteString(zip, body)
	require.NoError(t, err)
	require.NoError(t, zip.Close())
	c, recorder := imageStreamContext()
	resp := &http.Response{StatusCode: 200, Header: http.Header{"Content-Encoding": {"gzip"}, "Content-Length": {"999"}}, Body: io.NopCloser(bytes.NewReader(compressed.Bytes()))}
	_, err = handleImagesSuccess(c, resp, &config.EnvConfig{}, time.Now(), "/images/generations", true, false)
	require.NoError(t, err)
	require.Equal(t, body, recorder.Body.String())
	require.Empty(t, recorder.Header().Get("Content-Encoding"))
	require.Empty(t, recorder.Header().Get("Content-Length"))
}

func TestNativeImagesCount_UsesFinalRequest(t *testing.T) {
	for _, multipartRequest := range []bool{false, true} {
		c, _ := imageStreamContext()
		body := []byte(`{"model":"gpt-image-1","prompt":"猫","n":2}`)
		contentType := "application/json"
		if multipartRequest {
			var formBody bytes.Buffer
			form := multipart.NewWriter(&formBody)
			require.NoError(t, form.WriteField("model", "gpt-image-1"))
			require.NoError(t, form.WriteField("n", "2"))
			require.NoError(t, form.Close())
			body, contentType = formBody.Bytes(), form.FormDataContentType()
		}
		c.Request.Header.Set("Content-Type", contentType)
		upstream := &config.UpstreamConfig{BaseURL: "https://example.invalid", ServiceType: "openai", ImagesExtraParams: map[string]string{"n": "3"}}
		req, err := buildImagesUpstreamRequest(c, upstream, "test", "/images/generations", body)
		require.NoError(t, err)
		defer req.Body.Close()
		require.Equal(t, 3, c.GetInt(nativeImageCountKey))
	}
}
