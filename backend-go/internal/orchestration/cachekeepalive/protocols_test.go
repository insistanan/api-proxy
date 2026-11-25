package cachekeepalive

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestHeartbeatPayload_PreservesFinalProtocolPrefixAndState(t *testing.T) {
	for _, tc := range []struct{ protocol, body, path, url string }{
		{"openai", `{"model":"mapped-model","stream":true,"max_tokens":100,"prompt_cache_key":"fixed","reasoning_effort":"high","messages":[{"role":"system","content":"prefix"},{"role":"user","content":"question"}]}`, "messages", "https://example.test/v1/chat/completions"},
		{"responses", `{"model":"mapped-model","stream":true,"previous_response_id":"fixed-history","prompt_cache_key":"fixed","reasoning":{"effort":"high"},"input":[{"role":"user","content":"question"}]}`, "input", "https://example.test/v1/responses"},
		{"gemini", `{"systemInstruction":{"parts":[{"text":"prefix"}]},"contents":[{"role":"user","parts":[{"text":"question"}]}]}`, "contents", "https://example.test/v1beta/models/mapped-model:streamGenerateContent?alt=sse&key=fixture"},
	} {
		t.Run(tc.protocol, func(t *testing.T) {
			reads := 0
			source := sampleSource(tc.body, "attempt", &reads)
			source.Upstream.ServiceType = tc.protocol
			source.URL = tc.url
			s := &snapshot{source: source, policy: samplePolicy()}
			body, model, _, err := prepareBody(s)
			require.NoError(t, err)
			require.Equal(t, "mapped-model", model)
			require.True(t, s.stream)
			require.Equal(t, gjson.Get(tc.body, tc.path+".0").Raw, gjson.GetBytes(body, tc.path+".0").Raw)
			require.Equal(t, gjson.Get(tc.body, tc.path+".#").Int()+1, gjson.GetBytes(body, tc.path+".#").Int())
			for _, path := range []string{"systemInstruction", "tools", "prompt_cache_key", "previous_response_id", "reasoning", "reasoning_effort"} {
				require.Equal(t, gjson.Get(tc.body, path).Raw, gjson.GetBytes(body, path).Raw, path)
			}
			if tc.protocol == "responses" {
				require.False(t, gjson.GetBytes(body, "store").Bool())
				require.False(t, gjson.GetBytes(body, "background").Bool())
			}
			if tc.protocol == "openai" {
				require.EqualValues(t, 16, gjson.GetBytes(body, "max_tokens").Int())
				require.False(t, gjson.GetBytes(body, "max_completion_tokens").Exists())
				require.True(t, gjson.GetBytes(body, "stream_options.include_usage").Bool())
			}
			again, _, _, err := prepareBody(s)
			require.NoError(t, err)
			require.Equal(t, body, again, "心跳不得累积新消息或回答")
		})
	}
}

func TestHeartbeatPayload_ExplicitCacheAndUnsupportedMutableState(t *testing.T) {
	for _, tc := range []struct {
		protocol, body, url string
		invalid             bool
	}{
		{"responses", `{"model":"m","input":"hello","conversation":"mutable"}`, "https://example.test/v1/responses", true},
		{"responses", `{"model":"m","input":[],"tools":[{"type":"web_search"}]}`, "https://example.test/v1/responses", true},
		{"gemini", `{"contents":[],"cachedContent":"https://other.test/cache"}`, "https://example.test/v1beta/models/m:generateContent", true},
		{"gemini", `{"contents":[],"cachedContent":"cachedContents/existing","tools":[{"googleSearch":{}}]}`, "https://example.test/custom/v1beta/models/m:streamGenerateContent?alt=sse&key=fixture", false},
	} {
		reads := 0
		source := sampleSource(tc.body, "attempt", &reads)
		source.URL = tc.url
		source.Upstream.ServiceType = tc.protocol
		s := &snapshot{source: source, policy: samplePolicy()}
		body, _, ttl, err := prepareBody(s)
		if tc.invalid {
			require.Error(t, err)
			continue
		}
		require.NoError(t, err)
		require.Equal(t, "explicit", s.mode)
		require.Zero(t, ttl)
		require.JSONEq(t, `{}`, string(body))
		require.Equal(t, "https://example.test/custom/v1beta/cachedContents/existing?key=fixture", s.cacheURL)
	}
}

