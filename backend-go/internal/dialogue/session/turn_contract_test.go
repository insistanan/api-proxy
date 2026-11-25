package session

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"insistanan/api-proxy/internal/dialogue/conversation"
	"insistanan/api-proxy/internal/protocol/types"
	"insistanan/api-proxy/internal/storage/sqlitedb"
)

func newTurnFixture(t *testing.T) (*SessionManager, *conversation.Registry, *sql.DB, string) {
	t.Helper()
	db, err := sqlitedb.Open(filepath.Join(t.TempDir(), "dialogue.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	r, err := conversation.NewPersistentRegistryWithDB(db)
	require.NoError(t, err)
	t.Cleanup(func() { r.Stop() })
	m, err := NewPersistentSessionManagerWithDB(db, time.Hour, 0, 0)
	require.NoError(t, err)
	t.Cleanup(func() { m.Stop() })
	require.NoError(t, m.BindRegistry(r))
	rec, err := r.ObserveRequestWithError(context.Background(), conversation.Observation{APIKind: "responses", ConversationID: "test"})
	require.NoError(t, err)
	return m, r, db, rec.ID
}

func TestTurnRejectsConcurrentStaleCommitAndPublishesAliasAtomically(t *testing.T) {
	m, r, _, id := newTurnFixture(t)
	ctx := context.Background()
	a, err := m.BeginTurn(ctx, "", id)
	require.NoError(t, err)
	defer a.Close()
	b, err := m.BeginTurn(ctx, "", id)
	require.NoError(t, err)
	defer b.Close()
	require.NoError(t, a.Commit(ctx, []types.ResponsesItem{{Type: "message", Role: "assistant", Content: "winner"}}, 1, false, "r1", "owner", false))
	require.Error(t, b.Commit(ctx, []types.ResponsesItem{{Type: "compaction", EncryptedContent: "stale"}}, 0, false, "r2", "owner", true))
	resolved, err := r.ResolveExistingRecordIDWithError(context.Background(), "responses", conversation.Identity{ExplicitID: "r1", Source: "previous_response"})
	require.NoError(t, err)
	require.Equal(t, id, resolved)
	_, exists, err := m.ResponseOwner(context.Background(), "r2")
	require.NoError(t, err)
	require.False(t, exists)
	latest, err := m.GetOrCreateSessionForConversation(context.Background(), "r1", id)
	require.NoError(t, err)
	require.Equal(t, "winner", latest.Messages[0].Content)
}

func TestTurnSurvivesCachePressureAndBlocksCapacityEviction(t *testing.T) {
	m, _, _, id := newTurnFixture(t)
	ctx := context.Background()
	m.maxLoadedSessions = 1
	m.SetMaxSessions(1)
	turn, err := m.BeginTurn(ctx, "", id)
	require.NoError(t, err)
	defer turn.Close()
	_, err = m.GetOrCreateSessionForConversation(context.Background(), "", "")
	require.Error(t, err)
	require.NoError(t, turn.Commit(ctx, []types.ResponsesItem{{Type: "message", Content: "answer"}}, 0, false, "response", "owner", false))
}

func TestDeleteAndLateCommitShareLifecycleBoundary(t *testing.T) {
	m, r, db, id := newTurnFixture(t)
	ctx := context.Background()
	turn, err := m.BeginTurn(ctx, "", id)
	require.NoError(t, err)
	defer turn.Close()
	_, err = db.Exec(`CREATE TRIGGER reject_delete BEFORE DELETE ON conversations BEGIN SELECT RAISE(ABORT,'injected'); END`)
	require.NoError(t, err)
	require.Error(t, r.DeleteWithHistory(ctx, id, m))
	_, err = m.GetSession(turn.Snapshot.ID)
	require.NoError(t, err)
	_, err = db.Exec(`DROP TRIGGER reject_delete`)
	require.NoError(t, err)
	require.NoError(t, r.DeleteWithHistory(ctx, id, m))
	require.Error(t, turn.Commit(ctx, nil, 0, false, "late", "owner", false))
	_, err = m.BeginTurn(ctx, "", id)
	require.Error(t, err)
	var count int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM response_sessions`).Scan(&count))
	require.Zero(t, count)
}

func TestAliasFailureRollsBackHistoryAndMapping(t *testing.T) {
	m, _, db, id := newTurnFixture(t)
	ctx := context.Background()
	turn, err := m.BeginTurn(ctx, "", id)
	require.NoError(t, err)
	defer turn.Close()
	_, err = db.Exec(`CREATE TRIGGER reject_alias BEFORE INSERT ON conversation_aliases BEGIN SELECT RAISE(ABORT,'injected'); END`)
	require.NoError(t, err)
	require.Error(t, turn.Commit(ctx, []types.ResponsesItem{{Type: "message", Content: "not committed"}}, 3, false, "new", "owner", false))
	_, known, err := m.ResponseOwner(context.Background(), "new")
	require.NoError(t, err)
	require.False(t, known)
	current, err := m.GetSession(turn.Snapshot.ID)
	require.NoError(t, err)
	require.Empty(t, current.Messages)
	require.Zero(t, current.Version)
}

func TestResponseIDCollisionDoesNotChangeEitherHistory(t *testing.T) {
	m, _, _, id := newTurnFixture(t)
	ctx := context.Background()
	first, err := m.BeginTurn(ctx, "", id)
	require.NoError(t, err)
	defer first.Close()
	require.NoError(t, first.Commit(ctx, []types.ResponsesItem{{Type: "message", Content: "one"}}, 0, false, "same", "owner1", false))
	second, err := m.BeginTurn(ctx, "", "")
	require.NoError(t, err)
	defer second.Close()
	require.Error(t, second.Commit(ctx, []types.ResponsesItem{{Type: "message", Content: "two"}}, 0, false, "same", "owner2", false))
	owner, known, err := m.ResponseOwner(context.Background(), "same")
	require.NoError(t, err)
	require.True(t, known)
	require.Equal(t, "owner1", owner)
	unchanged, err := m.GetSession(second.Snapshot.ID)
	require.NoError(t, err)
	require.Empty(t, unchanged.Messages)
}

func TestSessionTTLDeletesColdHistoryAndDoesNotReviveHotHistory(t *testing.T) {
	m, _, db, id := newTurnFixture(t)
	ctx := context.Background()
	turn, err := m.BeginTurn(ctx, "", id)
	require.NoError(t, err)
	require.NoError(t, turn.Commit(ctx, []types.ResponsesItem{{Type: "message", Content: "old"}}, 0, false, "expired", "owner", false))
	turn.Close()
	m.mu.Lock()
	m.sessions[turn.Snapshot.ID].LastAccessAt = time.Now().Add(-2 * time.Hour)
	m.mu.Unlock()
	_, err = m.GetOrCreateSessionForConversation(context.Background(), "expired", id)
	require.Error(t, err)
	_, err = db.Exec(`UPDATE response_session_access SET last_access_at=?`, time.Now().Add(-2*time.Hour).Unix())
	require.NoError(t, err)
	m.cleanup()
	var count int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM response_sessions`).Scan(&count))
	require.Zero(t, count)
}

