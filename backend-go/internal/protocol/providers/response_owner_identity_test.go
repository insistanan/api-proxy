package providers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"insistanan/api-proxy/internal/dialogue/session"
	"insistanan/api-proxy/internal/infra/config"
	"insistanan/api-proxy/internal/protocol/types"
)

func TestResponseOwner_OldIdentityRestoresHistoryInsteadOfReusingChain(t *testing.T) {
	upstream := &config.UpstreamConfig{BaseURL: "https://example.com/v1/responses", ServiceType: "responses", UseFullURL: true}
	credential := sha256.Sum256([]byte("key"))
	// 旧版对原始 BaseURL 取摘要，无法区分自动补全和完整地址模式。
	oldDigest := sha256.Sum256(append([]byte(upstream.BaseURL+"\x00responses\x00"), credential[:]...))
	oldOwner := hex.EncodeToString(oldDigest[:])
	owner := upstream.RequestIdentity("key").Fingerprint()
	require.NotEqual(t, oldOwner, owner)

	manager, err := session.NewPersistentSessionManager(filepath.Join(t.TempDir(), "responses.db"), time.Hour, 0, 0)
	require.NoError(t, err)
	t.Cleanup(manager.Stop)
	sess, err := manager.GetOrCreateSessionForConversation(context.Background(), "", "conv-identity")
	require.NoError(t, err)
	require.NoError(t, commitFixtureTurn(manager, sess, []types.ResponsesItem{
		{Type: "text", Content: "第一轮问题"},
		{Type: "message", Role: "assistant", Content: "第一轮回答"},
	}, 10, false, "resp-old", oldOwner))
	body := []byte(`{"model":"m","previous_response_id":"resp-old","input":"第二轮问题"}`)
	restored, err := PrepareNativeResponseHistory(context.Background(), manager, "conv-identity", body, owner, nil)
	require.NoError(t, err)
	require.False(t, gjson.GetBytes(restored, "previous_response_id").Exists())
	require.Len(t, gjson.GetBytes(restored, "input").Array(), 3)
	require.Contains(t, string(restored), "第一轮回答")
}

func TestResponseOwner_EquivalentAddressesKeepVerifiedChain(t *testing.T) {
	upstream := &config.UpstreamConfig{BaseURL: "https://example.com", ServiceType: "responses"}
	owner := upstream.RequestIdentity("key").Fingerprint()
	manager, err := session.NewPersistentSessionManager(filepath.Join(t.TempDir(), "responses.db"), time.Hour, 0, 0)
	require.NoError(t, err)
	t.Cleanup(manager.Stop)
	sess, err := manager.GetOrCreateSessionForConversation(context.Background(), "", "conv-equivalent")
	require.NoError(t, err)
	require.NoError(t, commitFixtureTurn(manager, sess, []types.ResponsesItem{{Type: "text", Content: "已知历史"}}, 10, false, "resp-known", owner))
	body := []byte(`{"model":"m","previous_response_id":"resp-known","input":"后续问题"}`)
	for _, target := range []config.UpstreamConfig{
		{BaseURL: "https://EXAMPLE.com:443/v1", ServiceType: "responses"},
		{BaseURL: "https://example.com/v1/responses", ServiceType: "responses", UseFullURL: true},
	} {
		kept, err := PrepareNativeResponseHistory(context.Background(), manager, "conv-equivalent", body, target.RequestIdentity("key").Fingerprint(), nil)
		require.NoError(t, err)
		require.JSONEq(t, string(body), string(kept))
	}
}
