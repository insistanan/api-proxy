package session

import (
	"strings"
	"testing"

	"insistanan/api-proxy/internal/protocol/types"
)

func BenchmarkCloneResponsesHistory(b *testing.B) {
	items := make([]types.ResponsesItem, 256)
	for i := range items {
		items[i] = types.ResponsesItem{Type: "message", Role: "user", Content: []any{map[string]any{"type": "input_text", "text": strings.Repeat("上下文 example ", 128)}}}
	}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := cloneResponsesItems(items); err != nil {
			b.Fatal(err)
		}
	}
}
