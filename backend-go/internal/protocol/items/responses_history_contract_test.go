package respitems

import (
	"encoding/json"
	"testing"

	"github.com/insistanan/api-proxy/internal/protocol/types"
	"github.com/stretchr/testify/require"
)

func TestHistoryDedupPreservesBusinessDifferences(t *testing.T) {
	for _, pair := range [][2]string{
		{`{"type":"function_call","call_id":"a","arguments":"{\"annotations\":1}"}`, `{"type":"function_call","call_id":"a","arguments":"{\"annotations\":2}"}`},
		{`{"type":"file_search_call","queries":["a"]}`, `{"type":"file_search_call","queries":["b"]}`},
		{`{"type":"message","role":"assistant","content":[{"type":"output_text","text":"a","annotations":[1]}]}`, `{"type":"message","role":"assistant","content":[{"type":"output_text","text":"a","annotations":[2]}]}`},
		{`{"type":"future_tool","input":{"id":9007199254740993}}`, `{"type":"future_tool","input":{"id":9007199254740992}}`},
	} {
		var left, right types.ResponsesItem
		require.NoError(t, json.Unmarshal([]byte(pair[0]), &left))
		require.NoError(t, json.Unmarshal([]byte(pair[1]), &right))
		_, matched := ResponsesItemsPrefixMatch([]types.ResponsesItem{left}, []types.ResponsesItem{right})
		require.False(t, matched)
		_, rawMatched := ResponsesItemsPrefixMatchRaw([]types.ResponsesItem{left}, []json.RawMessage{json.RawMessage(pair[1])})
		require.False(t, rawMatched)
	}
}

func TestHistoryDedupDoesNotSkipExplicitReasoning(t *testing.T) {
	history := []types.ResponsesItem{{Type: "reasoning", EncryptedContent: "one"}, {Type: "message", Role: "assistant", Content: "answer"}}
	_, matched := ResponsesItemsPrefixMatch(history, history[1:])
	require.True(t, matched)
	current := append([]types.ResponsesItem(nil), history...)
	current[0].EncryptedContent = "two"
	_, matched = ResponsesItemsPrefixMatch(history, current)
	require.False(t, matched)
}
