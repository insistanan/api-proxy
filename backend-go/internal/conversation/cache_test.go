package conversation

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func persistentTestRegistry(t *testing.T) *Registry {
	t.Helper()
	r, err := NewPersistentRegistry(filepath.Join(t.TempDir(), "conversations.db"))
	require.NoError(t, err)
	t.Cleanup(r.Stop)
	return r
}

func TestCacheEvictionPreservesHistoryAliasesAndRouting(t *testing.T) {
	r := persistentTestRegistry(t)
	r.maxCachedRecords = 2
	first, err := r.ObserveRequestWithError(Observation{APIKind: "responses", ConversationID: "original", Prompts: []string{"第一轮"}})
	require.NoError(t, err)
	r.MarkComplete(first.ID, "responses")
	_, err = r.SetName(first.ID, "保留历史")
	require.NoError(t, err)
	_, err = r.SetRouteOverride(first.ID, "responses", 3, "固定渠道")
	require.NoError(t, err)
	require.NoError(t, r.AssociateExternalID(first.ID, "responses", "resp-old"))
	for i := 0; i < 8; i++ {
		rec := r.ObserveRequest(Observation{APIKind: "responses", ConversationID: fmt.Sprint(i)})
		r.MarkComplete(rec.ID, "responses")
	}
	require.LessOrEqual(t, len(r.records), 2)
	require.NotContains(t, r.records, first.ID)
	continued, err := r.ObserveRequestWithError(Observation{APIKind: "responses", Identity: Identity{ExplicitID: "resp-old", Source: "previous_response"}, Prompts: []string{"第二轮"}})
	require.NoError(t, err)
	require.Equal(t, first.ID, continued.ID)
	require.EqualValues(t, 2, continued.RequestCount)
	require.Equal(t, "保留历史", continued.Name)
	require.Equal(t, 3, continued.RouteOverride.ChannelIndex)
	require.Equal(t, []string{"第一轮", "第二轮"}, continued.Prompts)
	r.MarkComplete(first.ID, "responses")
	page, err := r.QueryPage(ListOptions{Page: 1, PageSize: 3})
	require.NoError(t, err)
	require.Equal(t, 9, page.Total)
	require.Len(t, page.Conversations, 3)
	require.LessOrEqual(t, len(r.records), 2)
}

func TestCacheByteBudgetPreservesCompleteLargePrompts(t *testing.T) {
	r := persistentTestRegistry(t)
	r.maxCacheBytes = 2048
	prompt := strings.Repeat("完整输入", 10000)
	rec := r.ObserveRequest(Observation{APIKind: "messages", ConversationID: "large", FirstPrompt: prompt})
	require.Contains(t, r.records, rec.ID, "发送中的大记录不能淘汰")
	r.MarkComplete(rec.ID, "messages")
	require.Empty(t, r.records)
	loaded, err := r.Lookup(rec.ID)
	require.NoError(t, err)
	require.Equal(t, prompt, loaded.Prompts[0])
	require.Empty(t, r.records, "超预算正文只供当前调用使用")
}

func TestCachePinsAllInflightRequests(t *testing.T) {
	r := persistentTestRegistry(t)
	r.maxCachedRecords = 1
	ids := make([]string, 0)
	for i := 0; i < 4; i++ {
		rec := r.ObserveRequest(Observation{APIKind: "messages", ConversationID: fmt.Sprint(i)})
		ids = append(ids, rec.ID)
	}
	require.Len(t, r.records, 4)
	for _, id := range ids {
		r.MarkComplete(id, "messages")
	}
	require.Len(t, r.records, 1)
	for _, id := range ids {
		rec, err := r.Lookup(id)
		require.NoError(t, err)
		require.Zero(t, rec.ActiveRequests)
		require.False(t, rec.IsSending)
	}
}

func TestColdFrontierStillDetectsAmbiguousHistory(t *testing.T) {
	r := persistentTestRegistry(t)
	r.maxCachedRecords = 0
	obs := Observation{APIKind: "messages", Identity: Identity{ClientFamily: "cursor", ScopeID: "project"}, Transcript: Transcript{PrefixHashes: []string{"h1"}}}
	for i := 0; i < 2; i++ {
		rec := r.ObserveRequest(obs)
		r.MarkComplete(rec.ID, "messages")
	}
	require.Empty(t, r.records)
	obs.Transcript.PrefixHashes = []string{"h1", "h2"}
	rec := r.ObserveRequest(obs)
	require.Equal(t, "ambiguous_history", rec.IdentitySource)
}

