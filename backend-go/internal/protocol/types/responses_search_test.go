package types

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestResponsesSearchActionRoundTrip(t *testing.T) {
	for _, action := range []string{
		`{"type":"search","query":"Go 1.25","sources":[{"type":"url","url":"https://go.dev/"}]}`,
		`{"type":"open_page","url":"https://go.dev/"}`,
		`{"type":"find_in_page","url":"https://go.dev/","pattern":"release"}`,
	} {
		body := []byte(`{"id":"search_1","type":"web_search_call","status":"completed","action":` + action + `}`)
		var item ResponsesItem
		if err := json.Unmarshal(body, &item); err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(item)
		if err != nil {
			t.Fatal(err)
		}
		var original, restored map[string]interface{}
		if err := json.Unmarshal(body, &original); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(encoded, &restored); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(original, restored) {
			t.Fatalf("搜索动作在会话序列化中丢失: original=%s restored=%s", body, encoded)
		}
	}
}
