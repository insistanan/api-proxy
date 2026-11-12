package hooks

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/insistanan/api-proxy/internal/safety/sensitive"
)

func TestWriteAttachedContentSafetyErrorUsesProtocolJSONShape(t *testing.T) {
	tests := []struct {
		apiType string
		assert  func(*testing.T, map[string]any)
	}{
		{apiType: "messages", assert: assertMessagesSafetyPayload},
		{apiType: "responses", assert: assertOpenAISafetyPayload},
		{apiType: "chat", assert: assertOpenAISafetyPayload},
		{apiType: "gemini", assert: assertGeminiSafetyPayload},
	}
	for _, tt := range tests {
		t.Run(tt.apiType, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			AttachHookPipeline(c, NewPipeline(), HookContext{APIType: tt.apiType, RequestID: "req-test"})
			if err := WriteAttachedContentSafetyError(c, testSafetyError()); err != nil {
				t.Fatalf("写出非流式错误失败: %v", err)
			}
			if recorder.Code != 403 {
				t.Fatalf("状态码 = %d，期望 403", recorder.Code)
			}
			var payload map[string]any
			if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
				t.Fatalf("解析错误响应失败: %v", err)
			}
			tt.assert(t, payload)
		})
	}
}

func TestWriteAttachedStreamErrorUsesProtocolSSEShape(t *testing.T) {
	tests := []struct {
		apiType       string
		wantEventLine bool
		assert        func(*testing.T, map[string]any)
	}{
		{apiType: "messages", wantEventLine: true, assert: assertMessagesSafetyPayload},
		{apiType: "responses", wantEventLine: true, assert: assertResponsesStreamSafetyPayload},
		{apiType: "chat", assert: assertOpenAISafetyPayload},
		{apiType: "gemini", assert: assertGeminiSafetyPayload},
	}
	for _, tt := range tests {
		t.Run(tt.apiType, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			AttachHookPipeline(c, NewPipeline(), HookContext{APIType: tt.apiType, RequestID: "req-test", Stream: true})
			if err := WriteAttachedStreamError(c, testSafetyError()); err != nil {
				t.Fatalf("写出流式错误失败: %v", err)
			}
			body := recorder.Body.String()
			if strings.HasPrefix(body, "event: error\n") != tt.wantEventLine {
				t.Fatalf("SSE 事件名不符合协议: %q", body)
			}
			dataLine := ""
			for _, line := range strings.Split(body, "\n") {
				if strings.HasPrefix(line, "data: ") {
					dataLine = strings.TrimPrefix(line, "data: ")
					break
				}
			}
			if dataLine == "" {
				t.Fatalf("SSE 缺少 data 行: %q", body)
			}
			var payload map[string]any
			if err := json.Unmarshal([]byte(dataLine), &payload); err != nil {
				t.Fatalf("解析 SSE 错误数据失败: %v", err)
			}
			tt.assert(t, payload)
		})
	}
}

func TestContentSafetyErrorWriterRejectsUnknownProtocol(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	AttachHookPipeline(c, NewPipeline(), HookContext{APIType: "unknown"})
	if err := WriteAttachedContentSafetyError(c, testSafetyError()); err == nil {
		t.Fatal("未知协议应显式返回错误")
	}
	if err := WriteAttachedStreamError(c, testSafetyError()); err == nil {
		t.Fatal("未知流协议应显式返回错误")
	}
}

func testSafetyError() *ContentSafetyError {
	return &ContentSafetyError{
		BlockType: sensitive.BlockTypeDangerousCmd,
		RuleName:  "destructive",
		Snippet:   "rm -rf /",
	}
}

func assertMessagesSafetyPayload(t *testing.T, payload map[string]any) {
	t.Helper()
	if payload["type"] != "error" || payload["request_id"] != "req-test" {
		t.Fatalf("Messages 错误外层不正确: %+v", payload)
	}
	assertNestedSafetyError(t, payload)
}

func assertOpenAISafetyPayload(t *testing.T, payload map[string]any) {
	t.Helper()
	assertNestedSafetyError(t, payload)
}

func assertNestedSafetyError(t *testing.T, payload map[string]any) {
	t.Helper()
	detail, ok := payload["error"].(map[string]any)
	if !ok || detail["type"] != "content_safety_error" || detail["code"] != "DANGEROUS_COMMAND_BLOCKED" || detail["message"] == "" {
		t.Fatalf("内容安全错误详情不正确: %+v", payload)
	}
}

func assertResponsesStreamSafetyPayload(t *testing.T, payload map[string]any) {
	t.Helper()
	if payload["type"] != "error" || payload["code"] != "DANGEROUS_COMMAND_BLOCKED" || payload["message"] == "" {
		t.Fatalf("Responses 流错误结构不正确: %+v", payload)
	}
	if _, nested := payload["error"]; nested {
		t.Fatalf("Responses 流错误不应使用嵌套 error: %+v", payload)
	}
}

func assertGeminiSafetyPayload(t *testing.T, payload map[string]any) {
	t.Helper()
	detail, ok := payload["error"].(map[string]any)
	if !ok || detail["code"] != float64(403) || detail["status"] != "PERMISSION_DENIED" || detail["message"] == "" {
		t.Fatalf("Gemini 错误结构不正确: %+v", payload)
	}
	details, ok := detail["details"].([]any)
	if !ok || len(details) != 1 {
		t.Fatalf("Gemini 错误缺少 ErrorInfo: %+v", payload)
	}
	errorInfo, ok := details[0].(map[string]any)
	if !ok || errorInfo["reason"] != "DANGEROUS_COMMAND_BLOCKED" {
		t.Fatalf("Gemini ErrorInfo 不正确: %+v", payload)
	}
}
