package respitems

import (
	"encoding/json"
	"github.com/stretchr/testify/require"
	"testing"
)

func feedOutput(t *testing.T, c *ResponsesOutputCollector, events ...string) {
	t.Helper()
	for _, event := range events {
		var data map[string]interface{}
		require.NoError(t, json.Unmarshal([]byte(event), &data))
		require.NoError(t, c.Consume(data))
	}
}

func TestResponsesCollectorMergesLateIdentityAndIndependentParts(t *testing.T) {
	c := NewResponsesOutputCollector()
	feedOutput(t, c,
		`{"type":"response.output_item.added","output_index":0,"item":{"type":"message","content":[{"type":"output_text","text":""}]}}`,
		`{"type":"response.output_text.delta","output_index":0,"item_id":"m","content_index":0,"delta":"甲"}`,
		`{"type":"response.output_text.delta","item_id":"m","content_index":1,"delta":"乙"}`,
		`{"type":"response.output_text.delta","output_index":0,"content_index":0,"delta":"丙"}`,
		`{"type":"response.output_item.done","output_index":0,"item":{"id":"m","type":"message","status":"completed"}}`,
		`{"type":"response.output_item.added","output_index":1,"item":{"id":"reason","type":"reasoning"}}`,
		`{"type":"response.reasoning_summary_text.delta","item_id":"reason","summary_index":0,"delta":"旧摘要"}`,
		`{"type":"response.output_item.done","output_index":1,"item":{"id":"reason","type":"reasoning","summary":[{"type":"summary_text","text":"最终摘要"}],"encrypted_content":"opaque"}}`)
	items, err := c.Items()
	require.NoError(t, err)
	require.Len(t, items, 2)
	raw, err := json.Marshal(items)
	require.NoError(t, err)
	require.Contains(t, string(raw), "甲丙")
	require.Contains(t, string(raw), "乙")
	require.Contains(t, string(raw), "最终摘要")
	require.NotContains(t, string(raw), "旧摘要")
	require.Equal(t, "opaque", items[1].EncryptedContent)
}

func TestResponsesCollectorOnlyCommitsCompletePrefix(t *testing.T) {
	c := NewResponsesOutputCollector()
	feedOutput(t, c,
		`{"type":"response.output_item.done","output_index":0,"item":{"type":"function_call","call_id":"a","name":"exec","arguments":"{}"}}`,
		`{"type":"response.output_item.added","output_index":1,"item":{"type":"function_call","call_id":"b","name":"exec","arguments":"{"}}`)
	items, err := c.Items()
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, "a", items[0].CallID)
	// 明确的空终态是权威快照，不能复活先前增量。
	feedOutput(t, c, `{"type":"response.completed","response":{"output":[]}}`)
	items, err = c.Items()
	require.NoError(t, err)
	require.Empty(t, items)
}

func TestResponsesCollectorSnapshotReportsInvalidFields(t *testing.T) {
	c := NewResponsesOutputCollector()
	feedOutput(t, c, `{"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","name":123}}`)
	_, err := c.Snapshot()
	require.Error(t, err)
}
