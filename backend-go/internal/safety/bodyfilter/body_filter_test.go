package bodyfilter

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestFilterPrivateParams(t *testing.T) {
	t.Run("filters top level and nested private params", func(t *testing.T) {
		input := `{
			"model": "claude-3-7-sonnet-20250219",
			"_internal_id": "abc-123",
			"_debug_mode": true,
			"messages": [
				{
					"role": "user",
					"content": "hello",
					"_trace": "token-1"
				}
			],
			"metadata": {
				"user_id": "usr_99",
				"_secret_flag": "hide_me"
			}
		}`

		filtered, modified, removedKeys := FilterPrivateParams([]byte(input))
		if !modified {
			t.Fatalf("expected modified = true")
		}
		if len(removedKeys) != 4 {
			t.Fatalf("expected 4 removed keys, got %d: %v", len(removedKeys), removedKeys)
		}

		var result map[string]interface{}
		if err := json.Unmarshal(filtered, &result); err != nil {
			t.Fatalf("unmarshal filtered json failed: %v", err)
		}

		if _, has := result["_internal_id"]; has {
			t.Errorf("_internal_id should be removed")
		}
		if _, has := result["_debug_mode"]; has {
			t.Errorf("_debug_mode should be removed")
		}
		if result["model"] != "claude-3-7-sonnet-20250219" {
			t.Errorf("model should be preserved")
		}

		msgs := result["messages"].([]interface{})
		msg0 := msgs[0].(map[string]interface{})
		if _, has := msg0["_trace"]; has {
			t.Errorf("_trace in message should be removed")
		}

		meta := result["metadata"].(map[string]interface{})
		if _, has := meta["_secret_flag"]; has {
			t.Errorf("_secret_flag should be removed")
		}
		if meta["user_id"] != "usr_99" {
			t.Errorf("user_id should be preserved")
		}
	})

	t.Run("preserves json schema properties named with leading underscore", func(t *testing.T) {
		input := `{
			"model": "gpt-4o",
			"_private_req": "remove",
			"tools": [
				{
					"type": "function",
					"function": {
						"name": "query_db",
						"parameters": {
							"type": "object",
							"properties": {
								"_id": {
									"type": "string",
									"description": "MongoDB ObjectID"
								},
								"_rev": {
									"type": "string"
								}
							}
						}
					}
				}
			]
		}`

		filtered, modified, removedKeys := FilterPrivateParams([]byte(input))
		if !modified {
			t.Fatalf("expected modified = true")
		}
		if len(removedKeys) != 1 || removedKeys[0] != "_private_req" {
			t.Fatalf("expected only _private_req removed, got: %v", removedKeys)
		}

		var result map[string]interface{}
		if err := json.Unmarshal(filtered, &result); err != nil {
			t.Fatalf("unmarshal filtered json failed: %v", err)
		}

		if !strings.Contains(string(filtered), `"_id"`) || !strings.Contains(string(filtered), `"_rev"`) {
			t.Errorf("JSON Schema properties _id and _rev should be preserved")
		}
	})

	t.Run("supports whitelist", func(t *testing.T) {
		input := `{
			"model": "claude-3-7-sonnet-20250219",
			"_metadata": {"keep": true},
			"_forbidden": "kill"
		}`

		filtered, modified, removedKeys := FilterPrivateParamsWithWhitelist([]byte(input), []string{"_metadata"})
		if !modified {
			t.Fatalf("expected modified = true")
		}
		if len(removedKeys) != 1 || removedKeys[0] != "_forbidden" {
			t.Fatalf("expected only _forbidden removed, got: %v", removedKeys)
		}
		if !strings.Contains(string(filtered), `"_metadata"`) {
			t.Errorf("whitelisted _metadata should be preserved")
		}
	})
}
