package proxycore

import (
	"testing"
	"time"

	"github.com/insistanan/api-proxy/internal/infra/config"
	"github.com/insistanan/api-proxy/internal/orchestration/availability"
	"github.com/stretchr/testify/require"
)

func TestFreeModelRankingUsesActualCandidateHealth(t *testing.T) {
	now := time.Now()
	up := &config.UpstreamConfig{ID: "free-channel", BaseURL: config.KiloChatURL, ServiceType: "openai", UseFullURL: true,
		APIKeys: []string{""}, ModelMapping: map[string][]string{"free": {"bad", "unknown", "unknown-2", "healthy", "cooling", "missing", config.KiloAutoModelID}}}
	identity := up.RequestIdentity("").Fingerprint()
	sample := func(model, outcome string, count int64) availability.Aggregate {
		return availability.Aggregate{ChannelID: up.ID, Model: model, CandidateID: identity, Outcome: outcome,
			Count: count, Recent: count, LastSeen: now.UnixMilli(), Bucket: now.UnixMilli() / availability.BucketMillis * availability.BucketMillis}
	}
	cooling := sample("cooling", availability.RateLimited, 1)
	cooling.CooldownUntil = now.Add(time.Minute).UnixMilli()
	history := []availability.Aggregate{sample("bad", availability.Failure, 5), sample("healthy", availability.Success, 10),
		cooling, sample("missing", availability.Unsupported, 1), sample(config.KiloAutoModelID, availability.Success, 100)}
	ids := func(requested string, aggregates []availability.Aggregate) []string {
		result := []string{}
		for _, candidate := range rankFreeModels(up, requested, aggregates, now) {
			result = append(result, candidate.model)
		}
		return result
	}
	require.Equal(t, []string{"healthy", "unknown", "unknown-2", "bad", config.KiloAutoModelID}, ids("free", history))
	require.Equal(t, []string{"bad", "healthy", "unknown", "unknown-2", config.KiloAutoModelID}, ids("free/bad", history), "显式模型优先")
	require.Equal(t, config.KiloAutoModelID, ids("free/"+config.KiloAutoModelID, history)[0])
	require.NotContains(t, ids("free/cooling", history), "cooling", "指定模型也不能绕过冷却")
	// 不继承旧 URL/凭据组合的故障；无记录时保持清单顺序。
	stale := sample("bad", availability.Failure, 100)
	stale.CandidateID = "old-candidate"
	require.Equal(t, "bad", ids("free", []availability.Aggregate{stale})[0])
	cooling.CooldownUntil = now.Add(-time.Second).UnixMilli()
	require.Contains(t, ids("free", []availability.Aggregate{cooling}), "cooling")
}
