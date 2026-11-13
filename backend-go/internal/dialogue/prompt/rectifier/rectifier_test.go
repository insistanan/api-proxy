package rectifier

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestShouldRectifyThinkingBudget(t *testing.T) {
	cases := []struct {
		name     string
		body     string
		expected bool
	}{
		{
			name:     "1024 constraint error",
			body:     `{"error":{"type":"invalid_request_error","message":"thinking.budget_tokens: Input should be greater than or equal to 1024"}}`,
			expected: true,
		},
		{
			name:     "1024 constraint error with >= 1024",
			body:     `{"error":{"message":"budget_tokens must be >= 1024 for thinking"}}`,
			expected: true,
		},
		{
			name:     "max_tokens must be greater than budget_tokens",
			body:     `{"error":{"message":"max_tokens must be greater than budget_tokens"}}`,
			expected: true,
		},
		{
			name:     "unrelated error",
			body:     `{"error":{"message":"rate limit exceeded"}}`,
			expected: false,
		},
		{
			name:     "empty body",
			body:     ``,
			expected: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ShouldRectifyThinkingBudget([]byte(tc.body))
			if got != tc.expected {
				t.Errorf("ShouldRectifyThinkingBudget() = %v, want %v", got, tc.expected)
			}
		})
	}
}

func TestRectifyThinkingBudget(t *testing.T) {
	t.Run("normal rectification", func(t *testing.T) {
		input := `{"model":"claude-3-7-sonnet-20250219","thinking":{"type":"enabled","budget_tokens":500},"max_tokens":1000}`
		rectified, applied, stats := RectifyThinkingBudget([]byte(input))
		if !applied {
			t.Fatalf("expected applied = true")
		}
		if stats.PreviousBudgetTokens != 500 || stats.NewBudgetTokens != DefaultThinkingBudgetTokens {
			t.Errorf("unexpected budget tokens stats: %+v", stats)
		}
		if stats.NewMaxTokens != DefaultMaxTokensForBudget {
			t.Errorf("unexpected max tokens stats: %+v", stats)
		}

		var payload map[string]interface{}
		if err := json.Unmarshal(rectified, &payload); err != nil {
			t.Fatalf("unmarshal rectified failed: %v", err)
		}
		thinking := payload["thinking"].(map[string]interface{})
		if thinking["budget_tokens"].(float64) != DefaultThinkingBudgetTokens {
			t.Errorf("budget_tokens = %v, want %v", thinking["budget_tokens"], DefaultThinkingBudgetTokens)
		}
		if payload["max_tokens"].(float64) != DefaultMaxTokensForBudget {
			t.Errorf("max_tokens = %v, want %v", payload["max_tokens"], DefaultMaxTokensForBudget)
		}
	})

	t.Run("adaptive thinking skips rectification", func(t *testing.T) {
		input := `{"model":"claude-3-7-sonnet-20250219","thinking":{"type":"adaptive"},"max_tokens":4000}`
		_, applied, _ := RectifyThinkingBudget([]byte(input))
		if applied {
			t.Fatalf("expected applied = false for adaptive thinking")
		}
	})
}

func TestShouldRectifyThinkingSignature(t *testing.T) {
	cases := []struct {
		name     string
		body     string
		expected bool
	}{
		{
			name:     "invalid signature in thinking block",
			body:     `{"error":{"message":"Invalid 'signature' in 'thinking' block"}}`,
			expected: true,
		},
		{
			name:     "thought signature not valid",
			body:     `{"error":{"message":"Unable to submit request because Thought signature is not valid"}}`,
			expected: true,
		},
		{
			name:     "must start with a thinking block",
			body:     `{"error":{"message":"assistant message must start with a thinking block"}}`,
			expected: true,
		},
		{
			name:     "expected thinking found tool_use",
			body:     "{\"error\":{\"message\":\"Expected `thinking` or `redacted_thinking`, but found `tool_use`\"}}",
			expected: true,
		},
		{
			name:     "signature field required",
			body:     `{"error":{"message":"signature: Field required"}}`,
			expected: true,
		},
		{
			name:     "signature extra inputs not permitted",
			body:     `{"error":{"message":"content.0.signature: Extra inputs are not permitted"}}`,
			expected: true,
		},
		{
			name:     "cannot be modified",
			body:     `{"error":{"message":"thinking or redacted_thinking blocks cannot be modified"}}`,
			expected: true,
		},
		{
			name:     "unrelated 400",
			body:     `{"error":{"message":"invalid model name"}}`,
			expected: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ShouldRectifyThinkingSignature([]byte(tc.body))
			if got != tc.expected {
				t.Errorf("ShouldRectifyThinkingSignature() = %v, want %v", got, tc.expected)
			}
		})
	}
}

func TestRectifyThinkingSignature(t *testing.T) {
	t.Run("removes thinking and extra signature", func(t *testing.T) {
		input := `{
			"model": "claude-3-7-sonnet-20250219",
			"messages": [
				{
					"role": "assistant",
					"content": [
						{"type": "thinking", "thinking": "some thinking text", "signature": "sig123"},
						{"type": "redacted_thinking", "data": "abc"},
						{"type": "text", "text": "hello", "signature": "stray_sig"}
					]
				}
			]
		}`

		rectified, applied, stats := RectifyThinkingSignature([]byte(input))
		if !applied {
			t.Fatalf("expected applied = true")
		}
		if stats.RemovedThinkingBlocks != 1 {
			t.Errorf("RemovedThinkingBlocks = %d, want 1", stats.RemovedThinkingBlocks)
		}
		if stats.RemovedRedactedThinkingBlocks != 1 {
			t.Errorf("RemovedRedactedThinkingBlocks = %d, want 1", stats.RemovedRedactedThinkingBlocks)
		}
		if stats.RemovedSignatureFields != 1 {
			t.Errorf("RemovedSignatureFields = %d, want 1", stats.RemovedSignatureFields)
		}

		if strings.Contains(string(rectified), "stray_sig") {
			t.Errorf("expected stray_sig to be removed")
		}
		if strings.Contains(string(rectified), "some thinking text") {
			t.Errorf("expected thinking block to be removed")
		}
	})

	t.Run("removes top level thinking if tool_use turn lacks thinking block", func(t *testing.T) {
		input := `{
			"model": "claude-3-7-sonnet-20250219",
			"thinking": {"type": "enabled", "budget_tokens": 4000},
			"messages": [
				{
					"role": "assistant",
					"content": [
						{"type": "tool_use", "id": "call_1", "name": "test_tool", "input": {}}
					]
				}
			]
		}`

		rectified, applied, stats := RectifyThinkingSignature([]byte(input))
		if !applied {
			t.Fatalf("expected applied = true")
		}
		if !stats.RemovedTopLevelThinking {
			t.Errorf("expected RemovedTopLevelThinking = true")
		}
		if strings.Contains(string(rectified), `"thinking"`) {
			t.Errorf("expected top level thinking to be removed")
		}
	})
}
