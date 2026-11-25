package providers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/insistanan/api-proxy/internal/infra/config"
	"github.com/insistanan/api-proxy/internal/protocol/types"
)

func TestReasoningContentCache_EvictsOldest(t *testing.T) {
	c := newReasoningContentCache(2)
	c.put("a", "1")
	c.put("b", "2")
	c.put("c", "3")
	if got := c.get("a"); got != "" {
		t.Fatalf("oldest entry not evicted, got %q", got)
	}
	if got := c.get("b"); got != "2" {
		t.Fatalf("b = %q, want 2", got)
	}
	if got := c.get("c"); got != "3" {
		t.Fatalf("c = %q, want 3", got)
	}
}

// TestConvertMessage_PrefersExplicitThinkingOverCache 确认补回不会覆盖客户端正常回传的明文 thinking。
func TestConvertMessage_PrefersExplicitThinkingOverCache(t *testing.T) {
	clearReasoningContentCacheForTest()
	defer clearReasoningContentCacheForTest()

	// 缓存里预置一条会命中同文本的 reasoning
	storeReasoning("I will inspect the files.", "cached reasoning", reasoningTestScope)

	c := newReasoningContext(http.MethodPost, "/v1/messages", []byte(`{
		"model":"reasoning-model",
		"messages":[{"role":"assistant","content":[
			{"type":"thinking","thinking":"explicit reasoning"},
			{"type":"text","text":"I will inspect the files."}
		]}]
	}`), nil)
	upstream := &config.UpstreamConfig{BaseURL: "https://api.example.com", ServiceType: "openai"}

	req, _, err := (&OpenAIProvider{}).ConvertToProviderRequest(c, upstream, "sk-test")
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
	if got.Messages[0].ReasoningContent != "explicit reasoning" {
		t.Fatalf("reasoning_content = %q, want explicit reasoning (cache must not override)", got.Messages[0].ReasoningContent)
	}
}

// TestConvertMessage_OmitsUnavailableReasoningForOptionalToolCall 确认工具调用
// 不会误用纯文本缓存；非严格渠道缺少原文时省略字段，不生成占位思考。
func TestConvertMessage_OmitsUnavailableReasoningForOptionalToolCall(t *testing.T) {
	clearReasoningContentCacheForTest()
	defer clearReasoningContentCacheForTest()

	storeReasoning("I will inspect the files.", "cached reasoning", reasoningTestScope)

	c := newReasoningContext(http.MethodPost, "/v1/messages", []byte(`{
		"model":"reasoning-model",
		"messages":[{"role":"assistant","content":[
			{"type":"tool_use","id":"call_1","name":"lookup","input":{}}
		]}]
	}`), nil)
	upstream := &config.UpstreamConfig{BaseURL: "https://api.example.com", ServiceType: "openai"}

	req, _, err := (&OpenAIProvider{}).ConvertToProviderRequest(c, upstream, "sk-test")
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
	if got.Messages[0].ReasoningContent != "" {
		t.Fatalf("reasoning_content = %q, 非严格渠道应省略缺失推理", got.Messages[0].ReasoningContent)
	}
}

func TestConvertMessage_RejectsUnavailableRequiredReasoning(t *testing.T) {
	clearReasoningContentCacheForTest()
	defer clearReasoningContentCacheForTest()
	c := newReasoningContext(http.MethodPost, "/v1/messages", []byte(`{
		"model":"reasoning-model",
		"messages":[{"role":"assistant","content":"historical answer"}]
	}`), nil)
	req, _, err := (&OpenAIProvider{}).ConvertToProviderRequest(c, &config.UpstreamConfig{
		BaseURL: "https://api.example.com", ServiceType: "openai", RequireReasoningContent: true,
	}, "sk-test")
	if err == nil || req != nil || !strings.Contains(err.Error(), "原始 reasoning_content 无法恢复") {
		t.Fatalf("严格渠道缺少原文应报错，req=%v err=%v", req, err)
	}
	var historyErr *types.ReasoningHistoryError
	if !errors.As(err, &historyErr) || !historyErr.ClientRequestError() {
		t.Fatalf("缺失历史应返回请求错误，避免轮换密钥重试：%v", err)
	}
}

func TestReasoningContentRoundTrip_NonStreamToolCall(t *testing.T) {
	clearReasoningContentCacheForTest()
	defer clearReasoningContentCacheForTest()

	providerResp := &types.ProviderResponse{
		Body: []byte(`{
			"id":"chatcmpl_test",
			"choices":[{"finish_reason":"tool_calls","message":{
				"role":"assistant",
				"reasoning_content":"I need to inspect the repository.",
				"content":null,
				"tool_calls":[{"id":"call_read_1","type":"function","function":{"name":"Read","arguments":"{\"path\":\"README.md\"}"}}]
			}}]
		}`),
	}
	if _, err := newReasoningTestProvider().ConvertToClaudeResponse(providerResp); err != nil {
		t.Fatalf("ConvertToClaudeResponse() err = %v", err)
	}

	// Claude Code 会将部分历史 thinking 回传为 redacted_thinking。
	c := newReasoningContext(http.MethodPost, "/v1/messages", []byte(`{
		"model":"reasoning-model",
		"messages":[
			{"role":"assistant","content":[
				{"type":"redacted_thinking"},
				{"type":"tool_use","id":"call_read_1","name":"Read","input":{"path":"README.md"}}
			]},
			{"role":"user","content":[{"type":"tool_result","tool_use_id":"call_read_1","content":"# Project"}]}
		]
	}`), nil)
	req, _, err := (&OpenAIProvider{}).ConvertToProviderRequest(c, &config.UpstreamConfig{BaseURL: "https://api.example.com", ServiceType: "openai"}, "sk-test")
	if err != nil {
		t.Fatalf("ConvertToProviderRequest() err = %v", err)
	}

	var got types.OpenAIRequest
	if err := json.NewDecoder(req.Body).Decode(&got); err != nil {
		t.Fatalf("decode request body: %v", err)
	}
	if len(got.Messages) != 2 {
		t.Fatalf("messages = %#v", got.Messages)
	}
	if got.Messages[0].ReasoningContent != "I need to inspect the repository." {
		t.Fatalf("reasoning_content = %q", got.Messages[0].ReasoningContent)
	}
}

