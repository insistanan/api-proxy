package scheduler

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/insistanan/api-proxy/internal/dialogue/conversation"
	"github.com/insistanan/api-proxy/internal/infra/config"
	"github.com/insistanan/api-proxy/internal/orchestration/circuit"
	"github.com/stretchr/testify/require"
)

func TestSelection_FallsBackOnlyWhenMatchedPoolCannotAdmit(t *testing.T) {
	for _, scenario := range []string{"open", "probe-busy", "probe-available", "unhealthy"} {
		t.Run(scenario, func(t *testing.T) {
			sch, cleanup := createTestScheduler(t, config.Config{
				MessagePools: []config.ChannelPool{
					{ID: "matched", Name: "Matched", ModelMatcher: "model", Priority: 1},
					{ID: config.DefaultChannelPoolID, Name: "Default", ModelMatcher: "*", Priority: 2},
				},
				Upstream: []config.UpstreamConfig{
					{ID: "primary", Name: "primary", PoolID: "matched", BaseURL: "https://primary.example", APIKeys: []string{"primary"}, Status: "active", Priority: 1},
					{ID: "fallback", Name: "fallback", PoolID: config.DefaultChannelPoolID, BaseURL: "https://fallback.example", APIKeys: []string{"fallback"}, Status: "active", Priority: 1},
				},
			})
			t.Cleanup(cleanup)
			synctest.Test(t, func(t *testing.T) {
				manager := circuit.NewManager(circuit.Config{FailureThreshold: 1, MinRequests: 1, Cooldown: time.Minute, SuccessThreshold: 1}, true)
				sch.SetCircuitManager(manager)
				wantIndex := 0
				if scenario == "unhealthy" {
					for range 10 {
						sch.MetricsManager(ChannelKindMessages).RecordFailure("https://primary.example", "primary", 0)
					}
				} else {
					manager.RecordFailure("messages", 0)
					if scenario != "open" {
						time.Sleep(time.Minute)
					}
					if scenario == "probe-busy" {
						probe := manager.Allow("messages", 0)
						require.True(t, probe.Probe)
						defer manager.RecordNeutralGeneration("messages", 0, probe.Generation)
					}
					if scenario == "open" || scenario == "probe-busy" {
						wantIndex = 1
					}
				}
				selected, err := sch.SelectChannel(context.Background(), "", nil, ChannelKindMessages, "model")
				require.NoError(t, err)
				require.Equal(t, wantIndex, selected.ChannelIndex)
				require.EqualValues(t, 1, sch.GetChannelInFlight(ChannelKindMessages, wantIndex))
				if scenario == "unhealthy" {
					require.Equal(t, "fallback", selected.Reason, "仍可探测的命中分组应优先于兜底分组")
				}
				selected.Permit.Release()
				require.Zero(t, sch.GetChannelInFlight(ChannelKindMessages, wantIndex))
			})
		})
	}
}

func TestSelection_ExplicitTargetsShareCircuitAdmission(t *testing.T) {
	for _, entry := range []string{"conversation", "mapping", "vision", "vision-public", "vision-pool"} {
		t.Run(entry, func(t *testing.T) {
			sch, cleanup := createTestScheduler(t, config.Config{Upstream: []config.UpstreamConfig{
				{ID: "primary", Name: "primary", BaseURL: "https://primary.example", APIKeys: []string{"primary"}, Status: "active", VisionCapable: true, ExcludeFromConversation: entry == "vision-public"},
				{ID: "other", Name: "other", BaseURL: "https://other.example", APIKeys: []string{"other"}, Status: "active"},
			}})
			t.Cleanup(cleanup)
			registry := conversation.NewRegistry()
			t.Cleanup(registry.Stop)
			record := registry.ObserveRequest(conversation.Observation{APIKind: "messages", ConversationID: "fixed-admission"})
			_, err := registry.SetRouteOverride(record.ID, "messages", 0, "primary")
			require.NoError(t, err)
			sch.SetConversationRegistry(registry)
			selectTarget := func() (*SelectionResult, error) {
				switch entry {
				case "mapping":
					return sch.SelectMappedChannel(ChannelKindMessages, "primary")
				case "vision":
					return sch.SelectVisionChannel(context.Background(), ChannelKindMessages, "primary", config.DefaultChannelPoolID)
				case "vision-public", "vision-pool":
					var candidates []*SelectionResult
					if entry == "vision-public" {
						candidates = sch.ListPublicVisionChannels(context.Background(), "")
					} else {
						candidates = sch.ListPoolVisionChannels(context.Background(), ChannelKindMessages, "", config.DefaultChannelPoolID)
					}
					require.Len(t, candidates, 1)
					selected, err := sch.AdmitChannelCandidate(context.Background(), candidates[0])
					require.Nil(t, candidates[0].Permit, "准入不得修改列表候选")
					return selected, err
				default:
					return sch.SelectChannel(context.Background(), record.ID, nil, ChannelKindMessages, "model")
				}
			}
			synctest.Test(t, func(t *testing.T) {
				manager := circuit.NewManager(circuit.Config{FailureThreshold: 1, MinRequests: 1, Cooldown: time.Minute, SuccessThreshold: 1}, true)
				sch.SetCircuitManager(manager)
				manager.RecordFailure("messages", 0)
				selected, err := selectTarget()
				require.Error(t, err)
				require.True(t, IsCandidateUnavailable(err))
				require.Nil(t, selected, "固定目标熔断时不得转到其他渠道")
				require.Zero(t, sch.GetChannelInFlight(ChannelKindMessages, 0))
				time.Sleep(time.Minute)
				first, err := selectTarget()
				require.NoError(t, err)
				require.Equal(t, 0, first.ChannelIndex)
				require.Equal(t, 1, manager.SnapshotEntry("messages", 0).HalfOpenInFlight)
				second, err := selectTarget()
				require.Error(t, err)
				require.Nil(t, second)
				require.EqualValues(t, 1, sch.GetChannelInFlight(ChannelKindMessages, 0))
				first.Permit.Release()
				require.Zero(t, manager.SnapshotEntry("messages", 0).HalfOpenInFlight)
				third, err := selectTarget()
				require.NoError(t, err)
				third.Permit.RecordHealthSuccess()
				third.Permit.Release()
				require.Equal(t, circuit.StateClosed, manager.SnapshotEntry("messages", 0).State)
				require.Zero(t, sch.GetChannelInFlight(ChannelKindMessages, 0))
			})
		})
	}
}
