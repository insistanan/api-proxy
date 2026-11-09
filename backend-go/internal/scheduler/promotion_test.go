package scheduler

import (
	"context"
	"testing"
	"time"

	"github.com/insistanan/api-proxy/internal/circuit"
	"github.com/insistanan/api-proxy/internal/config"
	"github.com/stretchr/testify/require"
)

// 健康度、熔断和到期都不能因促销被绕过；恢复探测保持配置顺序。
func TestPromotionAdmissionAndEndRules(t *testing.T) {
	for _, scenario := range []string{"active", "failed", "expired", "count-exhausted", "circuit-open", "all-unhealthy"} {
		t.Run(scenario, func(t *testing.T) {
			until := time.Now().Add(time.Hour)
			if scenario == "expired" {
				until = time.Now().Add(-time.Hour)
			}
			cfg := config.Config{Upstream: []config.UpstreamConfig{
				{Name: "normal", BaseURL: "https://normal.example.com", APIKeys: []string{"normal"}, Status: "active", Priority: 1},
				{Name: "promoted", BaseURL: "https://promoted.example.com", APIKeys: []string{"promoted"}, Status: "active", Priority: 2, PromotionUntil: &until, PromotionCount: 1},
			}}
			s, cleanup := createTestScheduler(t, cfg)
			t.Cleanup(cleanup)
			failed := map[int]bool{}
			if scenario == "failed" {
				failed[1] = true
			}
			if scenario == "count-exhausted" {
				s.configManager.ConsumePromotionCount(1, "messages")
			}
			if scenario == "circuit-open" {
				manager := circuit.NewManager(circuit.Config{FailureThreshold: 1, MinRequests: 1, Cooldown: time.Hour, SuccessThreshold: 1}, true)
				s.SetCircuitManager(manager)
				s.RecordCircuitFailure(ChannelKindMessages, 1)
			}
			if scenario == "all-unhealthy" {
				for i, up := range cfg.Upstream {
					for n := 0; n < 10; n++ {
						s.MetricsManager(ChannelKindMessages).RecordFailure(up.BaseURL, up.APIKeys[0], i)
					}
				}
			}
			selected, err := s.SelectChannel(context.Background(), "", failed, ChannelKindMessages, "")
			require.NoError(t, err)
			defer selected.Permit.Release()
			wantIndex, wantReason := 0, "priority_order"
			if scenario == "active" {
				wantIndex, wantReason = 1, "promotion"
			} else if scenario == "all-unhealthy" {
				wantReason = "fallback"
			}
			require.Equal(t, wantIndex, selected.ChannelIndex)
			require.Equal(t, wantReason, selected.Reason)
		})
	}
}
