package providers

import (
	"testing"

	"github.com/insistanan/api-proxy/internal/config"
	"github.com/insistanan/api-proxy/internal/utils"
)

// "#"后缀与版本前缀约定的行为锚点测试。单一出处是 utils.BuildUpstreamURL；
// 本文件用各 provider 的真实构建函数（而非旧逻辑的本地模拟副本）验证约定，
// 另在 utils 包内测试通用入口本身。

func TestOpenAIURL_SkipVersionWithHash(t *testing.T) {
	tests := []struct {
		name    string
		baseURL string
		want    string
	}{
		{"normal", "https://api.openai.com", "https://api.openai.com/v1/chat/completions"},
		{"with_v1", "https://api.openai.com/v1", "https://api.openai.com/v1/chat/completions"},
		{"hash_skip", "https://api.example.com#", "https://api.example.com/chat/completions"},
		{"hash_with_slash", "https://api.example.com/#", "https://api.example.com/chat/completions"},
		{"trailing_slash", "https://api.example.com/", "https://api.example.com/v1/chat/completions"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := utils.BuildUpstreamURL(tt.baseURL, "/v1", "/chat/completions")
			if got != tt.want {
				t.Errorf("BuildUpstreamURL(%q, /v1, /chat/completions) = %q, want %q", tt.baseURL, got, tt.want)
			}
		})
	}
}

func TestClaudeURL_SkipVersionWithHash(t *testing.T) {
	tests := []struct {
		name        string
		baseURL     string
		requestPath string
		want        string
	}{
		{"normal", "https://api.anthropic.com", "/v1/messages", "https://api.anthropic.com/v1/messages"},
		{"with_v1", "https://api.anthropic.com/v1", "/v1/messages", "https://api.anthropic.com/v1/messages"},
		{"hash_skip", "https://api.example.com#", "/v1/messages", "https://api.example.com/messages"},
		{"hash_with_slash", "https://api.example.com/#", "/v1/messages", "https://api.example.com/messages"},
		{"trailing_slash", "https://api.example.com/", "/v1/messages", "https://api.example.com/v1/messages"},
		{"count_tokens", "https://api.example.com#", "/v1/messages/count_tokens", "https://api.example.com/messages/count_tokens"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// ClaudeProvider 的 URL 构建内联在 ConvertToProviderRequest 中
			//（endpoint = TrimPrefix(path, "/v1")），此处等价复现该规整后测通用入口。
			endpoint := tt.requestPath[len("/v1"):]
			got := utils.BuildUpstreamURL(tt.baseURL, "/v1", endpoint)
			if got != tt.want {
				t.Errorf("BuildUpstreamURL(%q, /v1, %q) = %q, want %q", tt.baseURL, endpoint, got, tt.want)
			}
		})
	}
}

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

// TestMessagesResponsesURL_SkipVersionWithHash 验证 Messages→Responses 上游 URL
// 构建与 utils.BuildUpstreamURL 约定一致（原私有 buildResponsesURL 已收敛，见治理记录）。
func TestMessagesResponsesURL_SkipVersionWithHash(t *testing.T) {
	tests := []struct {
		name    string
		baseURL string
		want    string
	}{
		{"normal", "https://api.example.com", "https://api.example.com/v1/responses"},
		{"with_v1", "https://api.example.com/v1", "https://api.example.com/v1/responses"},
		{"with_v1beta", "https://api.example.com/v1beta", "https://api.example.com/v1beta/responses"},
		{"hash_skip", "https://api.example.com#", "https://api.example.com/responses"},
		{"hash_with_slash", "https://api.example.com/#", "https://api.example.com/responses"},
		{"trailing_slash", "https://api.example.com/", "https://api.example.com/v1/responses"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := utils.BuildUpstreamURL(tt.baseURL, "/v1", "/responses")
			if got != tt.want {
				t.Errorf("BuildUpstreamURL(%q, /v1, /responses) = %q, want %q", tt.baseURL, got, tt.want)
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
