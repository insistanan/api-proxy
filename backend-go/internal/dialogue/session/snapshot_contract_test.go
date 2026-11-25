package session

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/insistanan/api-proxy/internal/protocol/types"
	"github.com/stretchr/testify/require"
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
