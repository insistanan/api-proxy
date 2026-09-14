// R4-a 验收测试：Provider 显式输入构造与 gin 薄壳的行为一致性。
// 同一组固定样本（方法 / 路径 / 头 / 载荷 / 会话决定）分别经
// ConvertToProviderRequest（gin）与 BuildRequest（显式）构造，
// 比对请求方法、目标、头与载荷，确保迁移没有改变转换行为。
package providers

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"github.com/BenedictKing/api-proxy/internal/config"
	"github.com/BenedictKing/api-proxy/internal/types"
	"github.com/gin-gonic/gin"
)

const sampleClaudeBody = `{"model":"claude-3-5-sonnet","max_tokens":128,"stream":false,"system":"你是测试","messages":[{"role":"user","content":"ping"}]}`

// ginContextForSample 构造与样本输入等价的 gin context。
func ginContextForSample(t *testing.T, body string, conversationID string) *gin.Context {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Custom-Header", "custom-value")
	c.Request = req
	if conversationID != "" {
		c.Set("conversation-user-id", conversationID)
	}
	return c
}

// assertRequestsEquivalent 比对两个请求的方法、URL、头（关键字段）与载荷。
func assertRequestsEquivalent(t *testing.T, got, want *http.Request, label string) {
	t.Helper()
	if got.Method != want.Method {
		t.Fatalf("%s: Method = %s, want %s", label, got.Method, want.Method)
	}
	if got.URL.String() != want.URL.String() {
		t.Fatalf("%s: URL = %s, want %s", label, got.URL.String(), want.URL.String())
	}
	// 头按规范化文本比较（排序后顺序无关）。
	normalize := func(h http.Header) string {
		keys := make([]string, 0, len(h))
		for key := range h {
			if key == "Host" {
				continue
			}
			keys = append(keys, key+": "+strings.Join(h[key], ","))
		}
		sort.Strings(keys)
		data, _ := json.Marshal(keys)
		return string(data)
	}
	if normalize(got.Header) != normalize(want.Header) {
		t.Fatalf("%s: Header 不一致\ngot:  %s\nwant: %s", label, normalize(got.Header), normalize(want.Header))
	}
	// 载荷比对。
	readBody := func(req *http.Request) string {
		if req == nil || req.Body == nil {
			return ""
		}
		data, _ := io.ReadAll(req.Body)
		return string(data)
	}
	if readBody(got) != readBody(want) {
		t.Fatalf("%s: Body 不一致\ngot:  %s\nwant: %s", label, readBody(got), readBody(want))
	}
}

// TestClaudeProviderExplicitMatchesGin：Claude 透传 provider 的一致性。
func TestClaudeProviderExplicitMatchesGin(t *testing.T) {
	upstream := &config.UpstreamConfig{
		BaseURL: "https://upstream.example.com",
		ModelMapping: map[string][]string{
			"claude-3-5-sonnet": {"mapped-model"},
		},
	}
	provider := &ClaudeProvider{}

	// gin 路径。
	c := ginContextForSample(t, sampleClaudeBody, "")
	ginReq, ginBody, err := provider.ConvertToProviderRequest(c, upstream, "sk-test")
	if err != nil {
		t.Fatalf("gin 路径构造失败: %v", err)
	}

	// 显式路径。
	outcome, err := provider.BuildRequest(RequestInput{
		Ctx:    c.Request.Context(),
		Method: http.MethodPost,
		Path:   "/v1/messages",
		Header: c.Request.Header,
		Body:   []byte(sampleClaudeBody),
	}, upstream, "sk-test")
	if err != nil {
		t.Fatalf("显式路径构造失败: %v", err)
	}

	assertRequestsEquivalent(t, outcome.Request, ginReq, "claude")
	if string(outcome.OriginalBody) != string(ginBody) {
		t.Fatalf("OriginalBody 不一致:\n got: %s\nwant: %s", outcome.OriginalBody, ginBody)
	}
	if !strings.Contains(string(outcome.OriginalBody), "mapped-model") {
		t.Fatal("模型映射应生效")
	}
}

