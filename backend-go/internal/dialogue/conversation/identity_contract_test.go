package conversation

import (
	"context"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/require"
)

func TestExplicitIdentityDoesNotFoldOrJoinOpaqueComponents(t *testing.T) {
	r := NewRegistry()
	t.Cleanup(func() { r.Stop() })
	seen := map[string]bool{}
	for _, identity := range []Identity{
		{ExplicitID: "Case"}, {ExplicitID: "case"},
		{ExplicitID: "x|agent:y"}, {ExplicitID: "x", AgentID: "y"},
		{ExplicitID: "agent:y"}, {AgentID: "y"},
	} {
		record, err := r.ObserveRequestWithError(context.Background(), Observation{APIKind: "responses", Identity: identity})
		require.NoError(t, err)
		require.False(t, seen[record.ID], "不同显式身份不能共享记录")
		seen[record.ID] = true
		again, err := r.ObserveRequestWithError(context.Background(), Observation{APIKind: "responses", Identity: identity})
		require.NoError(t, err)
		require.Equal(t, record.ID, again.ID)
	}
}

func TestRetryWithMultipleFailedCandidatesCreatesNewConversation(t *testing.T) {
	r := NewRegistry()
	t.Cleanup(func() { r.Stop() })
	obs := Observation{APIKind: "messages", Identity: Identity{ClientFamily: "cursor", ScopeID: "workspace"}, Transcript: Transcript{PrefixHashes: []string{"one", "two"}}}
	first, err := r.ObserveRequestWithError(context.Background(), obs)
	require.NoError(t, err)
	second, err := r.ObserveRequestWithError(context.Background(), obs)
	require.NoError(t, err)
	require.NotEqual(t, first.ID, second.ID)
	r.MarkFailure(first.ID, "messages", "failed")
	r.MarkFailure(second.ID, "messages", "failed")
	r.MarkComplete(first.ID, "messages")
	r.MarkComplete(second.ID, "messages")
	retry, err := r.ObserveRequestWithError(context.Background(), obs)
	require.NoError(t, err)
	require.NotEqual(t, first.ID, retry.ID)
	require.NotEqual(t, second.ID, retry.ID)
}

func TestTruncatePreservesUTF8(t *testing.T) {
	got := truncate("中文🙂内容", 3)
	require.True(t, utf8.ValidString(got))
	require.Equal(t, "中文🙂...", got)
}
