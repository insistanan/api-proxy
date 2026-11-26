package codec

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStreamSynthesisStopsCollectingAtLogBudget(t *testing.T) {
	line := `data: {"type":"response.function_call_arguments.delta","output_index":0,"delta":"` + strings.Repeat("x", 128) + `"}`
	s := NewStreamSynthesizer("responses", len(line)*2)
	for range 1000 {
		s.ProcessLine(line)
	}
	require.True(t, s.Truncated())
	require.Equal(t, strings.Repeat("x", 256), s.toolCallAccumulator[0].Arguments.String())
	empty := NewStreamSynthesizer("responses", 0)
	empty.ProcessLine(line)
	require.Empty(t, empty.toolCallAccumulator)
}
