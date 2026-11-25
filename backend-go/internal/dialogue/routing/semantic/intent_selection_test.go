package semantic

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSelectIntentUsesCompetingLabelsBeforeThreshold(t *testing.T) {
	hit := func(label string, score float64, negative bool) Hit {
		return Hit{Sample: Sample{Label: label, Negative: negative}, Score: score}
	}
	for _, tc := range []struct {
		name    string
		hits    []Hit
		margin  float64
		matched bool
	}{
		{"次佳低于阈值仍拒识", []Hit{hit("writing", 0.76, false), hit("testing", 0.74, false)}, 0.15, false},
		{"同类样本不互相竞争", []Hit{hit("writing", 0.91, false), hit("writing", 0.90, false), hit("testing", 0.60, false)}, 0.15, true},
		{"同类反例拒识", []Hit{hit("writing", 0.91, false), hit("writing", 0.90, true)}, 0.15, false},
		{"其他类别反例不否决", []Hit{hit("writing", 0.91, false), hit("testing", 0.99, true)}, 0.15, true},
		{"通用反例拒识", []Hit{hit("writing", 0.91, false), hit("unmatched", 0.95, true)}, 0.15, false},
		{"零差值仍不能接受平局", []Hit{hit("writing", 0.9, false), hit("testing", 0.9, false)}, 0, false},
		{"未排序的类别也比较最强对手", []Hit{hit("testing", 0.74, false), hit("writing", 0.76, false), hit("writing", 0.78, false)}, 0.15, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, matched := SelectIntent(Result{Hits: tc.hits}, 0.75, tc.margin)
			require.Equal(t, tc.matched, matched)
		})
	}
}
