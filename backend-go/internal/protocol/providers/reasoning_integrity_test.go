package providers

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/insistanan/api-proxy/internal/infra/config"
	"github.com/insistanan/api-proxy/internal/infra/utils"
	"github.com/insistanan/api-proxy/internal/protocol/types"
)

var reasoningTestScope = ReasoningCacheScope("reasoning-test", &config.UpstreamConfig{
	BaseURL: "https://api.example.com", ServiceType: "openai",
}, "reasoning-model")

func newReasoningContext(method, url string, body []byte, ctx context.Context) *gin.Context {
	c := newGinContext(method, url, body, ctx)
	c.Set(utils.ContextKeyConversationUserID, "reasoning-test")
	return c
}

func newReasoningTestProvider() *OpenAIProvider {
	return &OpenAIProvider{hideReasoning: true, reasoningScope: reasoningTestScope}
}

func TestOpenAIReasoning_PreservesIndependentContent(t *testing.T) {
	for _, content := range []string{"你好", "你好，我需要分析", "你好，我需要分析\n答案", "<think>这是代码示例</think>"} {
		t.Run(content, func(t *testing.T) {
			body, err := json.Marshal(map[string]interface{}{
				"choices": []interface{}{map[string]interface{}{
					"message":       map[string]string{"role": "assistant", "reasoning_content": "你好，我需要分析", "content": content},
					"finish_reason": "stop",
				}},
			})
			if err != nil {
				t.Fatal(err)
			}
			response, err := (&OpenAIProvider{}).ConvertToClaudeResponse(&types.ProviderResponse{Body: body})
			if err != nil {
				t.Fatal(err)
			}
			if len(response.Content) != 2 || response.Content[0].Thinking != "你好，我需要分析" || response.Content[1].Text != content {
				t.Fatalf("思考与正文应独立保留：%+v", response.Content)
			}
			if response.Content[0].Signature != "" {
				t.Fatal("不能伪造 Claude 签名")
			}
		})
	}
}

func TestEmbeddedReasoning_EveryChunkBoundary(t *testing.T) {
	for _, tag := range []string{"think", "thinking", "THINK"} {
		text := "前文<" + tag + ">思考</" + tag + ">答案<thi"
		for split := 0; split <= len(text); split++ {
			parser := embeddedReasoningParser{}
			parts := append(parser.feed(text[:split]), parser.feed(text[split:])...)
			tail, err := parser.finish()
			if err != nil {
				t.Fatalf("tag=%s split=%d: %v", tag, split, err)
			}
			var visible, reasoning strings.Builder
			for _, part := range append(parts, tail...) {
				if part.thinking {
					reasoning.WriteString(part.text)
				} else {
					visible.WriteString(part.text)
				}
			}
			if visible.String() != "前文答案<thi" || reasoning.String() != "思考" {
				t.Fatalf("tag=%s split=%d 正文=%q 推理=%q", tag, split, visible.String(), reasoning.String())
			}
		}
	}
}

func TestOpenAIReasoning_StreamModesAndSplitTags(t *testing.T) {
	for _, mode := range []struct {
		name string
		hide bool
	}{{name: "preserve"}, {name: "hide", hide: true}} {
		t.Run(mode.name, func(t *testing.T) {
			state := newChatToClaudeStreamState()
			state.hideReasoning = mode.hide
			state.parseEmbeddedReasoning = true
			var events strings.Builder
			for _, content := range []string{"<thi", "nk>思考</thi", "nk>答案"} {
				body, err := json.Marshal(map[string]interface{}{
					"model": "reasoning-model", "choices": []interface{}{map[string]interface{}{"delta": map[string]string{"content": content}}},
				})
				if err != nil {
					t.Fatal(err)
				}
				for _, event := range state.ProcessLine("data: " + string(body)) {
					events.WriteString(event)
				}
			}
			for _, event := range state.ProcessLine(`data: {"choices":[{"delta":{},"finish_reason":"stop"}]}`) {
				events.WriteString(event)
			}
			for _, event := range state.ProcessLine("data: [DONE]") {
				events.WriteString(event)
			}
			got := events.String()
			if state.Failed() || !strings.Contains(got, `"text":"答案"`) || strings.Count(got, "event: message_stop") != 1 {
				t.Fatalf("流式收尾或正文错误：%v\n%s", state.streamError, got)
			}
			if strings.Contains(got, `"type":"thinking_delta"`) == mode.hide || strings.Contains(got, "<thi") {
				t.Fatalf("思考输出模式错误：%s", got)
			}
			if !mode.hide && !strings.Contains(got, `"thinking":"思考"`) {
				t.Fatalf("思考原文丢失：%s", got)
			}
		})
	}
}

