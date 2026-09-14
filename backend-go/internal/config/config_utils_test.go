package config

import (
	"encoding/json"
	"testing"
)

func TestDefaultContentSafetyConfigIsFullyDisabled(t *testing.T) {
	settings := DefaultContentSafetyConfig()
	if settings.SensitiveData.Mode != ContentSafetyModeBlock {
		t.Fatalf("敏感数据默认处置模式 = %q，期望 block", settings.SensitiveData.Mode)
	}
	if settings.SensitiveWord.Enabled || settings.SensitiveWord.PornographyEnabled ||
		settings.SensitiveWord.GamblingEnabled || settings.SensitiveWord.DrugsEnabled ||
		settings.SensitiveWord.ViolenceTerrorEnabled || settings.SensitiveWord.PoliticalEnabled ||
		settings.SensitiveWord.IllegalCrimeEnabled || len(settings.SensitiveWord.CustomWords) != 0 {
		t.Fatalf("敏感词默认设置未完全关闭: %+v", settings.SensitiveWord)
	}
	if settings.SensitiveInfo.Enabled || len(settings.SensitiveInfo.EnabledRules) != 0 {
		t.Fatalf("敏感信息默认设置未完全关闭: %+v", settings.SensitiveInfo)
	}
	if settings.Credential.Enabled || len(settings.Credential.EnabledRules) != 0 {
		t.Fatalf("凭据默认设置未完全关闭: %+v", settings.Credential)
	}
	if settings.DangerousCmd.Enabled || len(settings.DangerousCmd.EnabledRules) != 0 {
		t.Fatalf("危险命令默认设置未完全关闭: %+v", settings.DangerousCmd)
	}
}

func TestValidateContentSafetyRejectsUnknownSensitiveDataMode(t *testing.T) {
	for _, mode := range []string{"", "fallback", "MASK"} {
		settings := DefaultContentSafetyConfig()
		settings.SensitiveData.Mode = mode
		if err := ValidateContentSafetyConfig(settings); err == nil {
			t.Fatalf("无效敏感数据处置模式 %q 应显式报错", mode)
		}
	}
}

func TestMigrateContentSafetyMovesLegacyAPIKeyRule(t *testing.T) {
	settings := ContentSafetyConfig{
		SensitiveInfo: SensitiveInfoConfig{
			Enabled:      true,
			EnabledRules: []string{"api_key", SensitiveInfoRuleEmail},
		},
	}
	raw := []byte(`{"sensitiveWord":{"enabled":false},"sensitiveInfo":{"enabled":true,"enabledRules":["api_key","email"]},"dangerousCmd":{"enabled":false,"enabledRules":[]}}`)
	if !migrateContentSafetyConfig(&settings, raw) {
		t.Fatal("旧版 API Key 规则应触发配置迁移")
	}
	if !settings.Credential.Enabled || settings.Credential.UserInputMode != ContentSafetyModeMask ||
		settings.Credential.ToolResultMode != ContentSafetyModeBlock ||
		len(settings.Credential.EnabledRules) != 1 || settings.Credential.EnabledRules[0] != CredentialRuleAPIKey {
		t.Fatalf("凭据迁移结果错误: %+v", settings.Credential)
	}
	if len(settings.SensitiveInfo.EnabledRules) != 1 || settings.SensitiveInfo.EnabledRules[0] != SensitiveInfoRuleEmail {
		t.Fatalf("个人信息规则迁移结果错误: %+v", settings.SensitiveInfo)
	}
}

func TestMigrateContentSafetyKeepsLegacyAPIKeyOnlyRuleEnabled(t *testing.T) {
	settings := ContentSafetyConfig{
		SensitiveInfo: SensitiveInfoConfig{
			Enabled:      true,
			EnabledRules: []string{"api_key"},
		},
	}
	raw := []byte(`{"sensitiveWord":{"enabled":false},"sensitiveInfo":{"enabled":true,"enabledRules":["api_key"]},"dangerousCmd":{"enabled":false,"enabledRules":[]}}`)
	if !migrateContentSafetyConfig(&settings, raw) {
		t.Fatal("旧版仅 API Key 规则应触发配置迁移")
	}
	if settings.SensitiveInfo.Enabled || len(settings.SensitiveInfo.EnabledRules) != 0 {
		t.Fatalf("旧版 API Key 不应继续留在个人信息配置: %+v", settings.SensitiveInfo)
	}
	if !settings.Credential.Enabled || settings.Credential.UserInputMode != ContentSafetyModeMask ||
		len(settings.Credential.EnabledRules) != 1 || settings.Credential.EnabledRules[0] != CredentialRuleAPIKey {
		t.Fatalf("旧版仅 API Key 规则迁移后不应被意外关闭: %+v", settings.Credential)
	}
}

func TestValidateContentSafetyRejectsEnabledGroupWithoutRules(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*ContentSafetyConfig)
	}{
		{name: "敏感词", mutate: func(settings *ContentSafetyConfig) { settings.SensitiveWord.Enabled = true }},
		{name: "个人信息", mutate: func(settings *ContentSafetyConfig) { settings.SensitiveInfo.Enabled = true }},
		{name: "凭据", mutate: func(settings *ContentSafetyConfig) { settings.Credential.Enabled = true }},
		{name: "危险命令", mutate: func(settings *ContentSafetyConfig) { settings.DangerousCmd.Enabled = true }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			settings := DefaultContentSafetyConfig()
			test.mutate(&settings)
			if err := ValidateContentSafetyConfig(settings); err == nil {
				t.Fatal("启用但未选择规则应返回错误")
			}
		})
	}
}

