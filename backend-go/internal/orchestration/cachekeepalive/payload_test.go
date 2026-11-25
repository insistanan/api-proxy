package cachekeepalive

import (
	"strings"
	"testing"
	"time"

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
		{"MCP 服务", "mcp_servers", []any{}},
		{"上下文管理", "context_management", map[string]any{}},
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
