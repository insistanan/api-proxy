package semantic

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestAssessSafety_RiskRecallAndBlockingConditionsAreSeparate(t *testing.T) {
	result := Result{Hits: []Hit{
		{Sample: Sample{Label: "delete", Pattern: "rm -rf"}, Score: .9},
		{Sample: Sample{Label: "delete", Negative: true}, Score: .85},
		{Sample: Sample{Label: "delete", Negative: true}, Score: .2},
	}}
	for _, tc := range []struct {
		text   string
		margin float64
		block  bool
	}{
		{"rm -rf /tmp/example", .1, false},
		{"rm -rf /tmp/example", .04, true},
		{"echo example", .04, false},
	} {
		matches, err := AssessSafety(result, tc.text, .8, tc.margin)
		require.NoError(t, err)
		require.Len(t, matches, 1, "未满足阻断条件的候选仍需审核")
		require.Equal(t, .85, matches[0].NegativeScore)
		require.Equal(t, tc.block, matches[0].BlockEligible)
	}
	result.Hits[0].Sample.Pattern = "["
	_, err := AssessSafety(result, "rm -rf", .8, 0)
	require.Error(t, err)
}
