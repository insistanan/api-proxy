package conversation

import (
	"context"
	"github.com/stretchr/testify/require"
	"path/filepath"
	"testing"
	"time"
)

func TestControlWritesRollbackAndObservationRetries(t *testing.T) {
	r, err := NewPersistentRegistry(filepath.Join(t.TempDir(), "dialogue.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, r.Stop()) })
	ctx := context.Background()
	rec := mustObserve(t, r, Observation{APIKind: "responses", ConversationID: "one"})
	_, err = r.SetRouteOverride(ctx, rec.ID, "responses", 1, "original")
	require.NoError(t, err)
	_, err = r.store.db.Exec(`CREATE TRIGGER fail_observation BEFORE UPDATE ON conversations BEGIN SELECT RAISE(ABORT,'injected'); END`)
	require.NoError(t, err)
	_, err = r.SetName(ctx, rec.ID, "失败名称")
	require.Error(t, err)
	_, err = r.SetRouteOverride(ctx, rec.ID, "responses", 2, "failed")
	require.Error(t, err)
	_, err = r.ClearRouteOverride(ctx, rec.ID)
	require.Error(t, err)
	_, err = r.ObserveRequestWithError(ctx, Observation{APIKind: "responses", ConversationID: "one", Prompts: []string{"失败提示"}})
	require.Error(t, err)
	current, err := r.Lookup(ctx, rec.ID)
	require.NoError(t, err)
	require.Empty(t, current.Name)
	require.Equal(t, 1, current.RouteOverride.ChannelIndex)
	require.Equal(t, rec.RequestCount, current.RequestCount)
	require.NotContains(t, current.Prompts, "失败提示")
	r.MarkComplete(rec.ID, "responses")
	current, err = r.Lookup(ctx, rec.ID)
	require.NoError(t, err)
	require.NotEmpty(t, current.PersistenceError)
	_, err = r.store.db.Exec(`DROP TRIGGER fail_observation`)
	require.NoError(t, err)
	r.mu.Lock()
	err = r.retryDirtyLocked(ctx)
	r.mu.Unlock()
	require.NoError(t, err)
	current, err = r.Lookup(ctx, rec.ID)
	require.NoError(t, err)
	require.Empty(t, current.PersistenceError)
	require.Zero(t, current.ActiveRequests)
	stored, err := r.store.loadRecords(ctx, "WHERE c.id=?", rec.ID)
	require.NoError(t, err)
	require.Empty(t, stored[0].Name)
	require.Equal(t, 1, stored[0].RouteOverride.ChannelIndex)
}
func TestActiveIDsIgnoreUnrelatedCorruptBodyAndReportStorageFailure(t *testing.T) {
	r, err := NewPersistentRegistry(filepath.Join(t.TempDir(), "dialogue.db"))
	require.NoError(t, err)
	defer r.Stop()
	rec := mustObserve(t, r, Observation{APIKind: "messages", ConversationID: "one"})
	r.MarkComplete(rec.ID, "messages")
	_, err = r.store.db.Exec(`UPDATE conversations SET prompts_json='broken'`)
	require.NoError(t, err)
	ids, err := r.ActiveIDs(context.Background(), time.Now().Add(-time.Minute))
	require.NoError(t, err)
	require.Equal(t, []string{rec.ID}, ids)
	require.NoError(t, r.store.db.Close())
	_, err = r.ActiveIDs(context.Background(), time.Now())
	require.Error(t, err)
}
func TestStopReportsUnsavedObservations(t *testing.T) {
	r, err := NewPersistentRegistry(filepath.Join(t.TempDir(), "dialogue.db"))
	require.NoError(t, err)
	rec := mustObserve(t, r, Observation{APIKind: "messages", ConversationID: "one"})
	require.NoError(t, r.store.db.Close())
	r.MarkComplete(rec.ID, "messages")
	require.Error(t, r.Stop())
	require.Error(t, r.Stop())
}
func TestConversationReadsCancelWhileWaitingForConnectionAndLock(t *testing.T) {
	r, err := NewPersistentRegistry(filepath.Join(t.TempDir(), "dialogue.db"))
	require.NoError(t, err)
	defer r.Stop()
	conn, err := r.store.db.Conn(context.Background())
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err = r.QueryPage(ctx, ListOptions{Page: 1, PageSize: 10})
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.NoError(t, conn.Close())
	r.mu.Lock()
	ctx, cancel = context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err = r.ObserveRequestWithError(ctx, Observation{APIKind: "messages"})
	r.mu.Unlock()
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestObservationBacklogIsBoundedAndAdmissionRecovers(t *testing.T) {
	r := NewRegistry()
	defer r.Stop()
	for i := 0; i < defaultCachedRecords; i++ {
		rec := mustObserve(t, r, Observation{APIKind: "messages"})
		r.mu.Lock()
		r.records[rec.ID].dirty = true
		r.mu.Unlock()
		r.MarkComplete(rec.ID, "messages")
	}
	_, err := r.ObserveRequestWithError(context.Background(), Observation{APIKind: "messages"})
	require.Error(t, err)
	require.Len(t, r.records, defaultCachedRecords)
	r.mu.Lock()
	for _, rec := range r.records {
		rec.dirty = false
	}
	r.mu.Unlock()
	_, err = r.ObserveRequestWithError(context.Background(), Observation{APIKind: "messages"})
	require.NoError(t, err)
}
func TestExpiredConversationInvalidatesLeaseAndCache(t *testing.T) {
	r := NewRegistry()
	defer r.Stop()
	rec := mustObserve(t, r, Observation{APIKind: "messages"})
	r.MarkComplete(rec.ID, "messages")
	lease := RequestLeaseFrom(r.WithRequestLease(context.Background(), rec.ID))
	cleared := false
	r.RegisterCacheInvalidator("test", func(id string) {
		if id == rec.ID {
			cleared = true
		}
	})
	r.mu.Lock()
	r.records[rec.ID].LastSeenAt = time.Now().Add(-8 * 24 * time.Hour)
	r.mu.Unlock()
	require.NoError(t, r.cleanup())
	require.True(t, cleared)
	require.False(t, lease.Store(func() { t.Fatal("过期会话不能回填") }))
}
