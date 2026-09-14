package hooks

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BenedictKing/api-proxy/internal/config"
	"github.com/BenedictKing/api-proxy/internal/sensitive"
)

// newAllowlistTestSnapshot 组装带 allowlist / customRules 的检测器快照。
func newAllowlistTestSnapshot(t testing.TB, settings config.ContentSafetyConfig) *contentSafetySnapshot {
	t.Helper()
	words, err := sensitive.NewWordFilter(settings.SensitiveWord)
	if err != nil {
		t.Fatalf("创建敏感词检测器失败: %v", err)
	}
	info, err := sensitive.NewInfoDetector(settings.SensitiveInfo)
	if err != nil {
		t.Fatalf("创建个人信息检测器失败: %v", err)
	}
	credential, err := sensitive.NewCredentialDetector(settings.Credential)
	if err != nil {
		t.Fatalf("创建凭据检测器失败: %v", err)
	}
	custom, err := sensitive.NewCustomDetector(settings.CustomRules)
	if err != nil {
		t.Fatalf("创建自定义规则检测器失败: %v", err)
	}
	return &contentSafetySnapshot{settings: settings, words: words, info: info, credential: credential, custom: custom}
}

// allowlistTestSettings 返回 mask 模式 + 启用 api_key 与 ip_address 规则的设置。
func allowlistTestSettings() config.ContentSafetyConfig {
	safety := config.DefaultContentSafetyConfig()
	safety.SensitiveData.Mode = config.ContentSafetyModeMask
	safety.Credential.Enabled = true
	safety.Credential.EnabledRules = []string{config.CredentialRuleAPIKey}
	safety.SensitiveInfo.Enabled = true
	safety.SensitiveInfo.Mode = config.ContentSafetyModeMask
	safety.SensitiveInfo.EnabledRules = []string{config.SensitiveInfoRuleIPAddress}
	return safety
}

// TestAllowlistSkipsWholeSegment 整段文本与放行字面量精确相等时跳过全部检测，
// 并写 allowlist 审计事件。
func TestAllowlistSkipsWholeSegment(t *testing.T) {
	safety := allowlistTestSettings()
	safety.Allowlist = config.AllowlistConfig{Enabled: true, Entries: []string{"localhost"}}
	snapshot := newAllowlistTestSnapshot(t, safety)
	recorder := &gateTestRecorder{}

	segment := SafetySegment{
		Protocol: "messages", Source: safetySourceUser, Path: "messages[0].content",
		Text: "localhost", Mutable: true,
		replace: func(value string) {},
	}
	segments := []SafetySegment{segment}
	prompts := make([]string, 0, 1)
	changed, err := applyContentSafetySegments(context.Background(), HookContext{APIType: "messages"}, segments, snapshot, recorder, &prompts)
	if err != nil {
		t.Fatalf("整段放行不应报错: %v", err)
	}
	if changed {
		t.Fatal("整段放行不应改写文本")
	}
	if len(recorder.entries) != 1 || recorder.entries[0].BlockType != sensitive.BlockTypeAllowlist {
		t.Fatalf("应写 1 条 allowlist 审计事件: %+v", recorder.entries)
	}
}

// TestAllowlistFiltersSubstringMatches 子串命中与放行字面量精确相等时丢弃该命中：
// 白名单 IP 不被掩码，非白名单 IP 照常掩码。
func TestAllowlistFiltersSubstringMatches(t *testing.T) {
	safety := allowlistTestSettings()
	safety.Allowlist = config.AllowlistConfig{Enabled: true, Entries: []string{"10.0.0.5"}}
	snapshot := newAllowlistTestSnapshot(t, safety)

	segment := SafetySegment{
		Protocol: "messages", Source: safetySourceUser, Path: "messages[0].content",
		Text:    "白名单机器 10.0.0.5 与外网 8.8.8.8 都要查",
		Mutable: true,
		replace: func(value string) {},
	}
	segments := []SafetySegment{segment}
	prompts := make([]string, 0, 1)
	changed, err := applyContentSafetySegments(context.Background(), HookContext{APIType: "messages"}, segments, snapshot, nil, &prompts)
	if err != nil {
		t.Fatalf("放行字面量过滤不应报错: %v", err)
	}
	if !changed {
		t.Fatal("非白名单 IP 应被掩码（changed=true）")
	}
	if !strings.Contains(segments[0].Text, "10.0.0.5") {
		t.Fatalf("放行 IP 应原样保留: %q", segments[0].Text)
	}
	if strings.Contains(segments[0].Text, "8.8.8.8") {
		t.Fatalf("非白名单 IP 应被掩码: %q", segments[0].Text)
	}
	if !strings.Contains(segments[0].Text, "MASKED_") {
		t.Fatalf("应产生占位符: %q", segments[0].Text)
	}
}

