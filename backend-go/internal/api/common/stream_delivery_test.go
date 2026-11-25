package common

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/insistanan/api-proxy/internal/protocol/types"
	"github.com/stretchr/testify/require"
)

type failingStreamWriter struct{ err error }

func (w failingStreamWriter) Write([]byte) (int, error) { return 0, w.err }

func TestWriteStream_PreservesDownstreamSource(t *testing.T) {
	for _, err := range []error{nil, errors.New("broken pipe")} {
		actual := WriteStream(failingStreamWriter{err}, nil, []byte("event"))
		require.True(t, types.IsDownstreamWriteError(actual))
		if err == nil {
			err = io.ErrShortWrite
		}
		require.ErrorIs(t, actual, err)
	}
}

func TestReportStreamFailure_NotifiesOnlyCommittedStream(t *testing.T) {
	for _, protocol := range []string{"chat", "responses", "gemini", "images"} {
		for _, committed := range []bool{false, true} {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
			if committed {
				c.Writer.WriteHeaderNow()
			}
			upstreamErr := errors.New("upstream boom")
			err := ReportStreamFailure(c, protocol, upstreamErr)
			require.ErrorIs(t, err, upstreamErr)
			if committed {
				require.Contains(t, recorder.Body.String(), "upstream boom")
				require.NotContains(t, recorder.Body.String(), "[DONE]")
			} else {
				require.False(t, c.Writer.Written())
			}
		}
	}
}
