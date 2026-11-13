package streams

import (
	"strings"
	"testing"
)

// TestEventRebuildPreservesLineStructure 锁定 SSE 事件改写的保真契约：
// 改写函数都会把事件按 "\n" 拆行再拼回，收敛前每行都补写 "\n"，
// 使以 "\n\n" 结尾的事件多出一个换行（"a\n\n" -> "a\n\n\n"），
// 相当于凭空插入一个空的 SSE 事件分隔。此测试对每个函数断言
// 换行数量与结尾分隔符与输入完全一致。
func TestEventRebuildPreservesLineStructure(t *testing.T) {
	assertSameLineStructure := func(t *testing.T, in, out string) {
		t.Helper()
		if gotN, wantN := strings.Count(out, "\n"), strings.Count(in, "\n"); gotN != wantN {
			t.Fatalf("换行数量 = %d, want %d\n in  %q\n out %q", gotN, wantN, in, out)
		}
		if !strings.HasSuffix(out, "\n\n") {
			t.Fatalf("结果未以 SSE 事件分隔符 \"\\n\\n\" 结尾: %q", out)
		}
		if strings.HasSuffix(out, "\n\n\n") {
			t.Fatalf("结果多出一个换行: %q", out)
		}
	}

	const usageEvent = "event: message_delta\ndata: {\"type\":\"message_delta\",\"usage\":{\"input_tokens\":0,\"output_tokens\":0}}\n\n"

	t.Run("PatchTokensInEventWithCache", func(t *testing.T) {
		out := PatchTokensInEventWithCache(usageEvent, 111, 22, 0, false, false, false)
		assertSameLineStructure(t, usageEvent, out)
		assertUsage(t, out, 111, 22)
	})

	t.Run("ReplaceSSEData", func(t *testing.T) {
		out := ReplaceSSEData(usageEvent, map[string]interface{}{
			"type":  "message_delta",
			"usage": map[string]interface{}{"input_tokens": 111, "output_tokens": 22},
		})
		assertSameLineStructure(t, usageEvent, out)
		assertUsage(t, out, 111, 22)
	})

	t.Run("PatchMessageStartEvent", func(t *testing.T) {
		event := "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"\",\"model\":\"m-old\"}}\n\n"
		out := PatchMessageStartEvent(event, "m-new", true, false)
		assertSameLineStructure(t, event, out)

		data, ok := ParseSSEEventData(out)
		if !ok {
			t.Fatalf("改写结果无法解析: %q", out)
		}
		msg, ok := data["message"].(map[string]interface{})
		if !ok {
			t.Fatalf("改写结果缺少 message: %#v", data)
		}
		if id, _ := msg["id"].(string); id == "" {
			t.Error("空 message.id 未被补全")
		}
		if model, _ := msg["model"].(string); model != "m-new" {
			t.Errorf("message.model = %q, want %q", model, "m-new")
		}
	})
}

// TestEventRebuildPassesThroughUnchanged 断言无可改写内容时事件逐字节原样返回，
// 既不改前缀风格（上游 "data:" 不带空格的写法保留），也不动行结构。
func TestEventRebuildPassesThroughUnchanged(t *testing.T) {
	cases := []struct {
		name  string
		event string
	}{
		{"无 data 行", "event: ping\n\n"},
		{"DONE 标记", "data: [DONE]\n\n"},
		{"非 JSON 载荷", "data: not-json\n\n"},
		{"无空格前缀且无 usage", "event: content_block_stop\ndata:{\"type\":\"content_block_stop\",\"index\":0}\n\n"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// PatchTokensInEventWithCache 对可解析的 JSON 会重新序列化（既有行为），
			// 故这里只对"解析不出 JSON"或"无需改写"的输入断言逐字节相等。
			if out := PatchMessageStartEvent(tc.event, "m", true, false); out != tc.event {
				t.Errorf("PatchMessageStartEvent 改动了事件:\n got  %q\n want %q", out, tc.event)
			}
			if !strings.Contains(tc.event, "{") {
				if out := PatchTokensInEventWithCache(tc.event, 1, 2, 0, false, false, false); out != tc.event {
					t.Errorf("PatchTokensInEventWithCache 改动了事件:\n got  %q\n want %q", out, tc.event)
				}
			}
		})
	}
}

// TestReplaceSSEDataWithoutDataLine 断言没有 data 行时返回原事件，不做重建。
func TestReplaceSSEDataWithoutDataLine(t *testing.T) {
	event := "event: ping\nid: 7\n\n"
	if out := ReplaceSSEData(event, map[string]interface{}{"a": 1}); out != event {
		t.Fatalf("got  %q\nwant %q", out, event)
	}
}

func assertUsage(t *testing.T, event string, wantInput, wantOutput float64) {
	t.Helper()
	data, ok := ParseSSEEventData(event)
	if !ok {
		t.Fatalf("结果无法解析: %q", event)
	}
	usage, ok := data["usage"].(map[string]interface{})
	if !ok {
		t.Fatalf("结果缺少 usage: %#v", data)
	}
	if got, _ := usage["input_tokens"].(float64); got != wantInput {
		t.Errorf("input_tokens = %v, want %v", usage["input_tokens"], wantInput)
	}
	if got, _ := usage["output_tokens"].(float64); got != wantOutput {
		t.Errorf("output_tokens = %v, want %v", usage["output_tokens"], wantOutput)
	}
}
