package upstreamcontrol

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestBackgroundAdmission_OnlyUsesIdleUpstreamAndReleasesLease(t *testing.T) {
	c := NewController()
	id := NewIdentity("https://example.test/v1/messages", "claude", "key")
	c.Reconcile(map[Identity]int{id: 2})
	background := WithBackgroundAdmission(WithCandidate(WithScope(context.Background()), c, id, false))
	foreground, _, err := c.Acquire(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if lease, _, _, err := AcquireRequest(background); !errors.Is(err, ErrBackgroundBusy) || lease != nil {
		t.Fatalf("前台使用上游时后台不应抢占剩余名额: %v", err)
	}
	foreground.Release()
	lease, _, _, err := AcquireRequest(background)
	if err != nil {
		t.Fatal(err)
	}
	// 后台不占满并发；随后到达的前台请求仍可使用另一个名额。
	foreground, _, err = c.Acquire(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	foreground.Release()
	lease.Release()
	c.CoolDown(id, time.Now().Add(time.Minute))
	if lease, _, _, err := AcquireRequest(background); !errors.Is(err, ErrBackgroundBusy) || lease != nil {
		t.Fatalf("后台不应进入冷却队列: %v", err)
	}
}