// TestResolveUpstreamModelWithBracketedName 固定的契约是：带方括号标记的模型名
// （历史上 Claude Code 的 "[1m]"）不再被特殊处理，只按原样走通用的精确/模糊匹配。
func TestResolveUpstreamModelWithBracketedName(t *testing.T) {
	tests := []struct {
		name     string
		model    string
		upstream *UpstreamConfig
		want     string
	}{
		{
			name:  "bracketed name hits exact mapping key",
			model: "opus[1m]",
			upstream: &UpstreamConfig{
				ModelMapping: map[string][]string{
					"opus[1m]": {"deepseek-v4-pro"},
				},
			},
			want: "deepseek-v4-pro",
		},
		{
			name:  "bracketed name fuzzy-matches shorter source",
			model: "opus[1m]",
			upstream: &UpstreamConfig{
				ModelMapping: map[string][]string{
					"opus": {"deepseek-v4-pro"},
				},
			},
			want: "deepseek-v4-pro",
		},
		{
			name:  "bracketed name without mapping passes through unchanged",
			model: "claude-opus-4-8[1m]",
			upstream: &UpstreamConfig{
				ModelMapping: map[string][]string{},
			},
			want: "claude-opus-4-8[1m]", // 保留原样，没有映射就不处理
		},
		{
			name:  "default model wins over name matching",
			model: "sonnet[1m]",
			upstream: &UpstreamConfig{
				DefaultModel: "gpt-5.4",
			},
			want: "gpt-5.4",
		},
		{
			name:     "plain name with nil upstream passes through",
			model:    "fable",
			upstream: nil,
			want:     "fable",
		},
		{
			// 不再剥后缀：源模型比请求名更长时，双向 Contains 都不成立
			// （"opus[1m]" 不含 "claude-opus"，"claude-opus" 也不含 "opus[1m]"）
			name:  "bracketed name no longer strips to reach a longer source",
			model: "opus[1m]",
			upstream: &UpstreamConfig{
				ModelMapping: map[string][]string{
					"claude-opus": {"deepseek-v4-pro"},
				},
			},
			want: "opus[1m]",
		},
		{
			name:  "wildcard mapping handles every model",
			model: "custom-model-2026",
			upstream: &UpstreamConfig{
				ModelMapping: map[string][]string{
					"*": {"gpt-5.4"},
				},
			},
			want: "gpt-5.4",
		},
		{
			name:  "wildcard mapping takes precedence over specific mapping",
			model: "gpt-5.4-codex",
			upstream: &UpstreamConfig{
				ModelMapping: map[string][]string{
					"*":     {"gpt-5.4"},
					"codex": {"gpt-5.4-codex"},
				},
			},
			want: "gpt-5.4",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ResolveUpstreamModel(tt.model, tt.upstream)
			if got != tt.want {
				t.Errorf("ResolveUpstreamModel() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestResolveUpstreamModelWildcardWithEmptyModel 锁定 "*" 兜底映射在空模型下
// 依然生效：内部调用自动模式（空模型）依赖该语义解析出可发送模型，"*" 检查必须
// 排在空模型早退之前（历史 bug：先 return nil 导致只配 "*" 的透传渠道被误判
// "解析不出模型"）。
func TestResolveUpstreamModelWildcardWithEmptyModel(t *testing.T) {
	upstream := &UpstreamConfig{
		ModelMapping: map[string][]string{
			"*": {"gpt-5.4"},
		},
	}
	if got := ResolveUpstreamModel("", upstream); got != "gpt-5.4" {
		t.Errorf("空模型 + \"*\" 映射应解析为兜底目标，got %q want %q", got, "gpt-5.4")
	}
	// 纯透传渠道（无 DefaultModel、无映射）在空模型下仍解析不出具体模型。
	if got := ResolveUpstreamModel("", &UpstreamConfig{}); got != "" {
		t.Errorf("空模型 + 无映射应返回空，got %q", got)
	}
}

func TestUpstreamConfigUnmarshalLegacyModelMapping(t *testing.T) {
	input := []byte(`{
		"baseUrl": "https://api.example.com",
		"apiKeys": ["sk-test"],
		"serviceType": "openai",
		"modelMapping": {
			"opus": "kimi-2.6",
			"sonnet": ["gpt-4o", "gpt-4o-mini"]
		}
	}`)

	var upstream UpstreamConfig
	if err := json.Unmarshal(input, &upstream); err != nil {
		t.Fatalf("json.Unmarshal() err = %v", err)
	}

	assertStringSlice(t, upstream.ModelMapping["opus"], []string{"kimi-2.6"})
	assertStringSlice(t, upstream.ModelMapping["sonnet"], []string{"gpt-4o", "gpt-4o-mini"})
}

func TestUpstreamUpdateUnmarshalLegacyModelMapping(t *testing.T) {
	input := []byte(`{
		"modelMapping": {
			"opus": "kimi-2.6",
			"sonnet": ["gpt-4o"]
		}
	}`)

	var update UpstreamUpdate
	if err := json.Unmarshal(input, &update); err != nil {
		t.Fatalf("json.Unmarshal() err = %v", err)
	}

	assertStringSlice(t, update.ModelMapping["opus"], []string{"kimi-2.6"})
	assertStringSlice(t, update.ModelMapping["sonnet"], []string{"gpt-4o"})
}

func assertStringSlice(t *testing.T, got []string, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("len(got) = %d, want %d; got=%v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got[%d] = %q, want %q; got=%v", i, got[i], want[i], got)
		}
	}
}
