package proxycore

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"insistanan/api-proxy/internal/protocol/visionmedia"
)

func BenchmarkConversationObservation(b *testing.B) {
	items := make([]any, 128)
	for i := range items {
		items[i] = map[string]any{"role": "user", "content": strings.Repeat("上下文 example ", 256)}
	}
	body, err := json.Marshal(map[string]any{"model": "test", "messages": items})
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		if _, _, _, err := parseConversationObservation(nil, "messages", body); err != nil {
			b.Fatal(err)
		}
	}
}

func TestConversationObservationPreservesIdentityTranscriptAndImages(t *testing.T) {
	for kind, raw := range map[string]string{
		"messages":  `{"metadata":{"conversation_id":"conversation","user_id":"owner"},"messages":[{"role":"user","content":[{"type":"text","text":"看看"},{"type":"image","source":{"type":"url","url":"https://image.example/a.png"}}]}]}`,
		"responses": `{"previous_response_id":"previous","input":[{"role":"user","content":[{"type":"input_text","text":"继续"},{"type":"input_image","image_url":"https://image.example/b.png"}]}]}`,
		"gemini":    `{"contents":[{"role":"user","parts":[{"text":"分析"},{"inlineData":{"mimeType":"image/png","data":"YWJj"}}]}]}`,
	} {
		t.Run(kind, func(t *testing.T) {
			body := []byte(raw)
			identity, transcript, images, err := parseConversationObservation(nil, kind, body)
			require.NoError(t, err)
			require.Equal(t, ResolveConversationIdentity(nil, body), identity)
			require.Equal(t, BuildConversationTranscript(kind, body), transcript)
			require.Equal(t, visionmedia.ExtractImageFingerprints(body), images)
		})
	}
	_, _, _, err := parseConversationObservation(nil, "messages", []byte(`{"messages":`))
	require.Error(t, err)
}
