package httpclient

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSendObserverExcludesCancelledBeforeAdmission(t *testing.T) {
	for _, cancelled := range []bool{true, false} {
		observed := 0
		ctx, cancel := context.WithCancel(context.Background())
		if cancelled {
			cancel()
		}
		req, err := http.NewRequestWithContext(WithSendObserver(ctx, func() { observed++ }), http.MethodPost, "https://example.com", strings.NewReader("{}"))
		require.NoError(t, err)
		client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("ok")), Request: request}, nil
		})}
		resp, err := sendWithAdmission(client, req)
		if cancelled {
			require.ErrorIs(t, err, context.Canceled)
			require.Zero(t, observed)
		} else {
			require.NoError(t, err)
			require.Equal(t, 1, observed)
			require.NoError(t, resp.Body.Close())
		}
		cancel()
	}
}
