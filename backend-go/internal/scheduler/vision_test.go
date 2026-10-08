package scheduler

import (
	"context"
	"reflect"
	"testing"

	"github.com/BenedictKing/api-proxy/internal/config"
)

func TestListVisionChannelsSeparatesPublicAndOwnerPoolsInConfigOrder(t *testing.T) {
	cfg := config.Config{
		MessagePools: []config.ChannelPool{
			{ID: "owner", Name: "Owner", ModelMatcher: "owner", Priority: 1},
			{ID: config.DefaultChannelPoolID, Name: "Default", ModelMatcher: "*", Priority: 2},
		},
		Upstream: []config.UpstreamConfig{
			{ID: "owner-first", Name: "owner-first", PoolID: "owner", BaseURL: "https://owner-first.example.com", APIKeys: []string{"sk-owner-first"}, Status: "active", VisionCapable: true},
			{ID: "public-first", Name: "public-first", BaseURL: "https://public-first.example.com", APIKeys: []string{"sk-public-first"}, Status: "active", VisionCapable: true, ExcludeFromConversation: true},
			{ID: "public-second", Name: "public-second", BaseURL: "https://public-second.example.com", APIKeys: []string{"sk-public-second"}, Status: "active", VisionCapable: true, ExcludeFromConversation: true},
			{ID: "other-pool", Name: "other-pool", PoolID: config.DefaultChannelPoolID, BaseURL: "https://other.example.com", APIKeys: []string{"sk-other"}, Status: "active", VisionCapable: true},
			{ID: "owner-second", Name: "owner-second", PoolID: "owner", BaseURL: "https://owner-second.example.com", APIKeys: []string{"sk-owner-second"}, Status: "active", VisionCapable: true},
			{ID: "public-suspended", Name: "public-suspended", BaseURL: "https://suspended.example.com", APIKeys: []string{"sk-suspended"}, Status: "suspended", VisionCapable: true, ExcludeFromConversation: true},
			{ID: "public-without-key", Name: "public-without-key", BaseURL: "https://without-key.example.com", Status: "active", VisionCapable: true, ExcludeFromConversation: true},
		},
	}
	scheduler, cleanup := createTestScheduler(t, cfg)
	defer cleanup()

	public := scheduler.ListPublicVisionChannels(context.Background(), ChannelKindMessages, "")
	if got, want := visionChannelIDs(public), []string{"public-first", "public-second"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("公共图片理解池渠道 = %v, want %v", got, want)
	}
	for _, selection := range public {
		if selection.Reason != "vision_layer_public_pool" {
			t.Fatalf("公共图片理解池选择原因 = %q", selection.Reason)
		}
	}

	ownerPool := scheduler.ListPoolVisionChannels(context.Background(), ChannelKindMessages, "", "owner")
	if got, want := visionChannelIDs(ownerPool), []string{"owner-first", "owner-second"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("当前分组图片理解渠道 = %v, want %v", got, want)
	}
	for _, selection := range ownerPool {
		if selection.Reason != "vision_layer_owner_pool" {
			t.Fatalf("当前分组图片理解渠道选择原因 = %q", selection.Reason)
		}
	}

	public = scheduler.ListPublicVisionChannels(context.Background(), ChannelKindMessages, "public-first")
	if got, want := visionChannelIDs(public), []string{"public-second"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("排除已尝试渠道后的公共图片理解池 = %v, want %v", got, want)
	}
}

func TestListVisionChannelsStopsForCanceledContext(t *testing.T) {
	cfg := config.Config{Upstream: []config.UpstreamConfig{
		{ID: "public", Name: "public", BaseURL: "https://public.example.com", APIKeys: []string{"sk-public"}, Status: "active", VisionCapable: true, ExcludeFromConversation: true},
	}}
	scheduler, cleanup := createTestScheduler(t, cfg)
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if candidates := scheduler.ListPublicVisionChannels(ctx, ChannelKindMessages, ""); len(candidates) != 0 {
		t.Fatalf("上下文取消后仍返回 %d 个图片理解渠道", len(candidates))
	}
}

func visionChannelIDs(selections []*SelectionResult) []string {
	ids := make([]string, 0, len(selections))
	for _, selection := range selections {
		ids = append(ids, selection.Upstream.ID)
	}
	return ids
}
