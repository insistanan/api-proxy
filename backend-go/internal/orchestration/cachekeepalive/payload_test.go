package cachekeepalive

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

func TestPrepareHeartbeat_PreservesCachePrefixAndRejectsUnsupportedRequests(t *testing.T) {
	for _, tc := range []struct {
		name  string
		path  string
		value any
	}{
		{"服务端工具", "tools.0.type", "web_search_20250305"},
		{"MCP 服务", "mcp_servers", []any{map[string]any{"type": "url", "url": "https://example.test/mcp", "name": "remote"}}},
		{"历史压缩", "context_management", map[string]any{"edits": []any{map[string]any{"type": "compact_20260112"}}}},
		{"手动思考超预算", "thinking", map[string]any{"type": "enabled", "budget_tokens": 1024}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body, err := sjson.Set(sampleBody, tc.path, tc.value)
			if err != nil {
				t.Fatal(err)
			}
			reads := 0
			_, _, _, err = prepareBody(&snapshot{source: sampleSource(body, "attempt", &reads), policy: samplePolicy()})
			if err == nil {
				t.Fatal("不支持的请求没有被明确拒绝")
			}
		})
	}
	reads := 0
	body, model, ttl, err := prepareBody(&snapshot{source: sampleSource(sampleBody, "attempt", &reads), policy: samplePolicy()})
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"tools", "system", "messages.0"} {
		if gjson.GetBytes(body, field).Raw != gjson.Get(sampleBody, field).Raw {
			t.Fatalf("缓存前缀 %s 被改写", field)
		}
	}
	if model != "claude-test" || ttl != 5*time.Minute || gjson.GetBytes(body, "stream").Bool() || gjson.GetBytes(body, "max_tokens").Int() != 16 {
		t.Fatal("心跳模型、TTL 或输出限制不正确")
	}
}

func TestPrepareHeartbeat_KeepsNonMutatingClaudeContextOptions(t *testing.T) {
	for _, value := range []string{
		`null`, `{}`, `{"edits":[]}`,
		`{"edits":[{"type":"clear_thinking_20251015","keep":"all"}]}`,
		`{"edits":[{"type":"clear_thinking_20251015","keep":{"type":"all"}}]}`,
	} {
		t.Run(value, func(t *testing.T) {
			original, err := sjson.SetRaw(sampleBody, "context_management", value)
			require.NoError(t, err)
			original, err = sjson.SetRaw(original, "thinking", `{"type":"adaptive","display":"updates"}`)
			require.NoError(t, err)
			original, err = sjson.SetRaw(original, "mcp_servers", `[]`)
			require.NoError(t, err)
			original, err = sjson.SetRaw(original, "container", `null`)
			require.NoError(t, err)
			reads := 0
			body, _, _, err := prepareBody(&snapshot{source: sampleSource(original, "attempt", &reads), policy: samplePolicy()})
			require.NoError(t, err)
			for _, field := range []string{"context_management", "thinking", "mcp_servers", "container", "tools", "system", "messages.0"} {
				require.Equal(t, gjson.Get(original, field).Raw, gjson.GetBytes(body, field).Raw, field)
			}
			require.Equal(t, int64(2), gjson.GetBytes(body, "messages.#").Int())
			require.Equal(t, int64(samplePolicy().MaxOutputTokens), gjson.GetBytes(body, "max_tokens").Int())
		})
	}
}

func TestPrepareHeartbeat_RejectsContextRewritesWithSpecificReason(t *testing.T) {
	for _, tc := range []struct{ value, reason string }{
		{`{"edits":[{"type":"clear_thinking_20251015","keep":{"type":"thinking_turns","value":1}}]}`, "清理历史思考"},
		{`{"edits":[{"type":"clear_thinking_20251015"}]}`, "清理历史思考"},
		{`{"edits":[{"type":"clear_thinking_20251015","keep":"all","future":true}]}`, "未知选项"},
		{`{"edits":[{"type":"clear_thinking_20251015","keep":{"type":"all","future":true}}]}`, "清理历史思考"},
		{`{"edits":[{"type":"clear_thinking_20251015","keep":"all"},{"type":"clear_tool_uses_20250919"}]}`, "清理历史工具记录"},
		{`{"edits":[{"type":"compact_20260112"}]}`, "服务端历史压缩"},
		{`{"edits":[{"type":"unknown"}]}`, "无法识别的历史处理策略"},
		{`{"edits":{}}`, "edits 不是数组"},
		{`{"edits":[],"future":true}`, "未知选项"},
		{`[]`, "格式无效"},
	} {
		t.Run(tc.value, func(t *testing.T) {
			body, err := sjson.SetRaw(sampleBody, "context_management", tc.value)
			require.NoError(t, err)
			reads := 0
			_, _, _, err = prepareBody(&snapshot{source: sampleSource(body, "attempt", &reads), policy: samplePolicy()})
			require.ErrorContains(t, err, tc.reason)
		})
	}
}

func TestCacheTTL_UsesShortestExplicitDuration(t *testing.T) {
	for _, tc := range []struct {
		body    string
		ttl     time.Duration
		invalid bool
	}{
		{sampleBody, 5 * time.Minute, false},
		{strings.ReplaceAll(sampleBody, `"type":"ephemeral"`, `"type":"ephemeral","ttl":"1h"`), time.Hour, false},
		{`{"cache_control":{"type":"ephemeral","ttl":"1h"},"system":[{"cache_control":{"type":"ephemeral"}}]}`, 5 * time.Minute, false},
		{`{"messages":[]}`, 0, true},
		{`{"cache_control":{"type":"ephemeral","ttl":"2h"}}`, 0, true},
	} {
		ttl, err := cacheTTL(gjson.Parse(tc.body))
		if (err != nil) != tc.invalid || ttl != tc.ttl {
			t.Fatalf("TTL=%v err=%v，预期 %v invalid=%t", ttl, err, tc.ttl, tc.invalid)
		}
	}
}
