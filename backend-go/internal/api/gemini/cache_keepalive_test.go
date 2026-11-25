package gemini

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/insistanan/api-proxy/internal/infra/config"
	"github.com/insistanan/api-proxy/internal/protocol/types"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestGeminiCacheReference_PreservesNativeReferenceAndRejectsCrossProvider(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/test", nil)
	var input types.GeminiRequest
	require.NoError(t, json.Unmarshal([]byte(`{"cachedContent":"cachedContents/existing","contents":[{"role":"user","parts":[{"text":"question"}]}]}`), &input))
	for _, target := range []string{"gemini", "claude", "openai"} {
		t.Run(target, func(t *testing.T) {
			up := &config.UpstreamConfig{BaseURL: "https://example.test", ServiceType: target}
			req, err := buildProviderRequest(c, up, up.BaseURL, "fixture", &input, "model", actionGenerateContent)
			if target != "gemini" {
				var invalid *types.InvalidRequestError
				require.ErrorAs(t, err, &invalid)
				require.Nil(t, req)
				return
			}
			require.NoError(t, err)
			body, err := io.ReadAll(req.Body)
			require.NoError(t, err)
			require.NoError(t, req.Body.Close())
			require.Equal(t, "cachedContents/existing", gjson.GetBytes(body, "cachedContent").String())
			require.NotNil(t, req.GetBody, "最终请求必须支持延迟取得不可变正文")
		})
	}
}

func TestGeminiCachePrefix_UsesConvertedTargetAndHonorsInjectionSetting(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/test", nil)
	input := &types.GeminiRequest{Contents: []types.GeminiContent{{Role: "user", Parts: []types.GeminiPart{{Text: "question"}}}}}
	for _, target := range []string{"gemini", "claude", "openai"} {
		for _, disabled := range []bool{false, true} {
			up := &config.UpstreamConfig{BaseURL: "https://example.test", ServiceType: target, DefaultModel: "mapped-model", DisablePromptCacheInjection: disabled}
			req, err := buildProviderRequest(c, up, up.BaseURL, "fixture", input, "client-model", actionStreamGenerateContent)
			require.NoError(t, err)
			body, err := io.ReadAll(req.Body)
			require.NoError(t, err)
			require.NoError(t, req.Body.Close())
			replay, err := req.GetBody()
			require.NoError(t, err)
			frozen, err := io.ReadAll(replay)
			require.NoError(t, err)
			require.NoError(t, replay.Close())
			require.Equal(t, body, frozen)
			if target == "gemini" {
				require.Contains(t, req.URL.Path, "mapped-model:streamGenerateContent")
				require.Equal(t, "question", gjson.GetBytes(body, "contents.0.parts.0.text").String())
			} else {
				require.Equal(t, "mapped-model", gjson.GetBytes(body, "model").String())
				require.True(t, gjson.GetBytes(body, "stream").Bool())
				if target == "claude" {
					require.Equal(t, !disabled, gjson.GetBytes(body, "messages.0.content.0.cache_control").Exists())
				}
			}
		}
	}
}

func TestGeminiConversion_RetainsActualCacheUsageForKeepaliveBaseline(t *testing.T) {
	for _, tc := range []struct {
		protocol, body        string
		input, read, creation int
	}{
		{"claude", `{"id":"msg","type":"message","model":"m","role":"assistant","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":10,"output_tokens":1,"cache_creation_input_tokens":1000,"cache_read_input_tokens":500}}`, 10, 500, 1000},
		{"openai", `{"id":"chat","model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1010,"completion_tokens":1,"prompt_tokens_details":{"cached_tokens":1000}}}`, 10, 1000, 0},
	} {
		t.Run(tc.protocol, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/test", nil)
			response := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(tc.body))}
			usage, err := handleSuccess(c, response, tc.protocol, &config.EnvConfig{}, time.Now(), &types.GeminiRequest{}, "m", false)
			require.NoError(t, err)
			require.NotNil(t, usage)
			require.Equal(t, tc.input, usage.InputTokens)
			require.Equal(t, tc.read, usage.CacheReadInputTokens)
			require.Equal(t, tc.creation, usage.CacheCreationInputTokens)
		})
	}
}
