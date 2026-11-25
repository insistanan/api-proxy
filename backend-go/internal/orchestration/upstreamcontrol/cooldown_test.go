package upstreamcontrol

import (
	"context"
	"net/http"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCooldownSnapshot_MatchesActualAdmissionWait(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		id := NewIdentity("https://example.com/v1/responses", "responses", "key")
		controller := NewController()
		controller.Reconcile(map[Identity]int{id: 1})
		ctx := WithCandidate(WithScope(context.Background()), controller, id, false)
		now := time.Now()
		Record429(ctx, http.Header{})
		until := controller.Cooldowns()[id]
		require.GreaterOrEqual(t, until.Sub(now), time.Second)
		require.LessOrEqual(t, until.Sub(now), 1500*time.Millisecond)
		lease, waited, err := controller.Acquire(ctx, id)
		require.NoError(t, err)
		defer lease.Release()
		require.Equal(t, until.Sub(now), waited)
		require.Empty(t, controller.Cooldowns(), "到期后观测和准入必须同时结束冷却")
	})
}

func TestCooldownSnapshot_PreservesLongestActiveDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		id := NewIdentity("https://example.com/v1/responses", "responses", "key")
		controller := NewController()
		controller.Reconcile(map[Identity]int{id: 1})
		ctx := WithCandidate(WithScope(context.Background()), controller, id, false)
		Record429(ctx, http.Header{"Retry-After": {"60"}})
		until := time.Now().Add(time.Minute)
		Record429(ctx, http.Header{"Retry-After": {"1"}})
		require.Equal(t, until, controller.Cooldowns()[id])
		snapshot := controller.Cooldowns()
		delete(snapshot, id)
		require.Equal(t, until, controller.Cooldowns()[id], "读取方不能修改控制器状态")
		controller.Reconcile(nil)
		require.Empty(t, controller.Cooldowns(), "移除的身份不再显示冷却")
	})
}
