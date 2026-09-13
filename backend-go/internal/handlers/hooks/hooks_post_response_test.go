package hooks

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BenedictKing/api-proxy/internal/config"
	"github.com/BenedictKing/api-proxy/internal/sensitive"
)

// newPostResponseHookForTest 按给定内容安全设置组装响应方向安全 Hook。
func newPostResponseHookForTest(t *testing.T, safety config.ContentSafetyConfig) (*contentSafetyPostResponseHook, *gateTestRecorder) {
	t.Helper()
	configManager, err := config.NewConfigManager(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatalf("创建配置管理器失败: %v", err)
	}
	t.Cleanup(func() { _ = configManager.Close() })
	settings := configManager.GetSettings()
	settings.ContentSafety = safety
	if err := configManager.UpdateSettings(settings); err != nil {
		t.Fatalf("更新内容安全设置失败: %v", err)
	}
	recorder := &gateTestRecorder{}
	return &contentSafetyPostResponseHook{cfgManager: configManager, recorder: recorder}, recorder
}

// responseCredentialSettings 返回启用 api_key 规则的响应方向内容安全设置。
func responseCredentialSettings(mode string) config.ContentSafetyConfig {
	safety := config.DefaultContentSafetyConfig()
	safety.Credential.Enabled = true
	safety.Credential.EnabledRules = []string{config.CredentialRuleAPIKey}
	safety.SensitiveData.Mode = mode
	safety.DangerousCmd.Enabled = false
	return safety
}

// TestResponseCredentialMaskAllows 锁定统一口径契约：sensitiveData.mode=mask 时，
// 非流式响应中工具参数的凭据命中放行并写审计事件，不再无条件阻断。
// （修复前此处无视 mode 一律返回 credential 拦截错误。）
func TestResponseCredentialMaskAllows(t *testing.T) {
	hook, recorder := newPostResponseHookForTest(t, responseCredentialSettings(config.ContentSafetyModeMask))
	metadata := HookContext{APIType: "messages", RequestID: "req-mask"}
	// messages 响应里的 tool_use input 含 API Key。
	body := []byte(`{"content":[{"type":"tool_use","id":"toolu_1","name":"Bash","input":{"command":"echo sk-ant-api03-abcdefghijklmnop"}}]}`)

	result, err := hook.Run(context.Background(), metadata, HookResult{ResponseBody: body})
	if err != nil {
		t.Fatalf("mask 模式下响应凭据命中应放行: %v", err)
	}
	if len(recorder.entries) == 0 {
		t.Fatal("放行应写审计事件")
	}
	if result.ResponseBody == nil {
		t.Fatal("响应体不应被丢弃")
	}
}

// TestResponseCredentialBlockStillBlocks 锁定 block 模式维持既有拦截行为。
func TestResponseCredentialBlockStillBlocks(t *testing.T) {
	hook, _ := newPostResponseHookForTest(t, responseCredentialSettings(config.ContentSafetyModeBlock))
	metadata := HookContext{APIType: "messages", RequestID: "req-block"}
	body := []byte(`{"content":[{"type":"tool_use","id":"toolu_1","name":"Bash","input":{"command":"echo sk-ant-api03-abcdefghijklmnop"}}]}`)

	_, err := hook.Run(context.Background(), metadata, HookResult{ResponseBody: body})
	if err == nil {
		t.Fatal("block 模式下响应凭据命中应拦截")
	}
	safetyErr := ContentSafetyErrorFrom(err)
	if safetyErr == nil || safetyErr.BlockType != sensitive.BlockTypeCredential {
		t.Fatalf("应返回凭据拦截错误: %v", err)
	}
	if !strings.Contains(safetyErr.RuleName, "api_key") && safetyErr.RuleName == "" {
		t.Fatalf("拦截错误应携带规则名: %+v", safetyErr)
	}
	var hookErr *ContentSafetyHookError
	if errors.As(err, &hookErr) {
		t.Fatal("策略命中不应被归类为基础设施错误")
	}
}
