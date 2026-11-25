package images

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/stretchr/testify/require"
	"insistanan/api-proxy/internal/infra/config"
	"insistanan/api-proxy/internal/protocol/types"
)

func TestResponsesImageJSON_WriteFailureIsNotSuccessful(t *testing.T) {
	for _, upstreamStream := range []bool{false, true} {
		c, recorder := imageStreamContext()
		writeErr := errors.New("JSON 下游写入失败")
		c.Writer = imageDeliveryWriter{ResponseWriter: c.Writer, err: writeErr}
		body := `{"status":"completed","output":[{"type":"image_generation_call","result":"AQID"}],"usage":{"input_tokens":10,"output_tokens":5}}`
		contentType := "application/json"
		if upstreamStream {
			body = "data: {\"type\":\"response.completed\",\"response\":" + body + "}\n\n"
			contentType = "text/event-stream"
		}
		usage, err := handleImagesResponsesSuccess(c, &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {contentType}}, Body: io.NopCloser(strings.NewReader(body))}, &config.EnvConfig{MaxRequestBodySize: 1 << 20}, "/images/generations", false)
		require.ErrorIs(t, err, writeErr)
		require.True(t, types.IsDownstreamWriteError(err))
		require.Equal(t, 5, usage.OutputTokens)
		require.Empty(t, recorder.Body.String())
	}
}

func TestResponsesImageCompletion_FinalBytesBeforeReadError(t *testing.T) {
	c, recorder := imageStreamContext()
	body := strings.TrimRight(completedImageStream("AQID"), "\n")
	reader := io.MultiReader(strings.NewReader(body), imageReadError{errors.New("connection reset")})
	_, err := convertResponsesImageStream(c, reader, make(http.Header), "/images/generations", true, 1<<20)
	require.NoError(t, err)
	require.Equal(t, 1, strings.Count(recorder.Body.String(), "event: image_generation.completed"))
	require.NotContains(t, recorder.Body.String(), "event: error")
}

func TestImagesInvalidUsageFailsBeforeDelivery(t *testing.T) {
	for _, native := range []bool{false, true} {
		c, recorder := imageStreamContext()
		body := `{"status":"completed","output":[{"type":"image_generation_call","result":"AQID"}],"usage":"invalid"}`
		if native {
			body = `{"data":[{"b64_json":"AQID"}],"usage":"invalid"}`
		}
		resp := &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
		var err error
		if native {
			_, err = writeImagesJSONAsCompletedStream(c, resp, &config.EnvConfig{MaxRequestBodySize: 1 << 20}, "/images/generations")
		} else {
			_, err = handleImagesResponsesSuccess(c, resp, &config.EnvConfig{MaxRequestBodySize: 1 << 20}, "/images/generations", true)
		}
		require.ErrorContains(t, err, "用量")
		require.False(t, c.Writer.Written())
		require.Empty(t, recorder.Body.String())
	}
}

func TestResponsesImageFailure_PreservesReportedUsageAndCause(t *testing.T) {
	for _, usageJSON := range []string{`{"input_tokens":10,"output_tokens":5}`, `"invalid"`} {
		c, recorder := imageStreamContext()
		body := "data: {\"type\":\"response.failed\",\"response\":{\"status\":\"failed\",\"error\":{\"message\":\"generation failed\"},\"usage\":" + usageJSON + "}}\n\n"
		usage, err := convertResponsesImageStream(c, strings.NewReader(body), make(http.Header), "/images/generations", true, 1<<20)
		require.ErrorContains(t, err, "generation failed")
		if usageJSON == `"invalid"` {
			require.ErrorContains(t, err, "解析 Responses 图片用量失败")
		} else {
			require.Equal(t, 5, usage.OutputTokens)
		}
		require.False(t, c.Writer.Written())
		require.Empty(t, recorder.Body.String())
	}
}
