package utils

import (
	"encoding/json"
	"testing"

	"github.com/insistanan/api-proxy/internal/protocol/types"
)

func TestResponsesSearchReplayComparesAction(t *testing.T) {
	raw := json.RawMessage(`{"id":"search_1","type":"web_search_call","action":{"type":"search","query":"first"}}`)
	var stored types.ResponsesItem
	if err := json.Unmarshal(raw, &stored); err != nil {
		t.Fatal(err)
	}
	if !responsesItemRawEqual(stored, raw) {
		t.Fatal("持久化后的搜索动作应匹配原始重放项")
	}
	changed := json.RawMessage(`{"id":"search_1","type":"web_search_call","action":{"type":"search","query":"changed"}}`)
	if responsesItemRawEqual(stored, changed) {
		t.Fatal("搜索动作改变时不能当作相同历史裁掉")
	}
}
