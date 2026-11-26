package converters

import (
	"context"
	"encoding/json"
	"insistanan/api-proxy/internal/protocol/codec"

	"strings"
	"testing"
)

// 上游 Chat error chunk（无 choices）必须转成 response.failed，
// 不再被静默丢弃后于 [DONE] 伪造 response.completed。
func TestConvertOpenAIChatToResponses_UpstreamErrorBecomesResponseFailed(t *testing.T) {
	var state any
	events := ConvertOpenAIChatToResponses(
		context.Background(), "gpt-4o", nil, nil,
		[]byte(`data: {"error":{"message":"boom","type":"server_error","code":"internal"}}`),
		&state,
	)
	joined := strings.Join(events, "")
	if !strings.Contains(joined, `"type":"response.failed"`) {
		t.Fatalf("应产出 response.failed 事件，实际:\n%s", joined)
	}
	// 事件是 SSE 格式（event: + data: 行），取 data 行解析。
	var payload map[string]interface{}
	for _, line := range strings.Split(joined, "\n") {
		data, ok := codec.SSEDataJSON(strings.TrimSpace(line))
		if !ok {
			continue
		}
		if err := json.Unmarshal([]byte(data), &payload); err != nil {
			t.Fatalf("data 行不是合法 JSON: %v", err)
		}
	}
	if payload == nil {
		t.Fatalf("未找到 data 行:\n%s", joined)
	}
	response, ok := payload["response"].(map[string]interface{})
	if !ok {
		t.Fatalf("response.failed 缺少 response 对象: %v", payload)
	}
	errObj, ok := response["error"].(map[string]interface{})
	if !ok || errObj["message"] != "boom" {
		t.Fatalf("response.error 应保留上游错误信封: %v", response["error"])
	}
}
