package httpclient

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func TestShouldForceIdentityEncoding(t *testing.T) {
	for _, tc := range []struct {
		name, path, accept string
		stream, expected   bool
	}{
		{"流式请求", "/v1/chat/completions", "", true, true},
		{"Gemini 流式端点", "/v1beta/models/model:streamGenerateContent?alt=sse", "", false, true},
		{"SSE 请求头", "/v1/responses", "text/event-stream", false, true},
		{"普通请求", "/v1/chat/completions", "application/json", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodPost, "https://example.com"+tc.path, nil)
			require.NoError(t, err)
			req.Header.Set("Accept", tc.accept)
			require.Equal(t, tc.expected, ShouldForceIdentityEncoding(req, tc.stream))
		})
	}
}

// 发送失败由编排层决定是否重试；传输层不能根据错误文本重放生成请求。
func TestSend_TransportFailureIsReturnedWithoutReplay(t *testing.T) {
	for _, message := range []string{"net/http: TLS handshake timeout", "connection reset"} {
		t.Run(message, func(t *testing.T) {
			opts := SendOptions{Timeout: 137 * time.Millisecond, ResponseHeaderTimeout: 139 * time.Millisecond}
			client, err := GetManager().GetClient(ClientOptions{Timeout: opts.Timeout, ResponseHeaderTimeout: opts.ResponseHeaderTimeout})
			require.NoError(t, err)
			original := client.Transport
			t.Cleanup(func() { client.Transport = original })
			attempts, replays := 0, 0
			failure := errors.New(message)
			client.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
				attempts++
				_ = request.Body.Close()
				return nil, failure
			})
			request, err := http.NewRequest(http.MethodPost, "https://upstream.example.com/v1/responses", strings.NewReader("image request"))
			require.NoError(t, err)
			request.GetBody = func() (io.ReadCloser, error) {
				replays++
				return io.NopCloser(strings.NewReader("image request")), nil
			}
			response, err := Send(request, opts)
			require.ErrorIs(t, err, failure)
			require.Nil(t, response)
			require.Equal(t, 1, attempts)
			require.Zero(t, replays)
		})
	}
}
