package utils

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestPrepareUpstreamHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name        string
		headers     map[string]string
		targetHost  string
		wantHost    string
		shouldExist map[string]bool
	}{
		{
			name: "移除代理相关头部",
			headers: map[string]string{
				"Content-Type":      "application/json",
				"x-proxy-key":       "secret",
				"X-Forwarded-Host":  "original.host",
				"X-Forwarded-Proto": "https",
			},
			targetHost: "upstream.api.com",
			wantHost:   "upstream.api.com",
			shouldExist: map[string]bool{
				"Content-Type":      true,
				"x-proxy-key":       false,
				"X-Forwarded-Host":  false,
				"X-Forwarded-Proto": false,
			},
		},
		{
			name: "移除所有可能泄露客户端IP的头部",
			headers: map[string]string{
				"Content-Type":     "application/json",
				"X-Forwarded-For":  "1.2.3.4, 5.6.7.8",
				"X-Real-IP":        "1.2.3.4",
				"X-Forwarded":      "for=1.2.3.4",
				"Forwarded":        "for=1.2.3.4;proto=https",
				"CF-Connecting-IP": "1.2.3.4",
				"True-Client-IP":   "1.2.3.4",
				"X-Client-IP":      "1.2.3.4",
			},
			targetHost: "upstream.api.com",
			wantHost:   "upstream.api.com",
			shouldExist: map[string]bool{
				"Content-Type":     true,
				"X-Forwarded-For":  false,
				"X-Real-IP":        false,
				"X-Forwarded":      false,
				"Forwarded":        false,
				"CF-Connecting-IP": false,
				"True-Client-IP":   false,
				"X-Client-IP":      false,
			},
		},
		{
			name: "保留其他头部",
			headers: map[string]string{
				"Content-Type":  "application/json",
				"User-Agent":    "TestClient/1.0",
				"Accept":        "*/*",
				"Custom-Header": "custom-value",
			},
			targetHost: "api.example.com",
			wantHost:   "api.example.com",
			shouldExist: map[string]bool{
				"Content-Type":  true,
				"User-Agent":    true,
				"Accept":        true,
				"Custom-Header": true,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 创建测试请求
			req := httptest.NewRequest("POST", "/test", nil)
			for k, v := range tt.headers {
				req.Header.Set(k, v)
			}

			// 创建Gin上下文
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = req

			// 调用函数
			result := PrepareUpstreamHeaders(c, tt.targetHost)

			// 验证Host头部
			if result.Get("Host") != tt.wantHost {
				t.Errorf("Host = %v, want %v", result.Get("Host"), tt.wantHost)
			}

			// 验证头部是否存在
			for header, shouldExist := range tt.shouldExist {
				exists := result.Get(header) != ""
				if exists != shouldExist {
					t.Errorf("Header %s existence = %v, want %v", header, exists, shouldExist)
				}
			}
		})
	}
}

func TestSetAuthenticationHeader(t *testing.T) {
	tests := []struct {
		name              string
		apiKey            string
		wantXApiKey       string
		wantAuthorization string
	}{
		{
			name:              "Claude官方格式密钥",
			apiKey:            "sk-ant-api03-1234567890",
			wantXApiKey:       "sk-ant-api03-1234567890",
			wantAuthorization: "",
		},
		{
			name:              "通用Bearer格式密钥",
			apiKey:            "sk-1234567890abcdef",
			wantXApiKey:       "",
			wantAuthorization: "Bearer sk-1234567890abcdef",
		},
		{
			name:              "其他格式密钥",
			apiKey:            "custom-key-format",
			wantXApiKey:       "",
			wantAuthorization: "Bearer custom-key-format",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			headers := http.Header{}
			SetAuthenticationHeader(headers, tt.apiKey)

			if tt.wantXApiKey != "" {
				if got := headers.Get("x-api-key"); got != tt.wantXApiKey {
					t.Errorf("x-api-key = %v, want %v", got, tt.wantXApiKey)
				}
				if headers.Get("Authorization") != "" {
					t.Errorf("Authorization should be empty, got %v", headers.Get("Authorization"))
				}
			} else {
				if got := headers.Get("Authorization"); got != tt.wantAuthorization {
					t.Errorf("Authorization = %v, want %v", got, tt.wantAuthorization)
				}
				if headers.Get("x-api-key") != "" {
					t.Errorf("x-api-key should be empty, got %v", headers.Get("x-api-key"))
				}
			}
		})
	}
}

