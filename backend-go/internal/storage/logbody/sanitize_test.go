package logbody

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestPrepareBodyReplacesMediaPayloads(t *testing.T) {
	anthropic, _ := json.Marshal(map[string]any{
		"messages": []any{map[string]any{"role": "user", "content": []any{map[string]any{
			"type": "image",
			"source": map[string]any{
				"type":       "base64",
				"media_type": "image/png",
				"data":       "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+ip1s=",
			},
		}}}},
	})
	for _, test := range []struct {
		name     string
		raw      []byte
		leaked   string
		wantMeta string
	}{
		{"anthropic image", anthropic, "iVBORw0KGgo", "[omitted image; media_type=image/png;"},
		{"data url", []byte(`{"image_url":{"url":"data:image/jpeg;base64,/9j/4AAQSkZJRgABAQAAAQABAAD"}}`), "/9j/", "media_type=image/jpeg"},
		{"gemini inline data", []byte(`{"inlineData":{"mimeType":"image/webp","data":"UklGRiQAAABXRUJQVlA4IBgAAAAwAQCdASoBAAEAAwA0JaQAA3AA/vuUAAA="}}`), "UklGRiQ", "media_type=image/webp"},
	} {
		t.Run(test.name, func(t *testing.T) {
			body, originalBytes, truncated := PrepareBody(test.raw)
			if originalBytes != len(test.raw) || truncated {
				t.Fatalf("originalBytes=%d truncated=%v, want %d false", originalBytes, truncated, len(test.raw))
			}
			if strings.Contains(body, test.leaked) {
				t.Fatalf("media payload still present: %s", body)
			}
			if !strings.Contains(body, test.wantMeta) {
				t.Fatalf("placeholder missing: %s", body)
			}
		})
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

func TestPrepareBodyRemovesPlaceholderMappingIDs(t *testing.T) {
	for _, test := range []struct{ marker, redacted string }{
		{"MASKED_SECRET_7K4M2P7QAB2CD_PRESERVE_EXACTLY", "MASKED_SECRET_REDACTED"},
		{"MASKED_API_TOKEN_TM5A7UUJ4YMVY_PRESERVE_EXACTLY", "MASKED_API_TOKEN_REDACTED"},
	} {
		body, _, _ := PrepareBody([]byte(`{"text":"` + test.marker + `"}`))
		if strings.Contains(body, test.marker) || !strings.Contains(body, test.redacted) {
			t.Fatalf("流量日志未移除占位符映射 ID: %s", body)
		}
	}
}
