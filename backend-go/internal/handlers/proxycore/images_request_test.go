package proxycore

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/insistanan/api-proxy/internal/config"
	"github.com/insistanan/api-proxy/internal/scheduler"
	"github.com/stretchr/testify/require"
)

type imagesTrackedBody struct {
	io.Reader
	closed bool
}

func (body *imagesTrackedBody) Close() error { body.closed = true; return nil }

func TestImagesEnvelopePrefixKeepsOriginalClose(t *testing.T) {
	body := &imagesTrackedBody{Reader: strings.NewReader(strings.Repeat("A", maxImagesEnvelopeSniffBytes+100))}
	resp := &http.Response{Body: body}
	_, complete, err := readImagesErrorEnvelopePrefix(resp)
	require.NoError(t, err)
	require.False(t, complete)
	require.NoError(t, resp.Body.Close())
	require.True(t, body.closed)
}

func TestImagesLargeJSONPassesSharedAttemptWithoutKeyRetry(t *testing.T) {
	original := `{"data":[{"b64_json":"` + strings.Repeat("A", maxEnvelopeSniffBytes+4096) + `"}]}`
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, original)
	}))
	defer server.Close()
	env := newHealthAttemptEnv(t)
	imageScheduler := scheduler.NewChannelScheduler(env.cfgManager, nil, nil, nil, nil, env.metricsManager, nil, nil, nil, nil)
	t.Cleanup(imageScheduler.Stop)
	attempt := newHealthAttempt(env, server.URL, []string{"key-a", "key-b"})
	attempt.ChannelScheduler = imageScheduler
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = attempt.Context.Request
	attempt.Context = c
	attempt.Kind = scheduler.ChannelKindImages
	attempt.APIType = "Images" // 实际调用使用大写名称，不能用字符串 images 判断协议。
	result := attempt.TryWithModelMappingFailover()
	require.True(t, result.Handled)
	require.NoError(t, result.LastError)
	require.Equal(t, channelHealthSuccess, result.Health)
	require.Equal(t, int32(1), requests.Load())
	require.Equal(t, original, recorder.Body.String())
}

func TestLocalRequestBuildErrorsDoNotRetryKeysOrAffectHealth(t *testing.T) {
	for _, example := range []struct {
		name   string
		err    error
		status int
	}{
		{"请求参数", &RequestValidationError{Err: errors.New("不支持 n=2")}, http.StatusBadRequest},
		{"渠道配置", &config.ConfigError{Message: "Responses 主模型缺失"}, http.StatusInternalServerError},
	} {
		t.Run(example.name, func(t *testing.T) {
			attempt := newHealthAttempt(newHealthAttemptEnv(t), "https://unused.example.com", []string{"key-a", "key-b"})
			var builds int
			body := &imagesTrackedBody{Reader: strings.NewReader("request")}
			attempt.BuildRequest = func(_ *gin.Context, _ *config.UpstreamConfig, _ string) (*http.Request, error) {
				builds++
				return &http.Request{Body: body}, example.err
			}
			result := attempt.TryWithModelMappingFailover()
			require.True(t, result.Handled)
			require.ErrorIs(t, result.LastError, example.err)
			require.Equal(t, channelHealthNeutral, result.Health)
			require.Equal(t, example.status, attempt.Context.Writer.Status())
			require.Equal(t, 1, builds)
			require.True(t, body.closed)
		})
	}
}
