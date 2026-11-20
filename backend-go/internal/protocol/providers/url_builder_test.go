package providers

import (
	"testing"
)

// 验证 Gemini Provider 的版本前缀、完整地址和流式 URL。

// TestGeminiURL_VersionPrefixDefaults 验证 Gemini 默认版本段为 /v1beta 且约定一致。
func TestGeminiURL_VersionPrefixDefaults(t *testing.T) {
	tests := []struct {
		name    string
		baseURL string
		model   string
		stream  bool
		want    string
	}{
		{"default_v1beta", "https://generativelanguage.googleapis.com", "gemini-2.0-flash", false, "https://generativelanguage.googleapis.com/v1beta/models/gemini-2.0-flash:generateContent"},
		{"explicit_v1", "https://generativelanguage.googleapis.com/v1", "gemini-2.0-flash", false, "https://generativelanguage.googleapis.com/v1/models/gemini-2.0-flash:generateContent"},
		{"hash_skip", "https://proxy.example.com#", "gemini-2.0-flash", true, "https://proxy.example.com/models/gemini-2.0-flash:streamGenerateContent?alt=sse"},
		{"tuned_model_prefix_preserved", "https://generativelanguage.googleapis.com", "tunedModels/my-tune", false, "https://generativelanguage.googleapis.com/v1beta/tunedModels/my-tune:generateContent"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildGeminiGenerateContentURL(tt.baseURL, tt.model, tt.stream)
			if got != tt.want {
				t.Errorf("buildGeminiGenerateContentURL(%q, %q, %v) = %q, want %q", tt.baseURL, tt.model, tt.stream, got, tt.want)
			}
		})
	}
}
