package converters

import (
	"encoding/json"
	"testing"

	"insistanan/api-proxy/internal/protocol/types"
)

func TestResponsesSearchCapabilityToolConstraints(t *testing.T) {
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
					_, err := ConvertResponsesRequestToUpstream(serviceType, req.Model, body, stream, nil, &req, nil)
					if _, ok := err.(*types.UnsupportedProtocolError); !ok {
						t.Fatalf("stream=%v 必须保留搜索能力错误: %v", stream, err)
					}

				}
			})
		}
	}
}

func TestResponsesSearchCapabilityKeepsOriginalRequestAndHistory(t *testing.T) {
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
			_, err = ConvertResponsesRequestToUpstream(serviceType, req.Model, body, false, sess, &req, nil)
			if _, ok := err.(*types.UnsupportedProtocolError); !ok {
				t.Fatalf("搜索历史不可等价回放时返回能力错误: %v", err)
			}
			after, err := json.Marshal([]interface{}{req, sess})
			if err != nil {
				t.Fatal(err)
			}
			if string(before) != string(after) {
				t.Fatal("能力检查修改了原始请求或会话")
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

func TestResponsesSearchCapabilityPreservesOtherValidationErrors(t *testing.T) {
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
			t.Fatalf("能力检查不能掩盖其他不支持的工具或非法参数: %s", body)
		}
	}
}
