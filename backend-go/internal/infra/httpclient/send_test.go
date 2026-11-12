package httpclient

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func TestTLSHandshakeTimeoutRetriesWithRebuiltBody(t *testing.T) {
	attempts := 0
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		attempts++
		body, err := io.ReadAll(request.Body)
		require.NoError(t, err)
		require.Equal(t, "image request", string(body))
		if attempts == 1 {
			return nil, errors.New("net/http: TLS handshake timeout")
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("ok")), Request: request}, nil
	})}
	request, err := http.NewRequest(http.MethodPost, "https://upstream.example.com/v1/responses", strings.NewReader("image request"))
	require.NoError(t, err)

	response, err := doWithTLSHandshakeRetry(client, request)
	require.NoError(t, err)
	require.Equal(t, 2, attempts)
	require.NoError(t, response.Body.Close())
}

func TestTLSHandshakeRetryDoesNotRetryOtherErrors(t *testing.T) {
	attempts := 0
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		attempts++
		return nil, errors.New("connection reset")
	})}
	request, err := http.NewRequest(http.MethodPost, "https://upstream.example.com/v1/responses", strings.NewReader("image request"))
	require.NoError(t, err)

	_, err = doWithTLSHandshakeRetry(client, request)
	require.ErrorContains(t, err, "connection reset")
	require.Equal(t, 1, attempts)
}
