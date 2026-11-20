package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIntentRecognition_OnlyFixedHybridCombinationsAreAccepted(t *testing.T) {
	for _, stages := range [][]string{{"builtin", "semantic"}, {"builtin", "assistant"}, {"builtin", "semantic", "assistant"}, {"semantic", "assistant"}} {
		cfg := DefaultIntentRouting()
		cfg.Strategy, cfg.RecognitionMode, cfg.Classifier = "auto", "hybrid", "llm"
		cfg.HybridMatchers = stages
		require.NoError(t, ValidateIntentRouting(cfg, nil))
	}
	for _, stages := range [][]string{{"builtin"}, {"assistant", "semantic"}, {"builtin", "builtin"}, {"builtin", "unknown"}, {"builtin", "semantic", "assistant", "assistant"}} {
		cfg := DefaultIntentRouting()
		cfg.Strategy, cfg.RecognitionMode, cfg.Classifier = "auto", "hybrid", "llm"
		cfg.HybridMatchers = stages
		require.ErrorContains(t, ValidateIntentRouting(cfg, nil), "固定顺序")
	}
}
