package providers

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"insistanan/api-proxy/internal/dialogue/conversation"
	"insistanan/api-proxy/internal/dialogue/session"
	"insistanan/api-proxy/internal/infra/config"
	"insistanan/api-proxy/internal/protocol/types"
)

func TestProtocolCachesFollowDeletionAndRejectLateWrites(t *testing.T) {
	for _, all := range []bool{false, true} {
		t.Run(map[bool]string{false: "单个删除", true: "全部删除"}[all], func(t *testing.T) {
			registry := conversation.NewRegistry()
			defer registry.Stop()
			BindConversationCaches(registry)
			record, err := registry.ObserveRequestWithError(context.Background(), conversation.Observation{APIKind: "messages", ConversationID: "cache"})
			require.NoError(t, err)
			ctx := registry.WithRequestLease(context.Background(), record.ID)
			scope := ReasoningCacheScope(record.ID, &config.UpstreamConfig{ID: "up", ServiceType: "openai"}, "model")
			write := func() {
				session.DefaultResponseChainManager().Set(record.ID, session.ResponseChainState{ResponseID: "response"})
				defaultGeminiShadowStore.Record("provider", record.ID, GeminiShadowTurn{AssistantContent: map[string]interface{}{"parts": []interface{}{map[string]interface{}{"text": "private"}}}})
				storeReasoning("answer", "private reasoning", scope)
			}
			lease := conversation.RequestLeaseFrom(ctx)
			require.True(t, lease.Store(write))
			require.NotEmpty(t, defaultGeminiShadowStore.Get("provider", record.ID).Turns)
			if all {
				require.NoError(t, registry.DeleteWithHistory(ctx, "", &session.SessionManager{}))
			} else {
				require.NoError(t, registry.DeleteWithHistory(ctx, record.ID, &session.SessionManager{}))
			}
			require.False(t, lease.Store(write))
			require.NoError(t, CacheClaudeResponseReasoning(ctx, &types.ClaudeResponse{Role: "assistant", Content: []types.ClaudeContent{{Type: "thinking", Thinking: "late"}, {Type: "text", Text: "answer"}}}, scope))
			_, exists := session.DefaultResponseChainManager().Get(record.ID)
			require.False(t, exists)
			require.Empty(t, defaultGeminiShadowStore.Get("provider", record.ID).Turns)
			require.Empty(t, reasoningContentForAssistantMessage(types.OpenAIMessage{Role: "assistant", Content: "answer"}, scope))
		})
	}
}
func TestProtocolCachesRejectWritesAfterRegistryStop(t *testing.T) {
	registry := conversation.NewRegistry()
	BindConversationCaches(registry)
	record, err := registry.ObserveRequestWithError(context.Background(), conversation.Observation{APIKind: "messages"})
	require.NoError(t, err)
	lease := conversation.RequestLeaseFrom(registry.WithRequestLease(context.Background(), record.ID))
	require.NoError(t, registry.Stop())
	require.False(t, lease.Store(func() { t.Fatal("停机后不应回填") }))
}
