package types

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestResponsesItemPreservesNativeFields(t *testing.T) {
	for _, wire := range []string{
		`{"type":"file_search_call","queries":["query"],"results":[{"id":9007199254740993,"score":0.7}]}`,
		`{"type":"future_tool","input":{"value":true},"output":[],"arguments":{"annotations":"business"}}`,
		`{"type":"image_generation_call","result":"base64","quality":"high"}`,
		`{"type":"tool_search_call","arguments":{"query":"tool"},"execution":"client"}`,
	} {
		var item ResponsesItem
		require.NoError(t, json.Unmarshal([]byte(wire), &item))
		encoded, err := json.Marshal(item)
		require.NoError(t, err)
		// RawMessage 比较也覆盖大整数，避免 float64 掩盖精度丢失。
		var before, after map[string]json.RawMessage
		require.NoError(t, json.Unmarshal([]byte(wire), &before))
		require.NoError(t, json.Unmarshal(encoded, &after))
		require.Equal(t, before, after)
	}
}

func TestResponsesItemRejectsMalformedKnownFields(t *testing.T) {
	for _, wire := range []string{`null`, `{"type":12}`, `{"type":"function_call","arguments":{}}`, `{"type":"tool_search_call","arguments":[]}`} {
		var item ResponsesItem
		require.Error(t, json.Unmarshal([]byte(wire), &item))
	}
}
