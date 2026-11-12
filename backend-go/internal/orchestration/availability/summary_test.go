package availability

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSummaryIsolatesKeysModelsAndRemovedCredentials(t *testing.T) {
	now := time.Date(2026, 11, 11, 12, 10, 0, 0, time.UTC)
	sample := func(candidate, outcome string, count int64) Aggregate {
		return Aggregate{ChannelID: "channel", Model: "model-a", CandidateID: candidate, Outcome: outcome,
			Bucket: now.UnixMilli() / BucketMillis * BucketMillis, Count: count, Recent: count, LastSeen: now.UnixMilli()}
	}
	good, bad, removed := sample("good", Success, 20), sample("bad", Unsupported, 1), sample("removed", Failure, 100)
	otherModel := sample("good", Failure, 100)
	otherModel.Model = "model-b"
	row := Summarize("channel", "渠道", "model-a", []Candidate{{ID: "good", Label: "1"}, {ID: "bad", Label: "2"}}, []Aggregate{good, bad, removed, otherModel}, now)
	require.Equal(t, "degraded", row.State)
	require.Equal(t, int64(21), row.Eligible())
	require.Equal(t, "healthy", row.Candidates[0].State)
	require.Equal(t, "unsupported", row.Candidates[1].State)
	require.Equal(t, int64(0), row.Failure)
	require.Len(t, row.Buckets, 24)
	require.Equal(t, int64(0), row.Buckets[0].Eligible())

	row = Summarize("channel", "渠道", "model-a", []Candidate{{ID: "bad"}, {ID: "new"}}, []Aggregate{bad}, now)
	require.NotEqual(t, "unsupported", row.State, "未尝试的新 Key 不能被旧 Key 的结果否定")
}

func TestSummaryRequiresFreshEvidenceAndEnoughSamples(t *testing.T) {
	now := time.Now()
	candidate := []Candidate{{ID: "key"}}
	data := []Aggregate{{ChannelID: "c", Model: "m", CandidateID: "key", Outcome: Success, Count: 1, Recent: 1, LastSeen: now.UnixMilli(), Bucket: now.UnixMilli() / BucketMillis * BucketMillis}}
	row := Summarize("c", "", "m", candidate, data, now)
	require.Equal(t, "unknown", row.State)
	data[0].Count, data[0].Recent = 10, 10
	row = Summarize("c", "", "m", candidate, data, now)
	require.Equal(t, "healthy", row.State)
	data[0].Recent = 0
	row = Summarize("c", "", "m", candidate, data, now.Add(2*time.Hour))
	require.Equal(t, "unknown", row.State)
}

func TestSuccessAfterRateLimitClearsCooldownAndCancellationDoesNot(t *testing.T) {
	now := time.Now()
	limited := Aggregate{ChannelID: "c", Model: "m", CandidateID: "key", Outcome: RateLimited, Count: 1, Recent: 1,
		LastSeen: now.Add(-time.Second).UnixMilli(), CooldownUntil: now.Add(time.Minute).UnixMilli(), LastSequence: 1}
	cancelled := limited
	cancelled.Outcome, cancelled.LastSeen, cancelled.LastSequence = Ignored, now.UnixMilli(), 2
	row := Summarize("c", "", "m", []Candidate{{ID: "key"}}, []Aggregate{limited, cancelled}, now)
	require.Equal(t, "cooling", row.State)
	succeeded := cancelled
	succeeded.Outcome, succeeded.LastSequence, succeeded.CooldownUntil = Success, 3, 0
	row = Summarize("c", "", "m", []Candidate{{ID: "key"}}, []Aggregate{limited, succeeded, cancelled}, now)
	require.NotEqual(t, "cooling", row.State)
	require.Equal(t, int64(0), row.Candidates[0].CooldownUntil)
}