func TestReasoningContentRoundTrip_StreamToolCall(t *testing.T) {
	clearReasoningContentCacheForTest()
	defer clearReasoningContentCacheForTest()

	body := strings.Join([]string{
		`data: {"id":"chatcmpl_test","model":"reasoning-model","choices":[{"index":0,"delta":{"reasoning_content":"I need to inspect the repository."},"finish_reason":null}]}`,
		``,
		`data: {"id":"chatcmpl_test","model":"reasoning-model","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_read_1","type":"function","function":{"name":"Read","arguments":"{\"path\":\"README.md\"}"}}]},"finish_reason":null}]}`,
		``,
		`data: {"id":"chatcmpl_test","model":"reasoning-model","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
		``,
	}, "\n")

	eventChan, errChan, err := newReasoningTestProvider().HandleStreamResponseCtx(context.Background(), io.NopCloser(strings.NewReader(body)))
	if err != nil {
		t.Fatalf("HandleStreamResponse() err = %v", err)
	}
	for range eventChan {
	}
	select {
	case err := <-errChan:
		if err != nil {
			t.Fatalf("stream err = %v", err)
		}
	default:
	}

	c := newReasoningContext(http.MethodPost, "/v1/messages", []byte(`{
		"model":"reasoning-model",
		"messages":[{"role":"assistant","content":[
			{"type":"redacted_thinking"},
			{"type":"tool_use","id":"call_read_1","name":"Read","input":{"path":"README.md"}}
		]}]
	}`), nil)
	req, _, err := (&OpenAIProvider{}).ConvertToProviderRequest(c, &config.UpstreamConfig{BaseURL: "https://api.example.com", ServiceType: "openai"}, "sk-test")
	if err != nil {
		t.Fatalf("ConvertToProviderRequest() err = %v", err)
	}

	var got types.OpenAIRequest
	if err := json.NewDecoder(req.Body).Decode(&got); err != nil {
		t.Fatalf("decode request body: %v", err)
	}
	if len(got.Messages) != 1 || got.Messages[0].ReasoningContent != "I need to inspect the repository." {
		t.Fatalf("messages = %#v", got.Messages)
	}
}

func TestReasoningContentCache_FallsBackToToolCallIDAfterCompaction(t *testing.T) {
	clearReasoningContentCacheForTest()
	defer clearReasoningContentCacheForTest()

	storeReasoningForAssistantMessage(types.OpenAIMessage{
		Role: "assistant",
		ToolCalls: []types.OpenAIToolCall{{
			ID: "call_stable_id",
			Function: types.OpenAIToolCallFunction{
				Name:      "Read",
				Arguments: `{"path":"very-long-original-path.md"}`,
			},
		}},
	}, "original reasoning", reasoningTestScope)

	got := lookupReasoningForAssistantMessage(types.OpenAIMessage{
		Role: "assistant",
		ToolCalls: []types.OpenAIToolCall{{
			ID: "call_stable_id",
			Function: types.OpenAIToolCallFunction{
				Name:      "Read",
				Arguments: `{"path":"compacted-path.md"}`,
			},
		}},
	}, reasoningTestScope)
	if got != "original reasoning" {
		t.Fatalf("lookupReasoningForAssistantMessage() = %q, want original reasoning", got)
	}
}

func TestCacheClaudeResponseReasoning_ScopedToolCall(t *testing.T) {
	clearReasoningContentCacheForTest()
	defer clearReasoningContentCacheForTest()

	CacheClaudeResponseReasoning(context.Background(), &types.ClaudeResponse{
		Role: "assistant",
		Content: []types.ClaudeContent{
			{Type: "thinking", Thinking: "reasoning from another provider"},
			{Type: "tool_use", ID: "call_cross_provider", Name: "Read", Input: map[string]interface{}{"path": "README.md"}},
		},
	}, reasoningTestScope)

	c := newReasoningContext(http.MethodPost, "/v1/messages", []byte(`{
		"model":"reasoning-model",
		"messages":[{"role":"assistant","content":[
			{"type":"redacted_thinking"},
			{"type":"tool_use","id":"call_cross_provider","name":"Read","input":{"path":"README.md"}}
		]}]
	}`), nil)
	req, _, err := (&OpenAIProvider{}).ConvertToProviderRequest(c, &config.UpstreamConfig{BaseURL: "https://api.example.com", ServiceType: "openai"}, "sk-test")
	if err != nil {
		t.Fatalf("ConvertToProviderRequest() err = %v", err)
	}
	var got types.OpenAIRequest
	if err := json.NewDecoder(req.Body).Decode(&got); err != nil {
		t.Fatalf("decode request body: %v", err)
	}
	if len(got.Messages) != 1 || got.Messages[0].ReasoningContent != "reasoning from another provider" {
		t.Fatalf("messages = %#v", got.Messages)
	}
}
