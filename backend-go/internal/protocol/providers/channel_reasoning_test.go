package providers

import (
	"bytes"
	"context"
	"io"
	"path/filepath"
	"testing"
	"time"

	"github.com/insistanan/api-proxy/internal/dialogue/session"
	"github.com/insistanan/api-proxy/internal/infra/config"
	"github.com/insistanan/api-proxy/internal/protocol/converters"
	"github.com/insistanan/api-proxy/internal/protocol/types"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestChannelReasoningOverridesClientAcrossProviderProtocols(t *testing.T) {
	messageBody := []byte(`{"model":"alias","max_tokens":20000,"thinking":{"type":"disabled"},"output_config":{"effort":"future"},"reasoning_effort":"future","reasoning":{"effort":"future"},"generationConfig":{"thinkingConfig":{"thinkingBudget":0}},"messages":[{"role":"user","content":"hello"}]}`)
	responseBody := []byte(`{"model":"alias","max_output_tokens":20000,"input":"hello","reasoning":{"effort":"future","summary":"auto"},"thinking":{"type":"enabled","budget_tokens":999999},"reasoning_effort":"future"}`)
	manager, err := session.NewPersistentSessionManager(filepath.Join(t.TempDir(), "responses.db"), time.Hour, 0, 0)
	require.NoError(t, err)
	t.Cleanup(manager.Stop)
	responsesProvider := &ResponsesProvider{SessionManager: manager}
	for _, tc := range []struct {
		name, protocol, model, path string
		body                        []byte
		build                       requestBuildFunc
	}{
		{"Claude原生", "claude", "claude-opus-5-5", "output_config.effort", messageBody, (&ClaudeProvider{}).BuildRequest},
		{"Messages转Chat", "openai", "gpt-model", "reasoning_effort", messageBody, (&OpenAIProvider{}).BuildRequest},
		{"Messages转Responses", "responses", "gpt-model", "reasoning.effort", messageBody, (&MessagesResponsesProvider{}).BuildRequest},
		{"Responses原生", "responses", "gpt-model", "reasoning.effort", responseBody, (&ResponsesProvider{}).BuildRequest},
		{"Responses转Chat", "openai", "gpt-model", "reasoning_effort", responseBody, responsesProvider.BuildRequest},
		{"Responses转Claude", "claude", "claude-opus-5-5", "output_config.effort", responseBody, responsesProvider.BuildRequest},
		{"Responses转Gemini", "gemini", "gemini-2.5-flash", "generationConfig.thinkingConfig.thinkingBudget", responseBody, responsesProvider.BuildRequest},
		{"Messages转Gemini", "gemini", "gemini-2.5-flash", "generationConfig.thinkingConfig.thinkingBudget", messageBody, (&GeminiProvider{}).BuildRequest},
	} {
		for _, effort := range []string{"auto", "none", "low", "high", "xhigh", "max"} {
			t.Run(tc.name+"/"+effort, func(t *testing.T) {
				before := bytes.Clone(tc.body)
				up := &config.UpstreamConfig{ID: "test", Name: "test", BaseURL: "https://example.invalid", ServiceType: tc.protocol, DefaultModel: tc.model, ReasoningEffort: effort, DisablePromptCacheInjection: true}
				outcome, err := tc.build(RequestInput{Ctx: context.Background(), Method: "POST", Path: "/v1/messages", Body: tc.body}, up, "fixture")
				require.NoError(t, err)
				defer outcome.Request.Body.Close()
				body, err := io.ReadAll(outcome.Request.Body)
				require.NoError(t, err)
				require.Equal(t, int64(len(body)), outcome.Request.ContentLength)
				replay, err := outcome.Request.GetBody()
				require.NoError(t, err)
				defer replay.Close()
				replayed, err := io.ReadAll(replay)
				require.NoError(t, err)
				require.Equal(t, body, replayed)
				require.Equal(t, before, tc.body, "构建一个候选不能修改下一个候选的输入")
				if effort == "auto" {
					for _, path := range []string{"thinking", "reasoning_effort", "reasoning", "output_config.effort", "generationConfig.thinkingConfig"} {
						require.False(t, gjson.GetBytes(body, path).Exists(), "自动思考不应发送 %s", path)
					}
				} else if tc.protocol == "claude" && effort == "none" {
					require.Equal(t, "disabled", gjson.GetBytes(body, "thinking.type").String())
					require.False(t, gjson.GetBytes(body, tc.path).Exists())
				} else if tc.protocol == "gemini" {
					require.Equal(t, int64(converters.ReasoningBudgetTokens(effort, 0)), gjson.GetBytes(body, tc.path).Int())
				} else {
					require.Equal(t, effort, gjson.GetBytes(body, tc.path).String())
				}
				if tc.protocol != "claude" {
					require.False(t, gjson.GetBytes(body, "thinking").Exists())
					require.False(t, gjson.GetBytes(body, "output_config").Exists())
				}
				if tc.protocol != "responses" {
					require.False(t, gjson.GetBytes(body, "reasoning").Exists())
				}
				if tc.protocol != "openai" {
					require.False(t, gjson.GetBytes(body, "reasoning_effort").Exists())
				}
			})
		}
	}
}

func TestChannelReasoningPreservesNativeHistoryAndUnrelatedOptions(t *testing.T) {
	body := []byte(`{"model":"alias","input":[{"type":"reasoning","encrypted_content":"opaque-original","summary":[]},{"role":"user","content":"next"}],"previous_response_id":"resp-original","reasoning":{"effort":"low","summary":"auto"},"metadata":{"number":9007199254740993}}`)
	up := &config.UpstreamConfig{BaseURL: "https://example.invalid", ServiceType: "responses", ReasoningEffort: "max"}
	manager, err := session.NewPersistentSessionManager(filepath.Join(t.TempDir(), "responses.db"), time.Hour, 0, 0)
	require.NoError(t, err)
	t.Cleanup(manager.Stop)
	sess, err := manager.GetOrCreateSessionForConversation(context.Background(), "", "conv-owner")
	require.NoError(t, err)
	require.NoError(t, commitFixtureTurn(manager, sess, []types.ResponsesItem{
		{Type: "text", Content: "first question"},
		{Type: "message", Role: "assistant", Content: "first answer"},
		{Type: "text", Content: "second question"},
		{Type: "message", Role: "assistant", Content: "second answer"},
	}, 10, false, "resp-original", up.RequestIdentity("fixture").Fingerprint()))
	outcome, err := (&ResponsesProvider{SessionManager: manager}).BuildRequest(RequestInput{Ctx: context.Background(), Body: body, ConversationID: "conv-owner"}, up, "fixture")
	require.NoError(t, err)
	defer outcome.Request.Body.Close()
	actual, err := io.ReadAll(outcome.Request.Body)
	require.NoError(t, err)
	for _, path := range []string{"input", "previous_response_id", "reasoning.summary", "metadata"} {
		require.Equal(t, gjson.GetBytes(body, path).Raw, gjson.GetBytes(actual, path).Raw, path)
	}
	require.Equal(t, "max", gjson.GetBytes(actual, "reasoning.effort").String())
	up.ReasoningEffort = "auto"
	outcome, err = (&ResponsesProvider{SessionManager: manager}).BuildRequest(RequestInput{Ctx: context.Background(), Body: body, ConversationID: "conv-owner"}, up, "fixture")
	require.NoError(t, err)
	defer outcome.Request.Body.Close()
	actual, err = io.ReadAll(outcome.Request.Body)
	require.NoError(t, err)
	require.False(t, gjson.GetBytes(actual, "reasoning").Exists())
	for _, path := range []string{"input", "previous_response_id", "metadata"} {
		require.Equal(t, gjson.GetBytes(body, path).Raw, gjson.GetBytes(actual, path).Raw, path)
	}

	claudeBody := []byte(`{"model":"alias","max_tokens":4000,"thinking":{"type":"enabled","budget_tokens":2048},"messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"original","signature":"original-signature"},{"type":"text","text":"answer"}]},{"role":"user","content":"next"}]}`)
	up = &config.UpstreamConfig{BaseURL: "https://example.invalid", ServiceType: "claude", DefaultModel: "real-model", ReasoningEffort: "none", DisablePromptCacheInjection: true}
	outcome, err = (&ClaudeProvider{}).BuildRequest(RequestInput{Ctx: context.Background(), Method: "POST", Path: "/v1/messages", Body: claudeBody}, up, "fixture")
	require.NoError(t, err)
	defer outcome.Request.Body.Close()
	actual, err = io.ReadAll(outcome.Request.Body)
	require.NoError(t, err)
	require.JSONEq(t, gjson.GetBytes(claudeBody, "messages").Raw, gjson.GetBytes(actual, "messages").Raw)
	require.Equal(t, "real-model", gjson.GetBytes(actual, "model").String())
	require.Equal(t, "disabled", gjson.GetBytes(actual, "thinking.type").String())
}

func TestChannelReasoningOff_DoesNotRequireMissingThinkingHistory(t *testing.T) {
	manager, err := session.NewPersistentSessionManager(filepath.Join(t.TempDir(), "responses.db"), time.Hour, 0, 0)
	require.NoError(t, err)
	t.Cleanup(manager.Stop)
	for _, tc := range []struct {
		name  string
		body  []byte
		build requestBuildFunc
	}{
		{"Messages", []byte(`{"model":"alias","max_tokens":4000,"messages":[{"role":"assistant","content":"previous answer"},{"role":"user","content":"next"}]}`), (&OpenAIProvider{}).BuildRequest},
		{"Responses", []byte(`{"model":"alias","input":[{"type":"message","role":"assistant","content":"previous answer"},{"type":"message","role":"user","content":"next"}]}`), (&ResponsesProvider{SessionManager: manager}).BuildRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			up := &config.UpstreamConfig{BaseURL: "https://example.invalid", ServiceType: "openai", RequireReasoningContent: true, ReasoningEffort: "high"}
			input := RequestInput{Ctx: context.Background(), Body: tc.body}
			_, err := tc.build(input, up, "fixture")
			var historyErr *types.ReasoningHistoryError
			require.ErrorAs(t, err, &historyErr)
			up.ReasoningEffort = "none"
			outcome, err := tc.build(input, up, "fixture")
			require.NoError(t, err)
			actual, err := io.ReadAll(outcome.Request.Body)
			require.NoError(t, err)
			require.NoError(t, outcome.Request.Body.Close())
			require.Equal(t, "none", gjson.GetBytes(actual, "reasoning_effort").String())
			require.Contains(t, string(actual), "previous answer")
			require.False(t, gjson.GetBytes(actual, "messages.0.reasoning_content").Exists())
			require.True(t, up.RequireReasoningContent, "不应改写其他候选或持久化的能力配置")
		})
	}
}

