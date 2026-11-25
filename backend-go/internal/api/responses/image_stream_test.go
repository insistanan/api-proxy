package responses

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/insistanan/api-proxy/internal/infra/config"
	"github.com/stretchr/testify/require"
)

func TestResponsesImagePassthrough_RequiresValidTerminal(t *testing.T) {
	prefix := "data: {\"type\":\"response.image_generation_call.partial_image\",\"partial_image_b64\":\"AQID\"}\n\n"
	terminal := "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"usage\":{\"input_tokens\":10,\"output_tokens\":5},\"output\":[]}}"
	for _, tc := range []struct {
		name, tail string
		success    bool
	}{
		{"完成", terminal, true},
		{"完成后不再交付", terminal + "\n\ndata: {\"text\":\"late\"}", true},
		{"截断", "", false},
		{"失败", "data: {\"type\":\"response.failed\",\"response\":{\"error\":{\"message\":\"boom\"}}}\n\n" + terminal, false},
		{"伪完成", "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"failed\"}}", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			resp := &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(prefix + tc.tail))}
			usage, err := handleResponsesImagePassthrough(c, resp, &config.EnvConfig{}, time.Now(), true)
			if tc.success {
				require.NoError(t, err)
				require.Equal(t, 5, usage.OutputTokens)
			} else {
				require.Error(t, err)
				require.Equal(t, 1, strings.Count(recorder.Body.String(), "event: error"))
				require.NotContains(t, recorder.Body.String(), `"type":"response.completed"`)
			}
			require.NotContains(t, recorder.Body.String(), "late")
		})
	}
}
