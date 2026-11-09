package providers

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/insistanan/api-proxy/internal/config"
	"github.com/insistanan/api-proxy/internal/types"
)

func TestReasoningContentCache_PutGet(t *testing.T) {
	clearReasoningContentCacheForTest()
	defer clearReasoningContentCacheForTest()

	storeReasoning("hello world", "think step by step")
	if got := lookupReasoning("hello world"); got != "think step by step" {
		t.Fatalf("lookupReasoning() = %q, want %q", got, "think step by step")
	}
	if got := lookupReasoning("another message"); got != "" {
		t.Fatalf("lookupReasoning(unknown) = %q, want empty", got)
	}

	// 空值不保存、不查询
	storeReasoning("", "should not store")
	storeReasoning("should not store", "")
	if got := lookupReasoning(""); got != "" {
		t.Fatalf("lookupReasoning(empty) = %q, want empty", got)
	}
	if got := lookupReasoning("should not store"); got != "" {
		t.Fatalf("lookupReasoning(empty reasoning) = %q, want empty", got)
	}
}

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

// TestReasoningContentRoundTrip_NonStream 覆盖非流式链路：
// 上游返回 reasoning_content → 代理隐藏思考并缓存 → 客户端回传历史丢失明文 thinking
// → messages→chat 转换时自动补回 reasoning_content。
func TestReasoningContentRoundTrip_NonStream(t *testing.T) {
	clearReasoningContentCacheForTest()
	defer clearReasoningContentCacheForTest()

	// 1. 上游返回 reasoning_content
	providerResp := &types.ProviderResponse{
		Body: []byte(`{
			"id":"chatcmpl_test",
			"choices":[{"finish_reason":"stop","message":{
				"role":"assistant",
				"reasoning_content":"need inspect files",
				"content":"I will inspect the files."
			}}]
		}`),
	}
	claudeResp, err := (&OpenAIProvider{}).ConvertToClaudeResponse(providerResp)
	if err != nil {
		t.Fatalf("ConvertToClaudeResponse() err = %v", err)
	}
	if len(claudeResp.Content) != 1 || claudeResp.Content[0].Type != "text" {
		t.Fatalf("claude content = %#v", claudeResp.Content)
	}

	// 2. 下一轮客户端回传历史，assistant 消息只剩 text（明文 thinking 已丢失）
	c := newGinContext(http.MethodPost, "/v1/messages", []byte(`{
		"model":"reasoning-model",
		"messages":[
			{"role":"user","content":"inspect now"},
			{"role":"assistant","content":[{"type":"text","text":"I will inspect the files."}]}
		]
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
	if len(got.Messages) != 2 {
		t.Fatalf("messages = %#v", got.Messages)
	}
	assistant := got.Messages[1]
	if assistant.ReasoningContent != "need inspect files" {
		t.Fatalf("assistant reasoning_content = %q, want %q", assistant.ReasoningContent, "need inspect files")
	}
	if assistant.Content != "I will inspect the files." {
		t.Fatalf("assistant content = %#v", assistant.Content)
	}
}

// TestReasoningContentRoundTrip_Stream 覆盖流式链路（Claude Code 默认 stream=true）。
func TestReasoningContentRoundTrip_Stream(t *testing.T) {
	clearReasoningContentCacheForTest()
	defer clearReasoningContentCacheForTest()

	// 1. 上游流式返回 reasoning_content + content
	body := strings.Join([]string{
		`data: {"id":"chatcmpl_test","model":"reasoning-model","choices":[{"index":0,"delta":{"reasoning_content":"need inspect"},"finish_reason":null}]}`,
		``,
		`data: {"id":"chatcmpl_test","model":"reasoning-model","choices":[{"index":0,"delta":{"content":"I will inspect."},"finish_reason":null}]}`,
		``,
		`data: {"id":"chatcmpl_test","model":"reasoning-model","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		``,
		`data: [DONE]`,
		``,
	}, "\n")

	p := &OpenAIProvider{}
	eventChan, errChan, err := p.HandleStreamResponse(io.NopCloser(strings.NewReader(body)))
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

	// 2. 下一轮补回（assistant 历史丢失明文 thinking）
	c := newGinContext(http.MethodPost, "/v1/messages", []byte(`{
		"model":"reasoning-model",
		"messages":[
			{"role":"user","content":"go"},
			{"role":"assistant","content":[{"type":"text","text":"I will inspect."}]}
		]
	}`), nil)
	upstream := &config.UpstreamConfig{BaseURL: "https://api.example.com", ServiceType: "openai"}

	req, _, err := p.ConvertToProviderRequest(c, upstream, "sk-test")
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
	assistant := got.Messages[1]
	if assistant.ReasoningContent != "need inspect" {
		t.Fatalf("assistant reasoning_content = %q, want %q", assistant.ReasoningContent, "need inspect")
	}
	if assistant.Content != "I will inspect." {
		t.Fatalf("assistant content = %#v", assistant.Content)
	}
}

// TestConvertMessage_PrefersExplicitThinkingOverCache 确认补回不会覆盖客户端正常回传的明文 thinking。
func TestConvertMessage_PrefersExplicitThinkingOverCache(t *testing.T) {
	clearReasoningContentCacheForTest()
	defer clearReasoningContentCacheForTest()

	// 缓存里预置一条会命中同文本的 reasoning
	storeReasoning("I will inspect the files.", "cached reasoning")

	c := newGinContext(http.MethodPost, "/v1/messages", []byte(`{
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

// TestConvertMessage_UsesCompatibilityFallbackForUnrecoverableToolCall 确认工具调用
// 不会误用纯文本缓存；原始推理不可恢复时改为注入稳定的兼容续接值。
func TestConvertMessage_UsesCompatibilityFallbackForUnrecoverableToolCall(t *testing.T) {
	clearReasoningContentCacheForTest()
	defer clearReasoningContentCacheForTest()

	storeReasoning("I will inspect the files.", "cached reasoning")

	c := newGinContext(http.MethodPost, "/v1/messages", []byte(`{
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
	if got.Messages[0].ReasoningContent != missingReasoningContentFallback {
		t.Fatalf("reasoning_content = %q, want compatibility fallback", got.Messages[0].ReasoningContent)
	}
}

func TestConvertMessage_ForceReasoningContentForAssistantTextHistory(t *testing.T) {
	clearReasoningContentCacheForTest()
	defer clearReasoningContentCacheForTest()

	c := newGinContext(http.MethodPost, "/v1/messages", []byte(`{
		"model":"reasoning-model",
		"messages":[{"role":"assistant","content":"historical answer"}]
	}`), nil)
	req, _, err := (&OpenAIProvider{}).ConvertToProviderRequest(c, &config.UpstreamConfig{
		BaseURL:                 "https://api.example.com",
		ServiceType:             "openai",
		RequireReasoningContent: true,
	}, "sk-test")
	if err != nil {
		t.Fatalf("ConvertToProviderRequest() err = %v", err)
	}

	var got types.OpenAIRequest
	if err := json.NewDecoder(req.Body).Decode(&got); err != nil {
		t.Fatalf("decode request body: %v", err)
	}
	if len(got.Messages) != 1 || got.Messages[0].ReasoningContent != missingReasoningContentFallback {
		t.Fatalf("messages = %#v", got.Messages)
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
	if _, err := (&OpenAIProvider{}).ConvertToClaudeResponse(providerResp); err != nil {
		t.Fatalf("ConvertToClaudeResponse() err = %v", err)
	}

	// Claude Code 会将部分历史 thinking 回传为 redacted_thinking。
	c := newGinContext(http.MethodPost, "/v1/messages", []byte(`{
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

	eventChan, errChan, err := (&OpenAIProvider{}).HandleStreamResponse(io.NopCloser(strings.NewReader(body)))
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

	c := newGinContext(http.MethodPost, "/v1/messages", []byte(`{
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
	}, "original reasoning")

	got := lookupReasoningForAssistantMessage(types.OpenAIMessage{
		Role: "assistant",
		ToolCalls: []types.OpenAIToolCall{{
			ID: "call_stable_id",
			Function: types.OpenAIToolCallFunction{
				Name:      "Read",
				Arguments: `{"path":"compacted-path.md"}`,
			},
		}},
	})
	if got != "original reasoning" {
		t.Fatalf("lookupReasoningForAssistantMessage() = %q, want original reasoning", got)
	}
}

func TestCacheClaudeResponseReasoning_CrossProviderToolCall(t *testing.T) {
	clearReasoningContentCacheForTest()
	defer clearReasoningContentCacheForTest()

	CacheClaudeResponseReasoning(&types.ClaudeResponse{
		Role: "assistant",
		Content: []types.ClaudeContent{
			{Type: "thinking", Thinking: "reasoning from another provider"},
			{Type: "tool_use", ID: "call_cross_provider", Name: "Read", Input: map[string]interface{}{"path": "README.md"}},
		},
	})

	c := newGinContext(http.MethodPost, "/v1/messages", []byte(`{
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
