// 本文件锁定 responses 链路纯函数缝隙的可观察行为（特征测试）：
// 图片生成请求识别、输出 token 估算、流事件内容/错误判定、completed 事件解析、
// input 解析、工具参数分键、compact 模型提取与 URL 构造。
// 这些函数是未来把 responses 迁入 RunProxyRequest 骨架时要复用的接缝，
// 迁移前先以本文件锁定行为。
package responses

import (
	"strings"
	"testing"

	"github.com/insistanan/api-proxy/internal/config"
	"github.com/insistanan/api-proxy/internal/types"
)

func TestIsResponsesImageGenerationRequest(t *testing.T) {
	cases := []struct {
		name string
		body string
		want bool
	}{
		{
			name: "tools contains image_generation",
			body: `{"model":"m","tools":[{"type":"function"},{"type":"image_generation"}]}`,
			want: true,
		},
		{
			name: "tools without image_generation",
			body: `{"model":"m","tools":[{"type":"function"}]}`,
			want: false,
		},
		{
			name: "no tools field",
			body: `{"model":"m","input":"hi"}`,
			want: false,
		},
		{
			name: "invalid json",
			body: `{not-json`,
			want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isResponsesImageGenerationRequest([]byte(tc.body)); got != tc.want {
				t.Errorf("isResponsesImageGenerationRequest = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestEstimateResponsesOutputFromItems(t *testing.T) {
	t.Run("empty output", func(t *testing.T) {
		if got := estimateResponsesOutputFromItems(nil); got != 0 {
			t.Fatalf("got %d, want 0", got)
		}
	})

	t.Run("string content accumulates estimate", func(t *testing.T) {
		items := []types.ResponsesItem{
			{Type: "message", Content: "hello world"},
			{Type: "message", Content: []types.ContentBlock{{Text: "abc"}}},
		}
		single := estimateResponsesOutputFromItems(items[:1])
		if single <= 0 {
			t.Fatalf("字符串内容应产出正估算, got %d", single)
		}
		got := estimateResponsesOutputFromItems(items)
		if got <= single {
			t.Fatalf("多 item 估算应累加, got %d, single %d", got, single)
		}
	})
}

func TestHasResponsesContent(t *testing.T) {
	cases := []struct {
		name  string
		event string
		want  bool
	}{
		{
			name:  "text delta counts as content",
			event: "data: {\"type\":\"response.output_text.delta\",\"delta\":\"hi\"}\n\n",
			want:  true,
		},
		{
			name:  "completed with non-empty output counts as content",
			event: "data: {\"type\":\"response.completed\",\"response\":{\"output\":[{\"id\":\"1\"}]}}\n\n",
			want:  true,
		},
		{
			name:  "completed with empty output does not count",
			event: "data: {\"type\":\"response.completed\",\"response\":{\"output\":[]}}\n\n",
			want:  false,
		},
		{
			name:  "ping event does not count",
			event: "data: {\"type\":\"ping\"}\n\n",
			want:  false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := hasResponsesContent(tc.event); got != tc.want {
				t.Errorf("hasResponsesContent = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestResponsesStreamEventError(t *testing.T) {
	t.Run("response.failed yields error", func(t *testing.T) {
		event := "data: {\"type\":\"response.failed\",\"response\":{\"error\":{\"message\":\"boom\"}}}\n\n"
		err := responsesStreamEventError(event)
		if err == nil || !strings.Contains(err.Error(), "upstream stream failed") {
			t.Fatalf("err = %v, want upstream stream failed", err)
		}
	})

	t.Run("capacity error maps to retry-same-candidate", func(t *testing.T) {
		event := "data: {\"type\":\"error\",\"code\":\"overloaded\",\"message\":\"selected model is at capacity\"}\n\n"
		err := responsesStreamEventError(event)
		if err == nil {
			t.Fatal("容量错误应产生 error")
		}
		// NewRetrySameCandidateError 包装 ErrEmptyStreamResponse，错误文本即其消息。
		if err.Error() != ErrEmptyStreamResponse.Error() {
			t.Fatalf("容量错误应映射为同候选重试（包装空流响应）, got %q", err.Error())
		}
	})

	t.Run("ordinary event yields nil", func(t *testing.T) {
		if err := responsesStreamEventError("data: {\"type\":\"response.output_text.delta\"}\n\n"); err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
	})
}

func TestIsResponsesCompletedEventAndIDExtraction(t *testing.T) {
	completed := "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_123\"}}\n\n"
	if !isResponsesCompletedEvent(completed) {
		t.Fatal("completed 事件应被识别")
	}
	if id := extractResponsesCompletedID(completed); id != "resp_123" {
		t.Fatalf("id = %q, want resp_123", id)
	}
	if isResponsesCompletedEvent("data: {\"type\":\"response.output_text.delta\"}\n\n") {
		t.Fatal("非 completed 事件不应被识别")
	}
	if id := extractResponsesCompletedID("data: {\"type\":\"response.completed\"}\n\n"); id != "" {
		t.Fatalf("缺 response.id 时应返回空串, got %q", id)
	}
}

func TestParseInputToItemsStringInput(t *testing.T) {
	items, err := parseInputToItems("hello")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(items) != 1 || items[0].Type != "text" {
		t.Fatalf("字符串输入应产出单个 text item, got %+v", items)
	}
}

func TestResponsesStreamToolArgumentKey(t *testing.T) {
	cases := []struct {
		name     string
		data     map[string]interface{}
		fallback int
		want     string
	}{
		{"item_id wins", map[string]interface{}{"item_id": "it_1", "call_id": "c_1"}, 3, "responses.tool.it_1"},
		{"call_id second", map[string]interface{}{"call_id": "c_1"}, 3, "responses.tool.c_1"},
		{"output_index last", map[string]interface{}{"output_index": 2}, 3, "responses.tool.2"},
		{"fallback when absent", map[string]interface{}{}, 7, "responses.tool.default.7"},
		{"blank value falls through", map[string]interface{}{"item_id": "  "}, 5, "responses.tool.default.5"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := responsesStreamToolArgumentKey(tc.data, tc.fallback); got != tc.want {
				t.Errorf("key = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestCompactRequestModel(t *testing.T) {
	if got := compactRequestModel([]byte(`{"model":"  gpt-x ","input":"..."}`)); got != "gpt-x" {
		t.Fatalf("model = %q, want gpt-x（应去除首尾空白）", got)
	}
	if got := compactRequestModel([]byte(`not-json`)); got != "" {
		t.Fatalf("非法 JSON 应返回空串, got %q", got)
	}
}

func TestBuildCompactURL(t *testing.T) {
	cases := []struct {
		name     string
		baseURL  string
		expected string
	}{
		{"with version suffix keeps single prefix", "https://api.example.com/v1", "https://api.example.com/v1/responses/compact"},
		{"without version adds v1", "https://api.example.com", "https://api.example.com/v1/responses/compact"},
		{"trailing slash trimmed then version detected", "https://api.example.com/openai/v1/", "https://api.example.com/openai/v1/responses/compact"},
		{"no version deep path", "https://proxy.example.com/api", "https://proxy.example.com/api/v1/responses/compact"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			upstream := &config.UpstreamConfig{BaseURL: tc.baseURL}
			if got := buildCompactURL(upstream); got != tc.expected {
				t.Errorf("url = %q, want %q", got, tc.expected)
			}
		})
	}
}