func TestHeartbeatUsage_NormalizesAllProtocolsAndRequiresStreamEnd(t *testing.T) {
	for _, tc := range []struct {
		protocol, body, stream string
		output                 int
	}{
		{"claude", `{"type":"message","usage":{"input_tokens":10,"cache_read_input_tokens":1000,"output_tokens":7}}`,
			"data: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":10,\"cache_read_input_tokens\":1000,\"output_tokens\":0}}}\n\ndata:{\"type\":\"message_delta\",\"usage\":{\"output_tokens\":7}}\n\ndata:{\"type\":\"message_stop\"}\n\n", 7},
		{"openai", `{"choices":[],"usage":{"prompt_tokens":1010,"completion_tokens":7,"prompt_tokens_details":{"cached_tokens":1000}}}`, "", 7},
		{"responses", `{"status":"completed","usage":{"input_tokens":1010,"output_tokens":7,"input_tokens_details":{"cached_tokens":1000}}}`, "", 7},
		{"gemini", `{"candidates":[{"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":1010,"cachedContentTokenCount":1000,"candidatesTokenCount":3,"thoughtsTokenCount":4}}`, "", 7},
	} {
		t.Run(tc.protocol, func(t *testing.T) {
			usage, err := parseProtocolResponse(tc.protocol, []byte(tc.body), 200)
			require.NoError(t, err)
			require.Equal(t, 10, usage.InputTokens)
			require.Equal(t, 1000, usage.CacheReadInputTokens)
			require.Equal(t, tc.output, usage.OutputTokens)
			stream := tc.stream
			if stream == "" {
				switch tc.protocol {
				case "responses":
					stream = "event: response.completed\ndata:{\"type\":\"response.completed\",\"response\":" + tc.body + "}\n\n"
				case "openai":
					stream = "data:" + tc.body + "\n\ndata: [DONE]\n\n"
				default:
					stream = "data:" + tc.body + "\n\n"
				}
			}
			streamUsage, err := parseStreamResponse(tc.protocol, []byte(stream), 200)
			require.NoError(t, err)
			require.Equal(t, usage, streamUsage)
			_, err = parseStreamResponse(tc.protocol, []byte("data: {}\n\n"), 200)
			require.Error(t, err)
			_, err = parseProtocolResponse(tc.protocol, []byte(tc.body), 429)
			require.Error(t, err)
		})
	}
	_, err := parseProtocolResponse("openai", []byte(`{"choices":[],"usage":{"prompt_tokens":10,"completion_tokens":1,"prompt_tokens_details":{"cached_tokens":1000}}}`), 200)
	require.Error(t, err, "累计缓存不能冒充本次输入命中")
}

func TestHeartbeatPayload_RetentionAndUnknownTTL(t *testing.T) {
	reads := 0
	for _, retention := range []string{"in_memory", "24h"} {
		source := sampleSource(`{"model":"m","input":"hello","prompt_cache_retention":"`+retention+`"}`, "attempt", &reads)
		source.Upstream.ServiceType = "responses"
		s := &snapshot{source: source, policy: samplePolicy()}
		s.policy.IntervalSeconds = 600
		body, _, ttl, err := prepareBody(s)
		if retention == "in_memory" {
			require.Error(t, err)
		} else {
			require.NoError(t, err)
			require.Equal(t, 24*time.Hour, ttl)
			require.EqualValues(t, 2, gjson.GetBytes(body, "input.#").Int())
		}
	}
	source := sampleSource(strings.ReplaceAll(sampleBody, `,"cache_control":{"type":"ephemeral"}`, ""), "attempt", &reads)
	s := &snapshot{source: source, policy: samplePolicy()}
	_, _, ttl, err := prepareBody(s)
	require.NoError(t, err)
	require.Zero(t, ttl)
	require.Equal(t, "implicit", s.mode)
}

func TestHeartbeatUsage_PreservesKnownUsageOnErrorsAndFinalGeminiMetadata(t *testing.T) {
	for _, tc := range []struct{ protocol, stream string }{
		{"responses", "data: {\"type\":\"response.failed\",\"response\":{\"status\":\"failed\",\"usage\":{\"input_tokens\":1010,\"output_tokens\":7,\"input_tokens_details\":{\"cached_tokens\":1000}}}}\n\n"},
		{"openai", "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":1010,\"completion_tokens\":7,\"prompt_tokens_details\":{\"cached_tokens\":1000}}}\n\ndata: {\"error\":{\"message\":\"failed\"}}\n\n"},
		{"claude", "data: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":10,\"cache_read_input_tokens\":1000,\"output_tokens\":0}}}\n\ndata:{\"type\":\"message_delta\",\"usage\":{\"output_tokens\":7}}\n\ndata:{\"type\":\"error\"}\n\n"},
	} {
		t.Run(tc.protocol, func(t *testing.T) {
			usage, err := parseStreamResponse(tc.protocol, []byte(tc.stream), 200)
			require.Error(t, err)
			require.NotNil(t, usage)
			require.Equal(t, 7, usage.OutputTokens)
			require.Equal(t, 1000, usage.CacheReadInputTokens)
		})
	}
	stream := "data: {\"candidates\":[{\"finishReason\":\"STOP\"}]}\n\ndata: {\"usageMetadata\":\n" +
		"data: {\"promptTokenCount\":1010,\"cachedContentTokenCount\":1000,\"candidatesTokenCount\":3,\"thoughtsTokenCount\":4}}\n\n"
	usage, err := parseStreamResponse("gemini", []byte(stream), 200)
	require.NoError(t, err)
	require.Equal(t, 7, usage.OutputTokens)
	require.Equal(t, 10, usage.InputTokens)
	_, err = parseStreamResponse("gemini", []byte(strings.ReplaceAll(stream, "STOP", "SAFETY")), 200)
	require.Error(t, err)
	usage, err = parseStreamResponse("claude", []byte("data: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":10,\"cache_read_input_tokens\":1000,\"output_tokens\":0}}}\n\ndata:{\"type\":\"message_stop\"}\n\n"), 200)
	require.Error(t, err)
	require.Nil(t, usage)
	usage, err = parseStreamResponse("gemini", []byte(stream+"event: error\ndata: {\"message\":\"failed after usage\"}\n\n"), 200)
	require.Error(t, err)
	require.NotNil(t, usage, "错误事件不能抹掉已经收到的用量")
}
