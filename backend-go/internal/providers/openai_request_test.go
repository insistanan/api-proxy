package providers

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/insistanan/api-proxy/internal/config"
	"github.com/insistanan/api-proxy/internal/types"
)

func TestOpenAIProviderConvertToProviderRequest_UsesSingleTokenField(t *testing.T) {
	t.Run("replays thinking as reasoning content", func(t *testing.T) {
		c := newGinContext(http.MethodPost, "/v1/messages", []byte(`{
			"model":"reasoning-model",
			"messages":[{"role":"assistant","content":[
				{"type":"thinking","thinking":"need inspect files"},
				{"type":"text","text":"I will inspect the files."}
			]}]
		}`), nil)
		upstream := &config.UpstreamConfig{BaseURL: "https://api.example.com", ServiceType: "openai"}

		p := &OpenAIProvider{}
		req, _, err := p.ConvertToProviderRequest(c, upstream, "sk-test")
		if err != nil {
			t.Fatalf("ConvertToProviderRequest() err = %v", err)
		}

		var got types.OpenAIRequest
		if err := json.NewDecoder(req.Body).Decode(&got); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		if len(got.Messages) != 1 {
			t.Fatalf("messages = %#v", got.Messages)
		}
		if got.Messages[0].ReasoningContent != "need inspect files" {
			t.Fatalf("reasoning_content = %q", got.Messages[0].ReasoningContent)
		}
		if got.Messages[0].Content != "I will inspect the files." {
			t.Fatalf("content = %#v", got.Messages[0].Content)
		}
	})

	t.Run("generic openai keeps max_tokens only", func(t *testing.T) {
		c := newGinContext(http.MethodPost, "/v1/messages", []byte(`{"model":"gpt-4o","max_tokens":123,"messages":[]}`), nil)
		upstream := &config.UpstreamConfig{
			BaseURL:     "https://api.example.com",
			ServiceType: "openai",
		}

		p := &OpenAIProvider{}
		req, _, err := p.ConvertToProviderRequest(c, upstream, "sk-test")
		if err != nil {
			t.Fatalf("ConvertToProviderRequest() err = %v", err)
		}

		var got types.OpenAIRequest
		if err := json.NewDecoder(req.Body).Decode(&got); err != nil {
			t.Fatalf("decode request body: %v", err)
		}

		if got.MaxTokens != 123 {
			t.Fatalf("MaxTokens = %d, want 123", got.MaxTokens)
		}
		if got.MaxCompletionTokens != 0 {
			t.Fatalf("MaxCompletionTokens = %d, want 0", got.MaxCompletionTokens)
		}
	})

	t.Run("kimi target prefers max_completion_tokens", func(t *testing.T) {
		c := newGinContext(http.MethodPost, "/v1/messages", []byte(`{"model":"claude-sonnet-4-5-20250929","max_tokens":64000,"messages":[]}`), nil)
		upstream := &config.UpstreamConfig{
			BaseURL:      "https://api.example.com",
			ServiceType:  "openai",
			ModelMapping: map[string][]string{"claude-sonnet-4-5-20250929": {"kimi-2.6"}},
		}

		p := &OpenAIProvider{}
		req, _, err := p.ConvertToProviderRequest(c, upstream, "sk-test")
		if err != nil {
			t.Fatalf("ConvertToProviderRequest() err = %v", err)
		}

		var got types.OpenAIRequest
		if err := json.NewDecoder(req.Body).Decode(&got); err != nil {
			t.Fatalf("decode request body: %v", err)
		}

		if got.Model != "kimi-2.6" {
			t.Fatalf("Model = %q, want kimi-2.6", got.Model)
		}
		if got.MaxCompletionTokens != 64000 {
			t.Fatalf("MaxCompletionTokens = %d, want 64000", got.MaxCompletionTokens)
		}
		if got.MaxTokens != 0 {
			t.Fatalf("MaxTokens = %d, want 0", got.MaxTokens)
		}
	})

	t.Run("stream requests include usage chunk", func(t *testing.T) {
		c := newGinContext(http.MethodPost, "/v1/messages", []byte(`{"model":"gpt-4o","stream":true,"messages":[]}`), nil)
		upstream := &config.UpstreamConfig{
			BaseURL:     "https://api.example.com",
			ServiceType: "openai",
		}

		p := &OpenAIProvider{}
		req, _, err := p.ConvertToProviderRequest(c, upstream, "sk-test")
		if err != nil {
			t.Fatalf("ConvertToProviderRequest() err = %v", err)
		}

		var got map[string]interface{}
		if err := json.NewDecoder(req.Body).Decode(&got); err != nil {
			t.Fatalf("decode request body: %v", err)
		}

		streamOptions, ok := got["stream_options"].(map[string]interface{})
		if !ok {
			t.Fatalf("stream_options missing: %#v", got["stream_options"])
		}
		if includeUsage, ok := streamOptions["include_usage"].(bool); !ok || !includeUsage {
			t.Fatalf("stream_options.include_usage = %#v, want true", streamOptions["include_usage"])
		}
	})

	t.Run("tool_choice none kept only when tools exist", func(t *testing.T) {
		c := newGinContext(http.MethodPost, "/v1/messages", []byte(`{
			"model":"gpt-4o",
			"tool_choice":"none",
			"tools":[{"name":"lookup","input_schema":{"type":"object","properties":{}}}],
			"messages":[{"role":"user","content":"hi"}]
		}`), nil)
		upstream := &config.UpstreamConfig{
			BaseURL:     "https://api.example.com",
			ServiceType: "openai",
		}

		p := &OpenAIProvider{}
		req, _, err := p.ConvertToProviderRequest(c, upstream, "sk-test")
		if err != nil {
			t.Fatalf("ConvertToProviderRequest() err = %v", err)
		}

		var got map[string]interface{}
		if err := json.NewDecoder(req.Body).Decode(&got); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		if got["tool_choice"] != "none" {
			t.Fatalf("tool_choice = %#v, want none", got["tool_choice"])
		}
	})

	t.Run("tool_choice dropped when tools missing", func(t *testing.T) {
		c := newGinContext(http.MethodPost, "/v1/messages", []byte(`{
			"model":"gpt-4o",
			"tool_choice":"none",
			"messages":[{"role":"user","content":"hi"}]
		}`), nil)
		upstream := &config.UpstreamConfig{
			BaseURL:     "https://api.example.com",
			ServiceType: "openai",
		}

		p := &OpenAIProvider{}
		req, _, err := p.ConvertToProviderRequest(c, upstream, "sk-test")
		if err != nil {
			t.Fatalf("ConvertToProviderRequest() err = %v", err)
		}

		var got map[string]interface{}
		if err := json.NewDecoder(req.Body).Decode(&got); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		if _, ok := got["tool_choice"]; ok {
			t.Fatalf("tool_choice should be dropped when tools missing, got %#v", got["tool_choice"])
		}
	})
}
