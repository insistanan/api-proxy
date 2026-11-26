package respitems

import (
	"insistanan/api-proxy/internal/protocol/types"
	"testing"
)

func TestCompactionTriggerDoesNotPersistAndKeepsCompleteWindow(t *testing.T) {
	input := []types.ResponsesItem{{Type: "message", Role: "user", Content: "旧历史"}, {Type: "compaction_trigger"}}
	output := []types.ResponsesItem{{Type: "message", Role: "user", Content: "保留的前缀"}, {Type: "compaction_summary", EncryptedContent: "opaque"}}
	items := ResponsesTurnItems(input, output)
	if len(items) != 2 || items[0].Content != "保留的前缀" || !ResponsesItemsContainCompaction(items) {
		t.Fatalf("新窗口被截断: %+v", items)
	}
	items = ResponsesTurnItems(input, nil)
	if len(items) != 1 || items[0].Type != "message" {
		t.Fatalf("控制项进入历史: %+v", items)
	}
}

func TestCompactionOutputRequiresOpaqueContent(t *testing.T) {
	for _, tc := range []struct {
		name  string
		items []types.ResponsesItem
		valid bool
	}{
		{"完整窗口", []types.ResponsesItem{{Type: "message", Content: "保留前缀"}, {Type: "compaction_summary", EncryptedContent: "opaque"}}, true},
		{"空输出", nil, false},
		{"普通消息", []types.ResponsesItem{{Type: "message", Content: "不是压缩结果"}}, false},
		{"缺失密文", []types.ResponsesItem{{Type: "compaction"}}, false},
		{"空白密文", []types.ResponsesItem{{Type: "compaction", EncryptedContent: "  "}}, false},
		{"错误密文类型", []types.ResponsesItem{{Type: "compaction", EncryptedContent: map[string]any{"value": "opaque"}}}, false},
		{"混入控制项", []types.ResponsesItem{{Type: "compaction", EncryptedContent: "opaque"}, {Type: "compaction_trigger"}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidateResponsesCompactionOutput(tc.items); (err == nil) != tc.valid {
				t.Fatalf("压缩输出校验错误: %v", err)
			}
		})
	}
}