func TestGeminiThinkingUsesMappedModelAndRejectsUnavailableOff(t *testing.T) {
	body := []byte(`{"model":"alias","max_tokens":4000,"output_config":{"effort":"low"},"messages":[{"role":"user","content":"hello"}]}`)
	up := &config.UpstreamConfig{BaseURL: "https://example.invalid", ServiceType: "gemini", ModelMapping: map[string][]string{"alias": {"gemini-3-pro"}}}
	provider := &GeminiProvider{}
	outcome, err := provider.BuildRequest(RequestInput{Ctx: context.Background(), Body: body}, up, "fixture")
	require.NoError(t, err)
	defer outcome.Request.Body.Close()
	actual, err := io.ReadAll(outcome.Request.Body)
	require.NoError(t, err)
	require.Contains(t, outcome.Request.URL.Path, "gemini-3-pro")
	require.Equal(t, "low", gjson.GetBytes(actual, "generationConfig.thinkingConfig.thinkingLevel").String())
	require.False(t, gjson.GetBytes(actual, "generationConfig.thinkingConfig.thinkingBudget").Exists())
	up.ReasoningEffort = "auto"
	outcome, err = provider.BuildRequest(RequestInput{Ctx: context.Background(), Body: body}, up, "fixture")
	require.NoError(t, err)
	defer outcome.Request.Body.Close()
	actual, err = io.ReadAll(outcome.Request.Body)
	require.NoError(t, err)
	require.False(t, gjson.GetBytes(actual, "generationConfig.thinkingConfig").Exists())
	up.ReasoningEffort = "none"
	outcome, err = provider.BuildRequest(RequestInput{Ctx: context.Background(), Body: body}, up, "fixture")
	if outcome != nil && outcome.Request != nil {
		defer outcome.Request.Body.Close()
	}
	require.True(t, types.IsUnsupportedProtocol(err), "%v", err)
}
