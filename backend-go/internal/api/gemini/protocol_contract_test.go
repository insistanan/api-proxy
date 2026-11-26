package gemini

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"insistanan/api-proxy/internal/infra/config"
	"insistanan/api-proxy/internal/protocol/types"
)

func TestGeminiBuildUsesPreparedBodyAndPreservesNativeFields(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/test", nil)
	c.Params = gin.Params{{Key: "modelAction", Value: "client:generateContent"}}
	c.Set("__gemini_req", &types.GeminiRequest{RawJSON: []byte(`{"contents":[{"parts":[{"text":"old-sensitive-value"}]}]}`)})
	prepared := []byte(`{"contents":[{"role":"user","parts":[{"text":"prepared"}]}],"generationConfig":{"maxOutputTokens":2048,"responseJsonSchema":{"type":"object"}},"newField":{"keep":true}}`)
	for _, protocol := range []string{"gemini", "openai"} {
		body := prepared
		if protocol == "openai" {
			body = []byte(`{"contents":[{"role":"user","parts":[{"text":"prepared"}]}]}`)
		}
		req, err := buildPreparedProviderRequest(c, &config.UpstreamConfig{ServiceType: protocol, BaseURL: "https://example.test"}, "key", body)
		require.NoError(t, err)
		actual, err := io.ReadAll(req.Body)
		require.NoError(t, err)
		require.NoError(t, req.Body.Close())
		require.Contains(t, string(actual), "prepared")
		require.NotContains(t, string(actual), "old-sensitive-value")
		if protocol == "gemini" {
			require.JSONEq(t, string(prepared), string(actual))
		}
	}
}

func TestNativeGeminiStreamKeepsLateCandidates(t *testing.T) {
	for _, complete := range []bool{true, false} {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodPost, "/test", nil)
		c.Set("__gemini_req", &types.GeminiRequest{GenerationConfig: &types.GeminiGenerationConfig{CandidateCount: 2}})
		body := "data: " + `{"candidates":[{"index":1,"content":{"parts":[{"text":"B"}]},"finishReason":"STOP"}]}` + "\n\n"
		if complete {
			body += "data: " + `{"candidates":[{"index":0,"content":{"parts":[{"text":"A"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":2}}` + "\n\n"
		}
		_, err := handleStreamSuccess(c, &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, "gemini", &config.EnvConfig{}, time.Now(), "m")
		if complete {
			require.NoError(t, err)
			require.Contains(t, recorder.Body.String(), `"text":"A"`)
			require.Equal(t, 2, strings.Count(recorder.Body.String(), `"finishReason":"STOP"`))
		} else {
			require.ErrorIs(t, err, io.ErrUnexpectedEOF)
		}
	}
}

func TestGeminiEntryStreamsCompleteToolCalls(t *testing.T) {
	for _, tc := range []struct{ protocol, body string }{
		{"claude", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":"initial"}}` + "\n" + `{"type":"content_block_stop","index":0}` + "\n" + `{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"a","name":"run","input":{"x":1}}}` + "\n" + `{"type":"content_block_stop","index":1}` + "\n" + `{"type":"message_delta","delta":{"stop_reason":"tool_use"}}` + "\n" + `{"type":"message_stop"}`},
		{"openai", `{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"a","function":{"name":"run","arguments":"{\"x\":"}}]}}]}` + "\n" + `{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"1}"}}]},"finish_reason":"tool_calls"}]}` + "\n" + `[DONE]`},
		{"responses", `{"type":"response.output_text.delta","delta":"initial"}` + "\n" + `{"type":"response.output_item.added","output_index":1,"item":{"type":"function_call","id":"fc_a","call_id":"a","name":"run"}}` + "\n" + `{"type":"response.function_call_arguments.delta","item_id":"fc_a","delta":"{\"x\":"}` + "\n" + `{"type":"response.function_call_arguments.delta","item_id":"fc_a","delta":"1}"}` + "\n" + `{"type":"response.output_item.done","output_index":1,"item":{"type":"function_call","id":"fc_a","call_id":"a","name":"run","arguments":"{\"x\":1}"}}` + "\n" + `{"type":"response.completed","response":{"status":"completed"}}`},
	} {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodPost, "/test", nil)
		body := "data: " + strings.ReplaceAll(tc.body, "\n", "\n\ndata: ") + "\n\n"
		_, err := handleStreamSuccess(c, &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, tc.protocol, &config.EnvConfig{}, time.Now(), "m")
		require.NoError(t, err)
		require.Contains(t, recorder.Body.String(), `"functionCall":{"id":"a","name":"run","args":{"x":1}}`)
		if tc.protocol == "claude" {
			require.Contains(t, recorder.Body.String(), "initial")
		}
	}
}

func TestConvertedGeminiStreamKeepsInitialUsageAndReadsWholeEvents(t *testing.T) {
	for _, tc := range []struct{ protocol, body string }{
		{"claude", "data: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":10,\"cache_read_input_tokens\":5}}}\n\n" +
			"data: {\"type\":\n" + "data: \"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"hello\"}}\n\n" +
			"data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"max_tokens\"},\"usage\":{\"output_tokens\":2}}\n\n" +
			"data: {\"type\":\"message_stop\"}"},
		{"openai", "data: {\"choices\":\n" + "data: [{\"delta\":{\"content\":\"hello\"},\"finish_reason\":null}]}\r\n\r\n" +
			"data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"length\"}]}\n\n" +
			"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":15,\"prompt_tokens_details\":{\"cached_tokens\":5},\"completion_tokens\":2}}\n\n" +
			"data: [DONE]"},
		{"responses", "data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\n" +
			"data: {\"type\":\"response.incomplete\",\"response\":{\"status\":\"incomplete\",\"incomplete_details\":{\"reason\":\"max_output_tokens\"},\"usage\":{\"input_tokens\":15,\"input_tokens_details\":{\"cached_tokens\":5},\"output_tokens\":2}}}\n\n"},
	} {
		t.Run(tc.protocol, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/test", nil)
			usage, err := handleStreamSuccess(c, &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(tc.body))}, tc.protocol, &config.EnvConfig{}, time.Now(), "m")
			require.NoError(t, err)
			require.Equal(t, 10, usage.InputTokens)
			require.Equal(t, 5, usage.CacheReadInputTokens)
			require.Equal(t, 2, usage.OutputTokens)
			require.Contains(t, recorder.Body.String(), `"text":"hello"`)
			require.Contains(t, recorder.Body.String(), `"promptTokenCount":15`)
			require.Contains(t, recorder.Body.String(), `"cachedContentTokenCount":5`)
			require.Equal(t, 1, strings.Count(recorder.Body.String(), `"finishReason":"MAX_TOKENS"`))
		})
	}
}
