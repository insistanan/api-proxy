package codec

import (
	"strings"
	"testing"
)

func BenchmarkStreamToolArguments(b *testing.B) {
	line := `data: {"type":"response.function_call_arguments.delta","output_index":0,"delta":"` + strings.Repeat("x", 1024) + `"}`
	b.ReportAllocs()
	for b.Loop() {
		s := NewStreamSynthesizer("responses", 2<<20)
		for i := 0; i < 1024; i++ {
			s.ProcessLine(line)
		}
	}
}
