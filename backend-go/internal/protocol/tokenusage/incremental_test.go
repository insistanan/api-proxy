package tokenusage

import "testing"

func TestIncrementalEstimateMatchesWholeTextAtEveryByteBoundary(t *testing.T) {
	for _, input := range []string{"English 中文かな 한글 🙂 \n", "\xff中\xe4\xb8", "\xe4\xe4\xb8\xad", "", "hello   world"} {
		for split := 0; split <= len(input); split++ {
			var estimator IncrementalEstimator
			estimator.WriteString(input[:split])
			if got := estimator.Tokens(); got != EstimateTokens(input[:split]) {
				t.Fatalf("前缀 %q: %d", input[:split], got)
			}
			estimator.WriteString(input[split:])
			if got := estimator.Tokens(); got != EstimateTokens(input) {
				t.Fatalf("边界 %d: %d, want %d", split, got, EstimateTokens(input))
			}
		}
	}
}
