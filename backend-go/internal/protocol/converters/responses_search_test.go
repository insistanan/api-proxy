package converters

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/insistanan/api-proxy/internal/protocol/types"
	"github.com/tidwall/gjson"
)

func TestResponsesSearchDowngradeToolConstraints(t *testing.T) {
	cases := []struct {
		name      string
		tools     string
		choice    string
		remaining string
		forced    bool
	}{
		{"only_required", `[{"type":"web_search"}]`, `"required"`, "", false},
		{"only_required_object", `[{"type":"web_search"}]`, `{"type":"required"}`, "", false},
		{"only_preview", `[{"type":"web_search_preview"}]`, `{"type":"web_search_preview"}`, "", false},
		{"mixed_required", `[{"type":"web_search"},{"type":"function","name":"read_file"}]`, `"required"`, "read_file", true},
		{"mixed_forced_search", `[{"type":"web_search"},{"type":"function","name":"read_file"}]`, `{"type":"web_search"}`, "read_file", false},
		{"client_search", `[{"type":"web_search"},{"type":"function","name":"web_search"}]`, `{"type":"function","name":"web_search"}`, "web_search", true},
	}
	for _, serviceType := range []string{ResponsesUpstreamOpenAI, ResponsesUpstreamClaude, ResponsesUpstreamGemini} {
		for _, tc := range cases {
			t.Run(serviceType+"/"+tc.name, func(t *testing.T) {
				for _, stream := range []bool{false, true} {
					streamValue := "false"
					if stream {
						streamValue = "true"
					}
					body := []byte(`{"model":"test-model","stream":` + streamValue + `,"input":"继续对话","tools":` + tc.tools + `,"tool_choice":` + tc.choice + `,"parallel_tool_calls":true}`)
					var req types.ResponsesRequest
					if err := json.Unmarshal(body, &req); err != nil {
						t.Fatal(err)
					}
					result, err := ConvertResponsesRequestToUpstream(serviceType, req.Model, body, stream, nil, &req, nil)
					if err != nil {
						t.Fatalf("stream=%v 搜索降级后转换失败: %v", stream, err)
					}
					root := gjson.ParseBytes(result)
					if !strings.Contains(string(result), "继续对话") {
						t.Fatalf("搜索降级不能丢失普通输入: %s", result)
					}
					if tc.remaining == "" {
						if root.Get("tools").Exists() || root.Get("tool_choice").Exists() || root.Get("toolConfig").Exists() || root.Get("parallel_tool_calls").Exists() {
							t.Fatalf("搜索是唯一工具时不得留下工具约束: %s", result)
						}
						continue
					}
					namePath, choicePath := "tools.0.function.name", "tool_choice"
					switch serviceType {
					case ResponsesUpstreamClaude:
						namePath = "tools.0.name"
					case ResponsesUpstreamGemini:
						namePath, choicePath = "tools.0.functionDeclarations.0.name", "toolConfig"
					}
					if root.Get(namePath).String() != tc.remaining || len(root.Get("tools").Array()) != 1 {
						t.Fatalf("应只保留客户端工具 %s: %s", tc.remaining, result)
					}
					if root.Get(choicePath).Exists() != tc.forced {
						t.Fatalf("客户端工具约束应保留，内置搜索约束应移除: %s", result)
					}
				}
			})
		}
	}
}

func TestResponsesSearchDowngradeKeepsOriginalRequestAndHistory(t *testing.T) {
	body := []byte(`{"model":"test-model","input":"继续对话","tools":[{"type":"web_search"}],"tool_choice":{"type":"web_search"},"include":["web_search_call.action.sources"]}`)
	for _, serviceType := range []string{ResponsesUpstreamOpenAI, ResponsesUpstreamClaude, ResponsesUpstreamGemini} {
		t.Run(serviceType, func(t *testing.T) {
			var req types.ResponsesRequest
			if err := json.Unmarshal(body, &req); err != nil {
				t.Fatal(err)
			}
			sess := &types.Session{Messages: []types.ResponsesItem{
				{Type: "message", Role: "user", Content: "历史问题"},
				{Type: "web_search_call", ID: "search_1", Action: map[string]interface{}{"type": "search", "query": "Go"}},
				{Type: "tool_search_call", CallID: "load-tools", Arguments: "{}"},
				{Type: "tool_search_output", CallID: "load-tools", Tools: []map[string]interface{}{{"type": "web_search"}}},
				{Type: "message", Role: "assistant", Content: "历史正文"},
			}}
			before, err := json.Marshal([]interface{}{req, sess})
			if err != nil {
				t.Fatal(err)
			}
			result, err := ConvertResponsesRequestToUpstream(serviceType, req.Model, body, false, sess, &req, nil)
			if err != nil {
				t.Fatal(err)
			}
			for _, text := range []string{"历史问题", "历史正文", "继续对话"} {
				if !strings.Contains(string(result), text) {
					t.Fatalf("搜索历史降级后丢失正文 %q: %s", text, result)
				}
			}
			if strings.Contains(string(result), "search_1") || strings.Contains(string(result), "load-tools") {
				t.Fatalf("不应留下无法执行的搜索调用元数据: %s", result)
			}
			after, err := json.Marshal([]interface{}{req, sess})
			if err != nil {
				t.Fatal(err)
			}
			if string(before) != string(after) {
				t.Fatal("搜索降级修改了原始请求或会话")
			}
			native, err := ConvertResponsesRequestToUpstream(ResponsesUpstreamResponses, req.Model, body, false, sess, &req, nil)
			if err != nil {
				t.Fatal(err)
			}
			if string(native) != string(body) {
				t.Fatalf("原生候选仍须保留全部搜索字段: %s", native)
			}
		})
	}
}

func TestResponsesSearchDowngradePreservesOtherValidationErrors(t *testing.T) {
	for _, body := range []string{
		`{"model":"test-model","input":"继续","tools":[{"type":"web_search"},{"type":"file_search"}]}`,
		`{"model":"test-model","input":"继续","tools":[{"type":"web_search"},{"type":"function"}]}`,
		`{"model":"test-model","input":"继续","tools":[{"type":"web_search"}],"tool_choice":42}`,
		`{"model":"test-model","input":[42],"tools":[{"type":"web_search"}]}`,
	} {
		var req types.ResponsesRequest
		if err := json.Unmarshal([]byte(body), &req); err != nil {
			t.Fatal(err)
		}
		if _, err := ConvertResponsesRequestToUpstream(ResponsesUpstreamOpenAI, req.Model, []byte(body), false, nil, &req, nil); err == nil {
			t.Fatalf("搜索降级不能掩盖其他不支持的工具或非法参数: %s", body)
		}
	}
}
