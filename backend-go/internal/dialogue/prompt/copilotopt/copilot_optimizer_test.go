package copilotopt

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestClassifyRequest(t *testing.T) {
	t.Run("classifies user initiated message", func(t *testing.T) {
		input := `{"model":"claude-3-7-sonnet-20250219","messages":[{"role":"user","content":"hello"}]}`
		res := ClassifyRequest([]byte(input), false, true, true)
		if res.Initiator != "user" {
			t.Errorf("expected initiator = user, got %s", res.Initiator)
		}
		if res.IsWarmup || res.IsCompact || res.IsSubagent {
			t.Errorf("unexpected flags: %+v", res)
		}
	})

	t.Run("classifies tool result as agent", func(t *testing.T) {
		input := `{"model":"claude-3-7-sonnet-20250219","messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"1","content":"done"}]}]}`
		res := ClassifyRequest([]byte(input), false, true, true)
		if res.Initiator != "agent" {
			t.Errorf("expected initiator = agent, got %s", res.Initiator)
		}
	})

	t.Run("detects compact request by prompt", func(t *testing.T) {
		input := `{
			"model": "claude-3-7-sonnet-20250219",
			"system": "You are a helpful AI assistant tasked with summarizing conversations...",
			"messages": [{"role":"user","content":"Please summarize"}]
		}`
		res := ClassifyRequest([]byte(input), false, true, true)
		if !res.IsCompact || res.Initiator != "agent" {
			t.Errorf("expected compact agent request, got %+v", res)
		}
	})

	t.Run("detects subagent request", func(t *testing.T) {
		input := `{
			"model": "claude-3-7-sonnet-20250219",
			"messages": [{"role":"user","content":"hello __SUBAGENT_MARKER__ task"}]
		}`
		res := ClassifyRequest([]byte(input), false, true, true)
		if !res.IsSubagent || res.Initiator != "agent" {
			t.Errorf("expected subagent agent request, got %+v", res)
		}
	})

	t.Run("detects warmup request", func(t *testing.T) {
		input := `{
			"model": "claude-3-7-sonnet-20250219",
			"messages": [{"role":"user","content":"probe"}]
		}`
		res := ClassifyRequest([]byte(input), true, true, true)
		if !res.IsWarmup || res.Initiator != "user" {
			t.Errorf("expected warmup request, got %+v", res)
		}
	})
}

func TestMergeToolResults(t *testing.T) {
	t.Run("merges text into tool_result within single message", func(t *testing.T) {
		input := `{
			"model": "claude-3-7-sonnet-20250219",
			"messages": [
				{
					"role": "user",
					"content": [
						{"type": "tool_result", "tool_use_id": "call_1", "content": "file read"},
						{"type": "text", "text": "I will proceed with next steps"}
					]
				}
			]
		}`

		merged, modified := MergeToolResults([]byte(input))
		if !modified {
			t.Fatalf("expected modified = true")
		}

		var payload map[string]interface{}
		if err := json.Unmarshal(merged, &payload); err != nil {
			t.Fatalf("unmarshal merged json failed: %v", err)
		}
		msgs := payload["messages"].([]interface{})
		msg0 := msgs[0].(map[string]interface{})
		blocks := msg0["content"].([]interface{})
		if len(blocks) != 1 {
			t.Fatalf("expected 1 block, got %d", len(blocks))
		}
		b0 := blocks[0].(map[string]interface{})
		if b0["type"] != "tool_result" {
			t.Errorf("expected block type = tool_result")
		}
		if !strings.Contains(b0["content"].(string), "I will proceed with next steps") {
			t.Errorf("expected absorbed text in tool_result content")
		}
	})

	t.Run("merges consecutive tool_result only messages", func(t *testing.T) {
		input := `{
			"model": "claude-3-7-sonnet-20250219",
			"messages": [
				{
					"role": "user",
					"content": [{"type": "tool_result", "tool_use_id": "call_1", "content": "res1"}]
				},
				{
					"role": "user",
					"content": [{"type": "tool_result", "tool_use_id": "call_2", "content": "res2"}]
				}
			]
		}`

		merged, modified := MergeToolResults([]byte(input))
		if !modified {
			t.Fatalf("expected modified = true")
		}

		var payload map[string]interface{}
		if err := json.Unmarshal(merged, &payload); err != nil {
			t.Fatalf("unmarshal merged json failed: %v", err)
		}
		msgs := payload["messages"].([]interface{})
		if len(msgs) != 1 {
			t.Fatalf("expected 1 combined message, got %d", len(msgs))
		}
		blocks := msgs[0].(map[string]interface{})["content"].([]interface{})
		if len(blocks) != 2 {
			t.Errorf("expected 2 blocks in combined message, got %d", len(blocks))
		}
	})
}

func TestApplyCopilotHeaders(t *testing.T) {
	headers := http.Header{}
	classification := CopilotClassification{
		Initiator:  "agent",
		IsSubagent: true,
	}
	ApplyCopilotHeaders(headers, classification)
	if headers.Get("x-initiator") != "agent" {
		t.Errorf("expected x-initiator = agent, got %s", headers.Get("x-initiator"))
	}
	if headers.Get("x-interaction-type") != "conversation-subagent" {
		t.Errorf("expected x-interaction-type = conversation-subagent, got %s", headers.Get("x-interaction-type"))
	}
}