func TestRegistryExpirationDeletesHistoryAtomically(t *testing.T) {
	m, r, db, id := newTurnFixture(t)
	turn, err := m.BeginTurn(context.Background(), "", id)
	require.NoError(t, err)
	require.NoError(t, turn.Commit(context.Background(), []types.ResponsesItem{{Type: "message", Content: "保留直到事务成功"}}, 0, false, "expiry", "owner", false))
	turn.Close()
	r.MarkComplete(id, "responses")
	_, err = db.Exec(`UPDATE conversations SET last_seen_at=? WHERE id=?`, time.Now().Add(-8*24*time.Hour).Unix(), id)
	require.NoError(t, err)
	_, err = db.Exec(`CREATE TRIGGER reject_history_delete BEFORE DELETE ON response_sessions BEGIN SELECT RAISE(ABORT,'injected'); END`)
	require.NoError(t, err)
	require.Error(t, r.BindHistory(m))
	var count int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM conversations WHERE id=?`, id).Scan(&count))
	require.Equal(t, 1, count)
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM response_session_mappings WHERE response_id='expiry'`).Scan(&count))
	require.Equal(t, 1, count)
	_, err = db.Exec(`DROP TRIGGER reject_history_delete`)
	require.NoError(t, err)
	require.NoError(t, r.BindHistory(m))
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM response_sessions`).Scan(&count))
	require.Zero(t, count)
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM conversations`).Scan(&count))
	require.Zero(t, count)
	_, err = m.GetSession(turn.Snapshot.ID)
	require.Error(t, err)
}

func TestHistoryReadAndCommitCancelWhileQueued(t *testing.T) {
	m, _, db, id := newTurnFixture(t)
	turn, err := m.BeginTurn(context.Background(), "", id)
	require.NoError(t, err)
	defer turn.Close()
	connection, err := db.Conn(context.Background())
	require.NoError(t, err)
	defer connection.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, _, err = m.ResponseOwner(ctx, "unknown")
	require.ErrorIs(t, err, context.DeadlineExceeded)
	ctx, cancel = context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	err = turn.Commit(ctx, []types.ResponsesItem{{Type: "message", Content: "cancel"}}, 0, false, "cancel", "owner", false)
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestFailedRegistryBindingKeepsWorkingLifecycle(t *testing.T) {
	m, original, _, id := newTurnFixture(t)
	incompatible := conversation.NewRegistry()
	defer incompatible.Stop()
	require.Error(t, m.BindRegistry(incompatible))
	require.Same(t, original, m.registry)
	turn, err := m.BeginTurn(context.Background(), "", id)
	require.NoError(t, err)
	defer turn.Close()
	require.NoError(t, turn.Commit(context.Background(), []types.ResponsesItem{{Type: "message", Content: "still works"}}, 0, false, "after-failed-bind", "owner", false))
}
