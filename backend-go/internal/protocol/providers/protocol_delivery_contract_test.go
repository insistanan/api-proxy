package providers

import (
	"context"
	"encoding/json"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"insistanan/api-proxy/internal/dialogue/session"
	"insistanan/api-proxy/internal/infra/config"
	"insistanan/api-proxy/internal/protocol/converters"
	"insistanan/api-proxy/internal/protocol/types"
)

func TestResponsesSessionToolsReachNormalAndStreamReplies(t *testing.T) {
	manager, err := session.NewPersistentSessionManager(filepath.Join(t.TempDir(), "session.db"), time.Hour, 0, 0)
	require.NoError(t, err)
	t.Cleanup(manager.Stop)
	sess, err := manager.GetOrCreateSessionForConversation(context.Background(), "", t.Name())
	require.NoError(t, err)
	definitions := []map[string]interface{}{
		{"type": "custom", "name": "run"},
		{"type": "namespace", "name": "fs", "tools": []map[string]interface{}{{"type": "function", "name": "read", "parameters": map[string]interface{}{"type": "object"}}}},
	}
	require.NoError(t, commitFixtureTurn(manager, sess, []types.ResponsesItem{{Type: "tool_search_output", CallID: "discovery", Tools: definitions}}, 0, false, "previous"))
	original := []byte(`{"model":"m","max_output_tokens":4096,"previous_response_id":"previous","input":"next"}`)
	outcome, err := (&ResponsesProvider{SessionManager: manager}).BuildRequest(RequestInput{Ctx: context.Background(), Body: original, ConversationID: t.Name()}, &config.UpstreamConfig{ServiceType: "openai", BaseURL: "https://example.test"}, "key")
	require.NoError(t, err)
	require.NotNil(t, outcome.ResponseTools)
	require.NoError(t, outcome.Request.Body.Close())
	body := []byte(`{"choices":[{"message":{"tool_calls":[{"id":"c","function":{"name":"run","arguments":"{\"input\":\"pwd\"}"}},{"id":"d","function":{"name":"fs__read","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`)
	response, err := converters.ConvertUpstreamResponseToResponses("openai", original, body, "", outcome.ResponseTools)
	require.NoError(t, err)
	require.Equal(t, "custom_tool_call", response.Output[0].Type)
	require.Equal(t, "fs", response.Output[1].Namespace)
	var state any
	_, err = converters.ConvertUpstreamStreamLineToResponses(context.Background(), "openai", "m", original, []byte(`data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"c","function":{"name":"run","arguments":"{\"input\":\"pwd\"}"}}]},"finish_reason":"tool_calls"}]}`), &state, outcome.ResponseTools)
	require.NoError(t, err)
	events, err := converters.FinishUpstreamStreamToResponses("openai", original, &state)
	require.NoError(t, err)
	require.Contains(t, strings.Join(events, ""), `"custom_tool_call"`)
	plain, err := converters.ConvertUpstreamResponseToResponses("openai", original, body, "")
	require.NoError(t, err)
	require.Equal(t, "function_call", plain.Output[0].Type, "映射不能泄漏到另一请求")
}

func TestProviderResponsesKeepInvalidShapesAndRefusalExplicit(t *testing.T) {
	for _, body := range []string{`null`, `{}`, `{"choices":[]}`, `{"choices":[{}]}`, `{"choices":[{"message":{},"finish_reason":"unknown"}]}`} {
		_, err := (&OpenAIProvider{}).ConvertToClaudeResponse(&types.ProviderResponse{Body: []byte(body)})
		require.Error(t, err, body)
	}
	for _, body := range []string{`{"candidates":[null]}`, `{"candidates":[{}]}`, `{"candidates":[{"finishReason":null}]}`} {
		_, err := (&GeminiProvider{}).ConvertToClaudeResponse(&types.ProviderResponse{Body: []byte(body)})
		require.Error(t, err, body)
	}
	for _, reason := range []string{"SAFETY", "MODEL_ARMOR", "IMAGE_PROHIBITED_CONTENT"} {
		response, err := (&GeminiProvider{}).ConvertToClaudeResponse(&types.ProviderResponse{Body: []byte(`{"candidates":[{"finishReason":"` + reason + `"}],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":1}}`)})
		require.NoError(t, err)
		require.Equal(t, "refusal", response.StopReason)
		require.Empty(t, response.Content)
		require.NotNil(t, response.Usage)
		require.Equal(t, 10, response.Usage.InputTokens)
	}
	for _, reason := range []string{"content_filter", "max_output_tokens", "unknown"} {
		body := `{"status":"incomplete","incomplete_details":{"reason":"` + reason + `"},"output":[{"type":"message","content":[{"type":"output_text","text":"partial"}]}]}`
		var response types.ResponsesResponse
		require.NoError(t, json.Unmarshal([]byte(body), &response))
		converted, err := responsesResponseToClaude(&response)
		state := newResponsesToClaudeStreamState()
		state.processLine(`data: {"type":"response.incomplete","response":` + body + `}`)
		if reason == "unknown" {
			require.Error(t, err)
			require.True(t, state.failed)
		} else {
			require.NoError(t, err)
			require.Equal(t, converted.StopReason, state.stopReason)
			if reason == "content_filter" {
				require.Equal(t, "refusal", converted.StopReason)
			}
		}
	}
}

