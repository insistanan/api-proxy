package providers

import (
	"testing"

	"github.com/insistanan/api-proxy/internal/infra/config"
)

// "#"后缀与版本前缀的通用边界由 utils/url_builder_test.go 验证；
// 此处仅保留调用真实 Provider 构建函数的测试。

func TestBuildTargetURL_SkipVersionWithHash(t *testing.T) {
	p := &ResponsesProvider{}

	tests := []struct {
		name        string
		baseURL     string
		serviceType string
		want        string
	}{
		// 正常情况：自动添加 /v1
		{"normal_responses", "https://api.example.com", "responses", "https://api.example.com/v1/responses"},
		{"normal_claude", "https://api.example.com", "claude", "https://api.example.com/v1/messages"},
		{"normal_openai", "https://api.example.com", "openai", "https://api.example.com/v1/chat/completions"},

		// 已有版本号：不添加 /v1
		{"with_version", "https://api.example.com/v1", "responses", "https://api.example.com/v1/responses"},
		{"with_v2", "https://api.example.com/v2", "openai", "https://api.example.com/v2/chat/completions"},

		// # 结尾：跳过 /v1
		{"hash_skip", "https://api.example.com#", "responses", "https://api.example.com/responses"},
		{"hash_skip_claude", "https://api.example.com#", "claude", "https://api.example.com/messages"},
		{"hash_skip_openai", "https://api.example.com#", "openai", "https://api.example.com/chat/completions"},

		// # 结尾 + 末尾斜杠：正确处理
		{"hash_with_slash", "https://api.example.com/#", "responses", "https://api.example.com/responses"},
		{"hash_with_slash_openai", "https://api.example.com/#", "openai", "https://api.example.com/chat/completions"},

		// 末尾斜杠：正确移除
		{"trailing_slash", "https://api.example.com/", "responses", "https://api.example.com/v1/responses"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			upstream := &config.UpstreamConfig{
				BaseURL:     tt.baseURL,
				ServiceType: tt.serviceType,
			}
			got := p.buildTargetURL(upstream)
			if got != tt.want {
				t.Errorf("buildTargetURL() = %q, want %q", got, tt.want)
			}
		})
	}
}

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