func TestEmbeddedReasoning_UnclosedIsExplicitError(t *testing.T) {
	if _, _, err := splitEmbeddedReasoning("<think>未闭合</thi"); err == nil {
		t.Fatal("非流式未闭合标签应报错")
	}
	state := newChatToClaudeStreamState()
	state.parseEmbeddedReasoning = true
	state.ProcessLine(`data: {"model":"test","choices":[{"delta":{"content":"<think>未闭合"}}]}`)
	state.ProcessLine("data: [DONE]")
	if !state.Failed() || state.messageStopEmitted {
		t.Fatal("流式未闭合标签不能伪造成功收尾")
	}
}

func TestOpenAIReasoning_DedicatedFieldTakesPrecedence(t *testing.T) {
	response, err := (&OpenAIProvider{parseEmbeddedReasoning: true}).ConvertToClaudeResponse(&types.ProviderResponse{Body: []byte(`{
		"choices":[{"message":{"role":"assistant","reasoning_content":"原始思考","content":"<think>重复思考</think>答案"}}]
	}`)})
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Content) != 2 || response.Content[0].Thinking != "原始思考" || response.Content[1].Text != "答案" {
		t.Fatalf("独立推理字段不能与嵌入副本拼接：%+v", response.Content)
	}
	state := newChatToClaudeStreamState()
	state.parseEmbeddedReasoning = true
	state.ProcessLine(`data: {"model":"test","choices":[{"delta":{"reasoning_content":"原始思考"}}]}`)
	state.ProcessLine(`data: {"model":"test","choices":[{"delta":{"content":"<think>重复思考</think>答案"}}]}`)
	if state.hasEmbeddedReasoning || !state.hasDedicatedReasoning || state.Failed() {
		t.Fatal("流式不能把独立推理与嵌入副本混为一份原文")
	}
}

