package converters

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/insistanan/api-proxy/internal/config"
	"github.com/insistanan/api-proxy/internal/types"
	"github.com/insistanan/api-proxy/internal/utils"
	"github.com/tidwall/gjson"
)

func TestResponsesToChat_AssociatesOriginalReasoningWithAssistantTurn(t *testing.T) {
	body := []byte(`{"model":"reasoning-model","input":[
		{"type":"reasoning","summary":[{"type":"summary_text","text":"展示摘要"}],"content":[{"type":"reasoning_text","text":"  原始思考\n"}]},
		{"type":"message","role":"assistant","content":[{"type":"output_text","text":"答案"}]},
		{"type":"function_call","call_id":"call_a","name":"Read","arguments":"{}"},
		{"type":"function_call_output","call_id":"call_a","output":"结果"}
	]}`)
	var req types.ResponsesRequest
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatal(err)
	}
	for _, include := range []bool{false, true} {
		converted, err := ConvertResponsesRequestToUpstream("openai", req.Model, body, false, nil, &req, &config.UpstreamConfig{
			RequireReasoningContent: true, IncludeHistoryThinking: include,
		})
		if err != nil {
			t.Fatal(err)
		}
		root := gjson.ParseBytes(converted)
		if len(root.Get("messages").Array()) != 2 || root.Get("messages.0.reasoning_content").String() != "  原始思考\n" ||
			root.Get("messages.0.content").String() != "答案" || root.Get("messages.0.tool_calls.0.id").String() != "call_a" ||
			root.Get("messages.1.role").String() != "tool" {
			t.Fatalf("同一 assistant 回合的文本、思考、工具调用应关联：%s", converted)
		}
		if strings.Contains(string(converted), "展示摘要") || strings.Contains(string(converted), "[thinking]") {
			t.Fatalf("摘要不能污染正文或替代原文：%s", converted)
		}
	}
}

func TestResponsesToChat_SummaryDoesNotReplaceRequiredOriginal(t *testing.T) {
	body := []byte(`{"model":"test","input":[
		{"type":"reasoning","summary":[{"type":"summary_text","text":"只有摘要"}],"encrypted_content":"opaque"},
		{"type":"function_call","call_id":"call_a","name":"Read","arguments":"{}"}
	]}`)
	var req types.ResponsesRequest
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatal(err)
	}
	_, err := ConvertResponsesRequestToUpstream("openai", req.Model, body, false, nil, &req, &config.UpstreamConfig{RequireReasoningContent: true})
	if err == nil {
		t.Fatal("严格 Chat 渠道不能用摘要替代原始推理")
	}
	var historyErr *types.ReasoningHistoryError
	if !errors.As(err, &historyErr) || !historyErr.ClientRequestError() {
		t.Fatalf("缺失原文应返回请求错误，避免无效重试：%v", err)
	}
	converted, err := ConvertResponsesRequestToUpstream("openai", req.Model, body, false, nil, &req, nil)
	if err != nil {
		t.Fatal(err)
	}
	if gjson.GetBytes(converted, "messages.0.reasoning_content").Exists() {
		t.Fatalf("非严格渠道也不能生成占位推理：%s", converted)
	}
}

func TestResponsesToChat_ToolCallBeforeTextKeepsOriginalReasoning(t *testing.T) {
	body := []byte(`{"model":"test","input":[
		{"type":"reasoning","content":[{"type":"reasoning_text","text":"原始思考"}]},
		{"type":"function_call","call_id":"call_a","name":"Read","arguments":"{}"},
		{"type":"message","role":"assistant","content":[{"type":"output_text","text":"答案"}]},
		{"type":"function_call_output","call_id":"call_a","output":"结果"}
	]}`)
	var req types.ResponsesRequest
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatal(err)
	}
	converted, err := ConvertResponsesRequestToUpstream("openai", req.Model, body, false, nil, &req, &config.UpstreamConfig{RequireReasoningContent: true})
	if err != nil {
		t.Fatal(err)
	}
	root := gjson.ParseBytes(converted)
	if len(root.Get("messages").Array()) != 2 || root.Get("messages.0.reasoning_content").String() != "原始思考" ||
		root.Get("messages.0.content").String() != "答案" || root.Get("messages.0.tool_calls.0.id").String() != "call_a" {
		t.Fatalf("工具调用先于正文也应保留同一回合的原始推理：%s", converted)
	}
}

func TestChatToResponses_OriginalReasoningSurvivesNonStreamRoundTrip(t *testing.T) {
	upstream := []byte(`{"id":"chat_test","model":"reasoning-model","choices":[{"message":{
		"role":"assistant","reasoning_content":"  原始思考\n","content":"答案"
	},"finish_reason":"stop"}]}`)
	response, err := ConvertUpstreamResponseToResponses("openai", []byte(`{"model":"reasoning-model"}`), upstream, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Output) != 2 || ExtractResponsesOriginalReasoningText(response.Output[0]) != "  原始思考\n" ||
		ExtractResponsesReasoningText(response.Output[0]) != "  原始思考\n" {
		t.Fatalf("推理原文不能丢失或重复：%+v", response.Output)
	}
	request, err := json.Marshal(types.ResponsesRequest{Model: "reasoning-model", Input: response.Output})
	if err != nil {
		t.Fatal(err)
	}
	converted := ConvertResponsesToOpenAIChatRequest("reasoning-model", request, false)
	if gjson.GetBytes(converted, "messages.0.reasoning_content").String() != "  原始思考\n" {
		t.Fatalf("下一轮应恢复原始推理：%s", converted)
	}
}

func TestChatToResponses_StreamCompletedRetainsOriginalReasoning(t *testing.T) {
	var state any
	var completed types.ResponsesResponse
	for _, line := range []string{
		`data: {"id":"chat_test","model":"test","choices":[{"delta":{"reasoning_content":"  原始思考\n"}}]}`,
		`data: {"id":"chat_test","model":"test","choices":[{"delta":{"content":"答案"},"finish_reason":"stop"}]}`,
		"data: [DONE]",
	} {
		events := ConvertOpenAIChatToResponses(context.Background(), "test", []byte(`{"model":"test"}`), nil, []byte(line), &state)
		for _, event := range events {
			for _, eventLine := range strings.Split(event, "\n") {
				payload, ok := utils.SSEDataJSON(eventLine)
				if !ok || gjson.Get(payload, "type").String() != "response.completed" {
					continue
				}
				if err := json.Unmarshal([]byte(gjson.Get(payload, "response").Raw), &completed); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	if len(completed.Output) < 1 || ExtractResponsesOriginalReasoningText(completed.Output[0]) != "  原始思考\n" {
		t.Fatalf("流式定稿必须保留可重放的原始推理：%+v", completed.Output)
	}
}