func TestResponsesRecoveryKeepsItemIDsBeforeIndexes(t *testing.T) {
	state := newResponsesToClaudeStreamState()
	state.processLine(`data: {"type":"response.output_text.delta","item_id":"a","content_index":0,"delta":"A"}`)
	state.processLine(`data: {"type":"response.output_text.delta","item_id":"b","content_index":0,"delta":"B"}`)
	state.processLine(`data: {"type":"response.completed","response":{"status":"completed","output":[{"id":"a","type":"message","content":[{"type":"output_text","text":"A"}]},{"id":"b","type":"message","content":[{"type":"output_text","text":"B!"}]}]}}`)
	require.False(t, state.failed, state.failedMessage)
	require.Equal(t, "AB!", state.assistantText.String())
}

func TestGeminiShadowUsesConversationCredentialAndMappedModel(t *testing.T) {
	upstream := &config.UpstreamConfig{ServiceType: "gemini", BaseURL: "https://example.test", DefaultModel: "model-a"}
	input := RequestInput{Ctx: context.Background(), ConversationID: t.Name(), Body: []byte(`{"model":"client","max_tokens":2048,"messages":[{"role":"assistant","content":"answer"}]}`)}
	owner := &GeminiProvider{}
	_, err := owner.BuildRequest(input, upstream, "key-a")
	require.NoError(t, err)
	require.NoError(t, defaultGeminiShadowStore.Record(input.Ctx, owner.shadowProviderID, owner.shadowSessionID, GeminiShadowTurn{AssistantContent: map[string]interface{}{"parts": []interface{}{map[string]interface{}{"text": "answer", "thoughtSignature": "signed"}}}}))
	for _, tc := range []struct {
		name, key, model, conversation string
		strip, signed                  bool
	}{
		{"same", "key-a", "model-a", input.ConversationID, false, true},
		{"credential", "key-b", "model-a", input.ConversationID, false, false},
		{"model", "key-a", "model-b", input.ConversationID, false, false},
		{"no-conversation", "key-a", "model-a", "", false, false},
		{"strip-text-signature", "key-a", "model-a", input.ConversationID, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			candidate, request := *upstream, input
			candidate.DefaultModel, candidate.StripThoughtSignature = tc.model, tc.strip
			request.ConversationID = tc.conversation
			outcome, err := (&GeminiProvider{}).BuildRequest(request, &candidate, tc.key)
			require.NoError(t, err)
			body, err := io.ReadAll(outcome.Request.Body)
			require.NoError(t, err)
			require.NoError(t, outcome.Request.Body.Close())
			require.Equal(t, tc.signed, strings.Contains(string(body), "signed"))
			require.Contains(t, string(body), "answer")
		})
	}
}

