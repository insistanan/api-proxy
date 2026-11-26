package session

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"insistanan/api-proxy/internal/protocol/types"
)

func TestCacheBudgetPreservesActiveHistoryAndRestoresEvictedHistory(t *testing.T) {
	manager, err := NewPersistentSessionManager(filepath.Join(t.TempDir(), "sessions.db"), time.Hour, 0, 0)
	require.NoError(t, err)
	defer manager.Stop()
	manager.maxLoadedBytes = 1024
	ctx := context.Background()
	turn, err := manager.BeginTurn(ctx, "", "conversation")
	require.NoError(t, err)
	body := strings.Repeat("完整历史", 1024)
	require.NoError(t, turn.Commit(ctx, []types.ResponsesItem{{Type: "message", Role: "assistant", Content: body}}, 0, false, "response", "", false))
	require.NotNil(t, manager.sessions[turn.Snapshot.ID], "活动会话不能被淘汰")
	turn.Close()
	require.Zero(t, manager.loadedBytes)
	require.Empty(t, manager.sessions)
	require.Empty(t, manager.responseMapping)
	restored, err := manager.BeginTurn(ctx, "response", "conversation")
	require.NoError(t, err)
	require.Equal(t, body, restored.Snapshot.Messages[0].Content)
	restored.Close()
	require.Zero(t, manager.loadedBytes)
}

func TestHistoryCloneOwnsNestedAndOpaqueFields(t *testing.T) {
	input := []types.ResponsesItem{{Type: "message", Content: []any{map[string]any{"text": "original"}}, ToolUse: &types.ToolUse{Input: map[string]any{"key": []any{"original"}}}, Extra: map[string]json.RawMessage{"opaque": json.RawMessage(`{"value":1}`)}}}
	cloned, err := cloneResponsesItems(input)
	require.NoError(t, err)
	input[0].Content.([]any)[0].(map[string]any)["text"] = "changed"
	input[0].ToolUse.Input.(map[string]any)["key"].([]any)[0] = "changed"
	input[0].Extra["opaque"][9] = '2'
	require.Equal(t, "original", cloned[0].Content.([]any)[0].(map[string]any)["text"])
	require.Equal(t, "original", cloned[0].ToolUse.Input.(map[string]any)["key"].([]any)[0])
	require.JSONEq(t, `{"value":1}`, string(cloned[0].Extra["opaque"]))
}
