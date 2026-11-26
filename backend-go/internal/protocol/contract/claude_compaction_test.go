package contract

import (
	"encoding/json"
	"testing"
)

func TestClaudeCompactionStreamRequiresSignedBlockAndTerminal(t *testing.T) {
	valid := []string{
		`{"type":"message_start","message":{"content":[]}}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"compaction","content":"摘要","signature":"opaque-signature"}}`,
		`{"type":"content_block_stop","index":0}`,
		`{"type":"message_delta","delta":{"stop_reason":"compaction"}}`,
		`{"type":"message_stop"}`,
	}
	for _, tc := range []struct {
		name     string
		events   []string
		complete bool
	}{
		{"完整签名块", valid, true},
		{"缺少终态", valid[:4], false},
		{"缺少签名", []string{`{"type":"content_block_start","index":0,"content_block":{"type":"compaction","content":"摘要"}}`}, false},
		{"截断块", []string{valid[0], valid[1], valid[4]}, false},
		{"非压缩停止原因", []string{valid[0], valid[1], valid[2], `{"type":"message_delta","delta":{"stop_reason":"max_tokens"}}`, valid[4]}, false},
		{"重复块", []string{valid[0], valid[1], valid[2], valid[1]}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var state ClaudeCompactionStream
			var observeErr error
			for _, event := range tc.events {
				var payload map[string]interface{}
				if err := json.Unmarshal([]byte(event), &payload); err != nil {
					t.Fatal(err)
				}
				if observeErr = state.Observe(payload); observeErr != nil {
					break
				}
			}
			if (observeErr == nil && state.Complete) != tc.complete {
				t.Fatalf("错误的压缩终态: complete=%v err=%v", state.Complete, observeErr)
			}
		})
	}
}

func TestClaudeCompactionResponseRejectsIncompleteBlocks(t *testing.T) {
	for _, tc := range []struct {
		body  string
		valid bool
	}{
		{`{"content":[{"type":"compaction","content":"摘要","signature":"opaque"}],"stop_reason":"compaction"}`, true},
		{`{"content":[{"type":"compaction","content":"摘要"}],"stop_reason":"compaction"}`, false},
		{`{"content":[{"type":"compaction","content":"摘要","signature":"opaque"}],"stop_reason":"max_tokens"}`, false},
		{`{"content":[{"type":"compaction","content":"摘要","signature":"opaque"}]`, false},
	} {
		if err := ValidateClaudeCompactionResponse([]byte(tc.body)); (err == nil) != tc.valid {
			t.Fatalf("压缩响应校验错误: %v", err)
		}
	}
}