func TestSetGeminiAuthenticationHeader(t *testing.T) {
	headers := http.Header{}
	apiKey := "AIzaSyABC123DEF456"

	SetGeminiAuthenticationHeader(headers, apiKey)

	if got := headers.Get("x-goog-api-key"); got != apiKey {
		t.Errorf("x-goog-api-key = %v, want %v", got, apiKey)
	}

	// 验证其他认证头被删除
	if headers.Get("authorization") != "" {
		t.Errorf("authorization should be empty, got %v", headers.Get("authorization"))
	}
	if headers.Get("x-api-key") != "" {
		t.Errorf("x-api-key should be empty, got %v", headers.Get("x-api-key"))
	}
}

func TestEnsureCompatibleUserAgent(t *testing.T) {
	tests := []struct {
		name            string
		serviceType     string
		initialUA       string
		expectedUA      string
		shouldBeChanged bool
	}{
		{
			name:            "Claude服务 - 空User-Agent",
			serviceType:     "claude",
			initialUA:       "",
			expectedUA:      "claude-cli/2.0.34 (external, cli)",
			shouldBeChanged: true,
		},
		{
			name:            "Claude服务 - 非Claude-CLI User-Agent",
			serviceType:     "claude",
			initialUA:       "Mozilla/5.0",
			expectedUA:      "claude-cli/2.0.34 (external, cli)",
			shouldBeChanged: true,
		},
		{
			name:            "Claude服务 - 已有Claude-CLI User-Agent",
			serviceType:     "claude",
			initialUA:       "claude-cli/2.0.34 (external, cli)",
			expectedUA:      "claude-cli/2.0.34 (external, cli)",
			shouldBeChanged: false,
		},
		{
			name:            "非Claude服务 - 保留原User-Agent",
			serviceType:     "openai",
			initialUA:       "CustomClient/1.0",
			expectedUA:      "CustomClient/1.0",
			shouldBeChanged: false,
		},
		{
			name:            "Gemini服务 - 保留原User-Agent",
			serviceType:     "gemini",
			initialUA:       "GeminiClient/2.0",
			expectedUA:      "GeminiClient/2.0",
			shouldBeChanged: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			headers := http.Header{}
			if tt.initialUA != "" {
				headers.Set("User-Agent", tt.initialUA)
			}

			EnsureCompatibleUserAgent(headers, tt.serviceType)

			got := headers.Get("User-Agent")
			if got != tt.expectedUA {
				t.Errorf("User-Agent = %v, want %v", got, tt.expectedUA)
			}
		})
	}
}

