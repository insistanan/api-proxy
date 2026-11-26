package providers

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"insistanan/api-proxy/internal/dialogue/conversation"
	"insistanan/api-proxy/internal/dialogue/session"
	"insistanan/api-proxy/internal/protocol/types"
	"insistanan/api-proxy/internal/storage/sqlitedb"
)

func TestGeminiShadowSurvivesEvictionAndRestartButFollowsDeletion(t *testing.T) {
	ctx := context.Background()
	db, err := sqlitedb.Open(filepath.Join(t.TempDir(), "conversations.db"))
	require.NoError(t, err)
	defer db.Close()
	manager, err := session.NewPersistentSessionManagerWithDB(db, time.Hour, 0, 0)
	require.NoError(t, err)
	defer manager.Stop()
	registry, err := conversation.NewPersistentRegistryWithDB(db)
	require.NoError(t, err)
	defer registry.Stop()
	require.NoError(t, manager.BindRegistry(registry))
	record, err := registry.ObserveRequestWithError(ctx, conversation.Observation{APIKind: "messages", ConversationID: t.Name()})
	require.NoError(t, err)
	store := newGeminiShadowStore(2, 256)
	store.persistence = manager
	registry.RegisterCacheInvalidator("test-shadow", store.clearConversation)
	turn := GeminiShadowTurn{AssistantContent: map[string]any{"parts": []any{map[string]any{"text": strings.Repeat("正文", 256), "thoughtSignature": "signature"}}}}
	for range 70 {
		require.NoError(t, store.Record(ctx, "provider", record.ID, turn))
	}
	loaded, err := store.Get(ctx, "provider", record.ID)
	require.NoError(t, err)
	require.Len(t, loaded.Turns, 70, "不得再按 64 轮丢弃签名")
	require.Empty(t, store.sessions, "超过预算只淘汰缓存")
	other, err := store.Get(ctx, "other-provider", record.ID)
	require.NoError(t, err)
	require.Empty(t, other.Turns)
	store.clearConversation("")
	restarted := newGeminiShadowStore(2, 1<<20)
	restarted.persistence = manager
	loaded, err = restarted.Get(ctx, "provider", record.ID)
	require.NoError(t, err)
	require.Len(t, loaded.Turns, 70)
	lease := conversation.RequestLeaseFrom(registry.WithRequestLease(ctx, record.ID))
	require.NoError(t, registry.DeleteWithHistory(ctx, record.ID, manager))
	require.False(t, lease.Store(func() { t.Fatal("删除后不允许晚到回填") }))
	loaded, err = store.Get(ctx, "provider", record.ID)
	require.NoError(t, err)
	require.Empty(t, loaded.Turns)
	require.Error(t, store.Record(ctx, "provider", record.ID, turn), "外键禁止孤立签名")
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	_, err = store.Get(canceled, "provider", "uncached")
	require.ErrorIs(t, err, context.Canceled)
}

func TestGeminiShadowSaveFailurePreventsSuccessfulTerminal(t *testing.T) {
	want := errors.New("签名存储不可用")
	provider := &GeminiProvider{persistShadow: func(GeminiShadowTurn) error { return want }}
	body := `{"candidates":[{"content":{"parts":[{"text":"answer","thoughtSignature":"signed"}]},"finishReason":"STOP"}]}`
	_, err := provider.ConvertToClaudeResponse(&types.ProviderResponse{Body: []byte(body)})
	require.ErrorIs(t, err, want)
	events, failures, err := provider.HandleStreamResponseCtx(context.Background(), io.NopCloser(strings.NewReader("data: "+body+"\n\n")))
	require.NoError(t, err)
	var delivered strings.Builder
	for event := range events {
		delivered.WriteString(event)
	}
	require.NotContains(t, delivered.String(), `"type":"message_stop"`)
	require.ErrorIs(t, <-failures, want)
}