func TestResponsesRecoveryTracksEachItemAndContentPart(t *testing.T) {
	state := newResponsesToClaudeStreamState()
	state.processLine(`data: {"type":"response.output_text.delta","item_id":"a","output_index":0,"content_index":0,"delta":"A"}`)
	events := state.processLine(`data: {"type":"response.completed","response":{"status":"completed","output":[{"id":"a","type":"message","role":"assistant","content":[{"type":"output_text","text":"A+"},{"type":"output_text","text":"B"}]},{"id":"b","type":"message","role":"assistant","content":[{"type":"output_text","text":"C"}]},{"id":"r1","type":"reasoning","summary":[{"type":"summary_text","text":"R1"}]},{"id":"r2","type":"reasoning","summary":[{"type":"summary_text","text":"R2"}]}]}}`)
	require.False(t, state.failed, state.failedMessage)
	require.Equal(t, "A+BC", state.assistantText.String())
	require.Equal(t, "R1R2", state.reasoning.String())
	require.Len(t, state.completedOutput, 4)
	require.NotContains(t, strings.Join(events, ""), `"text":"A"`)
}

func TestResponsesArgumentsDoneDoesNotExecuteUnfinishedTool(t *testing.T) {
	state := newResponsesToClaudeStreamState()
	state.processLine(`data: {"type":"response.output_item.added","output_index":0,"item":{"id":"i","type":"function_call","call_id":"c","name":"run","arguments":""}}`)
	events := state.processLine(`data: {"type":"response.function_call_arguments.done","output_index":0,"item_id":"i","arguments":"{}"}`)
	require.NotContains(t, strings.Join(events, ""), `"type":"tool_use"`)
	events = state.processLine(`data: {"type":"response.output_item.done","output_index":0,"item":{"id":"i","type":"function_call","status":"incomplete","call_id":"c","name":"run","arguments":"{}"}}`)
	require.NotContains(t, strings.Join(events, ""), `"type":"tool_use"`)
	events = state.processLine(`data: {"type":"response.incomplete","response":{"status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},"output":[]}}`)
	require.NotContains(t, strings.Join(events, ""), `"type":"tool_use"`)
	require.Equal(t, "max_tokens", state.stopReason)
}

func TestResponsesInvalidToolArgumentsFailExplicitly(t *testing.T) {
	for _, args := range []string{"{", "[]", "null", ""} {
		state := newResponsesToClaudeStreamState()
		payload, err := json.Marshal(map[string]interface{}{"type": "response.output_item.done", "output_index": 0, "item": map[string]interface{}{"id": "i", "type": "function_call", "status": "completed", "call_id": "c", "name": "run", "arguments": args}})
		require.NoError(t, err)
		events := state.processLine("data: " + string(payload))
		require.True(t, state.failed, args)
		require.NotEmpty(t, state.failedMessage)
		require.NotContains(t, strings.Join(events, ""), `"type":"tool_use"`)
		require.Empty(t, state.finish())
	}
}

func TestGeminiShadowPreservesCurrentHistory(t *testing.T) {
	snapshot := GeminiShadowSnapshot{Turns: []GeminiShadowTurn{{AssistantContent: map[string]interface{}{"parts": []interface{}{map[string]interface{}{"text": "A", "thoughtSignature": "signed"}}}}}}
	for _, text := range []string{"A", "B"} {
		converted, err := (&GeminiProvider{}).convertMessages([]types.ClaudeMessage{{Role: "assistant", Content: text}}, snapshot)
		require.NoError(t, err)
		raw, err := json.Marshal(converted)
		require.NoError(t, err)
		require.Contains(t, string(raw), `"text":"`+text+`"`)
		if text == "A" {
			require.Contains(t, string(raw), "signed")
		} else {
			require.NotContains(t, string(raw), "signed")
		}
	}
}

func TestGeminiStreamDoesNotDeduplicateIndependentCalls(t *testing.T) {
	state := newGeminiToClaudeStreamState()
	state.ProcessChunk(`{"candidates":[{"content":{"parts":[{"text":"answer","thoughtSignature":"text-signed"},{"functionCall":{"name":"run","args":{}}},{"functionCall":{"name":"run","args":{}}},{"functionCall":{"id":"explicit","name":"run","args":{}}}]}}]}`)
	state.ProcessChunk(`{"candidates":[{"content":{"parts":[{"functionCall":{"id":"explicit","name":"run","args":{}}},{"functionCall":{"name":"run","args":{}}}]},"finishReason":"STOP"}]}`)
	require.NoError(t, state.streamError)
	require.Len(t, state.toolUseParts, 4)
	require.NotEqual(t, state.toolUseParts[0].ID, state.toolUseParts[1].ID)
	raw, err := json.Marshal(state.shadowParts)
	require.NoError(t, err)
	require.Contains(t, string(raw), "text-signed")
}
