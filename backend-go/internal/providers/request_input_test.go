// R4-a 验收测试：Provider 显式输入构造与 gin 薄壳的行为一致性。
// 同一组固定样本（方法 / 路径 / 头 / 载荷 / 会话决定）分别经
// ConvertToProviderRequest（gin）与 BuildRequest（显式）构造，
// 比对请求方法、目标、头与载荷，确保迁移没有改变转换行为。
package providers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/insistanan/api-proxy/internal/config"
	"github.com/insistanan/api-proxy/internal/session"
	"github.com/insistanan/api-proxy/internal/types"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
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

// 工具结果合并会改变载荷长度；首次发送及多次重放必须使用合并后的同一份内容。
func TestClaudeProviderBuildRequest_FinalBodyIsReplayable(t *testing.T) {
	toolBody := `{"model":"claude-3-5-sonnet","max_tokens":128,"stream":true,"safeguards":{"enabled":true},"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"tool-1","name":"read_file","input":{}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"tool-1","content":"输出"},{"type":"text","text":"补充文本"}]}]}`
	cases := []struct {
		name     string
		body     string
		disguise bool
		merged   bool
	}{
		{name: "关闭伪装合并工具结果", body: toolBody, merged: true},
		{name: "开启伪装合并工具结果", body: toolBody, disguise: true, merged: true},
		{name: "关闭伪装普通请求", body: sampleClaudeBody},
		{name: "开启伪装普通请求", body: sampleClaudeBody, disguise: true},
		{name: "空请求体"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			headers := http.Header{}
			headers.Set("Content-Length", "999999")
			headers.Set("X-Claude-Code-Session-Id", "session-test")
			outcome, err := (&ClaudeProvider{}).BuildRequest(RequestInput{
				Ctx:                context.Background(),
				Method:             http.MethodPost,
				Path:               "/v1/messages",
				RawQuery:           "beta=true",
				Header:             headers,
				Body:               []byte(tc.body),
				ClaudeCodeDisguise: tc.disguise,
			}, &config.UpstreamConfig{
				BaseURL:                     "https://upstream.example.com",
				DisablePromptCacheInjection: true,
			}, "sk-test")
			require.NoError(t, err)
			body := assertRequestBodyReplay(t, outcome.Request)
			require.Equal(t, outcome.OriginalBody, body)
			require.Empty(t, outcome.Request.Header.Get("Content-Length"))
			require.Equal(t, "beta=true", outcome.Request.URL.RawQuery)
			require.Equal(t, "999999", headers.Get("Content-Length"), "不能修改客户端请求头")
			if tc.merged {
				blocks := gjson.GetBytes(body, "messages.1.content").Array()
				require.Len(t, blocks, 1, "首次发送也必须应用工具结果合并")
				require.Equal(t, "tool_result", blocks[0].Get("type").String())
				require.Contains(t, blocks[0].Get("content").String(), "补充文本")
				require.True(t, gjson.GetBytes(body, "safeguards.enabled").Bool())
			}
		})
	}
}

func assertRequestBodyReplay(t *testing.T, req *http.Request) []byte {
	t.Helper()
	if req.Body == nil {
		require.Zero(t, req.ContentLength)
		require.Nil(t, req.GetBody)
		return []byte{}
	}
	body, err := io.ReadAll(req.Body)
	require.NoError(t, err)
	require.NoError(t, req.Body.Close())
	require.Equal(t, int64(len(body)), req.ContentLength)
	require.NotNil(t, req.GetBody)
	for attempt := 0; attempt < 2; attempt++ {
		replay, err := req.GetBody()
		require.NoError(t, err)
		replayed, err := io.ReadAll(replay)
		require.NoError(t, err)
		require.NoError(t, replay.Close())
		require.Equal(t, body, replayed, "首次发送与重放的请求体必须逐字节一致")
	}
	return body
}

