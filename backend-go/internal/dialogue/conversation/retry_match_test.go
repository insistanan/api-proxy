package conversation

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// observeAndFail 模拟一次完整的失败请求生命周期。
func observeAndFail(t *testing.T, registry *Registry, obs Observation) *Record {
	t.Helper()
	rec := mustObserve(t, registry, obs)
	registry.MarkFailure(rec.ID, obs.APIKind, "upstream error")
	registry.MarkComplete(rec.ID, obs.APIKind)
	return rec
}

// observeAndSucceed 模拟一次完整的成功请求生命周期。
func observeAndSucceed(t *testing.T, registry *Registry, obs Observation) *Record {
	t.Helper()
	rec := mustObserve(t, registry, obs)
	registry.MarkSuccess(rec.ID, obs.APIKind, 0, "test-channel")
	registry.MarkComplete(rec.ID, obs.APIKind)
	return rec
}

func helloObservation(identity Identity, prefix ...string) Observation {
	return Observation{
		APIKind:    "messages",
		Identity:   identity,
		Transcript: Transcript{PrefixHashes: prefix},
		Prompts:    []string{"hello"},
	}
}

func TestRetryMatchMergesFailedRequest(t *testing.T) {
	scoped := Identity{ClientFamily: "cursor", ScopeID: "workspace-a", LaneHash: "lane-main"}

	t.Run("depth1 with scope", func(t *testing.T) {
		registry := NewRegistry()
		defer registry.Stop()
		first := observeAndFail(t, registry, helloObservation(scoped, "h1"))
		retry := mustObserve(t, registry, helloObservation(scoped, "h1"))
		require.Equal(t, first.ID, retry.ID)
		require.Equal(t, "retry_match", retry.IdentitySource)
	})

	t.Run("depth2 after success", func(t *testing.T) {
		registry := NewRegistry()
		defer registry.Stop()
		first := observeAndSucceed(t, registry, helloObservation(scoped, "h1"))
		second := observeAndFail(t, registry, helloObservation(scoped, "h1", "h2", "h3"))
		retry := mustObserve(t, registry, helloObservation(scoped, "h1", "h2", "h3"))
		require.Equal(t, second.ID, retry.ID)
		require.Equal(t, first.ID, retry.ID)
		require.Equal(t, "retry_match", retry.IdentitySource)
	})

	t.Run("repeated failed retries", func(t *testing.T) {
		registry := NewRegistry()
		defer registry.Stop()
		first := observeAndFail(t, registry, helloObservation(scoped, "h1"))
		retry1 := observeAndFail(t, registry, helloObservation(scoped, "h1"))
		require.Equal(t, first.ID, retry1.ID)
		retry2 := mustObserve(t, registry, helloObservation(scoped, "h1"))
		require.Equal(t, first.ID, retry2.ID)
		require.Equal(t, "retry_match", retry2.IdentitySource)
	})
}

func TestRetryMatchRejectsUnsafeMerge(t *testing.T) {
	scoped := Identity{ClientFamily: "cursor", ScopeID: "workspace-a", LaneHash: "lane-main"}
	tests := []struct {
		name   string
		first  func(t *testing.T, r *Registry) *Record
		retry  Observation
		mutate func(t *testing.T, r *Registry, id string)
	}{
		{
			name: "depth1 without scope",
			first: func(t *testing.T, r *Registry) *Record {
				return observeAndFail(t, r, helloObservation(Identity{ClientFamily: "unknown"}, "h1"))
			},
			retry: helloObservation(Identity{ClientFamily: "unknown"}, "h1"),
		},
		{
			name: "after success",
			first: func(t *testing.T, r *Registry) *Record {
				return observeAndSucceed(t, r, helloObservation(scoped, "h1"))
			},
			retry: helloObservation(scoped, "h1"),
		},
		{
			name: "concurrent request still active",
			first: func(t *testing.T, r *Registry) *Record {
				return observeAndFail(t, r, helloObservation(scoped, "h1"))
			},
			retry: helloObservation(scoped, "h1"),
			mutate: func(t *testing.T, r *Registry, id string) {
				r.mu.Lock()
				defer r.mu.Unlock()
				r.records[id].ActiveRequests = 1
			},
		},
		{
			name: "outside retry window",
			first: func(t *testing.T, r *Registry) *Record {
				return observeAndFail(t, r, helloObservation(scoped, "h1"))
			},
			retry: helloObservation(scoped, "h1"),
			mutate: func(t *testing.T, r *Registry, id string) {
				r.mu.Lock()
				defer r.mu.Unlock()
				r.records[id].LastCompletedAt = time.Now().Add(-10 * time.Minute)
			},
		},
		{
			name: "different scope",
			first: func(t *testing.T, r *Registry) *Record {
				return observeAndFail(t, r, helloObservation(Identity{ClientFamily: "cursor", ScopeID: "user-a", LaneHash: "lane-main"}, "h1"))
			},
			retry: helloObservation(Identity{ClientFamily: "cursor", ScopeID: "user-b", LaneHash: "lane-main"}, "h1"),
		},
		{
			name: "different lane",
			first: func(t *testing.T, r *Registry) *Record {
				return observeAndFail(t, r, helloObservation(scoped, "h1"))
			},
			retry: helloObservation(Identity{ClientFamily: "cursor", ScopeID: "workspace-a", LaneHash: "lane-sub"}, "h1"),
		},
		{
			name: "empty transcript",
			first: func(t *testing.T, r *Registry) *Record {
				return observeAndFail(t, r, helloObservation(scoped))
			},
			retry: helloObservation(scoped),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			registry := NewRegistry()
			defer registry.Stop()
			first := tt.first(t, registry)
			if tt.mutate != nil {
				tt.mutate(t, registry, first.ID)
			}
			retry := mustObserve(t, registry, tt.retry)
			require.NotEqual(t, first.ID, retry.ID)
		})
	}
}

func TestRetryMatchPrefersStrictExtensionOverRetry(t *testing.T) {
	registry := NewRegistry()
	defer registry.Stop()
	identity := Identity{ClientFamily: "cursor", ScopeID: "workspace-a", LaneHash: "lane-main"}

	first := observeAndSucceed(t, registry, helloObservation(identity, "h1"))
	observeAndFail(t, registry, helloObservation(identity, "h1", "h2", "h3"))

	// 严格扩展应优先于失败重试匹配，即使最近失败的 frontier 同样可以匹配。
	third := mustObserve(t, registry, helloObservation(identity, "h1", "h2", "h3", "h4"))
	require.Equal(t, first.ID, third.ID)
	require.Equal(t, "unique_history_extension", third.IdentitySource)
}
