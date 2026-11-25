// 验证 Provider 最终请求的可重放载荷、凭据归属与输入错误。
package providers

import (
	"context"
	"github.com/insistanan/api-proxy/internal/infra/upstreamreq"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/insistanan/api-proxy/internal/dialogue/session"
	"github.com/insistanan/api-proxy/internal/infra/config"
	"github.com/insistanan/api-proxy/internal/protocol/types"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const sampleClaudeBody = `{"model":"claude-3-5-sonnet","max_tokens":128,"stream":false,"system":"你是测试","messages":[{"role":"user","content":"ping"}]}`

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
	_, codexVersion, _, _ := upstreamreq.GetEffectiveDisguiseVersions()
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
				require.True(t, strings.HasPrefix(outcome.Request.Header.Get("User-Agent"), "codex_cli_rs/"+codexVersion+" "))
				require.Equal(t, codexVersion, outcome.Request.Header.Get("version"))
			} else {
				require.Equal(t, "api-proxy", outcome.Request.Header.Get("User-Agent"))
				require.Empty(t, outcome.Request.Header.Get("version"))
				require.Empty(t, outcome.Request.Header.Get("session_id"))
			}
			require.Equal(t, "0.133.0", headers.Get("version"), "不能修改客户端请求头")
		})
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
	sess, err := manager.GetOrCreateSessionForConversation(context.Background(), "", "conv-owner")
	require.NoError(t, err)
	upstream := &config.UpstreamConfig{BaseURL: "https://example.com/", ServiceType: "responses"}
	owner := upstream.RequestIdentity("first-key").Fingerprint()
	require.NoError(t, commitFixtureTurn(manager, sess, []types.ResponsesItem{
		{Type: "text", Content: "第一轮问题"},
		{Type: "message", Role: "assistant", Content: "第一轮回答"},
	}, 10, false, "resp-owned", owner))
	body := []byte(`{"model":"m","previous_response_id":"resp-owned","input":"第二轮问题"}`)

	// 末尾斜杠规范化必须与发送端一致，否则同一 Key 会被误判为换凭据。
	upstream.BaseURL = "https://example.com"
	kept, err := PrepareNativeResponseHistory(context.Background(), manager, "conv-owner", body, upstream.RequestIdentity("first-key").Fingerprint(), nil)
	require.NoError(t, err)
	require.JSONEq(t, string(body), string(kept))

	restored, err := PrepareNativeResponseHistory(context.Background(), manager, "conv-owner", body, upstream.RequestIdentity("second-key").Fingerprint(), nil)
	require.NoError(t, err)
	require.False(t, gjson.GetBytes(restored, "previous_response_id").Exists())
	require.Len(t, gjson.GetBytes(restored, "input").Array(), 3)
	require.Equal(t, "message", gjson.GetBytes(restored, "input.0.type").String())
	require.Equal(t, "user", gjson.GetBytes(restored, "input.0.role").String())
	require.Contains(t, string(restored), "第一轮回答")

	_, err = PrepareNativeResponseHistory(context.Background(), manager, "", []byte(`{"previous_response_id":"unknown-id","input":"后缀"}`), owner, nil)
	var historyErr interface{ ClientRequestError() bool }
	require.ErrorAs(t, err, &historyErr)
	require.True(t, historyErr.ClientRequestError())

	// 生产请求带有 conversation ID；不存在的历史必须保持客户端错误分类。
	_, err = PrepareNativeResponseHistory(context.Background(), manager, "conv-owner", []byte(`{"previous_response_id":"missing-id","input":"后缀"}`), owner, nil)
	var clientErr interface{ ClientRequestError() bool }
	require.ErrorAs(t, err, &clientErr)
	require.True(t, clientErr.ClientRequestError())

	require.NoError(t, commitFixtureTurn(manager, sess, []types.ResponsesItem{
		{Type: "text", Content: "第二轮问题"},
		{Type: "message", Role: "assistant", Content: "第二轮回答"},
	}, 10, false, "resp-newer", owner))
	// 同 Key 和换 Key 都不能用最新正文恢复旧分支；协议转换同样遵守此边界。
	provider := &ResponsesProvider{SessionManager: manager}
	for _, protocol := range []string{"responses", "openai"} {
		upstream.ServiceType = protocol
		for _, key := range []string{"first-key", "second-key"} {
			outcome, err := provider.BuildRequest(RequestInput{Ctx: context.Background(), Body: body, ConversationID: "conv-owner"}, upstream, key)
			require.ErrorAs(t, err, &historyErr)
			require.Nil(t, outcome.Request)
		}
		fullHistory := []byte(`{"model":"m","previous_response_id":"resp-owned","input":[{"role":"user","content":[{"type":"input_text","text":"第一轮问题"}]},{"role":"assistant","content":[{"type":"output_text","text":"第一轮回答"}]},{"role":"user","content":[{"type":"input_text","text":"新的分支"}]}]}`)
		outcome, err := provider.BuildRequest(RequestInput{Ctx: context.Background(), Body: fullHistory, ConversationID: "conv-owner"}, upstream, "second-key")
		require.ErrorAs(t, err, &historyErr)
		require.Nil(t, outcome.Request)

	}
}