func TestResponsesProviderBuildRequest_CodexDisguiseBodyIsReplayable(t *testing.T) {
	t.Setenv("CODEX_DISGUISE_VERSION", "8.8.8")
	body := `{"model":"gpt-5","input":"你好","stream":true,"prompt_cache_key":"old-key","prompt_cache_retention":"24h"}`
	for _, disguise := range []bool{false, true} {
		name := "关闭伪装"
		if disguise {
			name = "开启伪装"
		}
		t.Run(name, func(t *testing.T) {
			headers := http.Header{}
			headers.Set("User-Agent", "codex_cli_rs/0.133.0 old-client")
			headers.Set("version", "0.133.0")
			headers.Set("session_id", "session-test")
			outcome, err := (&ResponsesProvider{}).BuildRequest(RequestInput{
				Ctx:           context.Background(),
				Header:        headers,
				Body:          []byte(body),
				CodexDisguise: disguise,
			}, &config.UpstreamConfig{
				BaseURL:               "https://upstream.example.com",
				ServiceType:           "responses",
				DisablePromptCacheKey: true,
				ModelMapping:          map[string][]string{"gpt-5": {"mapped-model"}},
			}, "sk-test")
			require.NoError(t, err)
			finalBody := assertRequestBodyReplay(t, outcome.Request)
			require.Equal(t, "mapped-model", gjson.GetBytes(finalBody, "model").String())
			require.False(t, gjson.GetBytes(finalBody, "prompt_cache_key").Exists())
			require.False(t, gjson.GetBytes(finalBody, "prompt_cache_retention").Exists())
			if disguise {
				require.Equal(t, "session-test", outcome.Request.Header.Get("session_id"))
				require.True(t, strings.HasPrefix(outcome.Request.Header.Get("User-Agent"), "codex_cli_rs/8.8.8 "))
				require.Equal(t, "8.8.8", outcome.Request.Header.Get("version"))
			} else {
				require.Equal(t, "api-proxy", outcome.Request.Header.Get("User-Agent"))
				require.Empty(t, outcome.Request.Header.Get("version"))
				require.Empty(t, outcome.Request.Header.Get("session_id"))
			}
			require.Equal(t, "0.133.0", headers.Get("version"), "不能修改客户端请求头")
		})
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

// 已知归属可复用链；换 Key 恢复完整历史；未知归属且没有历史时必须显式拒绝。
func TestNativeResponseHistoryCredentialOwnership(t *testing.T) {
	manager, err := session.NewPersistentSessionManager(filepath.Join(t.TempDir(), "responses.db"), time.Hour, 0, 0)
	require.NoError(t, err)
	t.Cleanup(manager.Stop)
	sess, err := manager.GetOrCreateSessionForConversation("", "conv-owner")
	require.NoError(t, err)
	upstream := &config.UpstreamConfig{BaseURL: "https://example.com/", ServiceType: "responses"}
	owner := upstream.RequestIdentity("first-key").Fingerprint()
	require.NoError(t, manager.CommitTurn(sess.ID, []types.ResponsesItem{
		{Type: "text", Content: "第一轮问题"},
		{Type: "message", Role: "assistant", Content: "第一轮回答"},
	}, 10, false, "resp-owned", owner))
	body := []byte(`{"model":"m","previous_response_id":"resp-owned","input":"第二轮问题"}`)

	// 末尾斜杠规范化必须与发送端一致，否则同一 Key 会被误判为换凭据。
	upstream.BaseURL = "https://example.com"
	kept, err := PrepareNativeResponseHistory(manager, "conv-owner", body, upstream.RequestIdentity("first-key").Fingerprint())
	require.NoError(t, err)
	require.JSONEq(t, string(body), string(kept))

	restored, err := PrepareNativeResponseHistory(manager, "conv-owner", body, upstream.RequestIdentity("second-key").Fingerprint())
	require.NoError(t, err)
	require.False(t, gjson.GetBytes(restored, "previous_response_id").Exists())
	require.Len(t, gjson.GetBytes(restored, "input").Array(), 3)
	require.Equal(t, "message", gjson.GetBytes(restored, "input.0.type").String())
	require.Equal(t, "user", gjson.GetBytes(restored, "input.0.role").String())
	require.Contains(t, string(restored), "第一轮回答")

	_, err = PrepareNativeResponseHistory(manager, "", []byte(`{"previous_response_id":"unknown-id","input":"后缀"}`), owner)
	var historyErr *ResponseHistoryError
	require.ErrorAs(t, err, &historyErr)
	require.True(t, historyErr.ClientRequestError())
}
