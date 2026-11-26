package chat

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"insistanan/api-proxy/internal/infra/config"
)

func TestChatBuildersApplyChannelReasoningWithoutChangingInput(t *testing.T) {
	gin.SetMode(gin.TestMode)
	input := []byte(`{"model":"alias","messages":[{"role":"user","content":"hello"}],"reasoning_effort":"future","reasoning":{"effort":"future"},"output_config":{"effort":"future"}}`)
	for _, build := range []struct {
		name string
		fn   func(*gin.Context, *config.UpstreamConfig, string, []byte) (*http.Request, error)
	}{
		{"原生Chat", buildChatUpstreamRequest},
	} {
		t.Run(build.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			before := bytes.Clone(input)
			for _, effort := range []string{"auto", "none", "high"} {
				up := &config.UpstreamConfig{BaseURL: "https://example.invalid", ServiceType: "openai", DefaultModel: "real-model", ReasoningEffort: effort}
				req, err := build.fn(c, up, "fixture", input)
				require.NoError(t, err)
				actual, err := io.ReadAll(req.Body)
				require.NoError(t, err)
				require.NoError(t, req.Body.Close())
				if effort == "auto" {
					require.False(t, gjson.GetBytes(actual, "reasoning_effort").Exists())
				} else {
					require.Equal(t, effort, gjson.GetBytes(actual, "reasoning_effort").String())
				}
				require.False(t, gjson.GetBytes(actual, "reasoning").Exists())
				require.False(t, gjson.GetBytes(actual, "output_config").Exists())
				require.JSONEq(t, gjson.GetBytes(input, "messages").Raw, gjson.GetBytes(actual, "messages").Raw)
				require.Equal(t, before, input)
			}
		})
	}
	_, err := applyChatModelMapping(input, &config.UpstreamConfig{ServiceType: "openai", ReasoningEffort: "inherit"})
	require.ErrorContains(t, err, "reasoning")
}
