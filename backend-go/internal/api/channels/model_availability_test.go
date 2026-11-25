package channels

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"insistanan/api-proxy/internal/infra/config"
	"insistanan/api-proxy/internal/orchestration/availability"
)

func TestAvailabilityUsesStableChannelAndActualMappingTarget(t *testing.T) {
	upstreams := []config.UpstreamConfig{
		{ID: "target", Name: "目标", BaseURL: "https://user:secret@example.com/private-key?token=secret", APIKeys: []string{"secret-key"}, ServiceType: "openai"},
		{ID: "source", Name: "源", BaseURL: "https://source.example", APIKeys: []string{"source-key"}, ServiceType: "openai",
			ModelMapping: map[string][]string{"alias": {"actual"}}, ModelMappingChannels: map[string]map[string]string{"alias": {"actual": "target"}}},
	}
	rows := buildAvailabilityRows(upstreams, nil, nil, time.Now())
	require.Len(t, rows, 1)
	require.Equal(t, "target", rows[0].ChannelID)
	require.Equal(t, "actual", rows[0].Model)
	require.Equal(t, "unknown", rows[0].State)
	require.Equal(t, "https://example.com", rows[0].Candidates[0].Endpoint)
	require.NotContains(t, rows[0].Candidates[0].Label, "secret")
	now := time.Now()
	data := []availability.Aggregate{{ChannelID: "target", Model: "actual", CandidateID: rows[0].Candidates[0].ID, Outcome: availability.Success,
		Count: 10, Recent: 10, LastSeen: now.UnixMilli()}}
	upstreams[0], upstreams[1] = upstreams[1], upstreams[0]
	rows = buildAvailabilityRows(upstreams, data, nil, now)
	require.Equal(t, "healthy", rows[0].State, "重排渠道不应丢失归属")
	upstreams[1].APIKeys = []string{"replacement"}
	rows = buildAvailabilityRows(upstreams, data, nil, now)
	require.Equal(t, "unknown", rows[0].State, "更换 Key 后不能继承旧 Key 的健康")
}

func TestAvailability_SharedCooldownAppliesToEveryModel(t *testing.T) {
	now := time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)
	upstream := config.UpstreamConfig{ID: "c", BaseURL: "https://example.com", APIKeys: []string{"key"}, ServiceType: "responses",
		ModelMapping: map[string][]string{"alias": {"a", "b"}}}
	until := now.Add(time.Minute).UnixMilli()
	cooldowns := map[string]int64{upstream.RequestIdentity("key").Fingerprint(): until}
	rows := buildAvailabilityRows([]config.UpstreamConfig{upstream}, nil, cooldowns, now)
	require.Len(t, rows, 2)
	for _, row := range rows {
		require.Equal(t, "cooling", row.State)
		require.Equal(t, until, row.Candidates[0].CooldownUntil)
	}
}
