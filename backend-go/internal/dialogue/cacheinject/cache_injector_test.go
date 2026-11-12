package cacheinject

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestInjectPromptCacheBreakpoints(t *testing.T) {
	t.Run("injects up to 4 breakpoints on plain request", func(t *testing.T) {
		input := `{
			"model": "claude-3-7-sonnet-20250219",
			"tools": [
				{"name": "tool_a", "description": "desc A"},
				{"name": "tool_b", "description": "desc B"}
			],
			"system": "You are a helpful assistant.",
			"messages": [
				{"role": "user", "content": "msg 1"},
				{"role": "assistant", "content": "ans 1"},
				{"role": "user", "content": "msg 2"},
				{"role": "assistant", "content": "ans 2"}
			]
		}`

		injected, modified, stats := InjectPromptCacheBreakpoints([]byte(input))
		if !modified {
			t.Fatalf("expected modified = true")
		}
		if stats.InjectedCount != 4 {
			t.Fatalf("expected InjectedCount = 4, got %d", stats.InjectedCount)
		}
		if !stats.InjectedTools || !stats.InjectedSystem || !stats.InjectedLatestMsg || !stats.InjectedPriorUserMsg {
			t.Errorf("unexpected stats: %+v", stats)
		}

		var payload map[string]interface{}
		if err := json.Unmarshal(injected, &payload); err != nil {
			t.Fatalf("unmarshal injected failed: %v", err)
		}
		if count := CountExistingBreakpoints(payload); count != 4 {
			t.Errorf("expected CountExistingBreakpoints = 4, got %d", count)
		}
	})

	t.Run("preserves caller owned breakpoints when count >= 4", func(t *testing.T) {
		input := `{
			"model": "claude-3-7-sonnet-20250219",
			"system": [
				{"type": "text", "text": "sys", "cache_control": {"type": "ephemeral"}}
			],
			"tools": [
				{"name": "tool_a", "cache_control": {"type": "ephemeral"}},
				{"name": "tool_b", "cache_control": {"type": "ephemeral"}}
			],
			"messages": [
				{
					"role": "user",
					"content": [
						{"type": "text", "text": "hi", "cache_control": {"type": "ephemeral"}}
					]
				}
			]
		}`

		injected, modified, stats := InjectPromptCacheBreakpoints([]byte(input))
		if modified {
			t.Fatalf("expected modified = false when existing >= 4")
		}
		if stats.ExistingCount != 4 || stats.InjectedCount != 0 {
			t.Errorf("unexpected stats: %+v", stats)
		}
		if string(injected) != input {
			t.Errorf("expected body to be untouched")
		}
	})

	t.Run("skips thinking and redacted_thinking blocks for message breakpoint", func(t *testing.T) {
		input := `{
			"model": "claude-3-7-sonnet-20250219",
			"messages": [
				{
					"role": "assistant",
					"content": [
						{"type": "text", "text": "regular response"},
						{"type": "thinking", "thinking": "some thinking"}
					]
				}
			]
		}`

		injected, modified, stats := InjectPromptCacheBreakpoints([]byte(input))
		if !modified {
			t.Fatalf("expected modified = true")
		}
		if !stats.InjectedLatestMsg {
			t.Errorf("expected InjectedLatestMsg = true")
		}

		var payload map[string]interface{}
		if err := json.Unmarshal(injected, &payload); err != nil {
			t.Fatalf("unmarshal injected failed: %v", err)
		}
		msgs := payload["messages"].([]interface{})
		blocks := msgs[0].(map[string]interface{})["content"].([]interface{})
		textBlock := blocks[0].(map[string]interface{})
		thinkingBlock := blocks[1].(map[string]interface{})

		if _, has := textBlock["cache_control"]; !has {
			t.Errorf("cache_control should be injected on text block")
		}
		if _, has := thinkingBlock["cache_control"]; has {
			t.Errorf("cache_control should not be on thinking block")
		}
		if !strings.Contains(string(injected), `"cache_control":{"type":"ephemeral"}`) {
			t.Errorf("expected cache_control in serialized body")
		}
	})
}
