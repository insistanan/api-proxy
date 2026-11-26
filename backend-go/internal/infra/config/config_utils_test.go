package config

import (
	"encoding/json"
	"testing"
)

func TestContentSafetyMasterSwitch_DefaultAndSerialization(t *testing.T) {
	settings := DefaultContentSafetyConfig()
	if settings.Enabled {
		t.Fatal("内容安全总开关应默认关闭")
	}
	if err := json.Unmarshal([]byte(`{"sensitiveInfo":{"enabled":true}}`), &settings); err != nil {
		t.Fatal(err)
	}
	if settings.Enabled {
		t.Fatal("缺少总开关字段不能根据子规则推断为开启")
	}
	settings.Enabled = true
	body, err := json.Marshal(settings)
	if err != nil {
		t.Fatal(err)
	}
	var restored ContentSafetyConfig
	if err := json.Unmarshal(body, &restored); err != nil {
		t.Fatal(err)
	}
	if !restored.Enabled || !restored.SensitiveInfo.Enabled {
		t.Fatal("序列化必须保留总开关及子规则的独立状态")
	}
}

func TestValidateContentSafetyRejectsUnknownSensitiveDataMode(t *testing.T) {
	for _, mode := range []string{"", "fallback", "MASK"} {
		settings := DefaultContentSafetyConfig()
		settings.Enabled = true
		settings.SensitiveData.Mode = mode
		if err := ValidateContentSafetyConfig(settings); err == nil {
			t.Fatalf("无效敏感数据处置模式 %q 应显式报错", mode)
		}
	}
}

func TestMigrateContentSafetyLegacyAPIKeyRule(t *testing.T) {
	t.Run("mixed rules split api key into credential group", func(t *testing.T) {
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
	})

	t.Run("api key only rule keeps credential group enabled", func(t *testing.T) {
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
	})
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
			settings.Enabled = true
			test.mutate(&settings)
			if err := ValidateContentSafetyConfig(settings); err == nil {
				t.Fatal("启用但未选择规则应返回错误")
			}
		})
	}
}

// 模型名只按原样参与精确、模糊与通配匹配，不做方括号后缀剥离。
func TestResolveUpstreamModel(t *testing.T) {
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
				ModelMapping: map[string][]string{"opus[1m]": {"deepseek-v4-pro"}},
			},
			want: "deepseek-v4-pro",
		},
		{
			name:  "bracketed name fuzzy-matches shorter source",
			model: "opus[1m]",
			upstream: &UpstreamConfig{
				ModelMapping: map[string][]string{"opus": {"deepseek-v4-pro"}},
			},
			want: "deepseek-v4-pro",
		},
		{
			name:     "bracketed name without mapping passes through unchanged",
			model:    "claude-opus-4-8[1m]",
			upstream: &UpstreamConfig{ModelMapping: map[string][]string{}},
			want:     "claude-opus-4-8[1m]",
		},
		{
			name:     "bracketed name does not strip to reach a longer source",
			model:    "opus[1m]",
			upstream: &UpstreamConfig{ModelMapping: map[string][]string{"claude-opus": {"deepseek-v4-pro"}}},
			want:     "opus[1m]",
		},
		{
			name:     "default model wins over name matching",
			model:    "sonnet[1m]",
			upstream: &UpstreamConfig{DefaultModel: "gpt-5.4"},
			want:     "gpt-5.4",
		},
		{
			name:     "plain name with nil upstream passes through",
			model:    "fable",
			upstream: nil,
			want:     "fable",
		},
		{
			name:     "wildcard mapping handles every model",
			model:    "custom-model-2026",
			upstream: &UpstreamConfig{ModelMapping: map[string][]string{"*": {"gpt-5.4"}}},
			want:     "gpt-5.4",
		},
		{
			name:  "wildcard mapping takes precedence over specific mapping",
			model: "gpt-5.4-codex",
			upstream: &UpstreamConfig{
				ModelMapping: map[string][]string{"*": {"gpt-5.4"}, "codex": {"gpt-5.4-codex"}},
			},
			want: "gpt-5.4",
		},
		{
			// "*" 兜底必须排在空模型早退之前：内部自动模式依赖空模型解析出可发送模型，
			// 只配 "*" 的透传渠道不能被误判为解析不出模型。
			name:     "wildcard mapping applies to empty model",
			model:    "",
			upstream: &UpstreamConfig{ModelMapping: map[string][]string{"*": {"gpt-5.4"}}},
			want:     "gpt-5.4",
		},
		{
			name:     "empty model without mapping stays empty",
			model:    "",
			upstream: &UpstreamConfig{},
			want:     "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ResolveUpstreamModel(tt.model, tt.upstream); got != tt.want {
				t.Errorf("ResolveUpstreamModel() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestLegacyModelMappingOnlyMigratesUnversionedFiles(t *testing.T) {
	cm := newTransactionTestManager(t, "messages")
	legacy := []byte(`{"upstream":[{"baseUrl":"https://api.example.com","apiKeys":["test-key"],"serviceType":"openai","modelMapping":{"opus":"gpt-4o","sonnet":["gpt-4o","gpt-4o-mini"]}}]}`)
	candidate, migrated, err := cm.prepareCandidate(legacy)
	if err != nil || !migrated || candidate.SchemaVersion != currentConfigSchema {
		t.Fatalf("旧文件迁移失败: migrated=%v err=%v", migrated, err)
	}
	assertStringSlice(t, candidate.Upstream[0].ModelMapping["opus"], []string{"gpt-4o"})
	assertStringSlice(t, candidate.Upstream[0].ModelMapping["sonnet"], []string{"gpt-4o", "gpt-4o-mini"})
	current := append([]byte(`{"schemaVersion":1,`), legacy[1:]...)
	if _, _, err := cm.prepareCandidate(current); err == nil {
		t.Fatal("当前版本不能重新进入旧格式迁移")
	}
	var upstream UpstreamConfig
	if err := json.Unmarshal([]byte(`{"modelMapping":{"opus":"gpt-4o"}}`), &upstream); err == nil {
		t.Fatal("新增渠道不能接受旧模型映射")
	}
	var update UpstreamUpdate
	if err := json.Unmarshal([]byte(`{"modelMapping":{"opus":"gpt-4o"}}`), &update); err == nil {
		t.Fatal("编辑渠道不能接受旧模型映射")
	}
}
func TestExplicitActivationRequestsFailureStateReset(t *testing.T) {
	for _, tc := range []struct {
		name  string
		input string
		reset bool
	}{
		{"active", `{"status":"active"}`, true},
		{"normalized-active", `{"status":" ACTIVE "}`, true},
		{"suspended", `{"status":"suspended"}`, false},
		{"disabled", `{"status":"disabled"}`, false},
		{"unchanged-status", `{"name":"改名"}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, status := range []string{ChannelStatusActive, ChannelStatusSuspended} {
				upstream := UpstreamConfig{Status: status, APIKeys: []string{"fixture-key"}}
				var update UpstreamUpdate
				if err := json.Unmarshal([]byte(tc.input), &update); err != nil {
					t.Fatal(err)
				}
				reset, err := applyCommonUpdates(&upstream, 0, update, "test")
				if err != nil {
					t.Fatal(err)
				}
				if reset != tc.reset {
					t.Fatalf("原状态=%s，更新=%s，重置=%v，期望=%v", status, tc.input, reset, tc.reset)
				}
			}
		})
	}
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