func TestEmbeddedReasoning_TailPrecedesToolCall(t *testing.T) {
	state := newChatToClaudeStreamState()
	state.parseEmbeddedReasoning = true
	state.ProcessLine(`data: {"model":"test","choices":[{"delta":{"content":"正文<thi"}}]}`)
	events := state.ProcessLine(`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_a","function":{"name":"Read","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`)
	joined := strings.Join(events, "")
	textIndex, toolIndex := -1, -1
	for index, line := range strings.Split(joined, "\n") {
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var event struct {
			Delta struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"delta"`
			ContentBlock struct {
				Type string `json:"type"`
			} `json:"content_block"`
		}
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event); err != nil {
			t.Fatal(err)
		}
		if event.Delta.Type == "text_delta" && strings.HasSuffix(event.Delta.Text, "<thi") {
			textIndex = index
		}
		if event.ContentBlock.Type == "tool_use" {
			toolIndex = index
		}
	}
	if state.Failed() || textIndex < 0 || toolIndex < 0 || textIndex > toolIndex {
		t.Fatalf("正文尾部必须在工具调用前下发：%v\n%s", state.streamError, joined)
	}
}

func TestReasoningCache_IsolatesConversationAndSource(t *testing.T) {
	clearReasoningContentCacheForTest()
	defer clearReasoningContentCacheForTest()
	up := &config.UpstreamConfig{ID: "channel-a", ServiceType: "openai", BaseURL: "https://api.example.com"}
	scope := ReasoningCacheScope("conversation-a", up, "model-a")
	message := types.OpenAIMessage{Role: "assistant", Content: "相同答案"}
	storeReasoningForAssistantMessage(message, "原始思考", scope)
	otherChannel := up.Clone()
	otherChannel.ID = "channel-b"
	for _, other := range []string{
		ReasoningCacheScope("conversation-b", up, "model-a"),
		ReasoningCacheScope("conversation-a", otherChannel, "model-a"),
		ReasoningCacheScope("conversation-a", up, "model-b"),
		ReasoningCacheScope("", up, "model-a"),
	} {
		if got := lookupReasoningForAssistantMessage(message, other); got != "" {
			t.Fatalf("缓存不能跨会话或来源使用：%q", got)
		}
	}
	if got := lookupReasoningForAssistantMessage(message, scope); got != "原始思考" {
		t.Fatalf("同范围应恢复原文：%q", got)
	}
	storeReasoningForAssistantMessage(message, "另一轮不同思考", scope)
	if got := lookupReasoningForAssistantMessage(message, scope); got != "" {
		t.Fatalf("存在歧义不能猜测原文：%q", got)
	}
}

func TestOpenAIReasoning_ConfigurationAndExplicitRoundTrip(t *testing.T) {
	up := &config.UpstreamConfig{BaseURL: "https://api.example.com", ServiceType: "openai", ReasoningOutputMode: "hide", ParseEmbeddedReasoning: true}
	provider := &OpenAIProvider{}
	c := newReasoningContext(http.MethodPost, "/v1/messages", []byte(`{"model":"reasoning-model","messages":[]}`), nil)
	req, _, err := provider.ConvertToProviderRequest(c, up, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	defer req.Body.Close()
	if !provider.hideReasoning || !provider.parseEmbeddedReasoning || provider.reasoningScope == "" {
		t.Fatal("请求构造必须保存输出选项和会话来源")
	}
	up.ReasoningOutputMode = "unknown"
	if _, err := provider.BuildRequest(RequestInput{Ctx: context.Background(), Body: []byte(`{}`)}, up, "fixture"); err == nil {
		t.Fatal("未知模式必须报错")
	}
	up.ReasoningOutputMode = "preserve"
	up.RequireReasoningContent = true
	c = newReasoningContext(http.MethodPost, "/v1/messages", []byte(`{
		"model":"reasoning-model","messages":[{"role":"assistant","content":[
			{"type":"thinking","thinking":"  完整原文\n"},{"type":"text","text":"答案"}
		]}]
	}`), nil)
	req, _, err = provider.ConvertToProviderRequest(c, up, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	defer req.Body.Close()
	var payload types.OpenAIRequest
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if payload.Messages[0].ReasoningContent != "  完整原文\n" {
		t.Fatalf("不能修剪原始推理：%q", payload.Messages[0].ReasoningContent)
	}
}

func TestReasoningCache_AmbiguousToolIDCannotUseAnotherID(t *testing.T) {
	clearReasoningContentCacheForTest()
	defer clearReasoningContentCacheForTest()
	message := types.OpenAIMessage{
		Role: "assistant", Content: "原始正文",
		ToolCalls: []types.OpenAIToolCall{{ID: "call_reused"}, {ID: "call_unique"}},
	}
	storeReasoningForAssistantMessage(message, "第一轮思考", reasoningTestScope)
	storeReasoningForAssistantMessage(types.OpenAIMessage{
		Role: "assistant", Content: "另一轮正文",
		ToolCalls: []types.OpenAIToolCall{{ID: "call_reused"}},
	}, "另一轮思考", reasoningTestScope)
	message.Content = "压缩后的正文"
	if got := lookupReasoningForAssistantMessage(message, reasoningTestScope); got != "" {
		t.Fatalf("一个工具 ID 有歧义时，不能借另一个 ID 猜测原文：%q", got)
	}
}

func TestReasoningCache_OversizedConflictInvalidatesOldValue(t *testing.T) {
	cache := newReasoningContentCache(2)
	cache.maxBytes = 8
	cache.put("key", "old")
	cache.put("key", "oversized different reasoning")
	if value, exists := cache.lookup("key"); !exists || value != "" {
		t.Fatalf("新推理过大也不能保留旧推理造成误恢复：value=%q exists=%v", value, exists)
	}
}
