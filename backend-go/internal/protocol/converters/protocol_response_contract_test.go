package converters

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"insistanan/api-proxy/internal/protocol/converters/togemini"
	"insistanan/api-proxy/internal/protocol/types"
)

func TestCrossProtocolResponsesRejectUnrepresentedContent(t *testing.T) {
	for _, tc := range []struct{ protocol, body, chunk string }{
		{"gemini", `{"candidates":[{"content":{"parts":[{"text":"visible"},{"inlineData":{"mimeType":"image/png","data":"AQID"}}]},"finishReason":"STOP"}]}`, `{"candidates":[{"content":{"parts":[{"text":"visible"},{"inlineData":{"mimeType":"image/png","data":"AQID"}}]},"finishReason":"STOP"}]}`},
		{"claude", `{"content":[{"type":"text","text":"visible"},{"type":"image","source":{"type":"base64","data":"AQID"}}],"stop_reason":"end_turn"}`, `{"type":"content_block_start","index":0,"content_block":{"type":"image","source":{"type":"base64","data":"AQID"}}}`},
		{"openai", `{"choices":[{"message":{"content":"visible","audio":{"data":"AQID"}},"finish_reason":"stop"}]}`, `{"choices":[{"index":0,"delta":{"content":"visible","audio":{"data":"AQID"}},"finish_reason":"stop"}]}`},
	} {
		_, err := ConvertUpstreamResponseToResponses(tc.protocol, nil, []byte(tc.body), "")
		require.True(t, types.IsUnsupportedProtocol(err), "%s: %v", tc.protocol, err)
		_, err = convertStreamFixture(t, tc.protocol, `{}`, []string{tc.chunk})
		require.True(t, types.IsUnsupportedProtocol(err), "%s: %v", tc.protocol, err)
	}
}

func TestResponseDispatchRejectsMissingCompletion(t *testing.T) {
	for _, tc := range []struct{ protocol, body string }{
		{"openai", `{"choices":[{"message":{"content":"partial"}}]}`},
		{"claude", `{"content":[{"type":"text","text":"partial"}]}`},
		{"gemini", `{"candidates":[null]}`},
		{"gemini", `{"candidates":[{"content":{"parts":[{"text":"partial"}]}}]}`},
	} {
		_, err := ConvertUpstreamResponseToResponses(tc.protocol, nil, []byte(tc.body), "")
		require.Error(t, err, tc.protocol)
		if tc.protocol == "gemini" {
			continue
		}
		var object map[string]interface{}
		require.NoError(t, json.Unmarshal([]byte(tc.body), &object))
		if tc.protocol == "openai" {
			_, err = togemini.ChatResponse(object)
		} else {
			_, err = togemini.ClaudeResponse(object)
		}
		require.Error(t, err, tc.protocol)
	}
}

func convertStreamFixture(t *testing.T, protocol, request string, lines []string) (string, error) {
	t.Helper()
	var state any
	var events []string
	for _, line := range lines {
		output, err := ConvertUpstreamStreamLineToResponses(context.Background(), protocol, "m", []byte(request), []byte("data: "+line), &state)
		if err != nil {
			return strings.Join(events, ""), err
		}
		events = append(events, output...)
	}
	output, err := FinishUpstreamStreamToResponses(protocol, []byte(request), &state)
	return strings.Join(append(events, output...), ""), err
}

func finalResponse(events string) gjson.Result {
	var result gjson.Result
	for _, line := range strings.Split(events, "\n") {
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		root := gjson.Parse(strings.TrimPrefix(line, "data: "))
		if typ := root.Get("type").String(); typ == "response.completed" || typ == "response.incomplete" {
			result = root.Get("response")
		}
	}
	return result
}

func TestResponsesCompletionPreservesTruncation(t *testing.T) {
	for _, tc := range []struct {
		protocol, body string
		lines          []string
	}{
		{"openai", `{"choices":[{"message":{"content":"partial"},"finish_reason":"length"}]}`, []string{`{"choices":[{"index":0,"delta":{"content":"partial"},"finish_reason":"length"}]}`, `[DONE]`}},
		{"claude", `{"content":[{"type":"text","text":"partial"}],"stop_reason":"max_tokens"}`, []string{`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":"partial"}}`, `{"type":"content_block_stop","index":0}`, `{"type":"message_delta","delta":{"stop_reason":"max_tokens"}}`, `{"type":"message_stop"}`}},
		{"gemini", `{"candidates":[{"content":{"parts":[{"text":"partial"}]},"finishReason":"MAX_TOKENS"}]}`, []string{`{"candidates":[{"index":0,"content":{"parts":[{"text":"partial"}]},"finishReason":"MAX_TOKENS"}]}`}},
	} {
		t.Run(tc.protocol, func(t *testing.T) {
			normal, err := ConvertUpstreamResponseToResponses(tc.protocol, []byte(`{"model":"m"}`), []byte(tc.body), "")
			require.NoError(t, err)
			require.Equal(t, "incomplete", normal.Status)
			events, err := convertStreamFixture(t, tc.protocol, `{"model":"m"}`, tc.lines)
			require.NoError(t, err)
			completed := finalResponse(events)
			require.Equal(t, "incomplete", completed.Get("status").String())
			require.Equal(t, "max_output_tokens", completed.Get("incomplete_details.reason").String())
			require.Len(t, completed.Get("output").Array(), 1)
			require.Equal(t, "partial", completed.Get("output.0.content.0.text").String())
		})
	}
}

