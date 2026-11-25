package httpclient

import (
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestClient_ReturnsRedirectWithoutForwardingCredentialsOrBody(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, status := range []int{301, 302, 303, 307, 308} {
			for _, target := range []string{"/another-path", "https://another.example/request", "http://upstream.example/request"} {
				t.Run(strconv.FormatBool(stream)+"/"+strconv.Itoa(status)+"/"+target, func(t *testing.T) {
					manager := NewClientManager()
					t.Cleanup(manager.CloseIdleConnections)
					client, err := manager.GetClient(ClientOptions{IsStream: stream, Timeout: time.Minute, ResponseHeaderTimeout: time.Second})
					require.NoError(t, err)
					calls := 0
					client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
						calls++
						require.Equal(t, "https://upstream.example/request", req.URL.String())
						if req.Body != nil {
							require.NoError(t, req.Body.Close())
						}
						return &http.Response{StatusCode: status, Header: http.Header{"Location": {target}}, Body: io.NopCloser(strings.NewReader("redirect")), Request: req}, nil
					})
					req, err := http.NewRequest(http.MethodPost, "https://upstream.example/request", strings.NewReader("private prompt"))
					require.NoError(t, err)
					for _, key := range []string{"Authorization", "X-Api-Key", "X-Goog-Api-Key"} {
						req.Header.Set(key, "private-key")
					}
					response, err := client.Do(req)
					require.NoError(t, err)
					require.Equal(t, status, response.StatusCode)
					body, err := io.ReadAll(response.Body)
					require.NoError(t, err)
					require.Equal(t, "redirect", string(body))
					require.NoError(t, response.Body.Close())
					require.Equal(t, 1, calls)
				})
			}
		}
	}
}

type trackedTransport struct {
	closed int
}

func (t *trackedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody, Header: make(http.Header), Request: req}, nil
}

func (t *trackedTransport) CloseIdleConnections() { t.closed++ }

func TestClientCache_EvictsLeastRecentlyUsedAndRemainsUsable(t *testing.T) {
	manager := NewClientManager()
	t.Cleanup(manager.CloseIdleConnections)
	options := ClientOptions{Timeout: time.Minute, ResponseHeaderTimeout: time.Second}
	first, err := manager.GetClient(options)
	require.NoError(t, err)
	firstTransport := &trackedTransport{}
	first.Transport = firstTransport
	options.Timeout++
	second, err := manager.GetClient(options)
	require.NoError(t, err)
	secondTransport := &trackedTransport{}
	second.Transport = secondTransport
	for index := 2; index < maxCachedClients; index++ {
		options.Timeout = time.Minute + time.Duration(index)
		_, err := manager.GetClient(options)
		require.NoError(t, err)
	}
	options.Timeout = time.Minute
	again, err := manager.GetClient(options)
	require.NoError(t, err)
	require.Same(t, first, again)
	options.Timeout = 2 * time.Minute
	_, err = manager.GetClient(options)
	require.NoError(t, err)
	require.Len(t, manager.clients, maxCachedClients)
	require.Zero(t, firstTransport.closed)
	require.Equal(t, 1, secondTransport.closed)
	// 淘汰不使已持有的客户端失效。
	response, err := second.Get("https://example.com")
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	manager.CloseIdleConnections()
	require.Empty(t, manager.clients)
	require.Equal(t, 1, firstTransport.closed)
}

func TestClient_RejectsUnboundedStreamHeaderWait(t *testing.T) {
	manager := NewClientManager()
	_, err := manager.GetClient(ClientOptions{IsStream: true})
	require.Error(t, err)
	require.Empty(t, manager.clients)
}
