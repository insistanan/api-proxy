// 验证 Responses 流式搜索条目、会话压缩窗口和上游错误语义。
package responses

import (
	"github.com/insistanan/api-proxy/internal/protocol/items"
	"strings"
	"testing"

	"github.com/insistanan/api-proxy/internal/protocol/types"
)

func TestResponsesStreamSearchActionPreserved(t *testing.T) {
	for _, events := range [][]string{
		{
			`data: {"type":"response.output_item.added","output_index":0,"item":{"id":"search_1","type":"web_search_call","status":"in_progress"}}` + "\n\n",
			`data: {"type":"response.output_item.done","output_index":0,"item":{"id":"search_1","type":"web_search_call","status":"completed","action":{"type":"search","query":"Go"}}}` + "\n\n",
		},
		{
			`data: {"type":"response.completed","response":{"id":"response_1","output":[{"id":"search_1","type":"web_search_call","status":"completed","action":{"type":"search","query":"Go"}}]}}` + "\n\n",
		},
	} {
		collector := newResponsesStreamSessionCollector()
		for _, event := range events {
			collector.consumeEvent(event)
		}
		if err := collector.Err(); err != nil {
			t.Fatal(err)
		}
		items := collector.Items()
		if len(items) != 1 {
			t.Fatalf("搜索项应保留一次，实际为 %+v", items)
		}
		action, ok := items[0].Action.(map[string]interface{})
		if !ok || action["type"] != "search" || action["query"] != "Go" {
			t.Fatalf("流式会话收集丢失搜索动作: %+v", items[0])
		}
	}
}

func TestResponsesTurnItemsCompactionWindows(t *testing.T) {
	retained := types.ResponsesItem{Type: "message", Role: "user", Content: "保留消息"}
	oldRoot := types.ResponsesItem{Type: "compaction", EncryptedContent: "old-root"}
	newRoot := types.ResponsesItem{Type: "compaction", EncryptedContent: "new-root"}
	answer := types.ResponsesItem{Type: "message", Role: "assistant", Content: "回答"}
	for _, tc := range []struct {
		name   string
		input  []types.ResponsesItem
		output []types.ResponsesItem
		want   []types.ResponsesItem
	}{
		{"普通轮次", []types.ResponsesItem{retained}, []types.ResponsesItem{answer}, []types.ResponsesItem{retained, answer}},
		{"完整compact窗口", []types.ResponsesItem{retained, oldRoot}, []types.ResponsesItem{answer}, []types.ResponsesItem{retained, oldRoot, answer}},
		{"服务端新压缩", []types.ResponsesItem{retained}, []types.ResponsesItem{newRoot, answer}, []types.ResponsesItem{newRoot, answer}},
		{"压缩窗口再次自动压缩", []types.ResponsesItem{retained, oldRoot}, []types.ResponsesItem{newRoot, answer}, []types.ResponsesItem{newRoot, answer}},
		{"服务端多个压缩根", []types.ResponsesItem{retained}, []types.ResponsesItem{oldRoot, retained, newRoot, answer}, []types.ResponsesItem{newRoot, answer}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := respitems.ResponsesTurnItems(tc.input, tc.output)
			if len(got) != len(tc.want) {
				t.Fatalf("窗口长度=%d，预期=%d", len(got), len(tc.want))
			}
			for i := range got {
				if got[i].Type != tc.want[i].Type || got[i].Content != tc.want[i].Content || got[i].EncryptedContent != tc.want[i].EncryptedContent {
					t.Fatalf("第%d项丢失规范窗口: got=%+v want=%+v", i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestResponsesStreamEventError(t *testing.T) {
	t.Run("response.failed yields error", func(t *testing.T) {
		event := "data: {\"type\":\"response.failed\",\"response\":{\"error\":{\"message\":\"boom\"}}}\n\n"
		err := responsesStreamEventError(event)
		if err == nil || !strings.Contains(err.Error(), "upstream stream failed") {
			t.Fatalf("err = %v, want upstream stream failed", err)
		}
	})

	t.Run("capacity error maps to retry-same-candidate", func(t *testing.T) {
		event := "data: {\"type\":\"error\",\"code\":\"overloaded\",\"message\":\"selected model is at capacity\"}\n\n"
		err := responsesStreamEventError(event)
		if err == nil {
			t.Fatal("容量错误应产生 error")
		}
		// NewRetrySameCandidateError 包装 ErrEmptyStreamResponse，错误文本即其消息。
		if err.Error() != ErrEmptyStreamResponse.Error() {
			t.Fatalf("容量错误应映射为同候选重试（包装空流响应）, got %q", err.Error())
		}
	})

	t.Run("ordinary event yields nil", func(t *testing.T) {
		if err := responsesStreamEventError("data: {\"type\":\"response.output_text.delta\"}\n\n"); err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
	})
}