func TestResponseToolTypesRoundTrip(t *testing.T) {
	request := `{"model":"m","tools":[{"type":"custom","name":"run"},{"type":"namespace","name":"fs","tools":[{"type":"function","name":"read"}]},{"type":"tool_search","execution":"client"}]}`
	for _, tc := range []struct{ name, args, typ string }{
		{"run", `{"input":"line 1\n\"quoted\""}`, "custom_tool_call"},
		{"fs__read", `{"path":"a"}`, "function_call"},
		{"tool_search", `{"query":"files"}`, "tool_search_call"},
	} {
		for _, protocol := range []string{"claude", "gemini", "openai"} {
			t.Run(protocol+"/"+tc.name, func(t *testing.T) {
				var args map[string]interface{}
				require.NoError(t, json.Unmarshal([]byte(tc.args), &args))
				var body interface{}
				var chunks []string
				if protocol == "claude" {
					block := map[string]interface{}{"type": "tool_use", "id": "c", "name": tc.name, "input": args}
					body = map[string]interface{}{"content": []interface{}{block}, "stop_reason": "tool_use"}
					start, _ := json.Marshal(map[string]interface{}{"type": "content_block_start", "index": 0, "content_block": block})
					chunks = []string{string(start), `{"type":"content_block_stop","index":0}`, `{"type":"message_delta","delta":{"stop_reason":"tool_use"}}`, `{"type":"message_stop"}`}
				} else if protocol == "openai" {
					call := map[string]interface{}{"index": 0, "id": "c", "type": "function", "function": map[string]interface{}{"name": tc.name, "arguments": tc.args}}
					body = map[string]interface{}{"choices": []interface{}{map[string]interface{}{"message": map[string]interface{}{"tool_calls": []interface{}{call}}, "finish_reason": "tool_calls"}}}
					chunk, err := json.Marshal(map[string]interface{}{"choices": []interface{}{map[string]interface{}{"index": 0, "delta": map[string]interface{}{"tool_calls": []interface{}{call}}, "finish_reason": "tool_calls"}}})
					require.NoError(t, err)
					chunks = []string{string(chunk), `[DONE]`}
				} else {
					body = map[string]interface{}{"candidates": []interface{}{map[string]interface{}{"content": map[string]interface{}{"parts": []interface{}{map[string]interface{}{"functionCall": map[string]interface{}{"id": "c", "name": tc.name, "args": args}}}}, "finishReason": "STOP"}}}
				}
				encoded, err := json.Marshal(body)
				require.NoError(t, err)
				if protocol == "gemini" {
					chunks = []string{string(encoded)}
				}
				normal, err := ConvertUpstreamResponseToResponses(protocol, []byte(request), encoded, "")
				require.NoError(t, err)
				require.Len(t, normal.Output, 1)
				require.Equal(t, tc.typ, normal.Output[0].Type)
				events, err := convertStreamFixture(t, protocol, request, chunks)
				require.NoError(t, err)
				item := finalResponse(events).Get("output.0")
				require.Equal(t, tc.typ, item.Get("type").String())
				for _, line := range strings.Split(events, "\n") {
					event := gjson.Parse(strings.TrimPrefix(line, "data: "))
					if event.Get("type").String() == "response.output_item.added" {
						require.Equal(t, tc.typ, event.Get("item.type").String())
					}
				}
				if tc.name == "run" {
					require.Equal(t, "line 1\n\"quoted\"", item.Get("input").String())
					require.Contains(t, events, "response.custom_tool_call_input.done")
				}
				if tc.name == "fs__read" {
					require.Equal(t, "read", item.Get("name").String())
					require.Equal(t, "fs", item.Get("namespace").String())
				}
			})
		}
	}
}

