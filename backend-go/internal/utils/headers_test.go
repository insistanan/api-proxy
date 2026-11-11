package utils

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestAnonymousAuthenticationRemovesInboundCredentials(t *testing.T) {
	for _, key := range []string{"Authorization", "X-Api-Key", "X-Goog-Api-Key"} {
		headers := make(http.Header)
		headers.Set(key, "private-client-key")
		SetAuthenticationHeader(headers, "")
		for _, name := range []string{"Authorization", "X-Api-Key", "X-Goog-Api-Key"} {
			if headers.Get(name) != "" {
				t.Fatalf("匿名请求仍包含认证头 %s", name)
			}
		}
	}
}

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
		{
			name: "移除浏览器凭据、内部控制头和 Connection 点名的逐跳头",
			headers: map[string]string{
				"Cookie": "fixture=session", "Origin": "https://client.example", "Referer": "https://client.example/page",
				"Connection": "X-Hop-Only, keep-alive", "X-Hop-Only": "private", "Keep-Alive": "timeout=5",
				"X-Proxy-Purpose": "quick_test", "Content-Length": "999", "X-Business-Trace": "preserve",
			},
			targetHost: "upstream.example", wantHost: "upstream.example",
			shouldExist: map[string]bool{
				"Cookie": false, "Origin": false, "Referer": false, "Connection": false,
				"X-Hop-Only": false, "Keep-Alive": false, "X-Proxy-Purpose": false, "Content-Length": false,
				"X-Business-Trace": true,
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

func TestDisguiseVersion(t *testing.T) {
	// 每个子用例恢复完整状态，latest 缓存也必须清理，支持重复和乱序执行。
	setVersions := func(t *testing.T, claude, codex, latestClaude, latestCodex string) {
		t.Helper()
		disguiseMu.Lock()
		oldClaude, oldCodex := customClaudeCodeVersion, customCodexVersion
		oldLatestClaude, oldLatestCodex := latestNpmClaudeCodeVersion, latestNpmCodexVersion
		oldAuto := disguiseAutoUpdate
		customClaudeCodeVersion, customCodexVersion = strings.TrimSpace(claude), strings.TrimSpace(codex)
		latestNpmClaudeCodeVersion, latestNpmCodexVersion = latestClaude, latestCodex
		disguiseAutoUpdate = true
		disguiseMu.Unlock()
		t.Cleanup(func() {
			disguiseMu.Lock()
			defer disguiseMu.Unlock()
			customClaudeCodeVersion, customCodexVersion = oldClaude, oldCodex
			latestNpmClaudeCodeVersion, latestNpmCodexVersion = oldLatestClaude, oldLatestCodex
			disguiseAutoUpdate = oldAuto
		})
	}
	t.Run("Codex旧版本头与UA同步更新并保留会话追踪", func(t *testing.T) {
		setVersions(t, "", "8.8.8", "", "")
		headers := http.Header{}
		headers.Set("User-Agent", "codex_cli_rs/0.133.0 old-client")
		headers.Set("version", "0.133.0")
		headers.Set("session_id", "session-existing")
		headers.Set("X-Codex-Installation-Id", "installation-existing")
		headers.Set("X-Codex-Window-Id", "window-existing")
		headers.Set("X-Oai-Request-Id", "request-existing")
		ApplyCodexDisguise(headers, true)
		if !strings.HasPrefix(headers.Get("User-Agent"), "codex_cli_rs/8.8.8 ") || headers.Get("version") != "8.8.8" {
			t.Fatalf("伪装版本不一致: User-Agent=%q version=%q", headers.Get("User-Agent"), headers.Get("version"))
		}
		for key, want := range map[string]string{
			"session_id":              "session-existing",
			"X-Codex-Installation-Id": "installation-existing",
			"X-Codex-Window-Id":       "window-existing",
			"X-Oai-Request-Id":        "request-existing",
		} {
			if got := headers.Get(key); got != want {
				t.Fatalf("已有 %s 被改变: got %q, want %q", key, got, want)
			}
		}
		if headers.Get("X-Request-Id") != "" {
			t.Fatal("已有 X-Oai-Request-Id 时不应生成另一份追踪 ID")
		}
	})

	t.Run("未设置时返回默认值", func(t *testing.T) {
		setVersions(t, "", "", "", "")
		if got := claudeCodeDisguiseVersion(); got != defaultClaudeCodeDisguiseVersion {
			t.Fatalf("claudeCodeDisguiseVersion() = %q, want %q", got, defaultClaudeCodeDisguiseVersion)
		}
		if got := codexDisguiseVersion(); got != defaultCodexDisguiseVersion {
			t.Fatalf("codexDisguiseVersion() = %q, want %q", got, defaultCodexDisguiseVersion)
		}
	})

	t.Run("自定义版本覆盖伪装版本", func(t *testing.T) {
		setVersions(t, "9.9.9", "8.8.8", "", "")

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

	t.Run("空白自定义版本回退默认值或最新值", func(t *testing.T) {
		setVersions(t, "   ", "", "2.5.0", "")

		if got := claudeCodeDisguiseVersion(); got != "2.5.0" {
			t.Fatalf("claudeCodeDisguiseVersion() = %q, want %q", got, "2.5.0")
		}
		if got := codexDisguiseVersion(); got != defaultCodexDisguiseVersion {
			t.Fatalf("codexDisguiseVersion() = %q, want %q", got, defaultCodexDisguiseVersion)
		}
	})

	t.Run("请求体计费块使用自定义版本", func(t *testing.T) {
		setVersions(t, "9.9.9", "", "", "")
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
			t.Fatalf("计费块应包含自定义版本: %q", text)
		}
	})

	t.Run("关闭自动跟随后忽略缓存并保留自定义值", func(t *testing.T) {
		setVersions(t, "", "", "9.1.1", "8.1.1")
		SetDisguiseSettings("", "", false)
		claude, codex, latestClaude, latestCodex := GetEffectiveDisguiseVersions()
		if claude != defaultClaudeCodeDisguiseVersion || codex != defaultCodexDisguiseVersion || latestClaude != "9.1.1" || latestCodex != "8.1.1" {
			t.Fatal("关闭自动跟随应保留观测缓存，但使用内置默认版本")
		}
		SetDisguiseSettings("7.1.1", "6.1.1", false)
		SetDisguiseLatestVersions("9.2.2", "8.2.2")
		claude, codex, _, _ = GetEffectiveDisguiseVersions()
		if claude != "7.1.1" || codex != "6.1.1" {
			t.Fatal("后台缓存刷新不能覆盖新保存的自定义版本")
		}
	})

	t.Run("旧env不覆盖已发布的设置", func(t *testing.T) {
		setVersions(t, "7.1.1", "6.1.1", "", "")
		t.Setenv("CLAUDE_CODE_DISGUISE_VERSION", "1.0.0")
		t.Setenv("CODEX_DISGUISE_VERSION", "1.0.0")
		claude, codex, _, _ := GetEffectiveDisguiseVersions()
		if claude != "7.1.1" || codex != "6.1.1" {
			t.Fatal("旧 env 不应覆盖持久化设置")
		}
	})
}

func TestValidateDisguiseVersion(t *testing.T) {
	for _, version := range []string{"", "2.1.238", "0.150.1-beta.1", "1.2.3+build.4"} {
		if err := ValidateDisguiseVersion(version); err != nil {
			t.Fatalf("合法版本 %q 被拒绝: %v", version, err)
		}
	}
	for _, version := range []string{"1.2.3\nInjected: value", "1.2.3\x00", "latest", "v1.2.3", "1.2"} {
		if err := ValidateDisguiseVersion(version); err == nil {
			t.Fatalf("非法版本 %q 应被拒绝", version)
		}
	}
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
