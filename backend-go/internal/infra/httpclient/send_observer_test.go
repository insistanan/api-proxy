package httpclient

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSendObserverExcludesCancelledBeforeAdmission(t *testing.T) {
	for _, cancelled := range []bool{true, false} {
		observed := 0
		headersObserved := 0
		ctx, cancel := context.WithCancel(context.Background())
		if cancelled {
			cancel()
		}
		ctx = WithSendObserver(ctx, func() { observed++ })
		ctx = WithResponseHeadersObserver(ctx, func(elapsed time.Duration) {
			headersObserved++
			require.GreaterOrEqual(t, elapsed, time.Duration(0))
		})
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://example.com", strings.NewReader("{}"))
		require.NoError(t, err)
		client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("ok")), Request: request}, nil
		})}
		resp, err := sendWithAdmission(client, req)
		if cancelled {
			require.ErrorIs(t, err, context.Canceled)
			require.Zero(t, observed)
			require.Zero(t, headersObserved)
		} else {
			require.NoError(t, err)
			require.Equal(t, 1, observed)
			require.Equal(t, 1, headersObserved)
			require.NoError(t, resp.Body.Close())
		}
		cancel()
	}
}

func TestResponseHeadersTimingIsAbsentOnTransportFailure(t *testing.T) {
	observed := false
	ctx := WithResponseHeadersObserver(context.Background(), func(time.Duration) { observed = true })
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://example.com", nil)
	require.NoError(t, err)
	failure := errors.New("模拟连接失败")
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) { return nil, failure })}
	_, err = sendWithAdmission(client, req)
	require.ErrorIs(t, err, failure)
	require.False(t, observed)
}
