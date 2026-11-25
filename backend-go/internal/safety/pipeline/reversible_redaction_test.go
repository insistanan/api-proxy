package pipeline

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"insistanan/api-proxy/internal/infra/config"
	"insistanan/api-proxy/internal/safety/sensitive"
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
			settings.ContentSafety.Enabled = true
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
			placeholder := regexp.MustCompile(`\bMASKED_[A-Z0-9_]+_[A-Z2-7]{13}_(?:END|PRESERVE_EXACTLY)\b`).Find(upstreamBody)
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

func TestPersistentRedaction_ModeChangeUsesRequestSnapshot(t *testing.T) {
	manager, err := config.NewConfigManager(filepath.Join(t.TempDir(), "config.json"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, manager.Close()) })
	dir := t.TempDir()
	store, err := sensitive.NewMappingStore(filepath.Join(dir, "mappings.db"), filepath.Join(dir, "master-key"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	settings := manager.GetSettings()
	settings.ContentSafety.Enabled = true
	settings.ContentSafety.SensitiveInfo.Enabled = true
	settings.ContentSafety.SensitiveInfo.EnabledRules = []string{config.SensitiveInfoRulePhone}
	settings.ContentSafety.SensitiveData.Mode = config.ContentSafetyModeBlock
	require.NoError(t, manager.UpdateSettings(settings))
	pipeline := NewContentSafetyPipelineWithSemantic(manager, &gateTestRecorder{}, nil, nil, store)
	newRequest := func() *gin.Context {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("POST", "/v1/messages", nil)
		AttachHookPipeline(c, pipeline, HookContext{APIType: "messages", RedactionScope: "test-session"})
		t.Cleanup(func() { ClearAttachedSensitiveData(c) })
		return c
	}
	blockRequest := newRequest()
	settings.ContentSafety.SensitiveData.Mode = config.ContentSafetyModeMask
	require.NoError(t, manager.UpdateSettings(settings))
	maskRequest := newRequest()
	// 请求已绑定后再切回 block，已有请求仍应按 mask 处理并持久化。
	settings.ContentSafety.SensitiveData.Mode = config.ContentSafetyModeBlock
	require.NoError(t, manager.UpdateSettings(settings))
	for _, tc := range []struct {
		name string
		ctx  *gin.Context
		mask bool
	}{
		{name: "切换前的阻断请求", ctx: blockRequest},
		{name: "切换后立即到达的脱敏请求", ctx: maskRequest, mask: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "/", strings.NewReader(`{"messages":[{"role":"user","content":"电话 18012345523"}]}`))
			req.Header.Set("Content-Type", "application/json")
			err := RunAttachedPreRequestHooks(context.Background(), tc.ctx, req, "primary")
			if !tc.mask {
				require.NotNil(t, ContentSafetyErrorFrom(err))
				return
			}
			require.NoError(t, err)
			body, err := io.ReadAll(req.Body)
			require.NoError(t, err)
			placeholder := regexp.MustCompile(`MASKED_PHONE_[A-Z2-7]{13}_(?:END|PRESERVE_EXACTLY)`).FindString(string(body))
			require.NotEmpty(t, placeholder)
			// 全新 Vault 只能从数据库找回原文，避免只验证同请求内存映射。
			restored := store.NewScopedVault(context.Background(), "test-session", nil).Restore(placeholder)
			require.Equal(t, "18012345523", restored)
			response, err := RunAttachedPostResponseHooks(context.Background(), tc.ctx, []byte(`{"text":"`+placeholder+`"}`), nil)
			require.NoError(t, err)
			require.Contains(t, string(response), "18012345523")
			attached, err := attachedPipelineFromContext(tc.ctx)
			require.NoError(t, err)
			require.NoError(t, ensureAttachedStreamScanner(attached))
			require.Equal(t, config.ContentSafetyModeMask, attached.streamMaskMode)
		})
	}
}

func TestPersistentRedaction_RestoreMissUsesRequestIdentity(t *testing.T) {
	manager, err := config.NewConfigManager(filepath.Join(t.TempDir(), "config.json"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, manager.Close()) })
	settings := manager.GetSettings()
	settings.ContentSafety.Enabled = true
	settings.ContentSafety.SensitiveData.Mode = config.ContentSafetyModeMask
	settings.ContentSafety.SensitiveInfo.Enabled = true
	settings.ContentSafety.SensitiveInfo.EnabledRules = []string{config.SensitiveInfoRulePhone}
	require.NoError(t, manager.UpdateSettings(settings))
	dir := t.TempDir()
	store, err := sensitive.NewMappingStore(filepath.Join(dir, "mappings.db"), filepath.Join(dir, "master-key"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	const body = `{"text":"MASKED_PHONE_AAAAAAAAAAAAA_PRESERVE_EXACTLY"}`
	for _, protocol := range []string{"messages", "responses", "chat", "gemini", "images"} {
		t.Run(protocol, func(t *testing.T) {
			recorder := &gateTestRecorder{}
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("POST", "/", nil)
			AttachHookPipeline(c, NewContentSafetyPipelineWithSemantic(manager, recorder, nil, nil, store), HookContext{APIType: protocol, RequestID: "request-1"})
			defer ClearAttachedSensitiveData(c)
			require.NoError(t, AssignAttachedConversation(context.Background(), c, "conversation-1"))
			for i := 0; i < 2; i++ {
				restored, err := RestoreAttachedResponseBody(c, []byte(body))
				require.NoError(t, err)
				require.JSONEq(t, body, string(restored))
			}
			require.Len(t, recorder.entries, 1)
			require.Equal(t, protocol, recorder.entries[0].APIType)
			require.Equal(t, "request-1", recorder.entries[0].RequestID)
			require.Equal(t, "conversation-1", recorder.entries[0].ConversationID)
			require.Equal(t, sensitive.BlockTypeRestoreMiss, recorder.entries[0].BlockType)
		})
	}
	t.Run("审计失败返回原始错误", func(t *testing.T) {
		cause := errors.New("审计存储不可用")
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("POST", "/", nil)
		AttachHookPipeline(c, NewContentSafetyPipelineWithSemantic(manager, restoreMissFailureRecorder{cause}, nil, nil, store), HookContext{APIType: "chat"})
		defer ClearAttachedSensitiveData(c)
		_, err := RestoreAttachedResponseBody(c, []byte(body))
		require.ErrorIs(t, err, cause)
	})
}

type restoreMissFailureRecorder struct{ err error }

func (r restoreMissFailureRecorder) Record(context.Context, sensitive.BlockedLog) (sensitive.BlockedLog, error) {
	return sensitive.BlockedLog{}, r.err
}
