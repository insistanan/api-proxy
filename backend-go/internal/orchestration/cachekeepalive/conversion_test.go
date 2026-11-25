package cachekeepalive

import (
	"context"
	"io"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/insistanan/api-proxy/internal/dialogue/session"
	"github.com/insistanan/api-proxy/internal/infra/config"
	"github.com/insistanan/api-proxy/internal/protocol/providers"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestKeepalive_UsesProviderConversionOutputForEveryTarget(t *testing.T) {
	for _, entry := range []string{"messages", "responses"} {
		for _, target := range []string{"claude", "openai", "responses", "gemini"} {
			t.Run(entry+"/"+target, func(t *testing.T) {
				up := &config.UpstreamConfig{ID: "target", BaseURL: "https://upstream.test", ServiceType: target,
					ModelMapping: map[string][]string{"client-model": {"mapped-model"}}, CacheKeepalive: samplePolicy()}
				input := providers.RequestInput{Ctx: context.Background(), Method: http.MethodPost, Path: "/v1/messages", Header: http.Header{},
					ConversationID: "conversion-session", Body: []byte(`{"model":"client-model","max_tokens":128,"stream":true,"system":"fixed prefix","messages":[{"role":"user","content":"question"}]}`)}
				var provider providers.Provider = providers.GetProvider(target)
				if entry == "responses" {
					manager, err := session.NewPersistentSessionManager(filepath.Join(t.TempDir(), "responses.db"), time.Hour, 0, 0)
					require.NoError(t, err)
					t.Cleanup(manager.Stop)
					provider = &providers.ResponsesProvider{SessionManager: manager}
					input.Path = "/v1/responses"
					input.Body = []byte(`{"model":"client-model","max_output_tokens":128,"instructions":"fixed prefix","stream":true,"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"question"}]}]}`)
				}
				outcome, err := provider.BuildRequest(input, up, "fixture-key")
				require.NoError(t, err)
				req := outcome.Request
				original, err := io.ReadAll(req.Body)
				require.NoError(t, err)
				require.NoError(t, req.Body.Close())
				s := &snapshot{source: Source{Kind: entry, Open: req.GetBody, Size: req.ContentLength, Header: req.Header, URL: req.URL.String(), Upstream: up}, policy: samplePolicy()}
				body, model, _, err := prepareBody(s)
				require.NoError(t, err)
				require.Equal(t, "mapped-model", model)
				path := "messages"
				if target == "responses" {
					path = "input"
				}
				if target == "gemini" {
					path = "contents"
				}
				before, after := gjson.GetBytes(original, path).Array(), gjson.GetBytes(body, path).Array()
				require.Len(t, after, len(before)+1)
				for index := range before {
					require.Equal(t, before[index].Raw, after[index].Raw, "转换后的历史必须原样保留")
				}
				for _, field := range []string{"tools", "system", "instructions", "systemInstruction", "prompt_cache_key", "prompt_cache_retention", "previous_response_id"} {
					require.Equal(t, gjson.GetBytes(original, field).Raw, gjson.GetBytes(body, field).Raw, field)
				}
				require.True(t, s.stream)
			})
		}
	}
}
