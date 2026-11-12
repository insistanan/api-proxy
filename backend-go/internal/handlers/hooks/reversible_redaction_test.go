package hooks

import (
	"bytes"
	"context"
	"io"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/insistanan/api-proxy/internal/infra/config"
)

func TestReversibleRedactionCoversAllRequestProtocolsAndResponseFields(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		protocol string
		body     string
		secrets  []string
	}{
		{protocol: "messages", body: `{"system":"phone 18012345523","messages":[{"role":"assistant","content":"email bot@example.com"},{"role":"user","content":[{"type":"tool_result","content":{"host":"10.0.0.8"}}]},{"role":"assistant","content":[{"type":"tool_use","name":"connect","input":{"dsn":"postgres://user:p4ssword@10.0.0.9/db"}}]}]}`, secrets: []string{"18012345523", "bot@example.com", "10.0.0.8", "postgres://user:p4ssword@10.0.0.9/db"}},
		{protocol: "chat", body: `{"messages":[{"role":"system","content":"phone 18012345523"},{"role":"assistant","content":"email bot@example.com","tool_calls":[{"index":0,"function":{"name":"connect","arguments":"{\"host\":\"10.0.0.8\"}"}}]},{"role":"tool","name":"connect","content":"postgres://user:p4ssword@10.0.0.9/db"}]}`, secrets: []string{"18012345523", "bot@example.com", "10.0.0.8", "postgres://user:p4ssword@10.0.0.9/db"}},
		{protocol: "responses", body: `{"instructions":"phone 18012345523","input":[{"role":"assistant","content":"email bot@example.com"},{"type":"function_call","name":"connect","arguments":"{\"host\":\"10.0.0.8\"}"},{"type":"function_call_output","output":"postgres://user:p4ssword@10.0.0.9/db"}]}`, secrets: []string{"18012345523", "bot@example.com", "10.0.0.8", "postgres://user:p4ssword@10.0.0.9/db"}},
		{protocol: "gemini", body: `{"systemInstruction":{"parts":[{"text":"phone 18012345523"}]},"contents":[{"role":"model","parts":[{"text":"email bot@example.com"}]},{"role":"user","parts":[{"functionCall":{"name":"connect","args":{"host":"10.0.0.8"}}},{"functionResponse":{"name":"connect","response":{"dsn":"postgres://user:p4ssword@10.0.0.9/db"}}}]}]}`, secrets: []string{"18012345523", "bot@example.com", "10.0.0.8", "postgres://user:p4ssword@10.0.0.9/db"}},
		{protocol: "images", body: `{"model":"gpt-image-1","prompt":"phone 18012345523 at 10.0.0.8"}`, secrets: []string{"18012345523", "10.0.0.8"}},
	}

	for _, test := range tests {
		t.Run(test.protocol, func(t *testing.T) {
			configManager, err := config.NewConfigManager(filepath.Join(t.TempDir(), "config.json"))
			if err != nil {
				t.Fatalf("创建配置管理器失败: %v", err)
			}
			defer configManager.Close()
			settings := configManager.GetSettings()
			settings.ContentSafety.SensitiveData.Mode = config.ContentSafetyModeMask
			settings.ContentSafety.SensitiveInfo.Enabled = true
			settings.ContentSafety.SensitiveInfo.EnabledRules = []string{
				config.SensitiveInfoRulePhone, config.SensitiveInfoRuleEmail, config.SensitiveInfoRuleIPAddress,
			}
			settings.ContentSafety.SensitiveInfo.IPMaskScope = config.SensitiveInfoIPMaskScopeAll
			settings.ContentSafety.Credential.Enabled = true
			settings.ContentSafety.Credential.EnabledRules = []string{config.CredentialRuleConnectionString}
			if err := configManager.UpdateSettings(settings); err != nil {
				t.Fatalf("更新设置失败: %v", err)
			}

			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			AttachHookPipeline(c, NewContentSafetyPipeline(configManager), HookContext{APIType: test.protocol})
			req := httptest.NewRequest("POST", "/", bytes.NewBufferString(test.body))
			req.Header.Set("Content-Type", "application/json")
			if err := RunAttachedPreRequestHooks(context.Background(), c, req, "primary", test.protocol); err != nil {
				t.Fatalf("上行脱敏失败: %v", err)
			}
			upstreamBody, err := io.ReadAll(req.Body)
			if err != nil {
				t.Fatalf("读取上游请求失败: %v", err)
			}
			for _, secret := range test.secrets {
				if bytes.Contains(upstreamBody, []byte(secret)) {
					t.Fatalf("敏感原文仍在上游请求中: %q body=%s", secret, upstreamBody)
				}
			}
			placeholder := regexp.MustCompile(`\bMASKED_[A-Z0-9_]+_[A-Z2-7]{13}_PRESERVE_EXACTLY\b`).Find(upstreamBody)
			if len(placeholder) == 0 {
				t.Fatalf("上游请求未包含语义占位符: %s", upstreamBody)
			}
			attached, err := attachedPipelineFromContext(c)
			if err != nil {
				t.Fatalf("读取请求映射仓失败: %v", err)
			}
			expected := attached.vault.Restore(string(placeholder))
			response := []byte(`{"text":"` + string(placeholder) + `","tool":{"arguments":"` + string(placeholder) + `"}}`)
			restored, err := RunAttachedPostResponseHooks(context.Background(), c, response, nil)
			if err != nil {
				t.Fatalf("下行还原失败: %v", err)
			}
			if bytes.Contains(restored, placeholder) || strings.Count(string(restored), expected) != 2 {
				t.Fatalf("正文与工具参数未精确还原: %s", restored)
			}
		})
	}
}