func TestDisguiseVersionFromEnv(t *testing.T) {
	t.Run("未设置环境变量时返回默认值", func(t *testing.T) {
		if got := claudeCodeDisguiseVersion(); got != defaultClaudeCodeDisguiseVersion {
			t.Fatalf("claudeCodeDisguiseVersion() = %q, want %q", got, defaultClaudeCodeDisguiseVersion)
		}
		if got := codexDisguiseVersion(); got != defaultCodexDisguiseVersion {
			t.Fatalf("codexDisguiseVersion() = %q, want %q", got, defaultCodexDisguiseVersion)
		}
	})

	t.Run("环境变量覆盖伪装版本", func(t *testing.T) {
		t.Setenv(envKeyClaudeCodeDisguiseVersion, "9.9.9")
		t.Setenv(envKeyCodexDisguiseVersion, "8.8.8")

		claudeHeaders := http.Header{}
		ApplyClaudeCodeDisguise(claudeHeaders, false)
		if got := claudeHeaders.Get("User-Agent"); got != "claude-cli/9.9.9 (external, cli)" {
			t.Fatalf("Claude 伪装 UA = %q, want %q", got, "claude-cli/9.9.9 (external, cli)")
		}

		codexHeaders := http.Header{}
		ApplyCodexDisguise(codexHeaders, false)
		if got := codexHeaders.Get("User-Agent"); !strings.HasPrefix(got, "codex_cli_rs/8.8.8 ") {
			t.Fatalf("Codex 伪装 UA = %q, 应以 codex_cli_rs/8.8.8 开头", got)
		}
		if got := codexHeaders.Get("version"); got != "8.8.8" {
			t.Fatalf("Codex version 头 = %q, want %q", got, "8.8.8")
		}
	})

	t.Run("空白环境变量回退默认值", func(t *testing.T) {
		t.Setenv(envKeyClaudeCodeDisguiseVersion, "   ")
		t.Setenv(envKeyCodexDisguiseVersion, "")
		if got := claudeCodeDisguiseVersion(); got != defaultClaudeCodeDisguiseVersion {
			t.Fatalf("claudeCodeDisguiseVersion() = %q, want %q", got, defaultClaudeCodeDisguiseVersion)
		}
		if got := codexDisguiseVersion(); got != defaultCodexDisguiseVersion {
			t.Fatalf("codexDisguiseVersion() = %q, want %q", got, defaultCodexDisguiseVersion)
		}
	})

	t.Run("请求体计费块使用环境变量版本", func(t *testing.T) {
		t.Setenv(envKeyClaudeCodeDisguiseVersion, "9.9.9")
		body := []byte(`{"model":"claude-sonnet-4-5","messages":[{"role":"user","content":"hello world from test"}]}`)
		updated := ApplyClaudeCodeBodyDisguise(body, "session-1")
		var payload map[string]interface{}
		if err := json.Unmarshal(updated, &payload); err != nil {
			t.Fatalf("伪装后请求体不是合法 JSON: %v", err)
		}
		systemBlocks, _ := payload["system"].([]interface{})
		if len(systemBlocks) == 0 {
			t.Fatal("应注入计费 system 块")
		}
		text, _ := systemBlocks[0].(map[string]interface{})["text"].(string)
		if !strings.Contains(text, "cc_version=9.9.9.") {
			t.Fatalf("计费块应包含环境变量版本: %q", text)
		}
	})
}

func TestForwardResponseHeadersExSkipsUpstreamCORS(t *testing.T) {
	recorder := httptest.NewRecorder()
	recorder.Header().Set("Access-Control-Allow-Origin", "https://user.github.io")
	recorder.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

	upstream := http.Header{}
	upstream.Set("Access-Control-Allow-Origin", "*")
	upstream.Set("Access-Control-Allow-Credentials", "true")
	upstream.Set("Access-Control-Allow-Methods", "GET,POST,PUT,DELETE,OPTIONS")
	upstream.Set("Access-Control-Allow-Headers", "*")
	upstream.Set("Access-Control-Max-Age", "43200")
	upstream.Set("X-Oneapi-Request-Id", "req-1")
	upstream.Set("Content-Type", "text/event-stream")

	ForwardResponseHeadersEx(upstream, recorder, true)

	origin := recorder.Header().Values("Access-Control-Allow-Origin")
	if len(origin) != 1 || origin[0] != "https://user.github.io" {
		t.Fatalf("上游 CORS 来源不应叠加，得到 %#v", origin)
	}
	if got := recorder.Header().Get("Access-Control-Allow-Credentials"); got != "" {
		t.Fatalf("不应转发上游 Allow-Credentials，得到 %q", got)
	}
	if got := recorder.Header().Get("Access-Control-Allow-Headers"); got != "Content-Type, Authorization" {
		t.Fatalf("应保留网关自己的 Allow-Headers，得到 %q", got)
	}
	if got := recorder.Header().Get("Access-Control-Max-Age"); got != "" {
		t.Fatalf("不应转发上游 Max-Age，得到 %q", got)
	}
	if got := recorder.Header().Get("X-Oneapi-Request-Id"); got != "req-1" {
		t.Fatalf("普通响应头应保留，得到 %q", got)
	}
	if got := recorder.Header().Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("Content-Type = %q", got)
	}
}
