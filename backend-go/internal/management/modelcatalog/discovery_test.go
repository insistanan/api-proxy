package modelcatalog

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"insistanan/api-proxy/internal/infra/config"
	"insistanan/api-proxy/internal/infra/upstreamreq"
)

func TestDiscoveryDoesNotRetryAuthenticationOrMalformedPages(t *testing.T) {
	cm := newTestConfigManager(t, config.Config{})
	for _, status := range []int{http.StatusUnauthorized, http.StatusTooManyRequests, http.StatusOK} {
		calls := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls++
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`invalid-json`))
		}))
		upstream := &config.UpstreamConfig{ServiceType: "openai", BaseURL: server.URL, ProxyMode: "direct"}
		_, err := DiscoverUpstreamModels(context.Background(), cm, upstream, "fixture-key")
		server.Close()
		require.Error(t, err)
		require.Equal(t, 1, calls, "认证、限流及坏响应不能重新猜协议")
	}
}

func TestGeminiDiscoveryRejectsRepeatedCursorWithoutLeakingKey(t *testing.T) {
	cm := newTestConfigManager(t, config.Config{})
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		require.Empty(t, r.URL.Query().Get("key"))
		require.Equal(t, "fixture-key", r.Header.Get("x-goog-api-key"))
		_, _ = w.Write([]byte(`{"models":[{"name":"models/gemini-test"}],"nextPageToken":"repeat"}`))
	}))
	defer server.Close()
	upstream := &config.UpstreamConfig{ServiceType: "gemini", BaseURL: server.URL, ProxyMode: "direct"}
	_, err := DiscoverUpstreamModels(context.Background(), cm, upstream, "fixture-key")
	require.ErrorContains(t, err, "游标重复")
	require.Equal(t, 2, calls)

	cause := errors.New("TLS fixture-key")
	networkErr := &url.Error{Op: "Get", URL: "https://user:password@example.test/models?key=fixture-key", Err: cause}
	err = upstreamreq.RedactError(networkErr, []string{networkErr.URL}, "fixture-key")
	require.NotContains(t, err.Error(), "fixture-key")
	require.NotContains(t, err.Error(), "password")
	require.ErrorIs(t, err, cause)
	nested := &url.Error{Op: "proxy", URL: "https://proxy:other-password@proxy.test?token=secret", Err: errors.New("TLS other-password")}
	networkErr.Err = nested
	err = upstreamreq.RedactError(networkErr, []string{networkErr.URL}, "fixture-key")
	require.NotContains(t, err.Error(), "other-password")
	require.NotContains(t, err.Error(), "token=secret")
	require.ErrorIs(t, err, nested)
}
func TestParseModelsFlexibleFormats(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		body         string
		wantContains []string
		wantCount    int
	}{
		{
			name:         "openai standard",
			body:         `{"object":"list","data":[{"id":"gpt-4","object":"model"},{"id":"gpt-4o","object":"model"}]}`,
			wantContains: []string{"gpt-4", "gpt-4o"},
			wantCount:    2,
		},
		{
			name:         "string array data",
			body:         `{"code":0,"data":["deepseek-chat","deepseek-reasoner"]}`,
			wantContains: []string{"deepseek-chat", "deepseek-reasoner"},
			wantCount:    2,
		},
		{
			name:         "bare string array",
			body:         `["model-a","model-b"]`,
			wantContains: []string{"model-a", "model-b"},
			wantCount:    2,
		},
		{
			name:         "nested data envelope",
			body:         `{"success":true,"data":{"data":[{"id":"claude-sonnet-4"}]}}`,
			wantContains: []string{"claude-sonnet-4"},
			wantCount:    1,
		},
		{
			name:         "models name objects",
			body:         `{"models":[{"name":"llama3:8b"},{"model":"qwen2.5:7b"}]}`,
			wantContains: []string{"llama3:8b", "qwen2.5:7b"},
			wantCount:    2,
		},
		{
			name:         "result wrapper",
			body:         `{"result":[{"model_id":"glm-4"},{"modelName":"glm-4-flash"}]}`,
			wantContains: []string{"glm-4", "glm-4-flash"},
			wantCount:    2,
		},
		{
			name:         "gemini native name prefix",
			body:         `{"models":[{"name":"models/gemini-2.0-flash","supportedGenerationMethods":["generateContent"]}]}`,
			wantContains: []string{"gemini-2.0-flash"},
			wantCount:    1,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := parseModelsFlexible([]byte(tt.body))
			if err != nil {
				t.Fatalf("parseModelsFlexible() error = %v", err)
			}
			if tt.wantCount > 0 && len(got) != tt.wantCount {
				t.Fatalf("len(got)=%d want %d; got=%v", len(got), tt.wantCount, got)
			}
			set := make(map[string]bool, len(got))
			for _, id := range got {
				set[id] = true
			}
			for _, want := range tt.wantContains {
				if !set[want] {
					t.Fatalf("missing model %q in %v", want, got)
				}
			}
		})
	}
}

