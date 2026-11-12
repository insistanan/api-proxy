package mediasanitizer

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestIsUnsupportedImageError(t *testing.T) {
	cases := []struct {
		name     string
		status   int
		body     string
		expected bool
	}{
		{
			name:     "volcengine text only error",
			status:   http.StatusBadRequest,
			body:     `{"error":{"message":"Model only support text input"}}`,
			expected: true,
		},
		{
			name:     "unsupported image error",
			status:   http.StatusBadRequest,
			body:     `{"error":{"message":"Image input is not supported for this model"}}`,
			expected: true,
		},
		{
			name:     "415 unsupported media",
			status:   http.StatusUnsupportedMediaType,
			body:     `{"error":{"message":"Unsupported media type: image/png"}}`,
			expected: true,
		},
		{
			name:     "422 unprocessable entity with multimodal rejection",
			status:   http.StatusUnprocessableEntity,
			body:     `{"error":{"message":"Cannot handle vision modality in current endpoint"}}`,
			expected: true,
		},
		{
			name:     "regular 400 error",
			status:   http.StatusBadRequest,
			body:     `{"error":{"message":"invalid api key or missing headers"}}`,
			expected: false,
		},
		{
			name:     "500 internal error with image keyword",
			status:   http.StatusInternalServerError,
			body:     `{"error":{"message":"image service unavailable"}}`,
			expected: false, // 仅限 400, 415, 422, 501
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := IsUnsupportedImageError(tc.status, []byte(tc.body))
			if got != tc.expected {
				t.Errorf("IsUnsupportedImageError() = %v, want %v", got, tc.expected)
			}
		})
	}
}

func TestSanitizeImagesWithMarker(t *testing.T) {
	t.Run("sanitizes claude message images and tool results", func(t *testing.T) {
		input := `{
			"model": "deepseek-chat",
			"messages": [
				{
					"role": "user",
					"content": [
						{"type": "text", "text": "What is in this image?"},
						{"type": "image", "source": {"type": "base64", "data": "abc"}}
					]
				},
				{
					"role": "user",
					"content": [
						{
							"type": "tool_result",
							"tool_use_id": "tool_1",
							"content": [
								{"type": "image", "source": {"type": "base64", "data": "def"}}
							]
						}
					]
				}
			]
		}`

		sanitized, modified, count := SanitizeImagesWithMarker([]byte(input))
		if !modified {
			t.Fatalf("expected modified = true")
		}
		if count != 2 {
			t.Fatalf("expected replaced count = 2, got %d", count)
		}
		if strings.Contains(string(sanitized), `"type":"image"`) {
			t.Errorf("sanitized body still contains image blocks")
		}
		if strings.Contains(string(sanitized), `"base64"`) {
			t.Errorf("sanitized body still contains base64 image data")
		}

		var payload map[string]interface{}
		if err := json.Unmarshal(sanitized, &payload); err != nil {
			t.Fatalf("unmarshal sanitized payload failed: %v", err)
		}
	})

	t.Run("sanitizes responses input images", func(t *testing.T) {
		input := `{
			"model": "gpt-4o-mini",
			"input": [
				{"type": "input_text", "text": "hello"},
				{"type": "input_image", "image_url": "https://example.com/a.png"}
			]
		}`

		sanitized, modified, count := SanitizeImagesWithMarker([]byte(input))
		if !modified {
			t.Fatalf("expected modified = true")
		}
		if count != 1 {
			t.Fatalf("expected replaced count = 1, got %d", count)
		}
		if strings.Contains(string(sanitized), "input_image") {
			t.Errorf("sanitized body still contains input_image")
		}
		if !strings.Contains(string(sanitized), UnsupportedImageMarker) {
			t.Errorf("sanitized body missing marker")
		}
	})

	t.Run("sanitizes gemini contents inlineData images", func(t *testing.T) {
		input := `{
			"contents": [
				{
					"parts": [
						{"text": "analyze this:"},
						{"inlineData": {"mimeType": "image/jpeg", "data": "xyz"}}
					]
				}
			]
		}`

		sanitized, modified, count := SanitizeImagesWithMarker([]byte(input))
		if !modified {
			t.Fatalf("expected modified = true")
		}
		if count != 1 {
			t.Fatalf("expected count = 1, got %d", count)
		}
		if strings.Contains(string(sanitized), "inlineData") {
			t.Errorf("sanitized body still contains inlineData")
		}
	})
}
