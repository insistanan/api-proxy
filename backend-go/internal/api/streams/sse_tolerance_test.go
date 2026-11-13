package streams

import (
	"bytes"
	"testing"
)

// TestSSEDataLineToleranceInStreamHelpers 锁定跨层回归：
// 部分上游发送不带空格的 "data:{...}"（SSE 规范中冒号后的空格是可选的）。
// 收敛前本文件的 helper 一律严格匹配 "data: "，会静默丢弃整行，
// 导致 usage 检测、token 修补、文本提取全部失效。此测试对同一份事件的
// 带空格 / 不带空格两种写法断言结果一致。
func TestSSEDataLineToleranceInStreamHelpers(t *testing.T) {
	const payload = `{"type":"message_delta","delta":{"text":"hi"},"usage":{"input_tokens":11,"output_tokens":7}}`

	variants := map[string]string{
		"带空格": "event: message_delta\ndata: " + payload + "\n\n",
		"无空格": "event: message_delta\ndata:" + payload + "\n\n",
	}

	for name, event := range variants {
		t.Run(name, func(t *testing.T) {
			if _, ok := ParseSSEEventData(event); !ok {
				t.Fatal("ParseSSEEventData 未能解析 data 行")
			}

			if !HasEventWithUsage(event) {
				t.Fatal("HasEventWithUsage = false, want true")
			}

			hasUsage, _, _, collected := CheckEventUsageStatus(event, false)
			if !hasUsage {
				t.Fatal("CheckEventUsageStatus 未识别 usage")
			}
			if collected.InputTokens != 11 || collected.OutputTokens != 7 {
				t.Fatalf("collected usage = (%v, %v), want (11, 7)", collected.InputTokens, collected.OutputTokens)
			}

			if got := ExtractInputTokensFromEvent(event); got != 11 {
				t.Fatalf("ExtractInputTokensFromEvent = %d, want 11", got)
			}

			if !IsMessageDeltaEvent(event) {
				t.Fatal("IsMessageDeltaEvent = false, want true")
			}

			var buf bytes.Buffer
			ExtractTextFromEvent(event, &buf)
			if buf.String() != "hi" {
				t.Fatalf("ExtractTextFromEvent = %q, want %q", buf.String(), "hi")
			}
		})
	}
}

// TestPatchTokensInEventToleratesMissingSpace 断言 token 修补对无空格 data 行同样生效，
// 且输出统一为规范的 "data: " 前缀（修补路径本就重新序列化 JSON，不保留原始前缀风格）。
func TestPatchTokensInEventToleratesMissingSpace(t *testing.T) {
	event := "event: message_delta\ndata:" + `{"type":"message_delta","usage":{"input_tokens":0,"output_tokens":0}}` + "\n\n"

	patched := PatchTokensInEventWithCache(event, 123, 45, 0, false, false, false)

	data, ok := ParseSSEEventData(patched)
	if !ok {
		t.Fatalf("修补结果无法解析: %q", patched)
	}
	usage, ok := data["usage"].(map[string]interface{})
	if !ok {
		t.Fatalf("修补结果缺少 usage: %#v", data)
	}
	if got, _ := usage["input_tokens"].(float64); got != 123 {
		t.Fatalf("input_tokens = %v, want 123", usage["input_tokens"])
	}
	if got, _ := usage["output_tokens"].(float64); got != 45 {
		t.Fatalf("output_tokens = %v, want 45", usage["output_tokens"])
	}
}
