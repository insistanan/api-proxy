package gemini

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/insistanan/api-proxy/internal/infra/config"
	"github.com/insistanan/api-proxy/internal/protocol/types"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestGeminiBuilderOverridesReasoningForEachTargetWithoutChangingInput(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/test", nil)
	temperature := 0.7
	input := &types.GeminiRequest{
		Contents: []types.GeminiContent{{Role: "user", Parts: []types.GeminiPart{{Text: "hello"}}}},
		GenerationConfig: &types.GeminiGenerationConfig{MaxOutputTokens: 20000,
			Temperature:    &temperature,
			ThinkingConfig: &types.GeminiThinkingConfig{ThinkingLevel: "future", IncludeThoughts: true},
		},
	}
	before, err := json.Marshal(input)
	require.NoError(t, err)
	for _, tc := range []struct{ protocol, model, path string }{
		{"gemini", "gemini-2.5-flash", "generationConfig.thinkingConfig.thinkingBudget"},
		{"openai", "gpt-model", "reasoning_effort"},
		{"claude", "claude-model", "output_config.effort"},
	} {
		for _, effort := range []string{"auto", "none", "high"} {
			t.Run(tc.protocol+"/"+effort, func(t *testing.T) {
				up := &config.UpstreamConfig{BaseURL: "https://example.invalid", ServiceType: tc.protocol, DefaultModel: tc.model, ReasoningEffort: effort}
				req, err := buildProviderRequest(c, up, up.BaseURL, "fixture", input, "alias", actionStreamGenerateContent)
				require.NoError(t, err)
				actual, err := io.ReadAll(req.Body)
				require.NoError(t, err)
				require.NoError(t, req.Body.Close())
				if effort == "auto" {
					for _, path := range []string{"thinking", "reasoning_effort", "reasoning", "output_config.effort", "generationConfig.thinkingConfig"} {
						require.False(t, gjson.GetBytes(actual, path).Exists(), path)
					}
				} else if tc.protocol == "gemini" {
					budget := int64(8192)
					if effort == "none" {
						budget = 0
					}
					require.Equal(t, budget, gjson.GetBytes(actual, tc.path).Int())
					require.Contains(t, req.URL.Path, tc.model)
					require.InDelta(t, temperature, gjson.GetBytes(actual, "generationConfig.temperature").Float(), 0.0001)
				} else if tc.protocol == "claude" && effort == "none" {
					require.Equal(t, "disabled", gjson.GetBytes(actual, "thinking.type").String())
				} else {
					require.Equal(t, effort, gjson.GetBytes(actual, tc.path).String())
				}
				if tc.protocol != "gemini" {
					require.False(t, gjson.GetBytes(actual, "generationConfig").Exists())
					require.Equal(t, tc.model, gjson.GetBytes(actual, "model").String())
				}
				after, err := json.Marshal(input)
				require.NoError(t, err)
				require.Equal(t, before, after)
			})
		}
	}
}
