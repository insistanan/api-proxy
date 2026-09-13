package hooks

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/BenedictKing/api-proxy/internal/sensitive"
	"github.com/BenedictKing/api-proxy/internal/utils"
	"github.com/gin-gonic/gin"
)

func TestRestoreAttachedSSEEventAcrossChunksAndFlushesIncompletePrefix(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	attached := &attachedHookPipeline{pipeline: NewPipeline(), vault: sensitive.NewVault(), stream: sensitive.NewStreamCmdScanner(nil)}
	c.Set(contentSafetyPipelineKey, attached)
	original := "18012345523"
	placeholder, err := attached.vault.Mask(original, []sensitive.RedactionMatch{{
		Type: "PHONE", Rule: "phone", Original: original, Start: 0, End: len(original),
	}})
	if err != nil {
		t.Fatalf("创建占位符失败: %v", err)
	}

	for split := 0; split <= len(placeholder); split++ {
		attached.restorers = nil
		attached.restorePending = nil
		first, restoreErr := RestoreAttachedSSEEvent(c, sseTextEvent(t, placeholder[:split]))
		if restoreErr != nil {
			t.Fatalf("split=%d 处理首段失败: %v", split, restoreErr)
		}
		second, restoreErr := RestoreAttachedSSEEvent(c, sseTextEvent(t, placeholder[split:]+" ok"))
		if restoreErr != nil {
			t.Fatalf("split=%d 处理末段失败: %v", split, restoreErr)
		}
		if got := sseDeltaText(t, first) + sseDeltaText(t, second); got != original+" ok" {
			t.Fatalf("split=%d 跨事件还原结果 = %q", split, got)
		}
	}

	attached.restorers = nil
	attached.restorePending = nil
	exact, err := RestoreAttachedSSEEvent(c, sseTextEvent(t, placeholder))
	if err != nil {
		t.Fatalf("处理完整占位符事件失败: %v", err)
	}
	if got := sseDeltaText(t, exact); got != "" {
		t.Fatalf("尚未确认右边界时不应提前还原: %q", got)
	}
	terminal, err := RestoreAttachedSSEEvent(c, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
	if err != nil {
		t.Fatalf("处理完整占位符后的终止事件失败: %v", err)
	}
	if !strings.Contains(terminal, original) || strings.Index(terminal, original) > strings.Index(terminal, "message_stop") {
		t.Fatalf("完整占位符必须在终止事件前还原: %q", terminal)
	}

	attached.restorers = nil
	attached.restorePending = nil
	split := len(placeholder) / 2
	incomplete := placeholder[:split]
	event, err := RestoreAttachedSSEEvent(c, sseTextEvent(t, incomplete))
	if err != nil {
		t.Fatalf("处理未完成占位符失败: %v", err)
	}
	if got := sseDeltaText(t, event); got != "" {
		t.Fatalf("未完成前缀不应提前输出: %q", got)
	}
	terminal, err = RestoreAttachedSSEEvent(c, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
	if err != nil {
		t.Fatalf("处理终止事件失败: %v", err)
	}
	if !strings.Contains(terminal, incomplete) || strings.Index(terminal, incomplete) > strings.Index(terminal, "message_stop") {
		t.Fatalf("残片必须在终止事件前释放: %q", terminal)
	}
}

func TestRestoreAttachedSSEEventIsolatesParallelToolCallStreams(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	attached := &attachedHookPipeline{pipeline: NewPipeline(), vault: sensitive.NewVault(), stream: sensitive.NewStreamCmdScanner(nil)}
	c.Set(contentSafetyPipelineKey, attached)

	firstOriginal := "first-secret-value"
	secondOriginal := "second-secret-value"
	firstPlaceholder := maskTestValue(t, attached.vault, "SECRET", firstOriginal)
	secondPlaceholder := maskTestValue(t, attached.vault, "SECRET", secondOriginal)
	firstSplit := len(firstPlaceholder) / 2
	secondSplit := len(secondPlaceholder) / 2

	firstHead, err := RestoreAttachedSSEEvent(c, sseToolArgumentEvent(t, 0, firstPlaceholder[:firstSplit]))
	if err != nil {
		t.Fatalf("处理第一个工具流首段失败: %v", err)
	}
	secondHead, err := RestoreAttachedSSEEvent(c, sseToolArgumentEvent(t, 1, secondPlaceholder[:secondSplit]))
	if err != nil {
		t.Fatalf("处理第二个工具流首段失败: %v", err)
	}
	firstTail, err := RestoreAttachedSSEEvent(c, sseToolArgumentEvent(t, 0, firstPlaceholder[firstSplit:]+" "))
	if err != nil {
		t.Fatalf("处理第一个工具流末段失败: %v", err)
	}
	secondTail, err := RestoreAttachedSSEEvent(c, sseToolArgumentEvent(t, 1, secondPlaceholder[secondSplit:]+" "))
	if err != nil {
		t.Fatalf("处理第二个工具流末段失败: %v", err)
	}

	if got := sseToolArguments(t, firstHead) + sseToolArguments(t, firstTail); got != firstOriginal+" " {
		t.Fatalf("第一个工具流还原结果 = %q", got)
	}
	if got := sseToolArguments(t, secondHead) + sseToolArguments(t, secondTail); got != secondOriginal+" " {
		t.Fatalf("第二个工具流还原结果 = %q", got)
	}
}

func TestResetAttachedStreamRestorationKeepsVaultAndDropsAttemptFragments(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	attached := &attachedHookPipeline{pipeline: NewPipeline(), vault: sensitive.NewVault(), stream: sensitive.NewStreamCmdScanner(nil)}
	c.Set(contentSafetyPipelineKey, attached)
	original := "retry-secret-value"
	placeholder := maskTestValue(t, attached.vault, "SECRET", original)
	split := len(placeholder) / 2

	firstAttempt, err := RestoreAttachedSSEEvent(c, sseTextEvent(t, placeholder[:split]))
	if err != nil {
		t.Fatalf("处理首次尝试分片失败: %v", err)
	}
	if got := sseDeltaText(t, firstAttempt); got != "" {
		t.Fatalf("首次尝试的未完成前缀不应输出: %q", got)
	}
	if err := ResetAttachedStreamRestoration(c); err != nil {
		t.Fatalf("重置流式还原状态失败: %v", err)
	}

	retry, err := RestoreAttachedSSEEvent(c, sseTextEvent(t, placeholder+" "))
	if err != nil {
		t.Fatalf("处理重试事件失败: %v", err)
	}
	if got := sseDeltaText(t, retry); got != original+" " {
		t.Fatalf("重试应复用 Vault 且不拼接旧分片: %q", got)
	}
}

func TestRestoreAttachedResponseBodyRestoresJSONAndPlainErrorBodies(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	attached := &attachedHookPipeline{pipeline: NewPipeline(), vault: sensitive.NewVault(), stream: sensitive.NewStreamCmdScanner(nil)}
	c.Set(contentSafetyPipelineKey, attached)
	original := "upstream-secret-value"
	placeholder := maskTestValue(t, attached.vault, "SECRET", original)

	jsonBody, err := RestoreAttachedResponseBody(c, []byte(`{"error":{"message":"`+placeholder+`"}}`))
	if err != nil {
		t.Fatalf("还原 JSON 错误体失败: %v", err)
	}
	if strings.Contains(string(jsonBody), placeholder) || !strings.Contains(string(jsonBody), original) {
		t.Fatalf("JSON 错误体未精确还原: %s", jsonBody)
	}

	plainBody, err := RestoreAttachedResponseBody(c, []byte("upstream error: "+placeholder))
	if err != nil {
		t.Fatalf("还原纯文本错误体失败: %v", err)
	}
	if got := string(plainBody); got != "upstream error: "+original {
		t.Fatalf("纯文本错误体还原结果 = %q", got)
	}
}

func sseTextEvent(t *testing.T, text string) string {
	t.Helper()
	body, err := utils.MarshalJSONNoEscape(map[string]interface{}{
		"type":  "content_block_delta",
		"delta": map[string]interface{}{"type": "text_delta", "text": text},
	})
	if err != nil {
		t.Fatalf("序列化 SSE 测试事件失败: %v", err)
	}
	return "event: content_block_delta\ndata: " + string(body) + "\n\n"
}

func sseToolArgumentEvent(t *testing.T, index int, arguments string) string {
	t.Helper()
	body, err := utils.MarshalJSONNoEscape(map[string]interface{}{
		"choices": []interface{}{
			map[string]interface{}{
				"index": 0,
				"delta": map[string]interface{}{
					"tool_calls": []interface{}{
						map[string]interface{}{
							"index":    index,
							"function": map[string]interface{}{"arguments": arguments},
						},
					},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("序列化工具参数 SSE 测试事件失败: %v", err)
	}
	return "data: " + string(body) + "\n\n"
}

func sseToolArguments(t *testing.T, event string) string {
	t.Helper()
	for _, line := range strings.Split(event, "\n") {
		payload, ok := utils.SSEDataJSON(line)
		if !ok {
			continue
		}
		var value map[string]interface{}
		if err := json.Unmarshal([]byte(payload), &value); err != nil {
			t.Fatalf("解析工具参数 SSE 测试事件失败: %v", err)
		}
		choices := value["choices"].([]interface{})
		choice := choices[0].(map[string]interface{})
		delta := choice["delta"].(map[string]interface{})
		toolCalls := delta["tool_calls"].([]interface{})
		toolCall := toolCalls[0].(map[string]interface{})
		function := toolCall["function"].(map[string]interface{})
		return function["arguments"].(string)
	}
	return ""
}

func maskTestValue(t *testing.T, vault *sensitive.Vault, kind, original string) string {
	t.Helper()
	placeholder, err := vault.Mask(original, []sensitive.RedactionMatch{{
		Type: kind, Rule: "test", Original: original, Start: 0, End: len(original),
	}})
	if err != nil {
		t.Fatalf("创建测试占位符失败: %v", err)
	}
	return placeholder
}

func sseDeltaText(t *testing.T, event string) string {
	t.Helper()
	for _, line := range strings.Split(event, "\n") {
		payload, ok := utils.SSEDataJSON(line)
		if !ok {
			continue
		}
		var value map[string]interface{}
		if err := json.Unmarshal([]byte(payload), &value); err != nil {
			t.Fatalf("解析 SSE 测试事件失败: %v", err)
		}
		delta, _ := value["delta"].(map[string]interface{})
		text, _ := delta["text"].(string)
		return text
	}
	return ""
}

func TestStreamTerminalEventUsesProtocolFields(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		event    string
		terminal bool
	}{
		{name: "正文中的结束字样", event: `data: {"type":"content_block_delta","delta":{"text":"示例 [DONE]"}}`},
		{name: "结构中的普通字符串", event: `data: {"text":"message_stop","finish_reason":null}`},
		{name: "Messages 结束", event: "data: {\"type\":\t\"message_stop\"}\n\n", terminal: true},
		{name: "Responses 结束", event: "data:{\"type\":\t\"response.completed\"}\n\n", terminal: true},
		{name: "Chat 单候选结束不是整流结束", event: `data: {"choices":[{"finish_reason":"tool_calls"}]}`},
		{name: "Gemini 单候选结束不是整流结束", event: `data: {"candidates":[{"finishReason":"STOP"}]}`},
		{name: "精确结束标记", event: "data:[DONE]\n\n", terminal: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if got := streamSafetyBoundaryForEvent(testCase.event).terminal; got != testCase.terminal {
				t.Fatalf("终止判断=%v，期望=%v", got, testCase.terminal)
			}
		})
	}
}

func TestRestorationKeepsLogicalChoiceWhenArrayPositionChanges(t *testing.T) {
	vault := sensitive.NewVault()
	placeholder := maskTestValue(t, vault, "TEST", "original-value")
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	attached := &attachedHookPipeline{vault: vault, stream: sensitive.NewStreamCmdScanner(nil)}
	c.Set(contentSafetyPipelineKey, attached)
	defer ClearAttachedSensitiveData(c)
	split := len(placeholder) / 2
	first, err := json.Marshal(map[string]any{
		"choices": []any{
			map[string]any{"index": 0, "delta": map[string]any{"content": "other"}},
			map[string]any{"index": 1, "delta": map[string]any{"content": placeholder[:split]}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := RestoreAttachedSSEEvent(c, "data: "+string(first)+"\n\n"); err != nil {
		t.Fatal(err)
	}
	// index=1 在此块位于数组位置 0，必须继续前一块的还原状态。
	second, err := json.Marshal(map[string]any{
		"choices": []any{map[string]any{"index": 1, "delta": map[string]any{"content": placeholder[split:] + " "}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	output, err := RestoreAttachedSSEEvent(c, "data: "+string(second)+"\n\n")
	if err != nil || !strings.Contains(output, "original-value") {
		t.Fatalf("候选位置变化后应完整还原: %s, %v", output, err)
	}
	if len(attached.restorePending) != 0 {
		t.Fatal("完整还原后不应遗留旧数组位置的暂存状态")
	}
}
