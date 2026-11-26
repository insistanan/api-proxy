package scheduler

import (
	"context"
	"testing"

	"insistanan/api-proxy/internal/dialogue/conversation"
	"insistanan/api-proxy/internal/infra/config"
)

func TestConversationOverride_FollowsIdentityAfterReordering(t *testing.T) {
	registry := conversation.NewRegistry()
	t.Cleanup(func() { registry.Stop() })
	ctx := context.Background()
	record, err := registry.ObserveRequestWithError(ctx, conversation.Observation{APIKind: "messages", ConversationID: "identity-test"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.SetRouteOverride(ctx, record.ID, "messages", 0, "target", "stable"); err != nil {
		t.Fatal(err)
	}
	channels := []ChannelInfo{
		{Index: 0, Name: "replacement", Status: "active", upstream: &config.UpstreamConfig{ID: "other", APIKeys: []string{"key"}}},
		{Index: 1, Name: "target", Status: "active", upstream: &config.UpstreamConfig{ID: "stable", APIKeys: []string{"key"}}},
	}
	scheduler := &ChannelScheduler{}
	selected, fixed, err := scheduler.selectConversationRouteOverride(ctx, record.ID, ChannelKindMessages, channels, nil, registry)
	if err != nil || !fixed || selected == nil || selected.ChannelIndex != 1 {
		t.Fatalf("重排后应命中同一稳定 ID: selected=%+v fixed=%v err=%v", selected, fixed, err)
	}
	if _, _, err := scheduler.selectConversationRouteOverride(ctx, record.ID, ChannelKindMessages, channels[:1], nil, registry); err == nil {
		t.Fatal("原渠道消失后不得误用相同下标的其他渠道")
	}
}
