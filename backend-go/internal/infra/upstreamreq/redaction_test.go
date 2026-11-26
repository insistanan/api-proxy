package upstreamreq

import (
	"errors"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRedactionPreservesNetworkErrorCauseAndHidesNestedCredentials(t *testing.T) {
	cause := errors.New("TLS failed: long-password and access-secret")
	nested := &url.Error{Op: "proxy", URL: "https://proxy:long-password@proxy.example?token=access-secret", Err: cause}
	err := &url.Error{Op: "Get", URL: "https://user:password@example.test/v1/models?key=access-secret", Err: nested}
	redacted := RedactError(errors.Join(err, errors.New("access-secret")), []string{err.URL}, "access-secret")
	for _, secret := range []string{"password", "access-secret", "?token", "?key", "user:", "proxy:"} {
		require.NotContains(t, redacted.Error(), secret)
	}
	require.ErrorIs(t, redacted, cause)
	var original *url.Error
	require.ErrorAs(t, redacted, &original)
	require.Equal(t, err.URL, original.URL, "不修改原始错误和实际请求")
	require.Equal(t, "https://example.test/v1/models", RedactURL(err.URL))
}
