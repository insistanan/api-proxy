package intent

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"insistanan/api-proxy/internal/infra/config"
)

func TestCompactionKeepsConversationTargetForNextOpaqueTurn(t *testing.T) {
	s := intentTestService(t)
	up := &config.UpstreamConfig{ID: "channel", PoolID: "default", BaseURL: "https://test.invalid", ServiceType: "claude", APIKeys: []string{"key"},
		Compaction: config.CompactionConfig{NativeSourceModel: "main", NativeModel: "cheap", NativeCompatibilityConfirmed: true}}
	ctx, release, err := s.Begin(context.Background(), "messages", "conv", "main-turn", []byte(`{"messages":[{"role":"user","content":"翻译你好"}]}`), "")
	require.NoError(t, err)
	require.NoError(t, TurnFrom(ctx).CommitTarget(up, "key", "main"))
	require.NoError(t, TurnFrom(ctx).Complete(true))
	release()
	previous := s.state.Sessions[digest("messages\x00conv")]

	ctx, release, err = s.BeginProtocolControl(context.Background(), "messages", "conv", "compact-turn", []byte(`{"compaction":{"type":"summarize"},"messages":[{"role":"user","content":"历史"}]}`))
	require.NoError(t, err)
	turn := TurnFrom(ctx)
	require.NoError(t, turn.CheckTarget(up, "key", "cheap"))
	require.Error(t, turn.CheckTarget(up, "other-key", "cheap"))
	require.Error(t, turn.CheckTarget(up, "key", "unconfirmed"))
	require.NoError(t, turn.CommitTarget(up, "key", "cheap"))
	require.NoError(t, turn.Complete(true))
	release()
	require.Equal(t, previous, s.state.Sessions[digest("messages\x00conv")])
	require.Equal(t, "cheap", s.Events("messages")[0].Target.Model)

	ctx, release, err = s.Begin(context.Background(), "messages", "conv", "continue-turn", []byte(`{"messages":[{"role":"assistant","content":[{"type":"compaction","content":"摘要","signature":"opaque"}]},{"role":"user","content":"继续"}]}`), "")
	require.NoError(t, err)
	defer release()
	require.NoError(t, TurnFrom(ctx).CheckTarget(up, "key", "main"))
	require.Error(t, TurnFrom(ctx).CheckTarget(up, "key", "cheap"))
}
