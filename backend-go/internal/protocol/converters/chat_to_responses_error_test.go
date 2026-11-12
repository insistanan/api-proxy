package converters

import (
	"context"
	"encoding/json"

	"github.com/insistanan/api-proxy/internal/infra/utils"
	"strings"
	"testing"
)

// 反证切片 6：上游 Chat error chunk（无 choices）必须转成 response.failed，
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
		data, ok := utils.SSEDataJSON(strings.TrimSpace(line))
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

// 正常 chunk 不受影响：choices 存在时不误判为错误。
func TestConvertOpenAIChatToResponses_NormalChunkUnaffected(t *testing.T) {
	var state any
	events := ConvertOpenAIChatToResponses(
		context.Background(), "gpt-4o", nil, nil,
		[]byte(`data: {"id":"c1","choices":[{"index":0,"delta":{"content":"hello"},"finish_reason":null}]}`),
		&state,
	)
	joined := strings.Join(events, "")
	if strings.Contains(joined, "response.failed") {
		t.Fatalf("正常 chunk 不应产出 response.failed:\n%s", joined)
	}
	if !strings.Contains(joined, "response.output_text.delta") {
		t.Fatalf("正常 chunk 应产出文本 delta:\n%s", joined)
	}
}
