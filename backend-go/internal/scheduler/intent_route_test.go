package scheduler

import (
	"context"
	"testing"

	"github.com/insistanan/api-proxy/internal/config"
)

func TestIntentPoolOverridesModelMatcherWithoutChangingModel(t *testing.T) {
	s, cleanup := createTestScheduler(t, config.Config{
		MessagePools: []config.ChannelPool{{ID: "default", Name: "默认", ModelMatcher: "*"}, {ID: "intent", Name: "意图组", ModelMatcher: "unrelated", Priority: 1}},
		Upstream: []config.UpstreamConfig{
			{ID: "fallback", PoolID: "default", Name: "默认", BaseURL: "https://a.invalid", APIKeys: []string{"k"}, ServiceType: "claude", Status: "active"},
			{ID: "intent-channel", PoolID: "intent", Name: "目标", BaseURL: "https://b.invalid", APIKeys: []string{"k"}, ServiceType: "claude", Status: "active"},
		},
	})
	defer cleanup()
	ctx := WithPoolConstraint(context.Background(), PoolConstraint{PoolID: "intent"})
	selection, err := s.SelectChannel(ctx, "", map[int]bool{}, ChannelKindMessages, "original-model")
	if err != nil {
		t.Fatal(err)
	}
	defer selection.Permit.Release()
	if selection.Upstream.ID != "intent-channel" {
		t.Fatal("模型匹配规则覆盖了显式分组")
	}
	if got := s.ActiveChannelCountForRequest(ctx, ChannelKindMessages, "original-model"); got != 1 {
		t.Fatalf("渠道数未使用目标分组: %d", got)
	}
	if _, err := s.SelectChannel(ctx, "", map[int]bool{selection.ChannelIndex: true}, ChannelKindMessages, "original-model"); err == nil {
		t.Fatal("意图组耗尽不应悄悄进入其他分组")
	}
}