func TestBuildModelsURLs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		fn   func(string) string
		in   string
		want string
	}{
		{name: "versioned root", fn: buildVersionedModelsURL, in: "https://api.openai.com", want: "https://api.openai.com/v1/models"},
		{name: "versioned already v1", fn: buildVersionedModelsURL, in: "https://api.openai.com/v1", want: "https://api.openai.com/v1/models"},
		{name: "versioned hash skip", fn: buildVersionedModelsURL, in: "https://gateway.example.com/custom#", want: "https://gateway.example.com/custom/models"},
		{name: "strip chat completions", fn: buildVersionedModelsURL, in: "https://api.openai.com/v1/chat/completions", want: "https://api.openai.com/v1/models"},
		{name: "root models", fn: buildRootModelsURL, in: "https://example.com/v1", want: "https://example.com/v1/models"},
		{name: "ollama tags", fn: buildOllamaTagsURL, in: "http://127.0.0.1:11434", want: "http://127.0.0.1:11434/api/tags"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := tt.fn(tt.in)
			if got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}

	azureURL := buildAzureModelsURL("https://my-resource.openai.azure.com")
	if !strings.HasPrefix(azureURL, "https://my-resource.openai.azure.com/openai/models?") {
		t.Fatalf("unexpected azure url: %s", azureURL)
	}
	if !strings.Contains(azureURL, "api-version=") {
		t.Fatalf("azure url missing api-version: %s", azureURL)
	}

	geminiURL := buildGeminiModelsURL("https://generativelanguage.googleapis.com/v1beta", "secret-key")
	if geminiURL != "https://generativelanguage.googleapis.com/v1beta/models" {
		t.Fatalf("unexpected gemini url: %s", geminiURL)
	}
	if strings.Contains(geminiURL, "secret-key") {
		t.Fatalf("Gemini 目录 URL 泄露了密钥: %s", geminiURL)
	}
}

func TestParseAnthropicAndGeminiPages(t *testing.T) {
	t.Parallel()

	anthropicBody := []byte(`{
		"data":[{"id":"claude-opus-4"},{"id":"claude-sonnet-4"}],
		"has_more":true,
		"last_id":"claude-sonnet-4"
	}`)
	models, meta, err := parseAnthropicPage(anthropicBody)
	if err != nil {
		t.Fatalf("parseAnthropicPage error: %v", err)
	}
	if !meta.HasMore || meta.LastID != "claude-sonnet-4" {
		t.Fatalf("unexpected anthropic meta: %+v", meta)
	}
	if len(models) != 2 {
		t.Fatalf("anthropic models=%v", models)
	}

	geminiBody := []byte(`{
		"models":[{"name":"models/gemini-2.5-pro","supportedGenerationMethods":["generateContent"]},{"name":"models/embedding-001","supportedGenerationMethods":["embedContent"]}],
		"nextPageToken":"page-2"
	}`)
	gModels, gMeta, gErr := parseGeminiPage(geminiBody)
	if gErr != nil {
		t.Fatalf("parseGeminiPage error: %v", gErr)
	}
	if gMeta.NextPageToken != "page-2" {
		t.Fatalf("unexpected gemini meta: %+v", gMeta)
	}
	if len(gModels) != 1 || gModels[0] != "gemini-2.5-pro" {
		t.Fatalf("gemini models=%v", gModels)
	}
}