func TestChatCustomToolRequiresStringInput(t *testing.T) {
	request := `{"tools":[{"type":"custom","name":"run"}]}`
	for _, arguments := range []string{`{}`, `{"input":null}`, `{"input":42}`, `{"input":""}`} {
		call := map[string]interface{}{"index": 0, "id": "c", "function": map[string]interface{}{"name": "run", "arguments": arguments}}
		body, err := json.Marshal(map[string]interface{}{"choices": []interface{}{map[string]interface{}{"message": map[string]interface{}{"tool_calls": []interface{}{call}}, "finish_reason": "tool_calls"}}})
		require.NoError(t, err)
		result, normalErr := ConvertUpstreamResponseToResponses("openai", []byte(request), body, "")
		chunk, err := json.Marshal(map[string]interface{}{"choices": []interface{}{map[string]interface{}{"index": 0, "delta": map[string]interface{}{"tool_calls": []interface{}{call}}, "finish_reason": "tool_calls"}}})
		require.NoError(t, err)
		events, streamErr := convertStreamFixture(t, "openai", request, []string{string(chunk), `[DONE]`})
		if arguments == `{"input":""}` {
			require.NoError(t, normalErr)
			require.NoError(t, streamErr)
			require.Equal(t, "", result.Output[0].Content)
			require.Equal(t, "", finalResponse(events).Get("output.0.input").String())
		} else {
			require.Error(t, normalErr)
			require.Error(t, streamErr)
		}
	}
	result, err := ConvertUpstreamResponseToResponses("openai", []byte(`{}`), []byte(`{"choices":[{"message":{"tool_calls":[{"id":"c","function":{"name":"tool_search","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`), "")
	require.NoError(t, err)
	require.Equal(t, "function_call", result.Output[0].Type, "未声明搜索工具时不应按函数名称猜类型")
}

func TestResponsesRejectsMalformedResponseAndExtraCandidate(t *testing.T) {
	for _, raw := range []string{`null`, `{`, `{}`, `{"choices":[{"message":{},"finish_reason":"stop"},{"message":{},"finish_reason":"stop"}]}`, `{"choices":[{"message":{"tool_calls":[{"id":"c","function":{"name":"f","arguments":"{"}}]},"finish_reason":"tool_calls"}]}`} {
		_, err := ConvertUpstreamResponseToResponses("openai", nil, []byte(raw), "")
		require.Error(t, err, raw)
	}
	_, err := convertStreamFixture(t, "openai", `{}`, []string{`{"choices":[{"index":0,"delta":{"content":"A"},"finish_reason":"stop"}]}`, `{"choices":[{"index":1,"delta":{"content":"B"},"finish_reason":"stop"}]}`})
	require.Error(t, err)
	_, err = convertStreamFixture(t, "gemini", `{}`, []string{`{"candidates":[{"index":0,"finishReason":"STOP"}]}`, `{"candidates":[{"index":1,"finishReason":"STOP"}]}`})
	require.Error(t, err)
}

func TestGeminiStreamToolsKeepsParallelCallsAndInitialInput(t *testing.T) {
	var state togemini.StreamTools
	parts, err := state.Feed("openai", []byte(`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"a","function":{"name":"f","arguments":"{\"q\":"}},{"index":1,"id":"b","function":{"name":"f","arguments":"{\"q\":"}}]}}]}`))
	require.NoError(t, err)
	require.Empty(t, parts)
	parts, err = state.Feed("openai", []byte(`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":1,"function":{"arguments":"2}"}},{"index":0,"function":{"arguments":"1}"}}]},"finish_reason":"tool_calls"}]}`))
	require.NoError(t, err)
	require.Len(t, parts, 2)
	require.Equal(t, "a", parts[0].FunctionCall.ID)
	require.Equal(t, float64(2), parts[1].FunctionCall.Args["q"])
	require.NoError(t, state.CheckComplete())
	var claude togemini.StreamTools
	_, err = claude.Feed("claude", []byte(`{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"c","name":"run","input":{"x":1}}}`))
	require.NoError(t, err)
	parts, err = claude.Feed("claude", []byte(`{"type":"content_block_stop","index":1}`))
	require.NoError(t, err)
	require.Len(t, parts, 1)
	require.Equal(t, float64(1), parts[0].FunctionCall.Args["x"])
}

func TestClaudeToolInitialArgumentsCannotBeOverwrittenByDelta(t *testing.T) {
	lines := []string{
		`{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"c","name":"run","input":{"x":1}}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"x\":2}"}}`,
		`{"type":"content_block_stop","index":0}`,
		`{"type":"message_delta","delta":{"stop_reason":"tool_use"}}`,
	}
	_, err := convertStreamFixture(t, "claude", `{}`, lines)
	require.ErrorContains(t, err, "非空初始参数")
	var gemini togemini.StreamTools
	for _, line := range lines {
		_, err = gemini.Feed("claude", []byte(line))
		if err != nil {
			break
		}
	}
	require.ErrorContains(t, err, "非空初始参数")
}
