package logger

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestPrepareBodyReplacesAnthropicImage(t *testing.T) {
	payload := map[string]any{
		"messages": []any{
			map[string]any{
				"role": "user",
				"content": []any{
					map[string]any{
						"type": "image",
						"source": map[string]any{
							"type":       "base64",
							"media_type": "image/png",
							"data":       "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+ip1s=",
						},
					},
				},
			},
		},
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	body, originalBytes, truncated := PrepareBody(raw)
	if originalBytes != len(raw) {
		t.Fatalf("originalBytes=%d, want %d", originalBytes, len(raw))
	}
	if truncated {
		t.Fatal("small payload should not truncate")
	}
	if strings.Contains(body, "iVBORw0KGgo") {
		t.Fatalf("image payload still present: %s", body)
	}
	if !strings.Contains(body, "[omitted image; media_type=image/png;") {
		t.Fatalf("placeholder missing: %s", body)
	}
}

func TestPrepareBodyReplacesDataURL(t *testing.T) {
	payload := map[string]any{
		"image_url": map[string]any{
			"url": "data:image/jpeg;base64,/9j/4AAQSkZJRgABAQAAAQABAAD",
		},
	}
	raw, _ := json.Marshal(payload)
	body, _, _ := PrepareBody(raw)
	if strings.Contains(body, "/9j/") {
		t.Fatalf("data url still present: %s", body)
	}
	if !strings.Contains(body, "media_type=image/jpeg") {
		t.Fatalf("placeholder missing: %s", body)
	}
}

func TestPrepareBodyReplacesGeminiInlineData(t *testing.T) {
	payload := map[string]any{
		"inlineData": map[string]any{
			"mimeType": "image/webp",
			"data":     "UklGRiQAAABXRUJQVlA4IBgAAAAwAQCdASoBAAEAAwA0JaQAA3AA/vuUAAA=",
		},
	}
	raw, _ := json.Marshal(payload)
	body, _, _ := PrepareBody(raw)
	if strings.Contains(body, "UklGRiQ") {
		t.Fatalf("inline data still present: %s", body)
	}
	if !strings.Contains(body, "media_type=image/webp") {
		t.Fatalf("placeholder missing: %s", body)
	}
}

func TestPrepareBodyTruncatesOverCap(t *testing.T) {
	raw := []byte(`{"text":"` + strings.Repeat("a", MaxBodyBytes+64) + `"}`)
	body, originalBytes, truncated := PrepareBody(raw)
	if originalBytes != len(raw) {
		t.Fatalf("originalBytes=%d, want %d", originalBytes, len(raw))
	}
	if !truncated {
		t.Fatal("expected truncation")
	}
	if len(body) > MaxBodyBytes || !strings.Contains(body, "正文已省略") {
		t.Fatalf("超大正文应返回有界的省略说明: %s", body)
	}
}

func TestPrepareBodyOmitsLargeMediaWithoutLeakingContent(t *testing.T) {
	raw := []byte(`{"text":"MASKED_SECRET_7K4M2P7QAB2CD_PRESERVE_EXACTLY","inlineData":{"mimeType":"image/png","data":"` + strings.Repeat("A", 2*MaxBodyBytes) + `"}}`)
	body, originalBytes, truncated := PrepareBody(raw)
	if originalBytes != len(raw) || !truncated {
		t.Fatalf("原始长度或省略标记错误: originalBytes=%d, truncated=%v", originalBytes, truncated)
	}
	if !strings.Contains(body, "正文已省略") || strings.Contains(body, "7K4M2P7QAB2CD") || strings.Contains(body, strings.Repeat("A", 64)) {
		t.Fatalf("省略说明泄露了原始正文: %s", body)
	}
}

func TestNormalizeTrafficLogPreservesPreparedBodyMetadata(t *testing.T) {
	prepared := TrafficLog{}.WithPreparedBody(OmittedBody(0), 0, true)
	normalized := normalizeTrafficLog(prepared)
	if normalized.Body != prepared.Body || normalized.OriginalBytes != 0 || !normalized.Truncated {
		t.Fatalf("未知原始长度被替换成省略说明长度: %+v", normalized)
	}
}

func TestPrepareBodyRemovesSensitivePlaceholderID(t *testing.T) {
	placeholder := "MASKED_SECRET_7K4M2P7QAB2CD_PRESERVE_EXACTLY"
	body, _, _ := PrepareBody([]byte(`{"text":"` + placeholder + `"}`))
	if strings.Contains(body, placeholder) || !strings.Contains(body, "MASKED_SECRET_REDACTED") {
		t.Fatalf("流量日志未移除占位符映射 ID: %s", body)
	}
}

func TestPrepareBodyRemovesSemanticSensitivePlaceholderID(t *testing.T) {
	marker := "MASKED_API_TOKEN_TM5A7UUJ4YMVY_PRESERVE_EXACTLY"
	body, _, _ := PrepareBody([]byte(`{"text":"` + marker + `"}`))
	if strings.Contains(body, marker) || !strings.Contains(body, "MASKED_API_TOKEN_REDACTED") {
		t.Fatalf("流量日志未移除语义占位符映射 ID: %s", body)
	}
}
