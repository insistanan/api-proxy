package handlers

import (
	"testing"
	"time"

	"github.com/insistanan/api-proxy/internal/availability"
	"github.com/insistanan/api-proxy/internal/config"
	"github.com/stretchr/testify/require"
)

func TestAvailabilityUsesStableChannelAndActualMappingTarget(t *testing.T) {
	upstreams := []config.UpstreamConfig{
		{ID: "target", Name: "目标", BaseURL: "https://user:secret@example.com/private-key?token=secret", APIKeys: []string{"secret-key"}, ServiceType: "openai"},
		{ID: "source", Name: "源", BaseURL: "https://source.example", APIKeys: []string{"source-key"}, ServiceType: "openai",
			ModelMapping: map[string][]string{"alias": {"actual"}}, ModelMappingChannels: map[string]map[string]string{"alias": {"actual": "target"}}},
	}
	rows := buildAvailabilityRows(upstreams, nil, time.Now())
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
	rows = buildAvailabilityRows(upstreams, data, now)
	require.Equal(t, "healthy", rows[0].State, "重排渠道不应丢失归属")
	upstreams[1].APIKeys = []string{"replacement"}
	rows = buildAvailabilityRows(upstreams, data, now)
	require.Equal(t, "unknown", rows[0].State, "更换 Key 后不能继承旧 Key 的健康")
}
