package session

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"insistanan/api-proxy/internal/protocol/types"
)

func TestSessionSnapshotsOwnAllItemData(t *testing.T) {
	m, err := NewPersistentSessionManager(filepath.Join(t.TempDir(), "session.db"), time.Hour, 0, 0)
	require.NoError(t, err)
	t.Cleanup(m.Stop)
	sess, err := m.GetOrCreateSessionForConversation(context.Background(), "", "conversation")
	require.NoError(t, err)
	item := types.ResponsesItem{Type: "web_search_call", Action: map[string]interface{}{"query": "original"}, Extra: map[string]json.RawMessage{"results": json.RawMessage(`["original"]`)}}
	require.NoError(t, commitFixtureTurn(m, sess, []types.ResponsesItem{item}, 0, false, "response"))
	item.Action.(map[string]interface{})["query"] = "caller mutation"
	item.Extra["results"][2] = 'X'
	snapshot, err := m.GetSession(sess.ID)
	require.NoError(t, err)
	require.Equal(t, "original", snapshot.Messages[0].Action.(map[string]interface{})["query"])
	require.Equal(t, `["original"]`, string(snapshot.Messages[0].Extra["results"]))
	snapshot.Messages[0].Action.(map[string]interface{})["query"] = "reader mutation"
	again, err := m.GetSession(sess.ID)
	require.NoError(t, err)
	require.Equal(t, "original", again.Messages[0].Action.(map[string]interface{})["query"])
}

func TestSessionRejectsUnserializableInputWithoutChangingHistory(t *testing.T) {
	m, err := NewPersistentSessionManager(filepath.Join(t.TempDir(), "session.db"), time.Hour, 0, 0)
	require.NoError(t, err)
	t.Cleanup(m.Stop)
	sess, err := m.GetOrCreateSessionForConversation(context.Background(), "", "conversation")
	require.NoError(t, err)
	err = commitFixtureTurn(m, sess, []types.ResponsesItem{{Type: "message", Content: make(chan int)}}, 0, false, "response")
	require.Error(t, err)
	again, err := m.GetSession(sess.ID)
	require.NoError(t, err)
	require.Empty(t, again.Messages)
}

func TestCommittedHistoryDoesNotRetainTurnSnapshotReferences(t *testing.T) {
	m, _, _, id := newTurnFixture(t)
	ctx := context.Background()
	first, err := m.BeginTurn(ctx, "", id)
	require.NoError(t, err)
	require.NoError(t, first.Commit(ctx, []types.ResponsesItem{{Type: "web_search_call", Action: map[string]interface{}{"query": "original"}, Extra: map[string]json.RawMessage{"result": json.RawMessage(`["original"]`)}}}, 0, false, "r1", "owner", false))
	first.Close()
	second, err := m.BeginTurn(ctx, "r1", id)
	require.NoError(t, err)
	defer second.Close()
	require.NoError(t, second.Commit(ctx, []types.ResponsesItem{{Type: "message", Content: "next"}}, 0, false, "r2", "owner", false))
	second.Snapshot.Messages[0].Action.(map[string]interface{})["query"] = "mutated after commit"
	second.Snapshot.Messages[0].Extra["result"][2] = 'X'
	current, err := m.GetOrCreateSessionForConversation(ctx, "r2", id)
	require.NoError(t, err)
	require.Equal(t, "original", current.Messages[0].Action.(map[string]interface{})["query"])
	require.JSONEq(t, `["original"]`, string(current.Messages[0].Extra["result"]))
}