// TestAllowlistDisabledByDefaultEntries 关闭放行字面量后，出厂条目（如 localhost）
// 不再享受放行。
func TestAllowlistDisabledByDefaultEntries(t *testing.T) {
	safety := allowlistTestSettings()
	safety.Allowlist = config.AllowlistConfig{Enabled: false, Entries: []string{"127.0.0.1"}}
	snapshot := newAllowlistTestSnapshot(t, safety)

	segment := SafetySegment{
		Protocol: "messages", Source: safetySourceUser, Path: "messages[0].content",
		Text:    "连到 127.0.0.1 调试",
		Mutable: true,
		replace: func(value string) {},
	}
	segments := []SafetySegment{segment}
	prompts := make([]string, 0, 1)
	changed, err := applyContentSafetySegments(context.Background(), HookContext{APIType: "messages"}, segments, snapshot, nil, &prompts)
	if err != nil {
		t.Fatalf("不应报错: %v", err)
	}
	if !changed {
		t.Fatal("关闭放行后 127.0.0.1 应被掩码")
	}
}

// TestCustomRulesMaskAndRestore 自定义规则命中 → 占位符替换 → Vault 还原往返；
// 且内置凭据规则已占住的区间不被自定义规则重复命中。
func TestCustomRulesMaskAndRestore(t *testing.T) {
	safety := allowlistTestSettings()
	safety.CustomRules = config.CustomRulesConfig{
		Enabled: true,
		Rules: []config.CustomRuleItem{
			{Name: "内部代号", Type: "CODENAME", Pattern: `project-falcon|internal-[a-z]+`},
		},
	}
	snapshot := newAllowlistTestSnapshot(t, safety)

	segment := SafetySegment{
		Protocol: "messages", Source: safetySourceUser, Path: "messages[0].content",
		Text:    "查一下 project-falcon 的日志，密钥 sk-ant-api03-abcdefghijklmnop 也要看",
		Mutable: true,
		replace: func(value string) {},
	}
	segments := []SafetySegment{segment}
	prompts := make([]string, 0, 1)
	changed, err := applyContentSafetySegments(context.Background(), HookContext{APIType: "messages"}, segments, snapshot, nil, &prompts)
	if err != nil {
		t.Fatalf("自定义规则掩码失败: %v", err)
	}
	if !changed {
		t.Fatal("应发生掩码改写")
	}
	masked := segments[0].Text
	if strings.Contains(masked, "project-falcon") {
		t.Fatalf("自定义规则命中应被掩码: %q", masked)
	}
	if !strings.Contains(masked, "MASKED_CODENAME_") {
		t.Fatalf("应生成 CODENAME 类型占位符: %q", masked)
	}
	if !strings.Contains(masked, "MASKED_API_TOKEN_") && !strings.Contains(masked, "MASKED_APIKEY_") {
		t.Fatalf("凭据命中应照常掩码: %q", masked)
	}
	// 嵌套防御：占位符外不应再包一层自定义占位符。
	if strings.Contains(masked, "MASKED_CODENAME_MASKED_") {
		t.Fatalf("占位符被嵌套包裹: %q", masked)
	}

	// 还原往返：Mask→Restore 是同一张 Vault 表。"代号 "（代号+空格）占 7 个
	// UTF-8 字节，project-falcon 从字节 7 开始。
	vault := sensitive.NewVault()
	direct, err := vault.Mask("代号 project-falcon", []sensitive.RedactionMatch{{
		Type: "CODENAME", Rule: "内部代号", Original: "project-falcon", Start: 7, End: 21,
	}})
	if err != nil {
		t.Fatalf("直接掩码失败: %v", err)
	}
	restored := vault.Restore(direct)
	if restored != "代号 project-falcon" {
		t.Fatalf("还原往返失败: %q", restored)
	}
}

// TestCustomRulesBlockedInBlockMode 自定义规则仅在 mask 模式参与（接线层守卫）。
func TestCustomRulesBlockedInBlockMode(t *testing.T) {
	safety := allowlistTestSettings()
	safety.SensitiveData.Mode = config.ContentSafetyModeBlock
	safety.CustomRules = config.CustomRulesConfig{
		Enabled: true,
		Rules:   []config.CustomRuleItem{{Name: "代号", Type: "CODENAME", Pattern: `project-falcon`}},
	}
	// ValidateContentSafetyConfig 应拒绝（依赖 mask 模式）。
	if err := config.ValidateContentSafetyConfig(safety); err == nil {
		t.Fatal("block 模式下启用自定义规则应被校验拒绝")
	}
}

