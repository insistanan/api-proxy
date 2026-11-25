package converters

import (
	"encoding/json"
	"testing"

	"github.com/insistanan/api-proxy/internal/infra/config"
	"github.com/insistanan/api-proxy/internal/protocol/converters/togemini"
	"github.com/insistanan/api-proxy/internal/protocol/types"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func dispatchRequest(t *testing.T, target, body string, upstream *config.UpstreamConfig) ([]byte, error) {
	t.Helper()
	var req types.ResponsesRequest
	require.NoError(t, json.Unmarshal([]byte(body), &req))
	return ConvertResponsesRequestToUpstream(target, req.Model, []byte(body), req.Stream, nil, &req, upstream)
}

func TestResponsesRequestPreservesZeroAndExtractsSystem(t *testing.T) {
	body := `{"model":"m","max_output_tokens":4096,"temperature":0,"top_p":0,"instructions":"first","input":[{"role":"system","content":"second"},{"role":"developer","content":"third"},{"role":"user","content":"question"}],"tools":[{"type":"function","name":"BatchTool","parameters":{"type":"object","properties":{"title":{"type":"string","minLength":3}}}}]}`
	for _, target := range []string{"claude", "gemini", "openai"} {
		t.Run(target, func(t *testing.T) {
			raw, err := dispatchRequest(t, target, body, &config.UpstreamConfig{DisablePromptCacheInjection: true})
			require.NoError(t, err)
			require.NotContains(t, string(raw), "cache_control")
			require.Contains(t, string(raw), "BatchTool")
			require.Contains(t, string(raw), `"minLength":3`)
			path := "temperature"
			if target == "gemini" {
				path = "generationConfig.temperature"
			}
			require.True(t, gjson.GetBytes(raw, path).Exists())
			require.Zero(t, gjson.GetBytes(raw, path).Float())
			if target == "claude" {
				require.Equal(t, "first\n\nsecond\n\nthird", gjson.GetBytes(raw, "system").String())
				require.Len(t, gjson.GetBytes(raw, "messages").Array(), 1)
			}
			if target == "gemini" {
				require.Equal(t, "first\n\nsecond\n\nthird", gjson.GetBytes(raw, "systemInstruction.parts.0.text").String())
				require.Len(t, gjson.GetBytes(raw, "contents").Array(), 1)
			}
		})
	}
}

func TestResponsesNamespaceCollisionAndDynamicMerge(t *testing.T) {
	for _, target := range []string{"claude", "openai", "gemini"} {
		_, err := dispatchRequest(t, target, `{"model":"m","max_output_tokens":4096,"input":"go","tools":[{"type":"namespace","name":"a","tools":[{"type":"function","name":"b"}]},{"type":"function","name":"a__b"}]}`, nil)
		require.True(t, types.IsUnsupportedProtocol(err), "%s: %v", target, err)
		raw, err := dispatchRequest(t, target, `{"model":"m","max_output_tokens":4096,"input":[{"type":"tool_search_call","call_id":"s1","arguments":"{}","execution":"client"},{"type":"tool_search_output","call_id":"s1","tools":[{"type":"namespace","name":"a","tools":[{"type":"function","name":"b","parameters":{"type":"object"}}]}]},{"type":"tool_search_call","call_id":"s2","arguments":"{}","execution":"client"},{"type":"tool_search_output","call_id":"s2","tools":[{"type":"namespace","name":"a","tools":[{"type":"function","name":"c","parameters":{"type":"object"}}]}]}]}`, nil)
		require.NoError(t, err, target)
		require.Contains(t, string(raw), "a__b")
		require.Contains(t, string(raw), "a__c")
	}
}

func TestResponsesCustomHistoryUsesInput(t *testing.T) {
	raw, err := dispatchRequest(t, "openai", `{"model":"m","input":[{"type":"custom_tool_call","call_id":"c","name":"run","input":"line 1\n\"line 2\""}]}`, nil)
	require.NoError(t, err)
	require.JSONEq(t, `{"input":"line 1\n\"line 2\""}`, gjson.GetBytes(raw, "messages.0.tool_calls.0.function.arguments").String())
}

func TestGeminiHistoryLinksEveryToolResult(t *testing.T) {
	var request types.GeminiRequest
	require.NoError(t, json.Unmarshal([]byte(`{"generationConfig":{"maxOutputTokens":4096},"contents":[{"role":"model","parts":[{"functionCall":{"id":"a","name":"find","args":{"q":1}}},{"functionCall":{"id":"b","name":"find","args":{"q":2}}}]},{"role":"user","parts":[{"functionResponse":{"id":"b","name":"find","response":{"answer":2}}},{"functionResponse":{"id":"a","name":"find","response":{"answer":1}}}]}]}`), &request))
	chat, err := GeminiToOpenAIRequest(&request, "m")
	require.NoError(t, err)
	raw, err := json.Marshal(chat)
	require.NoError(t, err)
	require.Len(t, gjson.GetBytes(raw, "messages").Array(), 3)
	require.Equal(t, "b", gjson.GetBytes(raw, "messages.1.tool_call_id").String())
	require.Equal(t, "a", gjson.GetBytes(raw, "messages.2.tool_call_id").String())
	claude, err := GeminiToClaudeRequest(&request, "m")
	require.NoError(t, err)
	raw, err = json.Marshal(claude)
	require.NoError(t, err)
	require.Equal(t, "b", gjson.GetBytes(raw, "messages.1.content.0.tool_use_id").String())
	request.Contents[1].Parts[0].FunctionResponse.ID = ""
	_, err = GeminiToOpenAIRequest(&request, "m")
	require.True(t, types.IsUnsupportedProtocol(err), "同名并行调用不能猜测归属")
}

func TestGeminiNativeKeepsUnknownFieldsAndSchema(t *testing.T) {
	raw := []byte(`{"future":{"keep":true},"tools":[{"googleSearch":{}}],"contents":[{"role":"model","parts":[{"functionCall":{"id":"c","name":"f","args":{},"thoughtSignature":"real"},"future":42}]}],"generationConfig":{"candidateCount":2,"future":7}}`)
	var request types.GeminiRequest
	require.NoError(t, json.Unmarshal(raw, &request))
	got, err := togemini.NativeRequest(&request, false, true)
	require.NoError(t, err)
	require.JSONEq(t, string(raw), string(got))
}

func TestUnsupportedConversionAndInvalidReasoningRemainExplicit(t *testing.T) {
	for _, field := range []string{`"thinking":{"type":"enabled"}`, `"thinking":{"type":"enabled","budget_tokens":1024.5}`, `"thinking":{"type":"unknown"}`, `"thinking":[]`, `"output_config":{"effort":"future"}`} {
		err := ValidateClaudeConversion([]byte(`{"model":"m","max_tokens":4096,"messages":[],`+field+`}`), "openai")
		var invalid *types.InvalidRequestError
		require.ErrorAs(t, err, &invalid)
	}
	for _, fields := range []string{`"text":{"format":{"type":"json_schema","schema":{"type":"object"}}}`, `"input":[{"role":"user","content":[{"type":"input_audio","data":"AA"}]}]`} {
		_, err := dispatchRequest(t, "claude", `{"model":"m","max_output_tokens":4096,`+fields+`}`, nil)
		require.True(t, types.IsUnsupportedProtocol(err))
	}
	_, err := GeminiToClaudeRequest(&types.GeminiRequest{}, "unknown")
	require.True(t, types.IsUnsupportedProtocol(err), "缺失 Claude 输出预算应跳过该候选")
	for _, field := range []string{`"maxOutputTokens":0`, `"candidateCount":0`, `"maxOutputTokens":-1`} {
		var request types.GeminiRequest
		require.NoError(t, json.Unmarshal([]byte(`{"generationConfig":{`+field+`}}`), &request))
		_, err := GeminiToClaudeRequest(&request, "m")
		var invalid *types.InvalidRequestError
		require.ErrorAs(t, err, &invalid)
	}
	err = ValidateClaudeConversion([]byte(`{"system":[{"type":"image","source":{"type":"base64","data":"AQID","media_type":"image/png"}}]}`), "openai")
	require.True(t, types.IsUnsupportedProtocol(err))
}

func TestHistoryToolArgumentsAreClientErrorsAcrossTargets(t *testing.T) {
	for _, target := range []string{"openai", "claude", "gemini"} {
		for _, args := range []string{`null`, `[]`, `"scalar"`, `{}`} {
			body, err := json.Marshal(map[string]interface{}{"model": "m", "max_output_tokens": 4096, "input": []interface{}{map[string]interface{}{"type": "function_call", "call_id": "c", "name": "run", "arguments": args}}})
			require.NoError(t, err)
			_, err = dispatchRequest(t, target, string(body), nil)
			if args == `{}` {
				require.NoError(t, err)
			} else {
				var invalid *types.InvalidRequestError
				require.ErrorAs(t, err, &invalid)
			}
		}
		for _, choice := range []string{`"required"`, `{"type":"function","name":"missing"}`} {
			_, err := dispatchRequest(t, target, `{"model":"m","max_output_tokens":4096,"input":"hi","tool_choice":`+choice+`}`, nil)
			var invalid *types.InvalidRequestError
			require.ErrorAs(t, err, &invalid)
		}
	}
	for _, target := range []string{"openai", "responses", "gemini"} {
		for _, input := range []string{`null`, `[]`, `{}`} {
			err := ValidateClaudeConversion([]byte(`{"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"c","name":"run","input":`+input+`}]}]}`), target)
			if input == `{}` {
				require.NoError(t, err)
			} else {
				var invalid *types.InvalidRequestError
				require.ErrorAs(t, err, &invalid)
			}
		}
	}
}

func TestRequestMediaChecksMatchActualConversion(t *testing.T) {
	for _, block := range []string{`{"type":"image_url","image_url":{"url":"https://example.test/a.png"}}`, `{"type":"input_image","image_url":"https://example.test/a.png"}`} {
		err := ValidateClaudeConversion([]byte(`{"messages":[{"role":"user","content":[{"type":"text","text":"look"},`+block+`]}]}`), "gemini")
		require.True(t, types.IsUnsupportedProtocol(err), "%v", err)
	}
	err := ValidateClaudeConversion([]byte(`{"messages":[{"role":"assistant","content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"AQID"}}]}]}`), "responses")
	require.True(t, types.IsUnsupportedProtocol(err))
	for _, target := range []string{"openai", "claude", "gemini"} {
		_, err := dispatchRequest(t, target, `{"model":"m","max_output_tokens":4096,"input":[{"role":"user","content":[{"type":"input_text","text":"look"},{"type":"input_image"}]}]}`, nil)
		var invalid *types.InvalidRequestError
		require.ErrorAs(t, err, &invalid)
	}
}