func TestStartupPurgesExpiredRowsBeforeDecodingAndLoadsNothing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "conversations.db")
	r, err := NewPersistentRegistry(path)
	require.NoError(t, err)
	expired := r.ObserveRequest(Observation{APIKind: "messages", ConversationID: "expired"})
	r.MarkComplete(expired.ID, "messages")
	valid := r.ObserveRequest(Observation{APIKind: "messages", ConversationID: "valid", FirstPrompt: "仍然完整"})
	r.MarkComplete(valid.ID, "messages")
	_, err = r.store.db.Exec(`UPDATE conversations SET last_seen_at = ?, prompts_json = 'invalid-json' WHERE id = ?`, time.Now().Add(-8*24*time.Hour).Unix(), expired.ID)
	require.NoError(t, err)
	r.Stop()
	restored, err := NewPersistentRegistry(path)
	require.NoError(t, err)
	defer restored.Stop()
	require.Empty(t, restored.records)
	require.Empty(t, restored.identityIndex)
	require.Empty(t, restored.frontierIndex)
	got, err := restored.Lookup(valid.ID)
	require.NoError(t, err)
	require.Equal(t, "仍然完整", got.FirstPrompt)
	got, err = restored.Lookup(expired.ID)
	require.NoError(t, err)
	require.Nil(t, got)
}

func TestPagedQuerySearchesColdPromptsAndUsesLiveStatus(t *testing.T) {
	r := persistentTestRegistry(t)
	r.maxCachedRecords = 0
	first := r.ObserveRequest(Observation{APIKind: "messages", ConversationID: "one", Prompts: []string{"第一条", "查找第二条"}})
	r.MarkComplete(first.ID, "messages")
	active := r.ObserveRequest(Observation{APIKind: "responses", ConversationID: "two", FirstPrompt: "正在发送"})
	page, err := r.QueryPage(ListOptions{Page: 1, PageSize: 10, Query: "查找第二条"})
	require.NoError(t, err)
	require.Equal(t, 1, page.Total)
	require.Equal(t, first.ID, page.Conversations[0].ID)
	page, err = r.QueryPage(ListOptions{Page: 1, PageSize: 10, Status: "active"})
	require.NoError(t, err)
	require.Equal(t, 1, page.Total)
	require.Equal(t, active.ID, page.Conversations[0].ID)
	require.True(t, page.Conversations[0].IsSending)
	r.MarkComplete(active.ID, "responses")
	page, err = r.QueryPage(ListOptions{Page: 1, PageSize: 10, Status: "active"})
	require.NoError(t, err)
	require.Zero(t, page.Total)
}

func TestColdDatabaseErrorsAreReturned(t *testing.T) {
	r := persistentTestRegistry(t)
	require.NoError(t, r.store.db.Close())
	_, err := r.ObserveRequestWithError(Observation{APIKind: "messages", ConversationID: "unavailable"})
	require.Error(t, err)
	_, err = r.Lookup("missing")
	require.Error(t, err)
	_, err = r.QueryPage(ListOptions{Page: 1, PageSize: 10})
	require.Error(t, err)
	require.Empty(t, r.records)
}

func TestQueryPageBeyondRangeDoesNotOverflow(t *testing.T) {
	for _, persistent := range []bool{false, true} {
		t.Run(fmt.Sprint(persistent), func(t *testing.T) {
			var r *Registry
			if persistent {
				r = persistentTestRegistry(t)
			} else {
				r = NewRegistry()
				t.Cleanup(r.Stop)
			}
			_, err := r.ObserveRequestWithError(Observation{APIKind: "messages", ConversationID: "one"})
			require.NoError(t, err)
			page, err := r.QueryPage(ListOptions{Page: int(^uint(0) >> 1), PageSize: 100})
			require.NoError(t, err)
			require.Equal(t, 1, page.Total)
			require.Empty(t, page.Conversations)
		})
	}
}

func BenchmarkPersistentRegistryStartup(b *testing.B) {
	for _, count := range []int{0, 1000} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			path := filepath.Join(b.TempDir(), "conversations.db")
			store, err := NewSQLiteStore(path)
			require.NoError(b, err)
			for i := 0; i < count; i++ {
				require.NoError(b, store.Upsert(&Record{ID: fmt.Sprint(i), identityKey: fmt.Sprint(i), APIKind: "messages", LastSeenAt: time.Now(), Prompts: []string{strings.Repeat("历史", 1000)}}))
			}
			require.NoError(b, store.Close())
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				r, err := NewPersistentRegistry(path)
				require.NoError(b, err)
				r.Stop()
			}
		})
	}
}
