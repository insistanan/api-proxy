package types

import (
	"encoding/json"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestResponsesResponseReplacesOutputAndExtensionSnapshot(t *testing.T) {
	response := ResponsesResponse{Extra: map[string]interface{}{"output": []interface{}{map[string]interface{}{"text": "旧敏感正文"}}, "trace": "旧敏感扩展"}}
	require.NoError(t, json.Unmarshal([]byte(`{"id":"new","status":"completed","output":[{"type":"image_generation_call","result":"AQID","future":{"value":7}}],"trace":"已处理"}`), &response))
	data, err := json.Marshal(response)
	require.NoError(t, err)
	require.NotContains(t, string(data), "旧敏感")
	require.Contains(t, string(data), "已处理")
	require.Contains(t, string(data), `"result":"AQID"`)
	require.Contains(t, string(data), `"future"`)
	response.Output[0].Extra["result"] = json.RawMessage(`"BAUG"`)
	data, err = json.Marshal(response)
	require.NoError(t, err)
	require.Contains(t, string(data), "BAUG")
	require.NotContains(t, string(data), "AQID")
}