// TestCustomRulesAllowlistInteraction 放行字面量与自定义规则正交：命中原文与
// 条目相等则丢弃该自定义命中。
func TestCustomRulesAllowlistInteraction(t *testing.T) {
	safety := allowlistTestSettings()
	safety.Allowlist = config.AllowlistConfig{Enabled: true, Entries: []string{"internal-alpha"}}
	safety.CustomRules = config.CustomRulesConfig{
		Enabled: true,
		Rules:   []config.CustomRuleItem{{Name: "内部代号", Type: "CODENAME", Pattern: `internal-[a-z]+`}},
	}
	snapshot := newAllowlistTestSnapshot(t, safety)

	segment := SafetySegment{
		Protocol: "messages", Source: safetySourceUser, Path: "messages[0].content",
		Text:    "internal-alpha 和 internal-beta",
		Mutable: true,
		replace: func(value string) {},
	}
	segments := []SafetySegment{segment}
	prompts := make([]string, 0, 1)
	changed, err := applyContentSafetySegments(context.Background(), HookContext{APIType: "messages"}, segments, snapshot, nil, &prompts)
	if err != nil {
		t.Fatalf("掩码失败: %v", err)
	}
	if !changed {
		t.Fatal("internal-beta 应被掩码")
	}
	if !strings.Contains(segments[0].Text, "internal-alpha") {
		t.Fatalf("放行的代号应原样保留: %q", segments[0].Text)
	}
	if strings.Contains(segments[0].Text, "internal-beta") {
		t.Fatalf("未放行的代号应被掩码: %q", segments[0].Text)
	}
}

// TestValidateAllowlistAndCustomRules 锁定两个新配置的校验。
func TestValidateAllowlistAndCustomRules(t *testing.T) {
	base := allowlistTestSettings()

	cases := []struct {
		name    string
		mutate  func(*config.ContentSafetyConfig)
		wantErr bool
	}{
		{"合法放行字面量", func(s *config.ContentSafetyConfig) {
			s.Allowlist = config.AllowlistConfig{Enabled: true, Entries: []string{"10.0.0.5", "localhost"}}
		}, false},
		{"空放行字面量", func(s *config.ContentSafetyConfig) {
			s.Allowlist = config.AllowlistConfig{Enabled: true, Entries: []string{"  "}}
		}, true},
		{"重复放行字面量", func(s *config.ContentSafetyConfig) {
			s.Allowlist = config.AllowlistConfig{Enabled: true, Entries: []string{"a", "a"}}
		}, true},
		{"合法自定义规则", func(s *config.ContentSafetyConfig) {
			s.CustomRules = config.CustomRulesConfig{Enabled: true, Rules: []config.CustomRuleItem{{Name: "代号", Type: "CODENAME", Pattern: `abc\d+`}}}
		}, false},
		{"正则无法编译", func(s *config.ContentSafetyConfig) {
			s.CustomRules = config.CustomRulesConfig{Enabled: true, Rules: []config.CustomRuleItem{{Name: "代号", Type: "CODENAME", Pattern: `[`}}}
		}, true},
		{"类型前缀非法字符", func(s *config.ContentSafetyConfig) {
			s.CustomRules = config.CustomRulesConfig{Enabled: true, Rules: []config.CustomRuleItem{{Name: "代号", Type: "code-name", Pattern: `abc`}}}
		}, true},
		{"启用但无规则", func(s *config.ContentSafetyConfig) {
			s.CustomRules = config.CustomRulesConfig{Enabled: true, Rules: nil}
		}, true},
		{"mask 模式下合法", func(s *config.ContentSafetyConfig) {
			s.CustomRules = config.CustomRulesConfig{Enabled: true, Rules: []config.CustomRuleItem{{Name: "代号", Type: "CODENAME", Pattern: `abc`}}}
		}, false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			current := base
			testCase.mutate(&current)
			err := config.ValidateContentSafetyConfig(current)
			if testCase.wantErr && err == nil {
				t.Fatal("应返回校验错误")
			}
			if !testCase.wantErr && err != nil {
				t.Fatalf("不应报错: %v", err)
			}
		})
	}

	// block 模式下自定义规则启用 → 拒绝（上面 TestCustomRulesBlockedInBlockMode
	// 已覆盖，这里补一个"关闭时不校验规则内容"的对称分支）。
	off := base
	off.SensitiveData.Mode = config.ContentSafetyModeBlock
	off.CustomRules = config.CustomRulesConfig{Enabled: false, Rules: []config.CustomRuleItem{{Name: "x", Type: "BAD!", Pattern: `[`}}}
	if err := config.ValidateContentSafetyConfig(off); err != nil {
		t.Fatalf("自定义规则关闭时不应校验规则内容: %v", err)
	}
	_ = filepath.Join
}