// TestOpenAIProviderExplicitMatchesGin：OpenAI provider 的一致性。
func TestOpenAIProviderExplicitMatchesGin(t *testing.T) {
	upstream := &config.UpstreamConfig{
		BaseURL: "https://openai.example.com",
	}
	provider := &OpenAIProvider{}

	c := ginContextForSample(t, sampleClaudeBody, "")
	ginReq, ginBody, err := provider.ConvertToProviderRequest(c, upstream, "sk-test")
	if err != nil {
		t.Fatalf("gin 路径构造失败: %v", err)
	}

	outcome, err := provider.BuildRequest(RequestInput{
		Ctx:    c.Request.Context(),
		Method: http.MethodPost,
		Path:   "/v1/chat/completions",
		Header: c.Request.Header,
		Body:   []byte(sampleClaudeBody),
	}, upstream, "sk-test")
	if err != nil {
		t.Fatalf("显式路径构造失败: %v", err)
	}

	assertRequestsEquivalent(t, outcome.Request, ginReq, "openai")
	if string(outcome.OriginalBody) != string(ginBody) {
		t.Fatalf("OriginalBody 不一致")
	}
}

// TestResponsesProviderExplicitMatchesGin：Responses 转换产物（链状态）一致性。
func TestResponsesProviderExplicitMatchesGin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	// 无 SessionManager 时显式路径会报"会话管理器未初始化"（非透传分支），
	// 这里只验证透传分支（ServiceType=responses）的一致性。
	upstream := &config.UpstreamConfig{
		BaseURL:     "https://resp.example.com",
		ServiceType: "responses",
	}
	provider := &ResponsesProvider{}

	body := `{"model":"gpt-5","input":"hello","stream":false}`
	c := ginContextForSample(t, body, "conv-123")
	ginReq, ginBody, err := provider.ConvertToProviderRequest(c, upstream, "sk-test")
	if err != nil {
		t.Fatalf("gin 路径构造失败: %v", err)
	}
	dropped, _ := c.Get("responses-previous-id-dropped")
	boundary, _ := c.Get("responses-history-boundary")

	outcome, err := provider.BuildRequest(RequestInput{
		Ctx:            c.Request.Context(),
		Method:         http.MethodPost,
		Path:           "/v1/responses",
		Header:         c.Request.Header,
		Body:           []byte(body),
		ConversationID: "conv-123",
	}, upstream, "sk-test")
	if err != nil {
		t.Fatalf("显式路径构造失败: %v", err)
	}

	assertRequestsEquivalent(t, outcome.Request, ginReq, "responses")
	if string(outcome.OriginalBody) != string(ginBody) {
		t.Fatalf("OriginalBody 不一致")
	}
	if outcome.PreviousIDDropped != dropped.(bool) || outcome.HistoryBoundary != boundary.(bool) {
		t.Fatalf("转换产物不一致: outcome=%+v gin=(%v,%v)", outcome, dropped, boundary)
	}
}

// TestBuildUpstreamRequestRejectsNilProvider：防御性输入。
func TestBuildUpstreamRequestRejectsNilProvider(t *testing.T) {
	if _, err := BuildUpstreamRequest(nil, RequestInput{}, &config.UpstreamConfig{}, "k"); err == nil {
		t.Fatal("nil provider 应返回显式错误")
	}
}

// 确保样本体可被解析为 ClaudeRequest（守卫测试自身）。
func TestSampleBodyParses(t *testing.T) {
	var req types.ClaudeRequest
	if err := json.Unmarshal([]byte(sampleClaudeBody), &req); err != nil {
		t.Fatalf("样本体解析失败: %v", err)
	}
	if req.Model == "" || len(req.Messages) == 0 {
		t.Fatal("样本体缺字段")
	}
	_ = bytes.MinRead
}
